// Package worker contains the asynchronous order execution engine for the
// trading domain.
//
// When imported alongside the bank-wide internal/worker package, use an import
// alias to disambiguate:
//
//	tradingworker "banka-backend/services/bank-service/internal/trading/worker"
package worker

// =============================================================================
// engine.go — Asynchronous Order Execution Engine
//
// Responsibilities:
//   1. Poll for APPROVED orders on a configurable interval.
//   2. Decline orders whose underlying asset's settlement date has passed.
//   3. Spawn a dedicated goroutine per order; track active goroutines so that
//      the same order is never processed twice concurrently.
//   4. Inside each goroutine:
//      a. For STOP / STOP_LIMIT: block until the stop-price condition is met,
//         re-fetching the order from DB on every tick to detect cancellation.
//      b. Execute in partial fills (or a single AON fill), sleeping between
//         chunks according to the simulated timing formula.
//      c. Re-fetch the order from DB after every sleep to honour any external
//         cancellation that arrived while the goroutine was sleeping.
//      d. Record each fill in order_transactions and decrement remaining_portions.
//      e. Atomically mark the order DONE when all portions are filled.
//
// Concurrency model:
//   - Engine.active (sync.Map) maps orderID → struct{}, acting as a live-set.
//     An entry is added before the goroutine is spawned and removed via defer
//     when it exits, regardless of the exit reason.
//   - Only one goroutine per order is alive at any time; the polling loop
//     skips orders that are already in the live-set.
//
// Persistence atomicity notes:
//   - CreateTransaction and UpdateRemainingPortions are two separate DB calls.
//     If the process crashes between them, the next restart will re-process the
//     order and may record a duplicate fill.  Wrapping both in a DB transaction
//     is the correct fix but requires repository-level transaction support;
//     left as a known limitation for this sprint.
//   - MarkDone is a single UPDATE (status=DONE, is_done=true, remaining=0)
//     to prevent the order from being left in an ambiguous state.
//
// TOCTOU note (stop activation):
//   The stop-condition check and the subsequent execution are not atomic at
//   the DB level.  A very rapid market price reversal between the two could
//   result in a fill that marginally misses the limit.  Acceptable for a
//   simulation; a production system would use an order book or a locked read.
// =============================================================================

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
)

// isClientCtxKey je privatni ključ koji se koristi za prenošenje is_client
// flaga kroz context između engine-a i funds_manager-a.
type isClientCtxKey struct{}

// WithIsClient dodaje is_client flag u context za funds_manager konverziju kursa.
func WithIsClient(ctx context.Context, isClient bool) context.Context {
	return context.WithValue(ctx, isClientCtxKey{}, isClient)
}

// IsClientFromCtx čita is_client flag iz context-a. Vraća false ako nije postavljen.
func IsClientFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(isClientCtxKey{}).(bool)
	return v
}

// ─── Market data interface ────────────────────────────────────────────────────

// MarketSnapshot holds the live market values the engine needs for a single
// listing at a point in time.
type MarketSnapshot struct {
	Ask    float64
	Bid    float64
	Volume int64

	// SettlementDate is nil for STOCK and FOREX listings.
	// Non-nil for FUTURE and OPTION; used for settlement-date expiry checks.
	// The concrete implementation parses this from listing.details_json.
	SettlementDate *time.Time
}

// MarketDataProvider is the read-only interface the execution engine uses to
// obtain live market data.  It intentionally contains only what the engine
// needs — Ask, Bid, Volume, and SettlementDate — to keep the dependency
// surface narrow.
//
// The concrete implementation should wrap domain.ListingRepository.GetByID and
// parse details_json for the settlement date field.  No implementation is
// required in this sprint.
type MarketDataProvider interface {
	// GetMarketSnapshot returns current market data for the given listing.
	// Returns an error if the listing does not exist or the DB call fails.
	GetMarketSnapshot(ctx context.Context, listingID int64) (MarketSnapshot, error)
}

// ─── Engine constants ─────────────────────────────────────────────────────────

const (
	// defaultPollInterval is how often the engine scans for new APPROVED orders.
	defaultPollInterval = 5 * time.Second

	// stopPollInterval is how often a goroutine re-checks the stop-price
	// condition while waiting for a STOP or STOP_LIMIT order to activate.
	stopPollInterval = 5 * time.Second

	// afterHoursPenaltySeconds is added to every simulated wait when the order's
	// after_hours flag is true.
	afterHoursPenaltySeconds = 1800.0 // 30 minutes

	// minWaitSeconds prevents a busy-loop on extremely liquid assets where the
	// timing formula would otherwise return a sub-second delay.
	minWaitSeconds = 1.0

	// maxWaitSeconds caps the simulated delay so a thinly-traded or zero-volume
	// asset does not hang a goroutine indefinitely.
	maxWaitSeconds = 86400.0 // 24 hours

	// defaultWaitSeconds is used when volume is zero or remaining portions are
	// zero — both of which would cause a division-by-zero in the formula.
	defaultWaitSeconds = 30.0
)

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine is the asynchronous order execution engine.
// It is safe to call Start concurrently (though normally only called once).
type Engine struct {
	orders trading.OrderRepository
	market MarketDataProvider
	funds  trading.FundsManager

	// active tracks orderIDs that are currently being processed by a goroutine.
	// Using sync.Map because reads dominate and keys are added/deleted by
	// different goroutines.
	active sync.Map // map[int64]struct{}

	pollInterval time.Duration
}

// NewEngine constructs an Engine with its three dependencies.
//
// pollInterval controls how often the main loop scans for APPROVED orders.
// Pass 0 to use the package-level defaultPollInterval.
func NewEngine(
	orders trading.OrderRepository,
	market MarketDataProvider,
	funds trading.FundsManager,
	pollInterval time.Duration,
) *Engine {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Engine{
		orders:       orders,
		market:       market,
		funds:        funds,
		pollInterval: pollInterval,
	}
}

// Start runs the engine's main polling loop.  It blocks until ctx is canceled;
// call it in a dedicated goroutine:
//
//	go engine.Start(ctx)
//
// All per-order goroutines inherit ctx; canceling it causes every goroutine to
// exit at its next sleep boundary.
func (e *Engine) Start(ctx context.Context) {
	log.Printf("[trading/engine] engine started (poll=%s)", e.pollInterval)

	// Run once immediately so the first tick is not delayed by the full interval.
	e.tick(ctx)

	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.tick(ctx)
		case <-ctx.Done():
			log.Printf("[trading/engine] engine shutting down")
			return
		}
	}
}

// tick is one iteration of the main polling loop.
// It fetches every APPROVED non-done order and, for each one:
//  1. Checks whether the underlying asset's settlement date has expired.
//  2. Spawns a goroutine if none is already running for that order.
//
// Additionally, it checks all PENDING orders for expired settlement dates and
// auto-declines them so that supervisors cannot approve expired instruments.
func (e *Engine) tick(ctx context.Context) {
	// ── Auto-decline PENDING orders with expired settlement dates ─────────────
	// PENDING orders have not yet been approved; if the underlying instrument's
	// settlement date has passed, the supervisor's only valid action is DECLINE.
	// We auto-decline here so the supervisor sees them pre-declined on next load.
	pending := trading.OrderStatusPending
	pendingOrders, err := e.orders.ListByStatus(ctx, &pending)
	if err != nil {
		log.Printf("[trading/engine] failed to list pending orders for expiry check: %v", err)
	} else {
		for _, po := range pendingOrders {
			snap, snapErr := e.market.GetMarketSnapshot(ctx, po.ListingID)
			if snapErr != nil {
				log.Printf("[trading/engine] pending order %d: snapshot error: %v", po.ID, snapErr)
				continue
			}
			if isSettlementExpired(snap) {
				log.Printf("[trading/engine] pending order %d: settlement date expired — auto-declining", po.ID)
				if _, decErr := e.orders.UpdateStatus(ctx, po.ID, trading.OrderStatusDeclined, nil); decErr != nil {
					log.Printf("[trading/engine] pending order %d: auto-decline failed: %v", po.ID, decErr)
				}
			}
		}
	}

	// ── Process APPROVED orders ───────────────────────────────────────────────
	approved := trading.OrderStatusApproved
	orders, err := e.orders.ListByStatus(ctx, &approved)
	if err != nil {
		log.Printf("[trading/engine] failed to list approved orders: %v", err)
		return
	}

	for i := range orders {
		o := orders[i] // capture loop var for goroutine

		// ── Settlement date expiry check ──────────────────────────────────────
		// Futures and options with a passed settlement date must be declined
		// automatically, regardless of order state.  We do this in the polling
		// loop (not inside the goroutine) so the check runs even for orders
		// that haven't been picked up by a goroutine yet.
		snapshot, err := e.market.GetMarketSnapshot(ctx, o.ListingID)
		if err != nil {
			log.Printf("[trading/engine] order %d: failed to get market snapshot: %v", o.ID, err)
			continue
		}

		if isSettlementExpired(snapshot) {
			log.Printf("[trading/engine] order %d: settlement date expired — declining", o.ID)
			if _, err := e.orders.UpdateStatus(ctx, o.ID, trading.OrderStatusDeclined, nil); err != nil {
				log.Printf("[trading/engine] order %d: failed to decline (expired settlement): %v", o.ID, err)
			}
			continue
		}

		// ── Spawn goroutine if not already active ─────────────────────────────
		if _, loaded := e.active.LoadOrStore(o.ID, struct{}{}); loaded {
			// A goroutine is already running for this order — skip.
			continue
		}

		orderCtx := WithIsClient(ctx, o.IsClient)
		go e.runOrder(orderCtx, &o)
	}
}

// ─── Per-order goroutine ──────────────────────────────────────────────────────

// runOrder is the goroutine body for a single order.
// It owns the full lifecycle: stop activation → (optional) limit trigger → partial fills → completion.
func (e *Engine) runOrder(ctx context.Context, order *trading.Order) {
	defer e.active.Delete(order.ID)

	log.Printf("[trading/engine] order %d (%s %s): goroutine started", order.ID, order.OrderType, order.Direction)

	// Determine the effective order type after (potential) stop activation.
	effectiveType := order.OrderType

	switch order.OrderType {
	case trading.OrderTypeStop, trading.OrderTypeStopLimit:
		activated, err := e.waitForStopActivation(ctx, order)
		if err != nil {
			log.Printf("[trading/engine] order %d: stop activation error: %v", order.ID, err)
			return
		}
		if !activated {
			// Order was canceled or ctx was done while waiting.
			log.Printf("[trading/engine] order %d: stop activation aborted (canceled or shutdown)", order.ID)
			return
		}
		// STOP  → execute as MARKET (fill at live Ask / Bid)
		// STOP_LIMIT → execute as LIMIT (fill at user's PricePerUnit)
		if order.OrderType == trading.OrderTypeStop {
			effectiveType = trading.OrderTypeMarket
		} else {
			effectiveType = trading.OrderTypeLimit
		}
		log.Printf("[trading/engine] order %d: stop triggered — executing as %s", order.ID, effectiveType)

	case trading.OrderTypeMarket, trading.OrderTypeLimit:
		// No stop activation; proceed to limit trigger or execution.

	default:
		log.Printf("[trading/engine] order %d: unknown order type %q — aborting", order.ID, order.OrderType)
		return
	}

	// Limit / stop-limit (post-trigger): wait until Ask/Bid crosses the limit (price watcher).
	if effectiveType == trading.OrderTypeLimit {
		activated, err := e.waitForLimitActivation(ctx, order)
		if err != nil {
			log.Printf("[trading/engine] order %d: limit activation error: %v", order.ID, err)
			return
		}
		if !activated {
			log.Printf("[trading/engine] order %d: limit activation aborted (canceled or shutdown)", order.ID)
			return
		}
	}

	if err := e.executeOrder(ctx, order, effectiveType); err != nil {
		log.Printf("[trading/engine] order %d: execution error: %v", order.ID, err)
	}
}

// ─── Stop activation ──────────────────────────────────────────────────────────

// waitForStopActivation blocks until the stop-price condition is met for a
// STOP or STOP_LIMIT order, or until the order is canceled / ctx is done.
//
// Uslov (usklađeno sa specifikacijom):
//
//	BUY  STOP / STOP_LIMIT: Ask >= StopPrice (kupovni stop kad cena poraste)
//	SELL STOP / STOP_LIMIT: Bid <= StopPrice (prodajni stop kad cena padne)
//
// Posle aktivacije: STOP → izvršavanje kao MARKET; STOP_LIMIT → kao LIMIT (PricePerUnit).
//
// Returns (true, nil) when the condition is met.
// Returns (false, nil) when the order is no longer active (canceled externally
// or ctx was done).
// Returns (false, err) on unexpected DB or market-data errors.
func (e *Engine) waitForStopActivation(ctx context.Context, order *trading.Order) (bool, error) {
	if order.StopPrice == nil {
		// Should not happen — validated at creation. Treat as immediate trigger.
		log.Printf("[trading/engine] order %d: StopPrice is nil — triggering immediately", order.ID)
		return true, nil
	}

	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, nil

		case <-ticker.C:
			// Re-fetch the order to detect cancellation.
			current, err := e.orders.GetByID(ctx, order.ID)
			if err != nil {
				return false, err
			}
			if current.Status != trading.OrderStatusApproved || current.IsDone {
				return false, nil // canceled or done externally
			}

			// Check live market price against stop threshold.
			snapshot, err := e.market.GetMarketSnapshot(ctx, order.ListingID)
			if err != nil {
				// Log and continue — a transient market-data error shouldn't
				// abort the goroutine; the next tick will retry.
				log.Printf("[trading/engine] order %d: market snapshot error during stop poll: %v", order.ID, err)
				continue
			}

			stopPrice := *order.StopPrice
			switch order.Direction {
			case trading.OrderDirectionBuy:
				// BUY stop activates when Ask rises to or above StopPrice.
				if decimal.NewFromFloat(snapshot.Ask).GreaterThanOrEqual(stopPrice) {
					return true, nil
				}
			case trading.OrderDirectionSell:
				// SELL stop activates when Bid falls to or below StopPrice.
				if decimal.NewFromFloat(snapshot.Bid).LessThanOrEqual(stopPrice) {
					return true, nil
				}
			}

			log.Printf("[trading/engine] order %d: stop not triggered yet (Ask=%.6f Bid=%.6f Stop=%s)",
				order.ID, snapshot.Ask, snapshot.Bid, stopPrice.String())
		}
	}
}

// ─── Limit activation (price watcher) ─────────────────────────────────────────

// waitForLimitActivation blocks until a LIMIT order can execute:
//
//	BUY  LIMIT: Ask <= PricePerUnit (buy at or below the limit)
//	SELL LIMIT: Bid >= PricePerUnit

func (e *Engine) waitForLimitActivation(ctx context.Context, order *trading.Order) (bool, error) {
	if order.PricePerUnit == nil {
		log.Printf("[trading/engine] order %d: PricePerUnit missing on LIMIT — aborting", order.ID)
		return false, nil
	}
	limit := *order.PricePerUnit

	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, nil
		case <-ticker.C:
			current, err := e.orders.GetByID(ctx, order.ID)
			if err != nil {
				return false, err
			}
			if current.Status != trading.OrderStatusApproved || current.IsDone {
				return false, nil
			}
			snapshot, err := e.market.GetMarketSnapshot(ctx, order.ListingID)
			if err != nil {
				log.Printf("[trading/engine] order %d: market snapshot error during limit poll: %v", order.ID, err)
				continue
			}
			ask := decimal.NewFromFloat(snapshot.Ask)
			bid := decimal.NewFromFloat(snapshot.Bid)
			switch order.Direction {
			case trading.OrderDirectionBuy:
				if ask.LessThanOrEqual(limit) {
					return true, nil
				}
			case trading.OrderDirectionSell:
				if bid.GreaterThanOrEqual(limit) {
					return true, nil
				}
			}
			log.Printf("[trading/engine] order %d: limit not met yet (Ask=%.6f Bid=%.6f Limit=%s)",
				order.ID, snapshot.Ask, snapshot.Bid, limit.String())
		}
	}
}

// commissionForOrderType applies MARKET vs LIMIT fee schedules using the original
// order type (STOP counts as MARKET, STOP_LIMIT as LIMIT).
func commissionForOrderType(ot trading.OrderType, fullNotional decimal.Decimal) decimal.Decimal {
	switch ot {
	case trading.OrderTypeMarket, trading.OrderTypeStop:
		return trading.CalcMarketCommission(fullNotional)
	case trading.OrderTypeLimit, trading.OrderTypeStopLimit:
		return trading.CalcLimitCommission(fullNotional)
	default:
		return decimal.Zero
	}
}

// ─── Execution loop ───────────────────────────────────────────────────────────

// executeOrder runs the partial-fill simulation for one order until it is
// fully filled, canceled externally, or the context is canceled.
//
// Sleep is applied *between* partial fills (not before the first fill).
// After-hours adds a fixed 30 minutes per wait segment.
func (e *Engine) executeOrder(ctx context.Context, order *trading.Order, effectiveType trading.OrderType) error {
	for {
		current, err := e.orders.GetByID(ctx, order.ID)
		if err != nil {
			return err
		}
		if current.Status != trading.OrderStatusApproved || current.IsDone {
			log.Printf("[trading/engine] order %d: order not active (%s is_done=%v) — exiting",
				order.ID, current.Status, current.IsDone)
			return nil
		}

		snapshot, err := e.market.GetMarketSnapshot(ctx, current.ListingID)
		if err != nil {
			return err
		}

		executedPrice, err := resolveExecutionPrice(current, effectiveType, snapshot)
		if err != nil {
			return err
		}

		var chunkSize int32
		if current.AllOrNone {
			chunkSize = current.RemainingPortions
		} else {
			chunkSize = calcChunkSize(current.RemainingPortions)
		}

		if _, err := e.orders.CreateTransaction(ctx, current.ID, chunkSize, executedPrice); err != nil {
			return err
		}

		if current.RemainingPortions == current.Quantity {
			notional := executedPrice.Mul(decimal.NewFromInt(int64(current.Quantity)))
			commission := commissionForOrderType(current.OrderType, notional)
			if commission.IsPositive() {
				if err := e.funds.ChargeCommission(ctx, current.AccountID, commission); err != nil {
					log.Printf("[trading/engine] order %d: charge commission error: %v", current.ID, err)
				} else {
					log.Printf("[trading/engine] order %d: commission charged: %s", current.ID, commission.String())
				}
			}
		}

		fillAmount := executedPrice.Mul(decimal.NewFromInt(int64(chunkSize)))
		if current.Direction == trading.OrderDirectionBuy {
			if err := e.funds.SettleBuyFill(ctx, current.AccountID, fillAmount); err != nil {
				log.Printf("[trading/engine] order %d: settle buy fill: %v", current.ID, err)
			}
		} else {
			if err := e.funds.CreditSellFill(ctx, current.AccountID, fillAmount); err != nil {
				log.Printf("[trading/engine] order %d: credit sell fill: %v", current.ID, err)
			}
		}

		newRemaining := current.RemainingPortions - chunkSize
		log.Printf("[trading/engine] order %d: filled %d contracts at %s (remaining: %d → %d)",
			current.ID, chunkSize, executedPrice.String(), current.RemainingPortions, newRemaining)

		if newRemaining == 0 {
			if _, err := e.orders.MarkDone(ctx, current.ID); err != nil {
				return err
			}
			log.Printf("[trading/engine] order %d: fully executed — DONE", current.ID)
			return nil
		}

		if _, err := e.orders.UpdateRemainingPortions(ctx, current.ID, newRemaining, false); err != nil {
			return err
		}

		order = current
		order.RemainingPortions = newRemaining

		waitSecs := calcWaitSeconds(snapshot.Volume, newRemaining)
		if current.AfterHours {
			waitSecs += afterHoursPenaltySeconds
		}
		log.Printf("[trading/engine] order %d: sleeping %.1fs before next fill (remaining=%d)",
			order.ID, waitSecs, newRemaining)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(waitSecs * float64(time.Second))):
		}
	}
}

// ─── Pure helper functions ────────────────────────────────────────────────────

// isSettlementExpired returns true when the market snapshot carries a
// settlement date that is in the past (relative to UTC now).
// Always returns false for STOCK and FOREX listings (SettlementDate == nil).
func isSettlementExpired(s MarketSnapshot) bool {
	if s.SettlementDate == nil {
		return false
	}
	return s.SettlementDate.Before(time.Now().UTC())
}

// resolveExecutionPrice computes the per-contract execution price for a fill.
//
//	effectiveType = MARKET: ContractSize × Ask (BUY) or ContractSize × Bid (SELL)
//	effectiveType = LIMIT:  BUY  ContractSize × min(limit, Ask)
//	                   SELL ContractSize × max(limit, Bid)
//
// Returns an error only if a LIMIT order is missing PricePerUnit — which
// should have been caught at order creation; included here as a safety net.
func resolveExecutionPrice(
	order *trading.Order,
	effectiveType trading.OrderType,
	snapshot MarketSnapshot,
) (decimal.Decimal, error) {
	cs := decimal.NewFromInt(int64(order.ContractSize))

	switch effectiveType {
	case trading.OrderTypeMarket:
		var unitPrice float64
		if order.Direction == trading.OrderDirectionBuy {
			unitPrice = snapshot.Ask
		} else {
			unitPrice = snapshot.Bid
		}
		return cs.Mul(decimal.NewFromFloat(unitPrice)), nil

	case trading.OrderTypeLimit:
		if order.PricePerUnit == nil {
			// Defensive; validated at creation.
			return decimal.Zero, trading.ErrLimitPriceRequired
		}
		limit := *order.PricePerUnit
		askDec := decimal.NewFromFloat(snapshot.Ask)
		bidDec := decimal.NewFromFloat(snapshot.Bid)
		var unit decimal.Decimal
		if order.Direction == trading.OrderDirectionBuy {
			// min(limit, Ask)
			if limit.LessThan(askDec) {
				unit = limit
			} else {
				unit = askDec
			}
		} else {
			// max(limit, Bid)
			if limit.GreaterThan(bidDec) {
				unit = limit
			} else {
				unit = bidDec
			}
		}
		return cs.Mul(unit), nil

	default:
		// Unreachable after stop-activation normalises the type.
		return decimal.Zero, trading.ErrInvalidOrderType
	}
}

// calcWaitSeconds returns the simulated delay (in seconds) before the next
// partial fill, implementing the spec formula:
//
//	waitSecs = Random(0, (24 × 60) / (Volume / RemainingPortions))
//
// Where:
//   - "24 × 60" = 1440 is the spec's chosen time-unit constant.
//   - Volume / RemainingPortions proxies market liquidity relative to order size.
//   - Higher ratio (liquid market, small order) → small maxWait → fast fills.
//   - Lower ratio (thin market, large order)   → large maxWait → slow fills.
//
// Guard rails:
//   - volume == 0 or remaining == 0: returns defaultWaitSeconds.
//   - result < minWaitSeconds: clamped to minWaitSeconds.
//   - result > maxWaitSeconds: clamped to maxWaitSeconds.
func calcWaitSeconds(volume int64, remaining int32) float64 {
	if volume <= 0 || remaining <= 0 {
		return defaultWaitSeconds
	}

	turnsPerPortion := float64(volume) / float64(remaining)
	maxWait := float64(24*60) / turnsPerPortion

	switch {
	case maxWait < minWaitSeconds:
		maxWait = minWaitSeconds
	case maxWait > maxWaitSeconds:
		maxWait = maxWaitSeconds
	}

	return rand.Float64() * maxWait //nolint:gosec — simulation randomness, not crypto
}

// calcChunkSize returns a random partial-fill size in [1, min(remaining, 10)].
// The upper bound of 10 matches the spec: "choose a random number of items
// between 1 and MIN(RemainingPortions, 10)".
func calcChunkSize(remaining int32) int32 {
	maxChunk := remaining
	if maxChunk > 10 {
		maxChunk = 10
	}
	// rand.Intn(n) returns [0, n); adding 1 gives [1, maxChunk].
	return int32(rand.Intn(int(maxChunk))) + 1 //nolint:gosec — simulation randomness
}
