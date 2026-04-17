package repository

import (
	"context"
	"fmt"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"
	tradingworker "banka-backend/services/bank-service/internal/trading/worker"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type fundsManager struct {
	db              *gorm.DB
	exchangeService domain.ExchangeService
	// provizijaRate je stopa FX provizije koja se naplaćuje klijentima
	// pri konverziji USD iznosa u valutu njihovog računa (npr. 0.005 = 0.5%).
	// Odgovara EXCHANGE_PROVIZIJA_RATE konfiguracionoj vrednosti.
	// Primenjuje se u convertUSDToAccountCurrency kada targetCurrency != "USD".
	provizijaRate float64
}

// NewFundsManager vraća implementaciju trading.FundsManager koja direktno
// ažurira core_banking.racun u jednoj SQL naredbi (atomično).
// exchangeService se koristi za konverziju USD iznosa u valutu klijentskog računa.
// provizijaRate je stopa FX provizije (npr. 0.005 za 0.5%) koja se naplaćuje
// klijentima pri svakom plaćanju u ne-USD valuti.
func NewFundsManager(db *gorm.DB, exchangeService domain.ExchangeService, provizijaRate float64) trading.FundsManager {
	return &fundsManager{db: db, exchangeService: exchangeService, provizijaRate: provizijaRate}
}

// accountCurrency vraća ISO kod valute za dati račun.
// Vraća "USD" kao fallback ako dohvat ne uspe (konzervativno).
func (f *fundsManager) accountCurrency(ctx context.Context, accountID int64) string {
	var currency string
	f.db.WithContext(ctx).Raw(`
		SELECT v.oznaka FROM core_banking.racun r
		JOIN core_banking.valuta v ON v.id = r.id_valute
		WHERE r.id = ?
	`, accountID).Scan(&currency)
	if currency == "" {
		return "USD"
	}
	return currency
}

// convertUSDToAccountCurrency konvertuje USD iznos u valutu računa.
//
// isDebit određuje smer provizije:
//   - true  (klijent PLAĆA — buy, commission, reserve): klijent plaća više → × (1 + provizija)
//   - false (klijent PRIMA — sell credit): klijent prima manje → × (1 - provizija)
//
// Za klijente (isClient=true):
//   - USD → RSD: prodajni kurs USD (banka prodaje USD klijentu — nepovoljniji po klijenta)
//   - RSD → target: kupovni kurs target valute (banka kupuje target valutu od klijenta)
//   - Primenjuje se FX provizija u odgovarajućem smeru.
//
// Za zaposlene i sistem: srednji kurs, bez provizije.
func (f *fundsManager) convertUSDToAccountCurrency(ctx context.Context, usdAmount decimal.Decimal, targetCurrency string, isDebit bool) decimal.Decimal {
	if targetCurrency == "USD" {
		return usdAmount
	}

	isClient := tradingworker.IsClientFromCtx(ctx)

	rates, err := f.exchangeService.GetRates(ctx)
	if err != nil {
		log.Printf("[funds_manager] nije moguće dohvatiti kurseve za konverziju: %v — koristi se USD iznos", err)
		return usdAmount
	}

	// USD → RSD
	// Klijenti koriste prodajni kurs (banka prodaje USD), zaposleni srednji.
	var usdToRSD float64
	for _, r := range rates {
		if r.Oznaka == "USD" {
			if isClient && r.Prodajni > 0 {
				usdToRSD = r.Prodajni
			} else {
				usdToRSD = r.Srednji
			}
			break
		}
	}
	if usdToRSD <= 0 {
		return usdAmount
	}
	rsdAmount := usdAmount.Mul(decimal.NewFromFloat(usdToRSD))

	var applyProvizija func(d decimal.Decimal) decimal.Decimal
	if isClient && f.provizijaRate > 0 {
		if isDebit {
			// Klijent plaća — provizija povećava iznos koji se skida
			applyProvizija = func(d decimal.Decimal) decimal.Decimal {
				return d.Mul(decimal.NewFromFloat(1 + f.provizijaRate))
			}
		} else {
			// Klijent prima — provizija smanjuje iznos koji se dodaje
			applyProvizija = func(d decimal.Decimal) decimal.Decimal {
				return d.Mul(decimal.NewFromFloat(1 - f.provizijaRate))
			}
		}
	} else {
		applyProvizija = func(d decimal.Decimal) decimal.Decimal { return d }
	}

	if targetCurrency == "RSD" {
		return applyProvizija(rsdAmount)
	}

	// RSD → target currency.
	// Klijenti: kupovni kurs (banka kupuje target valutu od klijenta).
	// Zaposleni: srednji kurs.
	var rsdToTarget float64
	for _, r := range rates {
		if r.Oznaka == targetCurrency {
			if isClient && r.Kupovni > 0 {
				rsdToTarget = r.Kupovni
			} else if r.Srednji > 0 {
				rsdToTarget = r.Srednji
			}
			break
		}
	}
	if rsdToTarget <= 0 {
		log.Printf("[funds_manager] valuta %q nije pronađena u kursnoj listi — koristi se RSD iznos", targetCurrency)
		return applyProvizija(rsdAmount)
	}

	return applyProvizija(rsdAmount.Div(decimal.NewFromFloat(rsdToTarget)))
}

// ReserveFunds atomično provjerava i povećava rezervisana_sredstva u jednom SQL iskazu.
// BUG-1 fix: Prethodni kod je radio SELECT (HasSufficientFunds) pa UPDATE (ReserveFunds)
// kao dvije odvojene operacije — TOCTOU race je dozvoljavao over-reservation.
// Sada jedan conditional UPDATE garantuje: provjera i rezervacija su nedjeljivi.
// Ako slobodna sredstva (stanje − rezervisano) nisu dovoljna, RowsAffected == 0
// i vraćamo ErrInsufficientFunds bez ikakve promjene na računu.
func (f *fundsManager) ReserveFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, usdAmount, currency, true)
	result := f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET rezervisana_sredstva = rezervisana_sredstva + ?
		 WHERE id = ?
		   AND (stanje_racuna - rezervisana_sredstva) >= ?`,
		debit.InexactFloat64(), accountID, debit.InexactFloat64(),
	)
	if result.Error != nil {
		return fmt.Errorf("rezervacija sredstava za račun %d: %w", accountID, result.Error)
	}
	if result.RowsAffected == 0 {
		return trading.ErrInsufficientFunds
	}
	return nil
}

// ReleaseFunds smanjuje rezervisana_sredstva za dati iznos (ne ide ispod 0).
// amount je u USD; konvertuje se u valutu računa.
func (f *fundsManager) ReleaseFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, usdAmount, currency, true)
	result := f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
		 WHERE id = ?`,
		debit.InexactFloat64(), accountID,
	)
	if result.Error != nil {
		return fmt.Errorf("oslobađanje sredstava za račun %d: %w", accountID, result.Error)
	}
	return nil
}

// SettleBuyFill atomično smanjuje i stanje_racuna i rezervisana_sredstva
// za iznos konvertovan u valutu računa. Kreira i zapis u transakcija tabeli.
// amount je u USD (valuta berze); konvertuje se u valutu računa pre oduzimanja.
//
// Za klijente sa ne-USD računom koristi se 3-struko knjiženje kroz bankine trezorske račune
// (analogno menjačnici), čime se evidentira konverzija i provizija:
//  1. Zaduži klijentski račun za debit (u valuti računa, npr. EUR)
//  2. Odobri bankin trezor FROM (prima klijentovu valutu)
//  3. Zaduži bankin USD trezor (banka "plaća" USD za hartiju)
//
// Za zaposlene i USD račune koristi se direktno knjiženje bez posredničkih računa.
func (f *fundsManager) SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, amount, currency, true)
	isClient := tradingworker.IsClientFromCtx(ctx)

	// Zaposleni ili USD račun: direktno knjiženje (bez posrednika).
	// BUG-5 fix: Prethodni kod je radio UPDATE pa CREATE u odvojenim pozivima — ako
	// CREATE ne uspije, saldo je već zaduzen bez audit zapisa. Sada su oboje u transakciji.
	if !isClient || currency == "USD" {
		return f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Exec(
				`UPDATE core_banking.racun
				 SET stanje_racuna        = stanje_racuna - ?,
				     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
				 WHERE id = ?
				   AND stanje_racuna >= ?`,
				debit.InexactFloat64(), debit.InexactFloat64(), accountID, debit.InexactFloat64(),
			)
			if res.Error != nil {
				return fmt.Errorf("namirenje BUY filla za račun %d: %w", accountID, res.Error)
			}
			if res.RowsAffected == 0 {
				return trading.ErrInsufficientFunds
			}
			if err := tx.Create(&transakcijaModel{
				RacunID:          accountID,
				TipTransakcije:   "ISPLATA",
				Iznos:            debit.InexactFloat64(),
				Opis:             "Kupovina hartije od vrednosti",
				VremeIzvrsavanja: time.Now().UTC(),
				Status:           "IZVRSEN",
			}).Error; err != nil {
				return fmt.Errorf("audit zapis BUY filla za račun %d: %w", accountID, err)
			}
			return nil
		})
	}

	// Klijent sa ne-USD računom: 3-struko knjiženje kroz bankine trezorske račune.
	trezorFromID := f.fetchCurrencyTrezorID(ctx, currency)
	trezorUSDID := f.fetchCurrencyTrezorID(ctx, "USD")

	if trezorFromID == 0 || trezorUSDID == 0 {
		log.Printf("[funds_manager] trezorski račun nije pronađen (valuta=%s, USD trezor=%d) — direktno knjiženje", currency, trezorUSDID)
		result := f.db.WithContext(ctx).Exec(
			`UPDATE core_banking.racun
			 SET stanje_racuna        = stanje_racuna - ?,
			     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
			 WHERE id = ?
			   AND stanje_racuna >= ?`,
			debit.InexactFloat64(), debit.InexactFloat64(), accountID, debit.InexactFloat64(),
		)
		if result.Error != nil {
			return fmt.Errorf("namirenje BUY filla za račun %d: %w", accountID, result.Error)
		}
		if result.RowsAffected == 0 {
			return trading.ErrInsufficientFunds
		}
		f.db.WithContext(ctx).Create(&transakcijaModel{
			RacunID:          accountID,
			TipTransakcije:   "ISPLATA",
			Iznos:            debit.InexactFloat64(),
			Opis:             "Kupovina hartije od vrednosti",
			VremeIzvrsavanja: time.Now().UTC(),
			Status:           "IZVRSEN",
		})
		return nil
	}

	now := time.Now().UTC()
	opis := fmt.Sprintf("Kupovina hartije od vrednosti: %.6g USD → %.6g %s", amount.InexactFloat64(), debit.InexactFloat64(), currency)

	return f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Zaduži klijentski račun (stanje + rezervisana sredstva).
		res := tx.Exec(
			`UPDATE core_banking.racun
			 SET stanje_racuna        = stanje_racuna - ?,
			     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
			 WHERE id = ?
			   AND stanje_racuna >= ?`,
			debit.InexactFloat64(), debit.InexactFloat64(), accountID, debit.InexactFloat64(),
		)
		if res.Error != nil {
			return fmt.Errorf("zaduži klijentski račun %d: %w", accountID, res.Error)
		}
		if res.RowsAffected == 0 {
			return trading.ErrInsufficientFunds
		}

		// 2. Odobri bankin trezor FROM (prima klijentovu valutu, npr. EUR).
		if err := tx.Exec(
			`UPDATE core_banking.racun SET stanje_racuna = stanje_racuna + ? WHERE id = ?`,
			debit.InexactFloat64(), trezorFromID,
		).Error; err != nil {
			return fmt.Errorf("odobri trezor %s: %w", currency, err)
		}

		// 3. Zaduži bankin USD trezor (banka "plaća" USD za hartiju od vrednosti).
		if err := tx.Exec(
			`UPDATE core_banking.racun SET stanje_racuna = stanje_racuna - ? WHERE id = ?`,
			amount.InexactFloat64(), trezorUSDID,
		).Error; err != nil {
			return fmt.Errorf("zaduži USD trezor: %w", err)
		}

		// Audit trail: 3 transakcije.
		entries := []transakcijaModel{
			{
				RacunID:          accountID,
				TipTransakcije:   "ISPLATA",
				Iznos:            debit.InexactFloat64(),
				Opis:             opis,
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			},
			{
				RacunID:          trezorFromID,
				TipTransakcije:   "UPLATA",
				Iznos:            debit.InexactFloat64(),
				Opis:             opis,
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			},
			{
				RacunID:          trezorUSDID,
				TipTransakcije:   "ISPLATA",
				Iznos:            amount.InexactFloat64(),
				Opis:             opis,
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			},
		}
		if err := tx.Create(&entries).Error; err != nil {
			return fmt.Errorf("upiši transakcije BUY filla: %w", err)
		}
		return nil
	})
}

// CreditSellFill povećava stanje_racuna za iznos konvertovan u valutu računa.
// amount je u USD (valuta berze); konvertuje se u valutu računa pre dodavanja.
func (f *fundsManager) CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	credit := f.convertUSDToAccountCurrency(ctx, amount, currency, false)

	result := f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET stanje_racuna = stanje_racuna + ?
		 WHERE id = ?`,
		credit.InexactFloat64(), accountID,
	)
	if result.Error != nil {
		return fmt.Errorf("kredit SELL filla za račun %d: %w", accountID, result.Error)
	}
	f.db.WithContext(ctx).Create(&transakcijaModel{
		RacunID:          accountID,
		TipTransakcije:   "UPLATA",
		Iznos:            credit.InexactFloat64(),
		Opis:             "Prodaja hartije od vrednosti",
		VremeIzvrsavanja: time.Now().UTC(),
		Status:           "IZVRSEN",
	})
	return nil
}

// HasSufficientFunds vraća true kada slobodna sredstva računa (stanje − rezervisano),
// konvertovana u valutu računa, pokrivaju traženi USD iznos.
// Isti princip kao margin_checker, ali eksponiran kroz FundsManager koji već
// posjeduje db i exchangeService — nema potrebe za posebnim tipom.
func (f *fundsManager) HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error) {
	var row struct {
		StanjeRacuna       string `gorm:"column:stanje_racuna"`
		RezervovanaSredstva string `gorm:"column:rezervisana_sredstva"`
	}
	err := f.db.WithContext(ctx).
		Table("core_banking.racun").
		Select("stanje_racuna, rezervisana_sredstva").
		Where("id = ?", accountID).
		First(&row).Error
	if err != nil {
		return false, fmt.Errorf("provjera sredstava za račun %d: %w", accountID, err)
	}

	stanje, err := decimal.NewFromString(row.StanjeRacuna)
	if err != nil {
		return false, fmt.Errorf("parse stanje_racuna: %w", err)
	}
	rezervisano, err := decimal.NewFromString(row.RezervovanaSredstva)
	if err != nil {
		return false, fmt.Errorf("parse rezervisana_sredstva: %w", err)
	}

	currency := f.accountCurrency(ctx, accountID)
	required := f.convertUSDToAccountCurrency(ctx, usdAmount, currency, true)

	slobodna := stanje.Sub(rezervisano)
	return slobodna.GreaterThanOrEqual(required), nil
}

// HasSufficientFreeBalance vraća true kada slobodna sredstva računa (stanje − rezervisano)
// pokrivaju traženi iznos u matičnoj valuti računa (bez konverzije).
// Koristi se za FOREX SELL pre-validaciju.
func (f *fundsManager) HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	var row struct {
		StanjeRacuna        string `gorm:"column:stanje_racuna"`
		RezervovanaSredstva string `gorm:"column:rezervisana_sredstva"`
	}
	err := f.db.WithContext(ctx).
		Table("core_banking.racun").
		Select("stanje_racuna, rezervisana_sredstva").
		Where("id = ?", accountID).
		First(&row).Error
	if err != nil {
		return false, fmt.Errorf("provjera slobodnog balansa za račun %d: %w", accountID, err)
	}
	stanje, err := decimal.NewFromString(row.StanjeRacuna)
	if err != nil {
		return false, fmt.Errorf("parse stanje_racuna: %w", err)
	}
	rezervisano, err := decimal.NewFromString(row.RezervovanaSredstva)
	if err != nil {
		return false, fmt.Errorf("parse rezervisana_sredstva: %w", err)
	}
	return stanje.Sub(rezervisano).GreaterThanOrEqual(required), nil
}

// ConvertUSDToRSD konvertuje USD iznos u RSD koristeći srednji kurs (bez provizije).
// Koristi se pri proveri dnevnog limita agenta — limit je u RSD, nalog u USD.
func (f *fundsManager) ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error) {
	rates, err := f.exchangeService.GetRates(ctx)
	if err != nil {
		return usdAmount, fmt.Errorf("dohvatanje kurseva za USD→RSD konverziju: %w", err)
	}
	for _, r := range rates {
		if r.Oznaka == "USD" && r.Srednji > 0 {
			return usdAmount.Mul(decimal.NewFromFloat(r.Srednji)), nil
		}
	}
	// USD kurs nije pronađen — vrati originalni iznos kao fallback
	log.Printf("[funds_manager] USD kurs nije pronađen u kursnoj listi — limit se provjerava u USD")
	return usdAmount, nil
}

// bankTrezorAccountID pronalazi ID bankinog USD trezor računa putem broja računa.
// Broj "666000122200000008" je hardkodiran — isti kao u TradingCreateOrder handleru.
// Vraća 0 ako račun nije pronađen (u tom slučaju provizija se ne prebacuje).
func (f *fundsManager) bankTrezorAccountID(ctx context.Context) int64 {
	var id int64
	f.db.WithContext(ctx).Raw(
		`SELECT id FROM core_banking.racun WHERE broj_racuna = ?`,
		"666000122200000008",
	).Scan(&id)
	return id
}

// fetchCurrencyTrezorID pronalazi ID bankinog trezorskog računa za datu valutu.
// Trezorski računi su vlasništvo korisnika sa id=2 (trezor@exbanka.rs).
// Vraća 0 ako trezorski račun za tu valutu nije pronađen.
func (f *fundsManager) fetchCurrencyTrezorID(ctx context.Context, currencyOznaka string) int64 {
	var id int64
	f.db.WithContext(ctx).Raw(`
		SELECT ra.id
		FROM core_banking.racun ra
		JOIN core_banking.valuta v ON v.id = ra.id_valute
		WHERE ra.id_vlasnika = 2
		  AND v.oznaka = ?
		  AND ra.status = 'AKTIVAN'
		LIMIT 1
	`, currencyOznaka).Scan(&id)
	return id
}

// ChargeCommission naplaćuje proviziju za trading nalog.
//
// Klijenti: skida se sa klijentovog računa (konvertovano u valutu računa),
// a ekvivalentni iznos se uplaćuje na bankin trezor u istoj valuti.
//
// Zaposleni/supervizori/agenti: skida se direktno sa bankinog USD trezor računa
// (provizija je operativni trošak banke pri kupovini hartija).
func (f *fundsManager) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	if !tradingworker.IsClientFromCtx(ctx) {
		// Zaposleni: provizija se skida sa bankinog USD trezor računa.
		trezorID := f.fetchCurrencyTrezorID(ctx, "USD")
		if trezorID == 0 {
			log.Printf("[funds_manager] bankin USD trezor nije pronađen — provizija za zaposlenog se preskače")
			return nil
		}
		opis := fmt.Sprintf("Provizija za hartiju od vrednosti (%.6g USD)", amount.InexactFloat64())
		now := time.Now().UTC()
		return f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(
				`UPDATE core_banking.racun SET stanje_racuna = stanje_racuna - ? WHERE id = ?`,
				amount.InexactFloat64(), trezorID,
			).Error; err != nil {
				return fmt.Errorf("naplata provizije sa USD trezora: %w", err)
			}
			return tx.Create(&transakcijaModel{
				RacunID:          trezorID,
				TipTransakcije:   "ISPLATA",
				Iznos:            amount.InexactFloat64(),
				Opis:             opis,
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			}).Error
		})
	}

	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, amount, currency, true)

	// Trezor koji odgovara valuti klijentovog računa (EUR trezor za EUR račun, itd.)
	trezorID := f.fetchCurrencyTrezorID(ctx, currency)
	if trezorID == 0 {
		log.Printf("[funds_manager] trezorski račun za valutu %s nije pronađen — provizija se naplaćuje bez prebacivanja", currency)
	}

	opis := fmt.Sprintf("Provizija za hartiju od vrednosti (%.6g USD → %.6g %s)", amount.InexactFloat64(), debit.InexactFloat64(), currency)
	now := time.Now().UTC()
	return f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Debetuj klijentov račun za konvertovani iznos provizije.
		if err := tx.Exec(
			`UPDATE core_banking.racun SET stanje_racuna = stanje_racuna - ? WHERE id = ?`,
			debit.InexactFloat64(), accountID,
		).Error; err != nil {
			return fmt.Errorf("naplata provizije za račun %d: %w", accountID, err)
		}
		if err := tx.Create(&transakcijaModel{
			RacunID:          accountID,
			TipTransakcije:   "ISPLATA",
			Iznos:            debit.InexactFloat64(),
			Opis:             opis,
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}).Error; err != nil {
			return fmt.Errorf("audit provizije za račun %d: %w", accountID, err)
		}
		if trezorID == 0 {
			return nil
		}
		// 2. Kredituj bankin trezor u istoj valuti (EUR → EUR trezor, USD → USD trezor).
		if err := tx.Exec(
			`UPDATE core_banking.racun SET stanje_racuna = stanje_racuna + ? WHERE id = ?`,
			debit.InexactFloat64(), trezorID,
		).Error; err != nil {
			return fmt.Errorf("kredit trezora za proviziju: %w", err)
		}
		if err := tx.Create(&transakcijaModel{
			RacunID:          trezorID,
			TipTransakcije:   "UPLATA",
			Iznos:            debit.InexactFloat64(),
			Opis:             opis,
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}).Error; err != nil {
			return fmt.Errorf("audit provizije za trezor: %w", err)
		}
		return nil
	})
}

// ForexSwap atomically executes a currency swap for a forex order.
//
// BUY  BASE/QUOTE: debit (nominalBase × rate) from QUOTE account → credit nominalBase to BASE account.
// SELL BASE/QUOTE: debit nominalBase from BASE account → credit (nominalBase × rate) to QUOTE account.
//
// fromAccountID is the AccountID stored on the order (the user's "debit" account).
// The counterpart account is located by looking up the user's active account for the
// other currency. Both accounts are locked in id-ASC order before balances are checked.
//
// Returns trading.ErrForex* / trading.ErrInsufficientFunds for business-rule violations
// (caller should Decline the order). Other errors indicate unexpected DB failures.
func (f *fundsManager) ForexSwap(
	ctx context.Context,
	userID int64,
	fromAccountID int64,
	baseCurrency, quoteCurrency string,
	nominalBase, rate decimal.Decimal,
	direction trading.OrderDirection,
) error {
	if baseCurrency == quoteCurrency {
		return trading.ErrForexSameCurrency
	}

	// Determine which currency to debit / credit and compute amounts.
	var debitCurrency, creditCurrency string
	var debitAmount, creditAmount decimal.Decimal
	if direction == trading.OrderDirectionBuy {
		debitCurrency = quoteCurrency
		creditCurrency = baseCurrency
		debitAmount = nominalBase.Mul(rate)
		creditAmount = nominalBase
	} else {
		debitCurrency = baseCurrency
		creditCurrency = quoteCurrency
		debitAmount = nominalBase
		creditAmount = nominalBase.Mul(rate)
	}

	type accountInfo struct {
		ID       int64  `gorm:"column:id"`
		OwnerID  int64  `gorm:"column:id_vlasnika"`
		Currency string `gorm:"column:oznaka"`
	}

	isClient := tradingworker.IsClientFromCtx(ctx)

	// ── Resolve from/to accounts ───────────────────────────────────────────────
	// Zaposleni (supervizori/agenti): uvek koriste bankine trezorske račune
	// (id_vlasnika=2, trezor@exbanka.rs). Frontend ne mora da zna tačan accountId —
	// nalazimo račune automatski po valuti.
	//
	// Klijenti: fromAccountID mora da pripada korisniku i da ima ispravnu valutu;
	// counterpart (credit) se traži među korisnikovim aktivnim računima.
	var fromAcc, toAcc accountInfo

	if !isClient {
		// ── Zaposleni / Admin: trezorski računi ───────────────────────────────
		if err := f.db.WithContext(ctx).Raw(`
			SELECT r.id, r.id_vlasnika, v.oznaka
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id_vlasnika = 2 AND v.oznaka = ? AND r.status = 'AKTIVAN'
			ORDER BY r.id LIMIT 1
		`, debitCurrency).Scan(&fromAcc).Error; err != nil || fromAcc.ID == 0 {
			return trading.ErrForexAccountNotFound
		}
		if err := f.db.WithContext(ctx).Raw(`
			SELECT r.id, r.id_vlasnika, v.oznaka
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id_vlasnika = 2 AND v.oznaka = ? AND r.status = 'AKTIVAN'
			ORDER BY r.id LIMIT 1
		`, creditCurrency).Scan(&toAcc).Error; err != nil || toAcc.ID == 0 {
			return trading.ErrForexAccountNotFound
		}
	} else {
		// ── Klijent: fromAccountID mora da pripada korisniku ──────────────────
		if err := f.db.WithContext(ctx).Raw(`
			SELECT r.id, r.id_vlasnika, v.oznaka
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id = ?
		`, fromAccountID).Scan(&fromAcc).Error; err != nil || fromAcc.ID == 0 {
			return trading.ErrForexAccountNotFound
		}
		if fromAcc.OwnerID != userID {
			return trading.ErrForexAccountNotFound
		}
		if fromAcc.Currency != debitCurrency {
			return trading.ErrForexCurrencyMismatch
		}
		if err := f.db.WithContext(ctx).Raw(`
			SELECT r.id, r.id_vlasnika, v.oznaka
			FROM core_banking.racun r
			JOIN core_banking.valuta v ON v.id = r.id_valute
			WHERE r.id_vlasnika = ? AND v.oznaka = ? AND r.status = 'AKTIVAN'
			ORDER BY r.id LIMIT 1
		`, userID, creditCurrency).Scan(&toAcc).Error; err != nil || toAcc.ID == 0 {
			return trading.ErrForexAccountNotFound
		}
	}

	if fromAcc.ID == toAcc.ID {
		return trading.ErrForexSameAccount
	}

	// ── Lock both accounts in id-ASC order (deadlock prevention) ──────────────
	firstID, secondID := fromAcc.ID, toAcc.ID
	if firstID > secondID {
		firstID, secondID = secondID, firstID
	}
	type lockedRow struct {
		ID                  int64  `gorm:"column:id"`
		StanjeRacuna        string `gorm:"column:stanje_racuna"`
		RezervovanaSredstva string `gorm:"column:rezervisana_sredstva"`
	}
	var locked []lockedRow
	if err := f.db.WithContext(ctx).Raw(`
		SELECT id, stanje_racuna, rezervisana_sredstva
		FROM core_banking.racun
		WHERE id IN (?, ?)
		ORDER BY id
		FOR UPDATE
	`, firstID, secondID).Scan(&locked).Error; err != nil {
		return fmt.Errorf("forex: zaključavanje računa: %w", err)
	}

	// ── Re-validate debit balance (TOCTOU-safe, after lock) ───────────────────
	var lockedFrom *lockedRow
	for i := range locked {
		if locked[i].ID == fromAcc.ID {
			lockedFrom = &locked[i]
			break
		}
	}
	if lockedFrom == nil {
		return trading.ErrForexAccountNotFound
	}
	stanje, _ := decimal.NewFromString(lockedFrom.StanjeRacuna)
	rezervisano, _ := decimal.NewFromString(lockedFrom.RezervovanaSredstva)
	if stanje.Sub(rezervisano).LessThan(debitAmount) {
		return trading.ErrInsufficientFunds
	}

	// ── Execute the swap ───────────────────────────────────────────────────────
	if err := f.db.WithContext(ctx).Exec(`
		UPDATE core_banking.racun
		SET stanje_racuna = stanje_racuna - ?
		WHERE id = ?
	`, debitAmount.InexactFloat64(), fromAcc.ID).Error; err != nil {
		return fmt.Errorf("forex debit računa %d: %w", fromAcc.ID, err)
	}
	if err := f.db.WithContext(ctx).Exec(`
		UPDATE core_banking.racun
		SET stanje_racuna = stanje_racuna + ?
		WHERE id = ?
	`, creditAmount.InexactFloat64(), toAcc.ID).Error; err != nil {
		return fmt.Errorf("forex kredit računa %d: %w", toAcc.ID, err)
	}

	// ── Audit trail ───────────────────────────────────────────────────────────
	now := time.Now().UTC()
	opis := fmt.Sprintf("Forex swap %s/%s (%.6g %s → %.6g %s)",
		baseCurrency, quoteCurrency,
		debitAmount.InexactFloat64(), debitCurrency,
		creditAmount.InexactFloat64(), creditCurrency,
	)
	entries := []transakcijaModel{
		{
			RacunID:          fromAcc.ID,
			TipTransakcije:   "ISPLATA",
			Iznos:            debitAmount.InexactFloat64(),
			Opis:             opis,
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		},
		{
			RacunID:          toAcc.ID,
			TipTransakcije:   "UPLATA",
			Iznos:            creditAmount.InexactFloat64(),
			Opis:             opis,
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		},
	}
	if err := f.db.WithContext(ctx).Create(&entries).Error; err != nil {
		return fmt.Errorf("forex audit trail: %w", err)
	}
	return nil
}

// WithDB vraća novu instancu fundsManager koja koristi zadati *gorm.DB.
// Koristi se u engine.go da sve fill operacije izvrsimo unutar iste DB transakcije.
func (f *fundsManager) WithDB(db *gorm.DB) trading.FundsManager {
	return &fundsManager{db: db, exchangeService: f.exchangeService, provizijaRate: f.provizijaRate}
}
