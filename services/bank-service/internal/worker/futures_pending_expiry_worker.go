package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"
)

// FuturesPendingExpiryWorker runs on a daily interval and:
//  1. Declines PENDING futures/options orders with an expired settlement date.
//  2. Cancels APPROVED orders (any type) that have been idle for more than 24h
//     and releases any reserved funds (BUG-7).
type FuturesPendingExpiryWorker struct {
	orders         trading.OrderRepository
	listings       domain.ListingRepository
	tradingService trading.TradingService
}

func NewFuturesPendingExpiryWorker(orders trading.OrderRepository, listings domain.ListingRepository, tradingService trading.TradingService) *FuturesPendingExpiryWorker {
	return &FuturesPendingExpiryWorker{orders: orders, listings: listings, tradingService: tradingService}
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

	// Otkaži APPROVED naloge koji su stariji od 24h i nisu izvršeni (BUG-7).
	// CancelOrder oslobađa rezervisana sredstva i upisuje audit log.
	// Prolazimo requestedBy=o.UserID da bismo preskočili supervisor proveru.
	approved := trading.OrderStatusApproved
	approvedOrders, err := w.orders.ListByStatus(ctx, &approved)
	if err != nil {
		log.Printf("[worker] FuturesPendingExpiry: list approved: %v", err)
		return
	}
	for i := range approvedOrders {
		o := approvedOrders[i]
		if o.IsDone {
			continue
		}
		if time.Since(o.LastModified) <= 24*time.Hour {
			continue
		}
		if _, err := w.tradingService.CancelOrder(ctx, o.ID, o.UserID, false); err != nil {
			log.Printf("[worker] FuturesPendingExpiry: cancel approved order %d: %v", o.ID, err)
			continue
		}
		log.Printf("[worker] FuturesPendingExpiry: order %d canceled (APPROVED >24h idle)", o.ID)
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
