package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"
)

// FuturesPendingExpiryWorker runs on a daily interval and declines PENDING orders
// whose underlying FUTURE or OPTION listing has a settlement date in the past.
// Complements the trading engine tick (which performs the same check more frequently).
type FuturesPendingExpiryWorker struct {
	orders   trading.OrderRepository
	listings domain.ListingRepository
}

func NewFuturesPendingExpiryWorker(orders trading.OrderRepository, listings domain.ListingRepository) *FuturesPendingExpiryWorker {
	return &FuturesPendingExpiryWorker{orders: orders, listings: listings}
}

// Start blocks until ctx is cancelled. First run executes after a short delay, then roughly every 24h.
func (w *FuturesPendingExpiryWorker) Start(ctx context.Context) {
	log.Printf("[worker] FuturesPendingExpiryWorker started (interval≈24h)")
	// Align first tick ~1 minute after startup so migrations can settle.
	t := time.NewTimer(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[worker] FuturesPendingExpiryWorker stopped")
			return
		case <-t.C:
			w.run(ctx)
			t.Reset(24 * time.Hour)
		}
	}
}

func (w *FuturesPendingExpiryWorker) run(ctx context.Context) {
	pending := trading.OrderStatusPending
	orders, err := w.orders.ListByStatus(ctx, &pending)
	if err != nil {
		log.Printf("[worker] FuturesPendingExpiry: list pending: %v", err)
		return
	}
	for i := range orders {
		o := orders[i]
		listing, err := w.listings.GetByID(ctx, o.ListingID)
		if err != nil {
			continue
		}
		if listing.ListingType != domain.ListingTypeFuture && listing.ListingType != domain.ListingTypeOption {
			continue
		}
		if !settlementInPast(listing.DetailsJSON) {
			continue
		}
		if _, err := w.orders.UpdateStatus(ctx, o.ID, trading.OrderStatusDeclined, nil); err != nil {
			log.Printf("[worker] FuturesPendingExpiry: decline order %d: %v", o.ID, err)
			continue
		}
		log.Printf("[worker] FuturesPendingExpiry: order %d declined (expired %s settlement)", o.ID, listing.ListingType)
	}
}

func settlementInPast(detailsJSON string) bool {
	var details struct {
		SettlementDate string `json:"settlement_date"`
	}
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil || details.SettlementDate == "" {
		return false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, details.SettlementDate); err == nil {
			return t.Before(time.Now().UTC())
		}
	}
	return false
}
