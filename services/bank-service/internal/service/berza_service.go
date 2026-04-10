// Package service — exchange (berza) business logic.
package service

import (
	"context"
	"fmt"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

type berzaService struct {
	repo      domain.ExchangeRepository
	modeStore domain.MarketModeStore
}

func NewBerzaService(repo domain.ExchangeRepository, modeStore domain.MarketModeStore) domain.BerzaService {
	return &berzaService{repo: repo, modeStore: modeStore}
}

func (s *berzaService) ListExchanges(ctx context.Context, filter domain.ListExchangesFilter) ([]domain.Exchange, error) {
	return s.repo.List(ctx, filter)
}

func (s *berzaService) GetExchange(ctx context.Context, id int64, micCode string) (*domain.Exchange, error) {
	if micCode != "" {
		return s.repo.GetByMICCode(ctx, micCode)
	}
	return s.repo.GetByID(ctx, id)
}

// IsExchangeOpen vraća MarketStatus za berzu sa datim ID-jem.
// Pogledaj GetMarketStatus za detalje pravila.
func (s *berzaService) IsExchangeOpen(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
	ex, err := s.repo.GetByID(ctx, exchangeID)
	if err != nil {
		return domain.MarketStatusClosed, err
	}
	return s.GetMarketStatus(ctx, *ex)
}

// GetMarketStatus računata MarketStatus za već učitanu berzu.
// Koristiti ovaj metod u listama da se izbegne N+1 (GetByID se ne poziva ponovo).
//
// Redosled provera:
//  1. Redis test mode → uvek OPEN
//  2. Lokalni datum berze (time.LoadLocation) → praznik → CLOSED
//  3. Vikend (subota ili nedelja po lokalnom vremenu) → CLOSED
//  4. Sat:min po lokalnom vremenu:
//     07:00–ex.OpenTime  → PRE_MARKET
//     ex.OpenTime–ex.CloseTime → OPEN  (vremena se čitaju iz baze)
//     ex.CloseTime–20:00 → AFTER_HOURS
//     ostalo             → CLOSED
func (s *berzaService) GetMarketStatus(ctx context.Context, ex domain.Exchange) (domain.MarketStatus, error) {
	// 1. Redis test mode bypass
	testMode, err := s.modeStore.IsTestMode(ctx)
	if err != nil {
		return domain.MarketStatusClosed, fmt.Errorf("IsTestMode: %w", err)
	}
	if testMode {
		return domain.MarketStatusOpen, nil
	}

	// 2. Konvertuj UTC u lokalno vreme berze
	loc, err := time.LoadLocation(ex.Timezone)
	if err != nil {
		return domain.MarketStatusClosed, fmt.Errorf("nevalidna vremenska zona %q: %w", ex.Timezone, err)
	}
	now := time.Now().UTC().In(loc)

	// 3. Proveri praznike — koristimo samo datum (bez sata) za lookup
	localDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	holiday, err := s.repo.IsHoliday(ctx, ex.Polity, localDate)
	if err != nil {
		// Ne blokiramo — logujemo tiho i nastavljamo
		holiday = false
	}
	if holiday {
		return domain.MarketStatusClosed, nil
	}

	// 4. Vikend
	weekday := now.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return domain.MarketStatusClosed, nil
	}

	// 5. Radno vreme po satu i minutima
	// OpenTime i CloseTime dolaze iz baze kao TIME kolone koje predstavljaju
	// lokalno radno vreme berze (npr. 09:30 za NYSE znači 09:30 ET).
	// `now` je već konvertovan u lokalnu zonu berze (In(loc) gore), pa
	// koristimo Hour()/Minute() direktno — BEZ .UTC() — kako bi poređenje
	// bilo u istoj vremenskoj zoni.
	h, m := now.Hour(), now.Minute()
	totalMin := h*60 + m

	const (
		preMarketStart = 7*60 + 0  // 07:00 — fiksno
		afterHoursEnd  = 20*60 + 0 // 20:00 — fiksno
	)
	openStart := ex.OpenTime.Hour()*60 + ex.OpenTime.Minute()
	closeEnd  := ex.CloseTime.Hour()*60 + ex.CloseTime.Minute()

	switch {
	case totalMin >= openStart && totalMin < closeEnd:
		return domain.MarketStatusOpen, nil
	case totalMin >= preMarketStart && totalMin < openStart:
		return domain.MarketStatusPreMarket, nil
	case totalMin >= closeEnd && totalMin < afterHoursEnd:
		return domain.MarketStatusAfterHours, nil
	default:
		return domain.MarketStatusClosed, nil
	}
}

func (s *berzaService) ToggleMarketTestMode(ctx context.Context, enabled bool) error {
	return s.modeStore.SetTestMode(ctx, enabled)
}
