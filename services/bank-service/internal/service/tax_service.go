// Package service — TaxService: jedinstvena poslovna logika obračuna i naplate
// poreza na kapitalnu dobit (15%) za prodaju akcija (berza + OTC).
//
// Centralizuje logiku koju dele:
//   - MonthlyTaxWorker (automatski, 1. u mesecu 00:01 za prethodni mesec)
//   - TaxHandler POST /bank/tax/calculate (ručno, supervizor)
//   - TaxHandler GET /bank/tax/users (lista trgovačkih korisnika sa dugovanjem)
//   - PortfolioHandler (polja taxPaidRsd i taxUnpaid)
//
// Pravila implementirana ovde:
//   - 15% kapitalne dobiti samo za STOCK (listing_type='STOCK').
//   - Oporezivi profit = (wavg_sell − wavg_buy) × qty; negativan profit = 0.
//   - Profit u USD (valuta trgovanja) → RSD preko srednjeg kursa, bez provizije.
//   - Snapshot kursa se upisuje u tax_records.exchange_rate_used.
//   - Naplata obavezno ide sa originalnog funding računa (orders.account_id)
//     i završava na državnom RSD računu (drzava@exbanka.rs).
//   - Transakcija se izvršava atomski sa SELECT FOR UPDATE lock-om i per-row
//     advisory lock-om; parcijalna naplata ostavlja remaining_debt_rsd.
//   - Idempotencija: tax_records jedinstvenost po (user, account, period_start,
//     period_end); status=COLLECTED short-circuits.
package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/transport"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// TaxRate je stopa poreza na kapitalnu dobit (15%).
const TaxRate = 0.15

// drzavaEmail je email sistemskog korisnika kome pripada državni prihodni račun.
const drzavaEmail = "drzava@exbanka.rs"

// drzavaBrojRacuna je očekivani broj RSD računa države, seed-ovan u 000042.
const drzavaBrojRacuna = "666000112000000001"

// Mogući statusi tax_records reda.
const (
	TaxStatusOpen      = "OPEN"
	TaxStatusCollected = "COLLECTED"
	TaxStatusPartial   = "PARTIAL"
	TaxStatusUnpaid    = "UNPAID"
)

// Izvori obračuna (ulaze u tax_records.triggered_by).
const (
	TaxTriggeredByCron   = "CRON"
	TaxTriggeredByManual = "MANUAL"
)

// ErrPeriodAlreadyRunning se vraća kada se pokuša paralelni obračun za isti
// kalendarski period (advisory lock nije mogao biti dobijen).
var ErrPeriodAlreadyRunning = errors.New("obračun poreza za izabrani period je već u toku")

// ErrStateAccountMissing se vraća kada ne može da se pronađe državni RSD račun.
// U tom slučaju naplata se prekida — ne sme da novac „nestane” bez destinacije.
var ErrStateAccountMissing = errors.New("državni RSD račun (drzava@exbanka.rs) nije pronađen")

// ErrUSDRateUnavailable se vraća kada srednji kurs USD→RSD nije dostupan.
var ErrUSDRateUnavailable = errors.New("srednji kurs USD→RSD nije dostupan")

// ─── Javni tipovi ────────────────────────────────────────────────────────────

// UserAccountProfit je agregat oporezivog profita po (user, account) u datom prozoru.
type UserAccountProfit struct {
	UserID         int64
	AccountID      int64
	SourceCurrency string          // trenutno uvek "USD" (cene trgovanja)
	ProfitNative   decimal.Decimal // profit u valuti trgovanja
	TaxNative      decimal.Decimal // 15% od profit_native
	TaxRSD         decimal.Decimal // konvertovano u RSD srednjim kursom
	RateUsed       decimal.Decimal // srednji kurs korišćen za konverziju
	TxnCount       int             // broj pojedinačnih SELL fill-ova u prozoru
}

// PeriodSummary je rezultat CalculateAndCollectForPeriod.
type PeriodSummary struct {
	ProcessedUsers    int             `json:"processedUsers"`
	TotalCollectedRSD decimal.Decimal `json:"-"`
	TotalCollectedF64 float64         `json:"totalCollectedRsd"`
	FullyCollected    int             `json:"fullyCollected"`
	Partial           int             `json:"partial"`
	Unpaid            int             `json:"unpaid"`
	AlreadyCollected  int             `json:"alreadyCollected"`
	Errors            int             `json:"errors"`
	PeriodStart       time.Time       `json:"periodStart"`
	PeriodEnd         time.Time       `json:"periodEnd"`
}

// TaxUserFilter su opcioni query filteri za porez tracking listu korisnika.
type TaxUserFilter struct {
	Role      string // "", "client", "actuary"
	FirstName string
	LastName  string
}

// TaxUserRow je jedan red u odgovoru /bank/tax/users.
type TaxUserRow struct {
	UserID    int64
	UserType  string // "CLIENT" | "ACTUARY"
	FirstName string
	LastName  string
	Email     string
	TaxDebt   float64 // RSD; 0 ako nema dugovanja
}

// ─── TaxService ──────────────────────────────────────────────────────────────

// TaxService je jedini autoritativan sloj za obračun i naplatu poreza.
type TaxService struct {
	db         *gorm.DB
	exchange   domain.ExchangeService
	userClient *transport.UserServiceClient

	cfgStateRevenueAccountID int64

	stateAccountMu sync.Mutex
	cachedStateID  int64
}

// NewTaxService konstruiše servis. userClient može biti nil (enrichment by name
// je best-effort); ali za resolver državnog računa preporučeno nije nil.
func NewTaxService(
	db *gorm.DB,
	exchange domain.ExchangeService,
	userClient *transport.UserServiceClient,
	cfgStateRevenueAccountID int64,
) *TaxService {
	return &TaxService{
		db:                       db,
		exchange:                 exchange,
		userClient:               userClient,
		cfgStateRevenueAccountID: cfgStateRevenueAccountID,
	}
}

// ─── Prozor / periodi ────────────────────────────────────────────────────────

// PreviousMonthWindow vraća [1. prethodnog meseca 00:00:00, kraj meseca - 1µs]
// u lokalnoj time zoni.
func PreviousMonthWindow(now time.Time) (time.Time, time.Time) {
	start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
	end := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, start.Location()).Add(-time.Microsecond)
	return start, end
}

// CurrentMonthWindow vraća aligned kalendarski mesec [1. 00:00, kraj meseca − 1µs].
// Poravnat kraj meseca je neophodan za idempotenciju: svaki klik „Obračunaj"
// koristi isti (period_start, period_end) ključ, pa unique constraint i status
// COLLECTED short-circuit sprečavaju duplu naplatu. Sell-ovi u budućim danima
// fizički ne postoje, pa poravnat prozor ništa ne pokvari.
func CurrentMonthWindow(now time.Time) (time.Time, time.Time) {
	return MonthWindow(now.Year(), now.Month(), now.Location())
}

// MonthWindow vraća ceo kalendarski mesec [1. mesec 00:00, kraj meseca - 1µs].
// Koristi se kada supervizor eksplicitno traži obračun za zadati {year, month}.
func MonthWindow(year int, month time.Month, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.Local
	}
	start := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	end := time.Date(year, month+1, 1, 0, 0, 0, 0, loc).Add(-time.Microsecond)
	return start, end
}

// ─── Resolver državnog računa ────────────────────────────────────────────────

// ResolveStateRevenueAccountID vraća id RSD računa države. Pokušava redom:
//  1. env override (cfg.StateRevenueAccountID), ako je > 0 i red postoji;
//  2. lookup po fiksnom broju računa 666000112000000001 (seed iz 000042);
//  3. lookup preko email-a drzava@exbanka.rs (user-service) → id_vlasnika + RSD valuta.
func (s *TaxService) ResolveStateRevenueAccountID(ctx context.Context) (int64, error) {
	s.stateAccountMu.Lock()
	cached := s.cachedStateID
	s.stateAccountMu.Unlock()
	if cached > 0 {
		return cached, nil
	}

	// (1) env override
	if s.cfgStateRevenueAccountID > 0 {
		var exists bool
		err := s.db.WithContext(ctx).Raw(`
			SELECT EXISTS (SELECT 1 FROM core_banking.racun WHERE id = ?)
		`, s.cfgStateRevenueAccountID).Scan(&exists).Error
		if err == nil && exists {
			s.setCached(s.cfgStateRevenueAccountID)
			return s.cfgStateRevenueAccountID, nil
		}
	}

	// (2) fiksni broj računa seed-ovan u 000042
	var idByNumber int64
	_ = s.db.WithContext(ctx).Raw(`
		SELECT id FROM core_banking.racun WHERE broj_racuna = ?
	`, drzavaBrojRacuna).Scan(&idByNumber).Error
	if idByNumber > 0 {
		s.setCached(idByNumber)
		return idByNumber, nil
	}

	// (3) preko email-a → user-service → id_vlasnika → racun(RSD)
	if s.userClient != nil {
		// Ne možemo direktno "email → id" (nema ListClients filter po email-u koji
		// vraća samo jedan rezultat), ali GetClientInfo zahteva id. Pokušaj preko
		// SQL-a: ako postoji bilo koji RSD račun kome je vlasnik user čiji email
		// na user-service strani ne možemo proveriti bez runtime lookup-a, prva opcija
		// i dalje zavisi od toga da je seed-ovan pravi broj računa. Ovde samo
		// iskoristimo unique partial index iz 000042 (uq_drzava_single_account):
		// id_vlasnika = 5 (cross-service convention) i RSD valuta.
		var idByOwner int64
		_ = s.db.WithContext(ctx).Raw(`
			SELECT r.id
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id_vlasnika = 5 AND v.oznaka = 'RSD'
			ORDER BY r.id ASC
			LIMIT 1
		`).Scan(&idByOwner).Error
		if idByOwner > 0 {
			s.setCached(idByOwner)
			return idByOwner, nil
		}
	}

	return 0, ErrStateAccountMissing
}

func (s *TaxService) setCached(id int64) {
	s.stateAccountMu.Lock()
	s.cachedStateID = id
	s.stateAccountMu.Unlock()
}

// ─── Izračun profita ─────────────────────────────────────────────────────────

// computeProfitSQL je jedinstveni upit koji vraća oporezivi profit po
// (user_id, account_id) za dati prozor [$1, $2].
//
// Lineage je striktno po account_id: BUY agregat (wavg_buy) se grupiše po
// (user, account, listing), SELL agregat po istom ključu — tako da se porez
// uvek vezuje za originalni funding račun (orders.account_id).
//
// OTC prodaje idu kroz istu orders tabelu (direction='SELL', status='DONE',
// is_done=TRUE), pa su automatski obuhvaćene.
const computeProfitSQL = `
WITH sell_fills AS (
    SELECT
        o.user_id,
        o.account_id,
        o.listing_id,
        SUM(ot.executed_quantity)                                           AS sold_qty,
        SUM(CAST(ot.executed_price AS FLOAT) * ot.executed_quantity)
            / NULLIF(SUM(ot.executed_quantity), 0)                          AS avg_sell_price,
        COUNT(ot.id)                                                        AS txn_count
    FROM core_banking.order_transactions ot
    JOIN core_banking.orders o       ON o.id = ot.order_id
    JOIN core_banking.listing l      ON l.id = o.listing_id AND l.listing_type = 'STOCK'
    WHERE o.direction = 'SELL'
      AND o.status    = 'DONE'
      AND o.is_done   = TRUE
      AND ot.execution_time >= ?
      AND ot.execution_time <= ?
    GROUP BY o.user_id, o.account_id, o.listing_id
),
buy_avg AS (
    SELECT
        o.user_id,
        o.account_id,
        o.listing_id,
        CASE
            WHEN SUM(ot.executed_quantity) > 0
            THEN SUM(CAST(ot.executed_price AS FLOAT) * ot.executed_quantity)
                 / NULLIF(SUM(ot.executed_quantity), 0)
            ELSE AVG(CAST(o.price_per_unit AS FLOAT))
        END AS avg_buy_price
    FROM core_banking.orders o
    LEFT JOIN core_banking.order_transactions ot ON ot.order_id = o.id
    JOIN core_banking.listing l ON l.id = o.listing_id AND l.listing_type = 'STOCK'
    WHERE o.direction = 'BUY'
      AND o.status    = 'DONE'
      AND o.is_done   = TRUE
    GROUP BY o.user_id, o.account_id, o.listing_id
)
SELECT
    s.user_id,
    s.account_id,
    SUM(
        GREATEST(
            0,
            (s.avg_sell_price - COALESCE(b.avg_buy_price, 0)) * s.sold_qty
        )
    )                                     AS profit_native,
    SUM(s.txn_count)                      AS txn_count
FROM sell_fills s
LEFT JOIN buy_avg b
    ON b.user_id    = s.user_id
   AND b.account_id = s.account_id
   AND b.listing_id = s.listing_id
GROUP BY s.user_id, s.account_id
HAVING SUM(
    GREATEST(
        0,
        (s.avg_sell_price - COALESCE(b.avg_buy_price, 0)) * s.sold_qty
    )
) > 0
`

type profitRow struct {
	UserID       int64   `gorm:"column:user_id"`
	AccountID    int64   `gorm:"column:account_id"`
	ProfitNative float64 `gorm:"column:profit_native"`
	TxnCount     int     `gorm:"column:txn_count"`
}

// ComputeProfitForPeriod izračunava oporezive profite za sve (user, account)
// parove u prozoru [start, end]. Valuta je USD (srednji kurs se povlači jednom
// i primenjuje na sve redove).
func (s *TaxService) ComputeProfitForPeriod(ctx context.Context, start, end time.Time) ([]UserAccountProfit, error) {
	rate, err := s.usdToRSD(ctx)
	if err != nil {
		return nil, err
	}

	var rows []profitRow
	if err := s.db.WithContext(ctx).Raw(computeProfitSQL, start, end).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("compute profit: %w", err)
	}

	taxRate := decimal.NewFromFloat(TaxRate)
	result := make([]UserAccountProfit, 0, len(rows))
	for _, r := range rows {
		if r.ProfitNative <= 0 {
			continue
		}
		profit := decimal.NewFromFloat(r.ProfitNative)
		taxN := profit.Mul(taxRate)
		taxRSD := taxN.Mul(rate)
		result = append(result, UserAccountProfit{
			UserID:         r.UserID,
			AccountID:      r.AccountID,
			SourceCurrency: "USD",
			ProfitNative:   profit,
			TaxNative:      taxN,
			TaxRSD:         taxRSD,
			RateUsed:       rate,
			TxnCount:       r.TxnCount,
		})
	}
	return result, nil
}

// ─── CalculateAndCollectForPeriod ────────────────────────────────────────────

// CalculateAndCollectForPeriod je jedina ulazna tačka za obračun + naplatu za
// zadati prozor. Koristi pg_try_advisory_lock da serijalizuje paralelne pokušaje
// (cron vs manual) za isti kalendarski period.
func (s *TaxService) CalculateAndCollectForPeriod(
	ctx context.Context,
	start, end time.Time,
	triggeredBy string,
) (PeriodSummary, error) {
	summary := PeriodSummary{
		PeriodStart: start,
		PeriodEnd:   end,
	}

	periodLockKey := advisoryKey(fmt.Sprintf("tax:period:%d-%02d", start.Year(), int(start.Month())))

	// Pokušaj dobiti lock; ako ne uspe — drugi obračun je u toku.
	var gotLock bool
	if err := s.db.WithContext(ctx).Raw(`SELECT pg_try_advisory_lock(?)`, periodLockKey).Scan(&gotLock).Error; err != nil {
		return summary, fmt.Errorf("advisory lock: %w", err)
	}
	if !gotLock {
		return summary, ErrPeriodAlreadyRunning
	}
	defer func() {
		_ = s.db.WithContext(context.Background()).Exec(`SELECT pg_advisory_unlock(?)`, periodLockKey).Error
	}()

	stateAccountID, err := s.ResolveStateRevenueAccountID(ctx)
	if err != nil {
		return summary, err
	}

	profits, err := s.ComputeProfitForPeriod(ctx, start, end)
	if err != nil {
		return summary, err
	}

	rates, err := s.exchange.GetRates(ctx)
	if err != nil {
		return summary, fmt.Errorf("fetch rates: %w", err)
	}

	total := decimal.Zero
	for _, p := range profits {
		status, collectedRSD, cerr := s.collectOne(ctx, p, start, end, triggeredBy, stateAccountID, rates)
		if cerr != nil {
			summary.Errors++
			log.Printf("[tax] collect user=%d account=%d error: %v", p.UserID, p.AccountID, cerr)
			continue
		}
		switch status {
		case TaxStatusCollected:
			summary.FullyCollected++
		case TaxStatusPartial:
			summary.Partial++
		case TaxStatusUnpaid:
			summary.Unpaid++
		case "ALREADY":
			summary.AlreadyCollected++
			continue
		}
		summary.ProcessedUsers++
		total = total.Add(collectedRSD)
	}
	summary.TotalCollectedRSD = total
	summary.TotalCollectedF64, _ = total.Float64()
	return summary, nil
}

// ─── Per-row transakciona naplata ────────────────────────────────────────────

// collectOne izvršava obračun i naplatu za jedan (user, account) par u posebnoj
// DB transakciji, sa SELECT FOR UPDATE nad korisnikovim i državnim računom,
// i advisory xact lock-om per (user, account, period) da se spreče paralelni
// pokušaji za isti red tax_records.
//
// Vraća krajnji status ("COLLECTED"|"PARTIAL"|"UNPAID"|"ALREADY") i naplaćen iznos u RSD.
func (s *TaxService) collectOne(
	ctx context.Context,
	p UserAccountProfit,
	start, end time.Time,
	triggeredBy string,
	stateAccountID int64,
	rates []domain.ExchangeRate,
) (string, decimal.Decimal, error) {
	var finalStatus string
	collected := decimal.Zero

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Per-row advisory lock — serijalizuje paralelne pokušaje za isti red.
		rowKey := advisoryKey(fmt.Sprintf("tax:row:%d:%d:%s", p.UserID, p.AccountID, start.Format(time.RFC3339Nano)))
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, rowKey).Error; err != nil {
			return fmt.Errorf("xact lock: %w", err)
		}

		// Cilj naplate za ovaj mesec (ukupno dugovanje, bez obzira na prethodne pokušaje).
		amountTargetRSD := p.TaxRSD
		profitBase := p.ProfitNative
		rate := p.RateUsed

		// Učitaj postojeći red za tačan (user, account, period_start, period_end).
		var existing struct {
			ID                 int64           `gorm:"column:id"`
			AmountRSD          decimal.Decimal `gorm:"column:amount_rsd"`
			PaidAmountRSD      decimal.Decimal `gorm:"column:paid_amount_rsd"`
			RemainingDebtRSD   decimal.Decimal `gorm:"column:remaining_debt_rsd"`
			Status             string          `gorm:"column:status"`
			CollectionAttempts int             `gorm:"column:collection_attempts"`
		}
		if err := tx.Raw(`
			SELECT id, amount_rsd, paid_amount_rsd, remaining_debt_rsd, status, collection_attempts
			FROM core_banking.tax_records
			WHERE user_id = ? AND account_id = ? AND period_start = ? AND period_end = ?
			FOR UPDATE
		`, p.UserID, p.AccountID, start, end).Scan(&existing).Error; err != nil {
			return fmt.Errorf("select tax_records: %w", err)
		}

		// Delta = koliko jos treba naplatiti u RSD. Ako je ≤ 0, nema sta da se radi.
		paidSoFar := decimal.Zero
		if existing.ID != 0 {
			paidSoFar = existing.PaidAmountRSD
		}
		toCollectRSD := amountTargetRSD.Sub(paidSoFar)

		if toCollectRSD.LessThanOrEqual(decimal.Zero) {
			// Uskladi status na COLLECTED ako već nije (ništa se ne skida sa računa,
			// ne prave se transakcije, stanje se ne pomera — čisti idempotent no-op).
			if existing.ID != 0 && existing.Status != TaxStatusCollected {
				if err := tx.Exec(`
					UPDATE core_banking.tax_records
					SET status             = 'COLLECTED',
					    paid               = TRUE,
					    paid_at            = COALESCE(paid_at, NOW()),
					    remaining_debt_rsd = 0,
					    updated_at         = NOW()
					WHERE id = ?
				`, existing.ID).Error; err != nil {
					return err
				}
			}
			finalStatus = "ALREADY"
			return nil
		}

		// Upsert tax_records — amount_rsd se postavlja na trenutni target
		// (moguće je da je veći nego pre, ako je bilo novih prodaja u mesecu).
		if existing.ID == 0 {
			if err := tx.Exec(`
				INSERT INTO core_banking.tax_records
				    (user_id, account_id, year, month,
				     period_start, period_end,
				     amount_rsd, paid_amount_rsd, remaining_debt_rsd,
				     profit_base_amount, source_currency, exchange_rate_used,
				     taxable_transactions_cnt,
				     status, collection_attempts, last_attempt_at, triggered_by,
				     paid, paid_at, updated_at)
				VALUES (?, ?, ?, ?,
				        ?, ?,
				        ?, 0, ?,
				        ?, ?, ?,
				        ?,
				        ?, 1, NOW(), ?,
				        FALSE, NULL, NOW())
			`,
				p.UserID, p.AccountID, start.Year(), int(start.Month()),
				start, end,
				amountTargetRSD.StringFixed(4), amountTargetRSD.StringFixed(4),
				profitBase.StringFixed(4), p.SourceCurrency, rate.StringFixed(6),
				p.TxnCount,
				TaxStatusOpen, triggeredBy,
			).Error; err != nil {
				return fmt.Errorf("insert tax_records: %w", err)
			}
		} else {
			if err := tx.Exec(`
				UPDATE core_banking.tax_records
				SET amount_rsd               = ?,
				    remaining_debt_rsd       = ?,
				    profit_base_amount       = ?,
				    exchange_rate_used       = ?,
				    taxable_transactions_cnt = ?,
				    collection_attempts      = collection_attempts + 1,
				    last_attempt_at          = NOW(),
				    triggered_by             = ?,
				    updated_at               = NOW()
				WHERE id = ?
			`, amountTargetRSD.StringFixed(4), toCollectRSD.StringFixed(4),
				profitBase.StringFixed(4), rate.StringFixed(6), p.TxnCount,
				triggeredBy, existing.ID).Error; err != nil {
				return fmt.Errorf("update tax_records target: %w", err)
			}
		}

		// SELECT FOR UPDATE korisnikovog računa.
		var acc struct {
			ID     int64   `gorm:"column:id"`
			Stanje float64 `gorm:"column:stanje_racuna"`
			Oznaka string  `gorm:"column:oznaka"`
		}
		if err := tx.Raw(`
			SELECT r.id, r.stanje_racuna, v.oznaka
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id = ?
			FOR UPDATE
		`, p.AccountID).Scan(&acc).Error; err != nil {
			return fmt.Errorf("lock user account: %w", err)
		}
		if acc.ID == 0 {
			return fmt.Errorf("user account %d not found", p.AccountID)
		}

		// SELECT FOR UPDATE državnog računa.
		var stateAcc struct {
			ID     int64   `gorm:"column:id"`
			Stanje float64 `gorm:"column:stanje_racuna"`
		}
		if err := tx.Raw(`
			SELECT id, stanje_racuna FROM core_banking.racun WHERE id = ? FOR UPDATE
		`, stateAccountID).Scan(&stateAcc).Error; err != nil {
			return fmt.Errorf("lock state account: %w", err)
		}
		if stateAcc.ID == 0 {
			return ErrStateAccountMissing
		}

		// Koliko treba skinuti u valuti korisničkog računa (samo delta).
		deductionInAcc := rsdToNative(rates, toCollectRSD, acc.Oznaka)
		deductFloat, _ := deductionInAcc.Float64()
		stanjeDec := decimal.NewFromFloat(acc.Stanje)

		var actualDeductNative decimal.Decimal
		var actualCreditRSD decimal.Decimal

		switch {
		case acc.Stanje <= 0:
			// Nema sredstava — UNPAID. Dug ne nestaje (remaining_debt_rsd = toCollectRSD).
			finalStatus = TaxStatusUnpaid
			if err := tx.Exec(`
				UPDATE core_banking.tax_records
				SET status             = 'UNPAID',
				    paid               = FALSE,
				    remaining_debt_rsd = ?,
				    updated_at         = NOW()
				WHERE user_id = ? AND account_id = ? AND period_start = ? AND period_end = ?
			`, toCollectRSD.StringFixed(4), p.UserID, p.AccountID, start, end).Error; err != nil {
				return err
			}
			return nil

		case deductFloat <= acc.Stanje:
			// Puna naplata delte.
			actualDeductNative = deductionInAcc
			actualCreditRSD = toCollectRSD
			finalStatus = TaxStatusCollected

		default:
			// Delimična naplata — skini sve što ima.
			actualDeductNative = stanjeDec
			actualCreditRSD = nativeToRSD(rates, stanjeDec, acc.Oznaka)
			finalStatus = TaxStatusPartial
		}

		actualDeductFloat, _ := actualDeductNative.Float64()
		if actualDeductFloat <= 0 {
			// Pod-par float — tretiraj kao UNPAID (izbegava „phantom" 0-RSD transakcije).
			finalStatus = TaxStatusUnpaid
			if err := tx.Exec(`
				UPDATE core_banking.tax_records
				SET status             = 'UNPAID',
				    paid               = FALSE,
				    remaining_debt_rsd = ?,
				    updated_at         = NOW()
				WHERE user_id = ? AND account_id = ? AND period_start = ? AND period_end = ?
			`, toCollectRSD.StringFixed(4), p.UserID, p.AccountID, start, end).Error; err != nil {
				return err
			}
			return nil
		}

		// Debit user.
		if err := tx.Exec(`
			UPDATE core_banking.racun
			SET stanje_racuna = stanje_racuna - ?
			WHERE id = ?
		`, actualDeductFloat, p.AccountID).Error; err != nil {
			return fmt.Errorf("debit user: %w", err)
		}

		// Credit state (u RSD ekvivalentu skinutog iznosa).
		creditRSDFloat, _ := actualCreditRSD.Float64()
		if err := tx.Exec(`
			UPDATE core_banking.racun
			SET stanje_racuna = stanje_racuna + ?
			WHERE id = ?
		`, creditRSDFloat, stateAccountID).Error; err != nil {
			return fmt.Errorf("credit state: %w", err)
		}

		// Evidentiraj transakcije na obe strane.
		if err := tx.Exec(`
			INSERT INTO core_banking.transakcija
			    (racun_id, tip_transakcije, iznos, opis, vreme_izvrsavanja, status)
			VALUES (?, 'ISPLATA', ?, 'Porez na kapitalnu dobit', NOW(), 'IZVRSEN')
		`, p.AccountID, actualDeductFloat).Error; err != nil {
			return fmt.Errorf("isplata tx: %w", err)
		}
		if err := tx.Exec(`
			INSERT INTO core_banking.transakcija
			    (racun_id, tip_transakcije, iznos, opis, vreme_izvrsavanja, status)
			VALUES (?, 'UPLATA', ?, 'Porez na kapitalnu dobit (prijem državnog računa)', NOW(), 'IZVRSEN')
		`, stateAccountID, creditRSDFloat).Error; err != nil {
			return fmt.Errorf("uplata tx: %w", err)
		}

		// Update tax_records paid_amount_rsd / remaining / status.
		newPaid := paidSoFar.Add(actualCreditRSD)
		newRemaining := amountTargetRSD.Sub(newPaid)
		if newRemaining.LessThan(decimal.Zero) {
			newRemaining = decimal.Zero
		}

		if finalStatus == TaxStatusCollected {
			if err := tx.Exec(`
				UPDATE core_banking.tax_records
				SET paid_amount_rsd    = ?,
				    remaining_debt_rsd = 0,
				    status             = 'COLLECTED',
				    paid               = TRUE,
				    paid_at            = NOW(),
				    updated_at         = NOW()
				WHERE user_id = ? AND account_id = ? AND period_start = ? AND period_end = ?
			`, newPaid.StringFixed(4), p.UserID, p.AccountID, start, end).Error; err != nil {
				return fmt.Errorf("update collected: %w", err)
			}
		} else { // PARTIAL
			if err := tx.Exec(`
				UPDATE core_banking.tax_records
				SET paid_amount_rsd    = ?,
				    remaining_debt_rsd = ?,
				    status             = 'PARTIAL',
				    paid               = FALSE,
				    updated_at         = NOW()
				WHERE user_id = ? AND account_id = ? AND period_start = ? AND period_end = ?
			`, newPaid.StringFixed(4), newRemaining.StringFixed(4),
				p.UserID, p.AccountID, start, end).Error; err != nil {
				return fmt.Errorf("update partial: %w", err)
			}
		}

		collected = actualCreditRSD
		return nil
	})

	if err != nil {
		return "", decimal.Zero, err
	}
	return finalStatus, collected, nil
}

// ─── Listing eligible korisnika za tax tracking portal ───────────────────────

// ListTaxEligibleUsers vraća sve trgovačke korisnike (CLIENT koji su imali
// orders, + svi aktuari iz actuary_info). Uključuje one bez duga (taxDebt=0).
// Filteri: role (client/actuary), firstName, lastName (case-insensitive substring).
//
// Dug se računa kao zbir:
//
//	(a) remaining_debt_rsd iz tax_records (OPEN | PARTIAL | UNPAID) tekuće godine
//	(b) + procena oporezivog profita za tekući mesec (samo za prodaje koje još nisu
//	    pokrivene postojećim tax_records redom), preko ComputeProfitForPeriod.
func (s *TaxService) ListTaxEligibleUsers(ctx context.Context, filter TaxUserFilter) ([]TaxUserRow, error) {
	now := time.Now()
	currentYear := now.Year()

	// 1) Svi client user_id iz orders (anyone who has actually traded).
	type clientRow struct {
		UserID int64 `gorm:"column:user_id"`
	}
	var clientRows []clientRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT o.user_id
		FROM core_banking.orders o
		LEFT JOIN core_banking.actuary_info a ON a.employee_id = o.user_id
		WHERE a.employee_id IS NULL
	`).Scan(&clientRows).Error; err != nil {
		return nil, fmt.Errorf("list trading clients: %w", err)
	}

	// 2) Svi aktuari iz actuary_info.
	type actuaryRow struct {
		EmployeeID int64 `gorm:"column:employee_id"`
	}
	var actuaryRows []actuaryRow
	if err := s.db.WithContext(ctx).Raw(`SELECT employee_id FROM core_banking.actuary_info`).Scan(&actuaryRows).Error; err != nil {
		return nil, fmt.Errorf("list actuaries: %w", err)
	}

	userType := make(map[int64]string)
	for _, c := range clientRows {
		userType[c.UserID] = "CLIENT"
	}
	for _, a := range actuaryRows {
		userType[a.EmployeeID] = "ACTUARY"
	}

	// 3) Dugovanja iz tax_records za tekuću godinu.
	type debtRow struct {
		UserID int64   `gorm:"column:user_id"`
		Debt   float64 `gorm:"column:debt"`
	}
	var debtRows []debtRow
	_ = s.db.WithContext(ctx).Raw(`
		SELECT user_id, COALESCE(SUM(remaining_debt_rsd), 0) AS debt
		FROM core_banking.tax_records
		WHERE EXTRACT(YEAR FROM period_start) = ?
		  AND status IN ('OPEN', 'PARTIAL', 'UNPAID')
		GROUP BY user_id
	`, currentYear).Scan(&debtRows).Error
	debtMap := make(map[int64]decimal.Decimal, len(debtRows))
	for _, d := range debtRows {
		debtMap[d.UserID] = decimal.NewFromFloat(d.Debt)
	}

	// 4) Procena za tekući mesec (prodaje koje još nisu pokrivene tax_records redom).
	cmStart, cmEnd := CurrentMonthWindow(now)
	profits, err := s.ComputeProfitForPeriod(ctx, cmStart, cmEnd)
	if err == nil {
		// Ukloni deo koji je već u tax_records za taj isti period (da ne dupliramo).
		type existingRow struct {
			UserID    int64   `gorm:"column:user_id"`
			AmountRSD float64 `gorm:"column:amount_rsd"`
		}
		var existing []existingRow
		_ = s.db.WithContext(ctx).Raw(`
			SELECT user_id, COALESCE(SUM(amount_rsd), 0) AS amount_rsd
			FROM core_banking.tax_records
			WHERE period_start >= ? AND period_end <= ?
			GROUP BY user_id
		`, cmStart, cmEnd).Scan(&existing).Error
		coveredMap := make(map[int64]decimal.Decimal, len(existing))
		for _, e := range existing {
			coveredMap[e.UserID] = decimal.NewFromFloat(e.AmountRSD)
		}

		aggProfit := make(map[int64]decimal.Decimal)
		for _, p := range profits {
			aggProfit[p.UserID] = aggProfit[p.UserID].Add(p.TaxRSD)
		}
		for uid, total := range aggProfit {
			uncovered := total.Sub(coveredMap[uid])
			if uncovered.GreaterThan(decimal.Zero) {
				debtMap[uid] = debtMap[uid].Add(uncovered)
			}
		}
	}

	// 5) Primeni role filter i enrich imenima.
	roleFilter := strings.ToLower(strings.TrimSpace(filter.Role))
	firstFilter := strings.ToLower(strings.TrimSpace(filter.FirstName))
	lastFilter := strings.ToLower(strings.TrimSpace(filter.LastName))

	out := make([]TaxUserRow, 0, len(userType))
	for uid, ut := range userType {
		if roleFilter == "client" && ut != "CLIENT" {
			continue
		}
		if roleFilter == "actuary" && ut != "ACTUARY" {
			continue
		}

		first, last, email := "", "", ""
		if s.userClient != nil {
			info, err := s.userClient.GetClientInfo(ctx, uid)
			if err == nil && info != nil {
				first = info.FirstName
				last = info.LastName
				email = info.Email
			}
		}

		// Preskoči redove bez validnog imena (npr. osirotele user_id-jeve iz
		// orders tabele, ili aktuari za koje GetClientInfo ne vraća rezultat).
		// Supervizor ne dobija informativnu vrednost iz anonimnih redova.
		if strings.TrimSpace(first) == "" && strings.TrimSpace(last) == "" {
			continue
		}

		// Sistemski nalog države nikad ne sme da se pojavi u tax tracking listi.
		if strings.EqualFold(strings.TrimSpace(email), drzavaEmail) {
			continue
		}

		if firstFilter != "" && !strings.Contains(strings.ToLower(first), firstFilter) {
			continue
		}
		if lastFilter != "" && !strings.Contains(strings.ToLower(last), lastFilter) {
			continue
		}

		debt, _ := debtMap[uid].Float64()
		out = append(out, TaxUserRow{
			UserID:    uid,
			UserType:  ut,
			FirstName: first,
			LastName:  last,
			Email:     email,
			TaxDebt:   debt,
		})
	}

	return out, nil
}

// ─── Portfolio helpers (za PortfolioHandler) ─────────────────────────────────

// UserTaxPaidForYear vraća zbir plaćenog poreza u RSD za korisnika u zadatoj godini.
func (s *TaxService) UserTaxPaidForYear(ctx context.Context, userID int64, year int) (float64, error) {
	var total float64
	err := s.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(paid_amount_rsd), 0)
		FROM core_banking.tax_records
		WHERE user_id = ? AND EXTRACT(YEAR FROM period_start) = ?
	`, userID, year).Scan(&total).Error
	return total, err
}

// UserTaxUnpaidForMonth vraća zbir neplaćenog poreza u RSD za korisnika u
// datom mesecu. Uključuje:
//   - remaining_debt_rsd iz postojećih tax_records (OPEN | PARTIAL | UNPAID)
//   - procenu neobračunatog dela (prodaje u tom mesecu koje nisu upisane kao tax_record)
func (s *TaxService) UserTaxUnpaidForMonth(ctx context.Context, userID int64, start, end time.Time) (float64, error) {
	var recorded float64
	if err := s.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(remaining_debt_rsd), 0)
		FROM core_banking.tax_records
		WHERE user_id = ?
		  AND period_start >= ?
		  AND period_end   <= ?
		  AND status IN ('OPEN', 'PARTIAL', 'UNPAID')
	`, userID, start, end).Scan(&recorded).Error; err != nil {
		return 0, err
	}

	// Procena neobračunatog dela u istom prozoru.
	profits, err := s.ComputeProfitForPeriod(ctx, start, end)
	if err != nil {
		// Ako kurs nije dostupan, vrati samo deo iz tax_records — bolje nego pucati.
		return recorded, nil
	}

	var covered float64
	_ = s.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(amount_rsd), 0)
		FROM core_banking.tax_records
		WHERE user_id = ?
		  AND period_start >= ?
		  AND period_end   <= ?
	`, userID, start, end).Scan(&covered).Error

	var totalEstimated float64
	for _, p := range profits {
		if p.UserID != userID {
			continue
		}
		f, _ := p.TaxRSD.Float64()
		totalEstimated += f
	}

	uncovered := totalEstimated - covered
	if uncovered < 0 {
		uncovered = 0
	}
	return recorded + uncovered, nil
}

// ─── Helperi ─────────────────────────────────────────────────────────────────

// usdToRSD dohvata srednji USD→RSD kurs (bez provizije). Greška ako nedostupan.
func (s *TaxService) usdToRSD(ctx context.Context) (decimal.Decimal, error) {
	rates, err := s.exchange.GetRates(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("exchange rates: %w", err)
	}
	for _, r := range rates {
		if r.Oznaka == "USD" && r.Srednji > 0 {
			return decimal.NewFromFloat(r.Srednji), nil
		}
	}
	return decimal.Zero, ErrUSDRateUnavailable
}

// nativeToRSD konvertuje iznos iz valute računa u RSD preko srednjeg kursa.
func nativeToRSD(rates []domain.ExchangeRate, amount decimal.Decimal, currency string) decimal.Decimal {
	if currency == "RSD" {
		return amount
	}
	for _, r := range rates {
		if r.Oznaka == currency && r.Srednji > 0 {
			return amount.Mul(decimal.NewFromFloat(r.Srednji))
		}
	}
	return amount
}

// rsdToNative konvertuje RSD iznos u valutu računa preko srednjeg kursa.
func rsdToNative(rates []domain.ExchangeRate, rsdAmount decimal.Decimal, currency string) decimal.Decimal {
	if currency == "RSD" {
		return rsdAmount
	}
	for _, r := range rates {
		if r.Oznaka == currency && r.Srednji > 0 {
			return rsdAmount.Div(decimal.NewFromFloat(r.Srednji))
		}
	}
	return rsdAmount
}

// advisoryKey mapira string na int64 za pg_*_advisory_lock.
func advisoryKey(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64()) // signed overflow je OK za advisory lock ključ
}
