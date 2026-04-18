// Package domain — exchange rate domain models, interfaces, and sentinel errors.
package domain

import (
	"context"
	"errors"
)

// ─── Greške ───────────────────────────────────────────────────────────────────

var (
	ErrExchangeRateNotFound        = errors.New("kurs nije dostupan za izabranu valutu")
	ErrExchangeInvalidAmount       = errors.New("iznos mora biti veći od nule")
	ErrExchangeProviderUnavailable = errors.New("kursna lista trenutno nije dostupna")
)

// ─── Podaci o valutama ────────────────────────────────────────────────────────

// SupportedExchangeCodes su ISO 4217 kodovi koji se prikazuju u kursnoj listi (RSD je bazna).
var SupportedExchangeCodes = []string{"EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

// ExchangeCurrencyNames mapira ISO kod na srpski naziv valute.
var ExchangeCurrencyNames = map[string]string{
	"EUR": "Euro",
	"CHF": "Švajcarski franak",
	"USD": "Američki dolar",
	"GBP": "Britanska funta",
	"JPY": "Japanski jen",
	"CAD": "Kanadski dolar",
	"AUD": "Australijski dolar",
}

// ─── Domain objekti ───────────────────────────────────────────────────────────

// ExchangeRate sadrži sve kursne podatke za jednu stranu valutu vs. RSD.
type ExchangeRate struct {
	Oznaka   string  // ISO 4217 kod, npr. "EUR"
	Naziv    string  // Srpski naziv, npr. "Euro"
	Kupovni  float64 // Banka kupuje stranu valutu po ovom kursu (klijent prodaje)
	Srednji  float64 // Srednji / referentni kurs
	Prodajni float64 // Banka prodaje stranu valutu po ovom kursu (klijent kupuje)
}

// ExchangeConversionResult je rezultat konverzije valuta.
type ExchangeConversionResult struct {
	Result    float64 // Iznos koji klijent dobija (posle provizije)
	Bruto     float64 // Iznos pre odbitka provizije
	Provizija float64 // Odbijena provizija
	ViaRSD    bool    // true ako je konverzija išla posredno kroz RSD
	RateNote  string  // Opis primenjenog kursa (za prikaz)
}

// ─── Exchange Transfer ────────────────────────────────────────────────────────

var (
	ErrExchangeSameCurrency      = errors.New("izvorišna i odredišna valuta moraju biti različite za izvršenje konverzije")
	ErrExchangeAccountNotOwned   = errors.New("navedeni račun ne pripada prijavljenom korisniku")
	ErrExchangeSameAccount       = errors.New("izvorišni i odredišni račun moraju biti različiti")
	ErrExchangeWrongCurrency     = errors.New("valuta računa ne odgovara izabranoj valuti")
	ErrExchangeInsufficientFunds = errors.New("nedovoljno raspoloživih sredstava na izvorišnom računu")
	ErrExchangeAccountInactive   = errors.New("račun nije aktivan")
)

// ExchangeTransferInput is the validated DTO for executing an exchange transfer.
type ExchangeTransferInput struct {
	VlasnikID       int64
	SourceAccountID int64
	TargetAccountID int64
	FromOznaka      string  // source currency ISO code
	ToOznaka        string  // target currency ISO code
	Amount          float64 // amount in source currency
}

// ExchangeTransferResult is returned after a successful exchange transfer execution.
type ExchangeTransferResult struct {
	ReferenceID     string
	SourceAccountID int64
	TargetAccountID int64
	FromOznaka      string
	ToOznaka        string
	OriginalAmount  float64 // debited from source in source currency
	GrossAmount     float64 // converted amount before provizija (in target currency)
	Provizija       float64 // commission deducted (in target currency)
	NetAmount       float64 // credited to target in target currency
	ViaRSD          bool    // true if conversion went through RSD
	RateNote        string  // human-readable rate description
}

// ExchangeTransferRepository handles DB operations for atomic exchange transfer execution.
// Implementacija živi u repository paketu.
type ExchangeTransferRepository interface {
	// ExecuteTransfer validates both accounts (ownership, currency, active status, funds)
	// and atomically debits source + credits target within a DB transaction.
	ExecuteTransfer(ctx context.Context, input ExchangeTransferInput, conversion ExchangeConversionResult) (*ExchangeTransferResult, error)
}

// ─── Interfejsi ───────────────────────────────────────────────────────────────

// ExchangeProvider je interfejs prema eksternom provajderu kursnih podataka.
// Implementacija živi u repository paketu.
type ExchangeProvider interface {
	// GetMidRates vraća srednje kurseve za podržane valute, izražene u RSD.
	// Ključ: ISO kod (npr. "EUR"), vrednost: koliko RSD vredi 1 jedinica te valute.
	GetMidRates(ctx context.Context) (map[string]float64, error)
}

// ExchangeService definiše ugovor prema sloju poslovne logike za menjačnicu.
// Implementacija živi u service paketu.
type ExchangeService interface {
	// GetRates vraća kompletnu kursnu listu sa kupovnim, srednjim i prodajnim kursevima.
	GetRates(ctx context.Context) ([]ExchangeRate, error)

	// CalculateExchange konvertuje iznos iz fromOznaka u toOznaka uz primenu provizije.
	CalculateExchange(ctx context.Context, fromOznaka, toOznaka string, amount float64) (*ExchangeConversionResult, error)

	// ExecuteExchangeTransfer validates input, calculates the exchange, and atomically
	// debits the source account and credits the target account.
	ExecuteExchangeTransfer(ctx context.Context, input ExchangeTransferInput) (*ExchangeTransferResult, error)
}
