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
	"errors"
	"log"
	"math/rand"
	"sync"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
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

	// ExchangeID je ID berze na kojoj se hartija trguje.
	// Koristi se za proveru da li je berza otvorena pre svakog fill-a.
	ExchangeID int64

	// SettlementDate is nil for STOCK and FOREX listings.
	// Non-nil for FUTURE and OPTION; used for settlement-date expiry checks.
	// The concrete implementation parses this from listing.details_json.
	SettlementDate *time.Time

	// ListingType je tip hartije od vrednosti (STOCK, FOREX, FUTURE, OPTION).
	// Koristi se u engine-u za odlučivanje o putanji izvršavanja (standardni fill loop vs. forex swap).
	ListingType domain.ListingType

	// ForexBaseCurrency i ForexQuoteCurrency su popunjeni samo za FOREX listinge.
	// Npr. za EUR/USD: BaseCurrency="EUR", QuoteCurrency="USD".
	ForexBaseCurrency  string
	ForexQuoteCurrency string
}

// ExchangeChecker proverava da li je berza otvorena u trenutku izvršenja.
// Implementira ga domain.BerzaService koji je već inicijalizovan u main.go.
type ExchangeChecker interface {
	IsExchangeOpen(ctx context.Context, exchangeID int64) (domain.MarketStatus, error)
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
	orders   trading.OrderRepository
	market   MarketDataProvider
	funds    trading.FundsManager
	exchange ExchangeChecker
	db       *gorm.DB // koristi se za atomično omotavanje fill operacija u transakciju

	// active tracks orderIDs that are currently being processed by a goroutine.
	// Using sync.Map because reads dominate and keys are added/deleted by
	// different goroutines.
	active sync.Map // map[int64]struct{}

	pollInterval time.Duration

	// tickBus, ako je postavljen, isporučuje cenovne tikove od ListingRefresherWorker-a
	// direktno do waitForLimitActivation goroutina — eliminišući potrebu za polling-om
	// unutar svake goroutine i omogućavajući event-driven LIMIT okidanje.
	// Može biti nil; u tom slučaju waitForLimitActivation pada nazad na originalni polling.
	tickBus *PriceTickBus
}

// NewEngine constructs an Engine with its dependencies.
//
// db je GORM konekcija koja se koristi za atomično omotavanje fill operacija.
// pollInterval controls how often the main loop scans for APPROVED orders.
// Pass 0 to use the package-level defaultPollInterval.
// exchange se koristi za proveru da li je berza otvorena pre svakog fill-a.
// tickBus, ako nije nil, omogućava event-driven LIMIT aktivaciju umesto polling-a.
// Prosleđuje se i ListingRefresherWorker-u (kao worker.PriceTickPublisher) u main.go.
func NewEngine(
	orders trading.OrderRepository,
	market MarketDataProvider,
	funds trading.FundsManager,
	exchange ExchangeChecker,
	db *gorm.DB,
	pollInterval time.Duration,
	tickBus *PriceTickBus,
) *Engine {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Engine{
		orders:       orders,
		market:       market,
		funds:        funds,
		exchange:     exchange,
		db:           db,
		pollInterval: pollInterval,
		tickBus:      tickBus,
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

// ─── Limit activation (event-driven + fallback poll) ──────────────────────────

// waitForLimitActivation blocks until a LIMIT order can execute:
//
//	BUY  LIMIT: Ask <= PricePerUnit
//	SELL LIMIT: Bid >= PricePerUnit
//
// Kada je tickBus postavljen, goroutina se pretplaćuje na PriceTick events od
// ListingRefresherWorker-a i odmah proverava uslov čim cena stigne.  Ovo
// eliminiše kašnjenje koje je postojalo u originalnom 5s polling pristupu.
//
// Fallback poll ticker (stopPollInterval) ostaje aktivan kao safety net:
//   - kada je tickBus nil (backward-compatible)
//   - kada refresher pauzira ili preskače ažuriranje zbog nepostojanja live podataka
//
// Returns (true, nil)  — uslov je ispunjen, nalog se može izvršiti.
// Returns (false, nil) — nalog je otkazan ili je ctx završen.
// Returns (false, err) — neočekivana DB ili market-data greška.
func (e *Engine) waitForLimitActivation(ctx context.Context, order *trading.Order) (bool, error) {
	if order.PricePerUnit == nil {
		log.Printf("[trading/engine] order %d: PricePerUnit missing on LIMIT — aborting", order.ID)
		return false, nil
	}
	limit := *order.PricePerUnit

	// Pretplata na cenovne tikove za ovaj listing.
	// Nil tickCh u select case-u blokira zauvek → bezbedno kada tickBus nije postavljen.
	var tickCh <-chan PriceTick
	if e.tickBus != nil {
		ch := e.tickBus.Subscribe(order.ListingID)
		defer e.tickBus.Unsubscribe(order.ListingID, ch)
		tickCh = ch
		log.Printf("[trading/engine] order %d: subscribed to price ticks for listing %d (LIMIT=%s)",
			order.ID, order.ListingID, limit.String())
	}

	// Fallback poll — ostaje aktivan kao safety net.
	fallback := time.NewTicker(stopPollInterval)
	defer fallback.Stop()

	// limitMet vraća true kada je tržišna cena dostigla ili prošla limit.
	limitMet := func(ask, bid float64) bool {
		switch order.Direction {
		case trading.OrderDirectionBuy:
			return decimal.NewFromFloat(ask).LessThanOrEqual(limit)
		case trading.OrderDirectionSell:
			return decimal.NewFromFloat(bid).GreaterThanOrEqual(limit)
		}
		return false
	}

	for {
		select {
		case <-ctx.Done():
			return false, nil

		case tick := <-tickCh:
			// Najpre proveri da nalog nije otkazan spolja.
			current, err := e.orders.GetByID(ctx, order.ID)
			if err != nil {
				return false, err
			}
			if current.Status != trading.OrderStatusApproved || current.IsDone {
				return false, nil
			}
			if limitMet(tick.Ask, tick.Bid) {
				log.Printf("[trading/engine] order %d: LIMIT triggered by price tick (Ask=%.6f Bid=%.6f Limit=%s)",
					order.ID, tick.Ask, tick.Bid, limit.String())
				return true, nil
			}
			log.Printf("[trading/engine] order %d: tick received, limit not yet met (Ask=%.6f Bid=%.6f Limit=%s)",
				order.ID, tick.Ask, tick.Bid, limit.String())

		case <-fallback.C:
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
			if limitMet(snapshot.Ask, snapshot.Bid) {
				log.Printf("[trading/engine] order %d: LIMIT triggered by fallback poll (Ask=%.6f Bid=%.6f Limit=%s)",
					order.ID, snapshot.Ask, snapshot.Bid, limit.String())
				return true, nil
			}
			log.Printf("[trading/engine] order %d: limit not met (Ask=%.6f Bid=%.6f Limit=%s)",
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

// ─── Exchange open guard ──────────────────────────────────────────────────────

// exchangeCheckInterval je koliko čekamo između ponovnih provera statusa berze
// kada je berza zatvorena i čekamo na otvaranje.
const exchangeCheckInterval = 60 * time.Second

// waitForExchangeOpen blokira dok berza za dati exchangeID nije otvorena (OPEN).
// Ponavlja proveru svakih exchangeCheckInterval sekundi.
// Vraća false (bez greške) ako je ctx otkazan dok čekamo.
// Vraća grešku samo u slučaju neočekivanog DB/service problema.
// Ako ExchangeChecker nije injektovan (nil) ili exchangeID <= 0, prolazi bez čekanja.
func (e *Engine) waitForExchangeOpen(ctx context.Context, exchangeID int64) (bool, error) {
	if e.exchange == nil || exchangeID <= 0 {
		return true, nil
	}

	for {
		status, err := e.exchange.IsExchangeOpen(ctx, exchangeID)
		if err != nil {
			// Prolazna greška — logujemo i čekamo, ne zaustavljamo nalog.
			log.Printf("[trading/engine] waitForExchangeOpen (exchange=%d): greška pri proveri statusa: %v — ponavlja se", exchangeID, err)
		} else if status == domain.MarketStatusOpen ||
			status == domain.MarketStatusPreMarket ||
			status == domain.MarketStatusAfterHours {
			// Simulacija: nalozi se izvršavaju i u produženim satima (pre-market / after-hours).
			// Blokira se samo za vikende, praznike i noć (CLOSED).
			return true, nil
		} else {
			log.Printf("[trading/engine] waitForExchangeOpen (exchange=%d): berza %s (vikend/praznik) — čekanje na otvaranje", exchangeID, status)
		}

		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(exchangeCheckInterval):
		}
	}
}

// ─── Execution loop ───────────────────────────────────────────────────────────

// executeOrder runs the partial-fill simulation for one order until it is
// fully filled, canceled externally, or the context is canceled.
//
// Pre svakog fill-a proverava se da li je berza otvorena (waitForExchangeOpen).
// Ako berza nije otvorena, goroutina čeka dok se ne otvori ili dok se nalog ne otkaže.
// After-hours flag više ne dodaje vremensku kaznu — relevantnost after_hours
// ostaje samo kao informacija o tome kada je nalog kreiran.
//
// FOREX nalozi se izvršavaju odmah kao atomični currency swap (executeForexOrder)
// bez simulacije delimičnih fillova — ova grana se aktivira na početku metode.
func (e *Engine) executeOrder(ctx context.Context, order *trading.Order, effectiveType trading.OrderType) error {
	// ── FOREX: atomic single-shot swap execution ─────────────────────────────
	// Fetch snapshot once to determine listing type. FOREX orders bypass the
	// partial-fill loop entirely and execute as a one-shot currency swap.
	initialSnap, err := e.market.GetMarketSnapshot(ctx, order.ListingID)
	if err != nil {
		return err
	}
	if initialSnap.ListingType == domain.ListingTypeForex {
		return e.executeForexOrder(ctx, order, initialSnap)
	}

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

		// ── Provera da li je berza otvorena ──────────────────────────────────────
		// Fill se ne sme izvršiti dok berza nije OPEN — čak i ako je nalog APPROVED.
		// Goroutina blokira ovde dok se berza ne otvori ili dok se nalog ne otkaže.
		open, err := e.waitForExchangeOpen(ctx, snapshot.ExchangeID)
		if err != nil {
			return err
		}
		if !open {
			// ctx je otkazan — engine se gasi.
			return nil
		}

		// Re-fetch posle potencijalnog dugog čekanja — nalog možda otkazan tokom čekanja.
		current, err = e.orders.GetByID(ctx, order.ID)
		if err != nil {
			return err
		}
		if current.Status != trading.OrderStatusApproved || current.IsDone {
			log.Printf("[trading/engine] order %d: otkazan tokom čekanja na otvaranje berze — izlaz", order.ID)
			return nil
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

		// Sve četiri DB operacije jedne iteracije filla omotane su u jednu transakciju
		// (BUG-4): CreateTransaction + ChargeCommission + SettleBuyFill/CreditSellFill
		// + MarkDone/UpdateRemainingPortions. Ako server crasha između operacija,
		// nijedna parcijalna promena neće ostati u bazi.
		isFirstFill := current.RemainingPortions == current.Quantity
		newRemaining := current.RemainingPortions - chunkSize
		fillAmount := executedPrice.Mul(decimal.NewFromInt(int64(chunkSize)))

		var commission decimal.Decimal
		if isFirstFill {
			notional := executedPrice.Mul(decimal.NewFromInt(int64(current.Quantity)))
			commission = commissionForOrderType(current.OrderType, notional)
		}

		if err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			txOrders := e.orders.WithDB(tx)
			txFunds := e.funds.WithDB(tx)

			if _, err := txOrders.CreateTransaction(ctx, current.ID, chunkSize, executedPrice); err != nil {
				return err
			}
			if isFirstFill && commission.IsPositive() {
				if err := txFunds.ChargeCommission(ctx, current.AccountID, commission); err != nil {
					return err
				}
			}
			if current.Direction == trading.OrderDirectionBuy {
				if err := txFunds.SettleBuyFill(ctx, current.AccountID, fillAmount); err != nil {
					return err
				}
			} else {
				if err := txFunds.CreditSellFill(ctx, current.AccountID, fillAmount); err != nil {
					return err
				}
			}
			if newRemaining == 0 {
				_, err := txOrders.MarkDone(ctx, current.ID)
				return err
			}
			_, err := txOrders.UpdateRemainingPortions(ctx, current.ID, newRemaining, false)
			return err
		}); err != nil {
			if current.Direction == trading.OrderDirectionBuy && errors.Is(err, trading.ErrInsufficientFunds) {
				log.Printf("[trading/engine] order %d: nedovoljno sredstava na računu %d — nalog se odbija", current.ID, current.AccountID)
				if _, decErr := e.orders.UpdateStatus(ctx, current.ID, trading.OrderStatusDeclined, nil); decErr != nil {
					log.Printf("[trading/engine] order %d: greška pri odbijanju: %v", current.ID, decErr)
				}
				releaseAmt := executedPrice.Mul(decimal.NewFromInt(int64(current.RemainingPortions)))
				if relErr := e.funds.ReleaseFunds(ctx, current.AccountID, releaseAmt); relErr != nil {
					log.Printf("[trading/engine] order %d: greška pri oslobađanju rezervacije: %v", current.ID, relErr)
				}
				return nil
			}
			return err
		}

		log.Printf("[trading/engine] order %d: filled %d contracts at %s (remaining: %d → %d)",
			current.ID, chunkSize, executedPrice.String(), current.RemainingPortions, newRemaining)
		if isFirstFill && commission.IsPositive() {
			log.Printf("[trading/engine] order %d: commission charged: %s", current.ID, commission.String())
		}

		if newRemaining == 0 {
			log.Printf("[trading/engine] order %d: fully executed — DONE", current.ID)
			return nil
		}

		order = current
		order.RemainingPortions = newRemaining

		waitSecs := calcWaitSeconds(snapshot.Volume, newRemaining)
		log.Printf("[trading/engine] order %d: sleeping %.1fs before next fill (remaining=%d)",
			order.ID, waitSecs, newRemaining)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(waitSecs * float64(time.Second))):
		}
	}
}

// ─── Forex execution ──────────────────────────────────────────────────────────

// executeForexOrder izvršava forex nalog kao jednokratni atomični currency swap.
//
// Logika izvršenja (u skladu sa specifikacijom):
//   - BUY  BASE/QUOTE: skini (nominalBase × kurs) sa QUOTE računa, uplati nominalBase na BASE račun.
//   - SELL BASE/QUOTE: skini nominalBase sa BASE računa, uplati (nominalBase × kurs) na QUOTE račun.
//
// gde je nominalBase = contractSize × quantity.
//
// Ako je kurs nedostupan, nalog ostaje u APPROVED stanju i engine će ga pokupiti
// na sledećem tick-u (retry strategija bez eksplicitnog čekanja).
//
// Ako validacija računa ili sredstava ne prođe → nalog se odmah prebacuje u DECLINED.
// Sve operacije (swap + CreateTransaction + MarkDone) su u jednoj DB transakciji.
func (e *Engine) executeForexOrder(ctx context.Context, order *trading.Order, snap MarketSnapshot) error {
	// Re-fetch order to detect any external cancellation since the goroutine started.
	current, err := e.orders.GetByID(ctx, order.ID)
	if err != nil {
		return err
	}
	if current.Status != trading.OrderStatusApproved || current.IsDone {
		log.Printf("[trading/engine] forex order %d: not active (%s is_done=%v) — exiting",
			order.ID, current.Status, current.IsDone)
		return nil
	}

	// Wait for exchange to be open before attempting execution.
	open, err := e.waitForExchangeOpen(ctx, snap.ExchangeID)
	if err != nil {
		return err
	}
	if !open {
		return nil // ctx canceled — engine shutting down
	}

	// Re-fetch order and snapshot after potential long wait for exchange open.
	current, err = e.orders.GetByID(ctx, order.ID)
	if err != nil {
		return err
	}
	if current.Status != trading.OrderStatusApproved || current.IsDone {
		log.Printf("[trading/engine] forex order %d: canceled while waiting for exchange — exiting", order.ID)
		return nil
	}
	snap, err = e.market.GetMarketSnapshot(ctx, current.ListingID)
	if err != nil {
		// Rate unavailable — leave order in APPROVED; next tick will retry.
		log.Printf("[trading/engine] forex order %d: snapshot unavailable — will retry on next tick: %v", order.ID, err)
		return nil
	}

	// Determine execution rate: Ask for BUY, Bid for SELL.
	// For LIMIT orders (PricePerUnit != nil), enforce the limit price as a
	// floor/ceiling to protect against the TOCTOU window between activation
	// and this snapshot fetch:
	//   BUY  LIMIT: rate = min(Ask, limitPrice)  — never pay more than limit
	//   SELL LIMIT: rate = max(Bid, limitPrice)  — never receive less than limit
	var rate decimal.Decimal
	if current.Direction == trading.OrderDirectionBuy {
		rate = decimal.NewFromFloat(snap.Ask)
		if current.PricePerUnit != nil && rate.GreaterThan(*current.PricePerUnit) {
			rate = *current.PricePerUnit
		}
	} else {
		rate = decimal.NewFromFloat(snap.Bid)
		if current.PricePerUnit != nil && rate.LessThan(*current.PricePerUnit) {
			rate = *current.PricePerUnit
		}
	}
	if rate.IsZero() {
		log.Printf("[trading/engine] forex order %d: rate is zero — will retry on next tick", order.ID)
		return nil
	}

	// nominalBase = contractSize × quantity (units of BASE currency)
	nominalBase := decimal.NewFromInt(int64(current.ContractSize)).
		Mul(decimal.NewFromInt(int64(current.Quantity)))

	// Execute atomically: swap + transaction record + mark done in one DB transaction.
	var declined bool
	if txErr := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txOrders := e.orders.WithDB(tx)
		txFunds := e.funds.WithDB(tx)

		// Currency swap — validates accounts, locks rows, checks balance (TOCTOU-safe).
		if swapErr := txFunds.ForexSwap(ctx,
			current.UserID, current.AccountID,
			snap.ForexBaseCurrency, snap.ForexQuoteCurrency,
			nominalBase, rate, current.Direction,
		); swapErr != nil {
			if isForexDeclineError(swapErr) {
				declined = true
				log.Printf("[trading/engine] forex order %d: declining — %v", order.ID, swapErr)
				_, decErr := txOrders.UpdateStatus(ctx, current.ID, trading.OrderStatusDeclined, nil)
				return decErr // commit the DECLINED status; nil commits
			}
			return swapErr // unexpected DB error — roll back
		}

		// Record execution (single fill — full quantity, execution rate).
		if _, txErr := txOrders.CreateTransaction(ctx, current.ID, current.Quantity, rate); txErr != nil {
			return txErr
		}
		// Mark order as fully done.
		_, markErr := txOrders.MarkDone(ctx, current.ID)
		return markErr
	}); txErr != nil {
		return txErr
	}

	if declined {
		log.Printf("[trading/engine] forex order %d: DECLINED", order.ID)
	} else {
		log.Printf("[trading/engine] forex order %d: forex swap executed at rate %s — DONE", order.ID, rate.String())
	}
	return nil
}

// isForexDeclineError returns true for business-rule violations in ForexSwap that
// should cause the order to be declined rather than retried.
func isForexDeclineError(err error) bool {
	return errors.Is(err, trading.ErrForexAccountNotFound) ||
		errors.Is(err, trading.ErrForexCurrencyMismatch) ||
		errors.Is(err, trading.ErrForexSameAccount) ||
		errors.Is(err, trading.ErrForexSameCurrency) ||
		errors.Is(err, trading.ErrInsufficientFunds)
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
