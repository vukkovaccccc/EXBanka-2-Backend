package domain

import (
	"context"
	"errors"
)

// ─── Greške validacije ────────────────────────────────────────────────────────

var (
	ErrInvalidCurrency = errors.New("nevalidna valuta za kategoriju računa")
	ErrInvalidPodvrsta = errors.New("nevalidna podvrsta za tip računa")
)

// Currency je čisti domenski objekat — ne zna za GORM niti za gRPC.
type Currency struct {
	ID     int64
	Naziv  string
	Oznaka string
}

func (Currency) TableName() string {
	return "core_banking.valuta"
}

// CurrencyRepository definiše ugovor prema sloju podataka.
// Implementacija živi u repository paketu.
type CurrencyRepository interface {
	GetAll(ctx context.Context) ([]Currency, error)
	GetByID(ctx context.Context, id int64) (*Currency, error)
}

// CurrencyService definiše ugovor prema sloju poslovne logike.
// Implementacija živi u service paketu.
type CurrencyService interface {
	GetCurrencies(ctx context.Context) ([]Currency, error)
}

// Delatnost je čisti domenski objekat — ne zna za GORM niti za gRPC.
type Delatnost struct {
	ID     int64
	Sifra  string
	Naziv  string
	Grana  string
	Sektor string
}

// DelatnostRepository definiše ugovor prema sloju podataka.
type DelatnostRepository interface {
	GetAll(ctx context.Context) ([]Delatnost, error)
}

// DelatnostService definiše ugovor prema sloju poslovne logike.
type DelatnostService interface {
	GetDelatnosti(ctx context.Context) ([]Delatnost, error)
}

// ─── Account ──────────────────────────────────────────────────────────────────

// Firma sadrži podatke o poslovnom subjektu (popunjava se samo za POSLOVNI račun).
type Firma struct {
	Naziv       string
	MaticniBroj string
	PIB         string
	DelatnostID int64
	Adresa      string
}

// CreateAccountInput je ulazni domenski objekat za kreiranje računa.
type CreateAccountInput struct {
	ZaposleniID      int64
	VlasnikID        int64
	ValutaID         int64
	Firma            *Firma // nil za lične račune
	KategorijaRacuna string // "TEKUCI" | "DEVIZNI"
	VrstaRacuna      string // "LICNI" | "POSLOVNI"
	Podvrsta         string // npr. "STANDARDNI", "STEDNI" — relevantno za TEKUCI+LICNI
	NazivRacuna      string
	StanjeRacuna     float64
}

// AccountRepository definiše ugovor prema sloju podataka za operacije sa računima.
type AccountRepository interface {
	// CreateAccount izvršava transakciju (firma + racun INSERT).
	// Vraća surogat PK (racun.id) novokreiranog računa.
	CreateAccount(ctx context.Context, input CreateAccountInput, brojRacuna string) (int64, error)
}

// AccountService definiše ugovor prema sloju poslovne logike.
type AccountService interface {
	CreateAccount(ctx context.Context, input CreateAccountInput) (int64, error)
}
