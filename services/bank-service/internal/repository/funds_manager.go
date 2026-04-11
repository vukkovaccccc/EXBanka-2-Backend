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
}

// NewFundsManager vraća implementaciju trading.FundsManager koja direktno
// ažurira core_banking.racun u jednoj SQL naredbi (atomično).
// exchangeService se koristi za konverziju USD iznosa u valutu klijentskog računa.
func NewFundsManager(db *gorm.DB, exchangeService domain.ExchangeService) trading.FundsManager {
	return &fundsManager{db: db, exchangeService: exchangeService}
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
// Za klijente (isClient=true) koristi prodajni kurs (banka prodaje USD klijentu —
// nepovoljniji kurs po klijenta, kao u menjačnici).
// Za zaposlene i sistem koristi srednji kurs.
func (f *fundsManager) convertUSDToAccountCurrency(ctx context.Context, usdAmount decimal.Decimal, targetCurrency string) decimal.Decimal {
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

	if targetCurrency == "RSD" {
		return rsdAmount
	}

	// RSD → target currency (uvek srednji kurs za drugu valutu)
	for _, r := range rates {
		if r.Oznaka == targetCurrency && r.Srednji > 0 {
			return rsdAmount.Div(decimal.NewFromFloat(r.Srednji))
		}
	}

	// Valuta nije pronađena — vrati RSD iznos kao sigurni fallback
	log.Printf("[funds_manager] valuta %q nije pronađena u kursnoj listi — koristi se RSD iznos", targetCurrency)
	return rsdAmount
}

// ReserveFunds povećava rezervisana_sredstva za dati iznos.
// amount je u USD (valuta berze); konvertuje se u valutu računa kao i kod
// HasSufficientFunds / SettleBuyFill — inače bi se u RSD račun dodavao sirovi USD broj.
func (f *fundsManager) ReserveFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, usdAmount, currency)
	result := f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET rezervisana_sredstva = rezervisana_sredstva + ?
		 WHERE id = ?`,
		debit.InexactFloat64(), accountID,
	)
	if result.Error != nil {
		return fmt.Errorf("rezervacija sredstava za račun %d: %w", accountID, result.Error)
	}
	return nil
}

// ReleaseFunds smanjuje rezervisana_sredstva za dati iznos (ne ide ispod 0).
// amount je u USD; konvertuje se u valutu računa.
func (f *fundsManager) ReleaseFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) error {
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, usdAmount, currency)
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
	debit := f.convertUSDToAccountCurrency(ctx, amount, currency)
	isClient := tradingworker.IsClientFromCtx(ctx)

	// Zaposleni ili USD račun: direktno knjiženje (bez posrednika).
	if !isClient || currency == "USD" {
		result := f.db.WithContext(ctx).Exec(
			`UPDATE core_banking.racun
			 SET stanje_racuna        = stanje_racuna - ?,
			     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
			 WHERE id = ?`,
			debit.InexactFloat64(), debit.InexactFloat64(), accountID,
		)
		if result.Error != nil {
			return fmt.Errorf("namirenje BUY filla za račun %d: %w", accountID, result.Error)
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

	// Klijent sa ne-USD računom: 3-struko knjiženje kroz bankine trezorske račune.
	trezorFromID := f.fetchCurrencyTrezorID(ctx, currency)
	trezorUSDID := f.fetchCurrencyTrezorID(ctx, "USD")

	if trezorFromID == 0 || trezorUSDID == 0 {
		log.Printf("[funds_manager] trezorski račun nije pronađen (valuta=%s, USD trezor=%d) — direktno knjiženje", currency, trezorUSDID)
		result := f.db.WithContext(ctx).Exec(
			`UPDATE core_banking.racun
			 SET stanje_racuna        = stanje_racuna - ?,
			     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
			 WHERE id = ?`,
			debit.InexactFloat64(), debit.InexactFloat64(), accountID,
		)
		if result.Error != nil {
			return fmt.Errorf("namirenje BUY filla za račun %d: %w", accountID, result.Error)
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
		if err := tx.Exec(
			`UPDATE core_banking.racun
			 SET stanje_racuna        = stanje_racuna - ?,
			     rezervisana_sredstva = GREATEST(0, rezervisana_sredstva - ?)
			 WHERE id = ?`,
			debit.InexactFloat64(), debit.InexactFloat64(), accountID,
		).Error; err != nil {
			return fmt.Errorf("zaduži klijentski račun %d: %w", accountID, err)
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
	credit := f.convertUSDToAccountCurrency(ctx, amount, currency)

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
	required := f.convertUSDToAccountCurrency(ctx, usdAmount, currency)

	slobodna := stanje.Sub(rezervisano)
	return slobodna.GreaterThanOrEqual(required), nil
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

// ChargeCommission smanjuje stanje_racuna za iznos provizije (ne dirá rezervisana_sredstva)
// i kreira zapis u transakcija tabeli. Istovremeno uplaćuje proviziju na bankin trezor račun
// u istoj valuti (spec: "Provizija se prebacuje na bankin račun u istoj valuti").
func (f *fundsManager) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	// ── 1. Konverzija provizije u valutu računa ───────────────────────────────
	// amount je u USD (valuta berze). Debetujemo korisnika u valuti njegovog računa.
	currency := f.accountCurrency(ctx, accountID)
	debit := f.convertUSDToAccountCurrency(ctx, amount, currency)

	// ── 2. Debetuj korisnikov račun ────────────────────────────────────────────
	result := f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET stanje_racuna = stanje_racuna - ?
		 WHERE id = ?`,
		debit.InexactFloat64(), accountID,
	)
	if result.Error != nil {
		return fmt.Errorf("naplata provizije za račun %d: %w", accountID, result.Error)
	}
	f.db.WithContext(ctx).Create(&transakcijaModel{
		RacunID:          accountID,
		TipTransakcije:   "ISPLATA",
		Iznos:            debit.InexactFloat64(),
		Opis:             "Provizija za hartiju od vrednosti",
		VremeIzvrsavanja: time.Now().UTC(),
		Status:           "IZVRSEN",
	})

	// ── 3. Kredituj bankin trezor račun (USD) ──────────────────────────────────
	// Provizija se prebacuje na bankin prihodni račun u USD (valuta berze).
	// Trezor račun je uvek u USD; amount je već u USD — nema potrebe za konverzijom.
	trezorID := f.bankTrezorAccountID(ctx)
	if trezorID == 0 {
		log.Printf("[funds_manager] bankin trezor račun nije pronađen — provizija nije prebačena")
		return nil
	}
	f.db.WithContext(ctx).Exec(
		`UPDATE core_banking.racun
		 SET stanje_racuna = stanje_racuna + ?
		 WHERE id = ?`,
		amount.InexactFloat64(), trezorID,
	)
	f.db.WithContext(ctx).Create(&transakcijaModel{
		RacunID:          trezorID,
		TipTransakcije:   "UPLATA",
		Iznos:            amount.InexactFloat64(),
		Opis:             "Prihod od provizije za hartiju od vrednosti",
		VremeIzvrsavanja: time.Now().UTC(),
		Status:           "IZVRSEN",
	})
	return nil
}
