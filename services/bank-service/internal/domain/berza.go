package domain

import (
	"context"
	"errors"
	"time"
)

// ─── Greške ───────────────────────────────────────────────────────────────────

var (
	ErrExchangeNotFound = errors.New("berza nije pronađena")
)

// ─── MarketStatus ─────────────────────────────────────────────────────────────

// MarketStatus opisuje trenutni status berze.
type MarketStatus string

const (
	MarketStatusOpen       MarketStatus = "OPEN"
	MarketStatusPreMarket  MarketStatus = "PRE_MARKET"
	MarketStatusAfterHours MarketStatus = "AFTER_HOURS"
	MarketStatusClosed     MarketStatus = "CLOSED"
)

// ─── Exchange ─────────────────────────────────────────────────────────────────

// Exchange je čisti domenski objekat za berzu — ne zna za GORM niti za gRPC.
type Exchange struct {
	ID           int64
	Name         string
	Acronym      string
	MICCode      string
	Polity       string
	CurrencyID   int64
	CurrencyName string       // naziv valute, npr. "Američki dolar" — popunjava JOIN u repozitorijumu
	Timezone     string       // IANA timezone, e.g. "America/New_York"
	OpenTime     time.Time    // radno vreme otvaranja (TIME iz baze, samo sat:minut)
	CloseTime    time.Time    // radno vreme zatvaranja (TIME iz baze, samo sat:minut)
	MarketStatus MarketStatus // popunjava se u servisnom sloju; nije čuvano u bazi
}

// ListExchangesFilter parametri za filtriranje liste berzi.
type ListExchangesFilter struct {
	Polity string // tačno poklapanje; "" = bez filtera
	Search string // parcijalni match na name ili acronym; "" = bez filtera
}

// ExchangeRepository definiše ugovor prema sloju podataka.
type ExchangeRepository interface {
	List(ctx context.Context, filter ListExchangesFilter) ([]Exchange, error)
	GetByID(ctx context.Context, id int64) (*Exchange, error)
	GetByMICCode(ctx context.Context, micCode string) (*Exchange, error)
	// IsHoliday proverava da li je dati datum praznik za državu (polity) berze.
	// date treba da bude lokalni datum berze (ne UTC), bez vremenske komponente.
	IsHoliday(ctx context.Context, polity string, date time.Time) (bool, error)
}

// MarketModeStore apstrahuje čuvanje zastavice za bypass radnog vremena berzi.
// Implementacija živi u transport paketu (Redis), NoOp fallback kada Redis nije dostupan.
type MarketModeStore interface {
	SetTestMode(ctx context.Context, enabled bool) error
	IsTestMode(ctx context.Context) (bool, error)
}

// BerzaService definiše ugovor prema sloju poslovne logike.
type BerzaService interface {
	ListExchanges(ctx context.Context, filter ListExchangesFilter) ([]Exchange, error)
	GetExchange(ctx context.Context, id int64, micCode string) (*Exchange, error)

	// IsExchangeOpen vraća MarketStatus za berzu sa datim ID-jem.
	// Redosled provera:
	//   1. Redis test mode → uvek OPEN
	//   2. Lokalni datum berze → praznik → CLOSED
	//   3. Vikend → CLOSED
	//   4. Sat:min po lokalnom vremenu berze:
	//      07:00–OpenTime  → PRE_MARKET
	//      OpenTime–CloseTime → OPEN  (vremena se čitaju iz baze)
	//      CloseTime–20:00 → AFTER_HOURS
	//      ostalo          → CLOSED
	IsExchangeOpen(ctx context.Context, exchangeID int64) (MarketStatus, error)

	// GetMarketStatus računa MarketStatus za već učitanu berzu (bez extra DB poziva).
	// Koristi se u enriched HTTP handleru da se izbegne N+1 pri prikazu liste berzi.
	GetMarketStatus(ctx context.Context, ex Exchange) (MarketStatus, error)

	ToggleMarketTestMode(ctx context.Context, enabled bool) error
}
