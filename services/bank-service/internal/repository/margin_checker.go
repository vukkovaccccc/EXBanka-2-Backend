package repository

import (
	"context"
	"fmt"
	"log"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// racunMarginRow je minimalna projekcija core_banking.racun tabele
// koja je potrebna za proveru slobodnih sredstava.
type racunMarginRow struct {
	StanjeRacuna        string `gorm:"column:stanje_racuna"`
	RezervovanaSredstva string `gorm:"column:rezervisana_sredstva"`
}

func (racunMarginRow) TableName() string { return "core_banking.racun" }

type marginChecker struct {
	db              *gorm.DB
	exchangeService domain.ExchangeService
}

// NewMarginChecker vraća implementaciju trading.MarginChecker koja čita
// slobodna sredstva (stanje_racuna − rezervisana_sredstva) iz GORM-a.
// exchangeService se koristi za konverziju iznosa kredita u USD pri margin provjeri.
func NewMarginChecker(db *gorm.DB, exchangeService domain.ExchangeService) trading.MarginChecker {
	return &marginChecker{db: db, exchangeService: exchangeService}
}

// toUSD konvertuje iznos iz date valute u USD koristeći srednji kurs (bez provizije).
// Ako konverzija ne uspe, vraća originalni iznos (konzervativno — ne blokira check).
func (m *marginChecker) toUSD(ctx context.Context, amount decimal.Decimal, currency string) decimal.Decimal {
	if currency == "USD" {
		return amount
	}
	rates, err := m.exchangeService.GetRates(ctx)
	if err != nil {
		log.Printf("[margin_checker] nije moguće dohvatiti kurseve za konverziju %s→USD: %v", currency, err)
		return amount
	}
	var midCurrency, midUSD float64
	for _, r := range rates {
		if r.Oznaka == currency {
			midCurrency = r.Srednji
		}
		if r.Oznaka == "USD" {
			midUSD = r.Srednji
		}
	}
	if midCurrency <= 0 || midUSD <= 0 {
		log.Printf("[margin_checker] kurs za %s ili USD nije pronađen — koristi se originalni iznos", currency)
		return amount
	}
	// amount (currency) → RSD: amount * midCurrency
	// RSD → USD: rsd / midUSD
	rsd := amount.Mul(decimal.NewFromFloat(midCurrency))
	return rsd.Div(decimal.NewFromFloat(midUSD))
}

// HasApprovedCreditForMargin vraća (true, nil) kada korisnik ima barem jedan
// odobren (ODOBREN) kredit čiji iznos_kredita, konvertovan u USD, prelazi traženi iznos.
// Ovo je uslov 1 iz specifikacije: "Klijent: Neki kredit koji ima > Initial Margin Cost".
// IMC je uvek u USD (cene hartija su u USD), pa se iznos kredita konvertuje u USD
// pre poređenja kako bi se izbeglo pogrešno upoređivanje RSD sa USD.
func (m *marginChecker) HasApprovedCreditForMargin(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
	type kreditRow struct {
		Iznos  float64 `gorm:"column:iznos_kredita"`
		Valuta string  `gorm:"column:valuta"`
	}
	var krediti []kreditRow
	err := m.db.WithContext(ctx).Raw(`
		SELECT iznos_kredita, valuta
		FROM core_banking.kredit
		WHERE vlasnik_id = ? AND status = 'ODOBREN'
	`, userID).Scan(&krediti).Error
	if err != nil {
		return false, fmt.Errorf("margin check (kredit): %w", err)
	}
	for _, k := range krediti {
		usdAmount := m.toUSD(ctx, decimal.NewFromFloat(k.Iznos), k.Valuta)
		if usdAmount.GreaterThan(required) {
			return true, nil
		}
	}
	return false, nil
}

// HasSufficientMargin vraća (true, nil) kada slobodna sredstva naloga
// pokrivaju traženi iznos. Slobodna sredstva = stanje_racuna − rezervisana_sredstva.
func (m *marginChecker) HasSufficientMargin(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	var row racunMarginRow
	err := m.db.WithContext(ctx).
		Select("stanje_racuna, rezervisana_sredstva").
		Where("id = ?", accountID).
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, fmt.Errorf("margin check: račun %d nije pronađen", accountID)
		}
		return false, fmt.Errorf("margin check: %w", err)
	}

	stanje, err := decimal.NewFromString(row.StanjeRacuna)
	if err != nil {
		return false, fmt.Errorf("margin check: parse stanje_racuna: %w", err)
	}
	rezervisano, err := decimal.NewFromString(row.RezervovanaSredstva)
	if err != nil {
		return false, fmt.Errorf("margin check: parse rezervisana_sredstva: %w", err)
	}

	slobodna := stanje.Sub(rezervisano)
	return slobodna.GreaterThan(required), nil
}

// HasSufficientMarginTrezor vraća (true, nil) kada bankin trezorski račun za datu valutu
// (id_vlasnika=2) ima slobodna sredstva >= required.
// Koristi se za margin proveru aktuara — aktuar ne bira lični račun, backend automatski
// pronalazi bankin račun u valuti hartije (cene su u USD, pa je currency uvek "USD").
// Vraća (false, err) kada aktivni trezorski račun za tu valutu ne postoji.
func (m *marginChecker) HasSufficientMarginTrezor(ctx context.Context, currency string, required decimal.Decimal) (bool, error) {
	var row struct {
		ID                  int64  `gorm:"column:id"`
		StanjeRacuna        string `gorm:"column:stanje_racuna"`
		RezervovanaSredstva string `gorm:"column:rezervisana_sredstva"`
	}
	err := m.db.WithContext(ctx).Raw(`
		SELECT r.id, r.stanje_racuna, r.rezervisana_sredstva
		FROM core_banking.racun r
		JOIN core_banking.valuta v ON v.id = r.id_valute
		WHERE r.id_vlasnika = 2
		  AND v.oznaka = ?
		  AND r.status = 'AKTIVAN'
		ORDER BY r.id
		LIMIT 1
	`, currency).Scan(&row).Error
	if err != nil {
		return false, fmt.Errorf("margin check trezor (%s): %w", currency, err)
	}
	if row.ID == 0 {
		return false, fmt.Errorf("margin check: bankin trezor račun za valutu %s nije pronađen — nalog odbijen", currency)
	}

	stanje, err := decimal.NewFromString(row.StanjeRacuna)
	if err != nil {
		return false, fmt.Errorf("margin check trezor: parse stanje_racuna: %w", err)
	}
	rezervisano, err := decimal.NewFromString(row.RezervovanaSredstva)
	if err != nil {
		return false, fmt.Errorf("margin check trezor: parse rezervisana_sredstva: %w", err)
	}

	slobodna := stanje.Sub(rezervisano)
	return slobodna.GreaterThan(required), nil
}
