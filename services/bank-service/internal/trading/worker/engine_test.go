package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ─── Minimal mocks ────────────────────────────────────────────────────────────

type stubOrderRepo struct {
	listByStatusFn func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error)
	getByIDFn      func(ctx context.Context, id int64) (*trading.Order, error)
	updateStatusFn func(ctx context.Context, id int64, status trading.OrderStatus, approvedBy *string) (*trading.Order, error)
	cancelFn       func(ctx context.Context, id int64) (*trading.Order, error)
}

func (r *stubOrderRepo) Create(ctx context.Context, req trading.CreateOrderRequest, status trading.OrderStatus) (*trading.Order, error) {
	return &trading.Order{ID: 1, Status: status}, nil
}
func (r *stubOrderRepo) GetByID(ctx context.Context, id int64) (*trading.Order, error) {
	if r.getByIDFn != nil {
		return r.getByIDFn(ctx, id)
	}
	return nil, trading.ErrOrderNotFound
}
func (r *stubOrderRepo) UpdateStatus(ctx context.Context, id int64, status trading.OrderStatus, approvedBy *string) (*trading.Order, error) {
	if r.updateStatusFn != nil {
		return r.updateStatusFn(ctx, id, status, approvedBy)
	}
	return &trading.Order{ID: id, Status: status}, nil
}
func (r *stubOrderRepo) UpdateRemainingPortions(ctx context.Context, id int64, remaining int32, isDone bool) (*trading.Order, error) {
	return &trading.Order{ID: id}, nil
}
func (r *stubOrderRepo) ListByUserID(ctx context.Context, userID int64, filter *trading.OrderStatus) ([]trading.Order, error) {
	return nil, nil
}
func (r *stubOrderRepo) ListByStatus(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
	if r.listByStatusFn != nil {
		return r.listByStatusFn(ctx, status)
	}
	return nil, nil
}
func (r *stubOrderRepo) ListActiveByListing(ctx context.Context, listingID int64) ([]trading.Order, error) {
	return nil, nil
}
func (r *stubOrderRepo) CreateTransaction(ctx context.Context, orderID int64, qty int32, price decimal.Decimal) (*trading.OrderTransaction, error) {
	return &trading.OrderTransaction{}, nil
}
func (r *stubOrderRepo) GetTransactionsByOrderID(ctx context.Context, orderID int64) ([]trading.OrderTransaction, error) {
	return nil, nil
}
func (r *stubOrderRepo) MarkDone(ctx context.Context, id int64) (*trading.Order, error) {
	return &trading.Order{ID: id}, nil
}
func (r *stubOrderRepo) Cancel(ctx context.Context, id int64) (*trading.Order, error) {
	if r.cancelFn != nil {
		return r.cancelFn(ctx, id)
	}
	return &trading.Order{ID: id, Status: trading.OrderStatusCanceled}, nil
}
func (r *stubOrderRepo) GetNetHoldings(ctx context.Context, userID, listingID int64) (int64, error) {
	return 100, nil
}
func (r *stubOrderRepo) WithDB(db *gorm.DB) trading.OrderRepository { return r }

type stubMarketProvider struct {
	getSnapshotFn func(ctx context.Context, listingID int64) (MarketSnapshot, error)
}

func (m *stubMarketProvider) GetMarketSnapshot(ctx context.Context, listingID int64) (MarketSnapshot, error) {
	if m.getSnapshotFn != nil {
		return m.getSnapshotFn(ctx, listingID)
	}
	return MarketSnapshot{Ask: 10.0, Bid: 9.5}, nil
}

type stubFundsManagerEngine struct{}

func (s *stubFundsManagerEngine) ReserveFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) ReleaseFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *stubFundsManagerEngine) HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *stubFundsManagerEngine) ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error) {
	return usdAmount, nil
}
func (s *stubFundsManagerEngine) ReserveForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) ReleaseForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerEngine) ForexSwap(ctx context.Context, userID int64, fromAccountID int64, baseCurrency, quoteCurrency string, nominalBase, rate decimal.Decimal, direction trading.OrderDirection) error {
	return nil
}
func (s *stubFundsManagerEngine) WithDB(db *gorm.DB) trading.FundsManager { return s }

type stubExchangeChecker struct {
	statusFn func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error)
}

func (e *stubExchangeChecker) IsExchangeOpen(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
	if e.statusFn != nil {
		return e.statusFn(ctx, exchangeID)
	}
	return domain.MarketStatusOpen, nil
}

func newTestEngine(orders trading.OrderRepository, market MarketDataProvider, exchange ExchangeChecker) *Engine {
	return NewEngine(orders, market, &stubFundsManagerEngine{}, exchange, nil, 100*time.Millisecond, nil)
}

// ─── NewEngine ────────────────────────────────────────────────────────────────

func TestNewEngine_NotNil(t *testing.T) {
	e := NewEngine(&stubOrderRepo{}, &stubMarketProvider{}, &stubFundsManagerEngine{}, nil, nil, 0, nil)
	if e == nil {
		t.Error("NewEngine should return non-nil engine")
	}
}

func TestNewEngine_DefaultPollInterval(t *testing.T) {
	// Zero poll interval should fall back to defaultPollInterval
	e := NewEngine(&stubOrderRepo{}, &stubMarketProvider{}, &stubFundsManagerEngine{}, nil, nil, 0, nil)
	if e.pollInterval != defaultPollInterval {
		t.Errorf("expected defaultPollInterval %s, got %s", defaultPollInterval, e.pollInterval)
	}
}

func TestNewEngine_CustomPollInterval(t *testing.T) {
	interval := 2 * time.Second
	e := NewEngine(&stubOrderRepo{}, &stubMarketProvider{}, &stubFundsManagerEngine{}, nil, nil, interval, nil)
	if e.pollInterval != interval {
		t.Errorf("expected %s, got %s", interval, e.pollInterval)
	}
}

func TestNewEngine_WithTickBus(t *testing.T) {
	bus := NewPriceTickBus()
	e := NewEngine(&stubOrderRepo{}, &stubMarketProvider{}, &stubFundsManagerEngine{}, nil, nil, 0, bus)
	if e.tickBus != bus {
		t.Error("tickBus should be set")
	}
}

// ─── Start ────────────────────────────────────────────────────────────────────

func TestStart_ExitsOnContextCancel(t *testing.T) {
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			return nil, nil
		},
	}
	market := &stubMarketProvider{}
	e := newTestEngine(orders, market, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Start(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// Engine exited cleanly
	case <-time.After(2 * time.Second):
		t.Error("Start did not exit after context cancellation")
	}
}

// ─── tick ─────────────────────────────────────────────────────────────────────

func TestTick_EmptyOrders(t *testing.T) {
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			return nil, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)
	// Should not panic
	e.tick(context.Background())
}

func TestTick_ListByStatus_Error(t *testing.T) {
	callCount := 0
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			callCount++
			if callCount == 1 {
				// First call (pending orders) returns error
				return nil, errors.New("db error")
			}
			return nil, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)
	// Should not panic on error
	e.tick(context.Background())
}

func TestTick_PendingOrder_SnapshotError_Continues(t *testing.T) {
	pendingStatus := trading.OrderStatusPending
	callCount := 0
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			callCount++
			if callCount == 1 && status != nil && *status == pendingStatus {
				return []trading.Order{{ID: 1, ListingID: 99}}, nil
			}
			return nil, nil
		},
	}
	market := &stubMarketProvider{
		getSnapshotFn: func(ctx context.Context, listingID int64) (MarketSnapshot, error) {
			return MarketSnapshot{}, errors.New("snapshot unavailable")
		},
	}
	e := newTestEngine(orders, market, nil)
	// Should not panic; logs and continues
	e.tick(context.Background())
}

func TestTick_PendingOrder_ExpiredSettlement_Declines(t *testing.T) {
	pastDate := time.Now().UTC().Add(-24 * time.Hour)
	declined := false
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			if status != nil && *status == trading.OrderStatusPending {
				return []trading.Order{{ID: 42, ListingID: 1}}, nil
			}
			return nil, nil
		},
		updateStatusFn: func(ctx context.Context, id int64, status trading.OrderStatus, approvedBy *string) (*trading.Order, error) {
			if id == 42 && status == trading.OrderStatusDeclined {
				declined = true
			}
			return &trading.Order{ID: id, Status: status}, nil
		},
	}
	market := &stubMarketProvider{
		getSnapshotFn: func(ctx context.Context, listingID int64) (MarketSnapshot, error) {
			return MarketSnapshot{SettlementDate: &pastDate}, nil
		},
	}
	e := newTestEngine(orders, market, nil)
	e.tick(context.Background())

	if !declined {
		t.Error("expected pending order with expired settlement to be declined")
	}
}

func TestTick_ApprovedOrder_SnapshotError_Skips(t *testing.T) {
	approvedStatus := trading.OrderStatusApproved
	callCount := 0
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			callCount++
			if callCount == 1 {
				// pending
				return nil, nil
			}
			// approved - one order
			return []trading.Order{{ID: 5, ListingID: 10, Status: approvedStatus}}, nil
		},
	}
	market := &stubMarketProvider{
		getSnapshotFn: func(ctx context.Context, listingID int64) (MarketSnapshot, error) {
			return MarketSnapshot{}, errors.New("market unavailable")
		},
	}
	e := newTestEngine(orders, market, nil)
	// Should not panic
	e.tick(context.Background())
}

func TestTick_ApprovedOrder_ExpiredSettlement_Declines(t *testing.T) {
	pastDate := time.Now().UTC().Add(-48 * time.Hour)
	declined := false
	callCount := 0
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			callCount++
			if callCount == 1 {
				return nil, nil // pending
			}
			return []trading.Order{{ID: 7, ListingID: 1, Status: trading.OrderStatusApproved}}, nil
		},
		updateStatusFn: func(ctx context.Context, id int64, status trading.OrderStatus, approvedBy *string) (*trading.Order, error) {
			if id == 7 && status == trading.OrderStatusDeclined {
				declined = true
			}
			return &trading.Order{ID: id, Status: status}, nil
		},
	}
	market := &stubMarketProvider{
		getSnapshotFn: func(ctx context.Context, listingID int64) (MarketSnapshot, error) {
			return MarketSnapshot{SettlementDate: &pastDate}, nil
		},
	}
	e := newTestEngine(orders, market, nil)
	e.tick(context.Background())

	if !declined {
		t.Error("expected approved order with expired settlement to be declined")
	}
}

func TestTick_ApprovedOrder_ValidSnapshot_SpawnsGoroutine(t *testing.T) {
	callCount := 0
	orders := &stubOrderRepo{
		listByStatusFn: func(ctx context.Context, status *trading.OrderStatus) ([]trading.Order, error) {
			callCount++
			if callCount == 1 {
				return nil, nil
			}
			return []trading.Order{{
				ID: 8, ListingID: 1,
				Status:    trading.OrderStatusApproved,
				OrderType: trading.OrderTypeMarket,
				Direction: trading.OrderDirectionBuy,
				Quantity:  1, ContractSize: 1,
			}}, nil
		},
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			// Make the goroutine exit early by returning a done order
			return &trading.Order{ID: id, Status: trading.OrderStatusCanceled, IsDone: true}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)
	e.tick(context.Background())
	// Give spawned goroutine time to exit
	time.Sleep(50 * time.Millisecond)
}

// ─── isExchangeClosed ─────────────────────────────────────────────────────────

func TestIsExchangeClosed_NilExchange(t *testing.T) {
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, nil)
	result := e.isExchangeClosed(context.Background(), 1)
	if result {
		t.Error("nil exchange checker should be treated as open (not closed)")
	}
}

func TestIsExchangeClosed_ZeroExchangeID(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			t.Error("should not be called for exchangeID <= 0")
			return domain.MarketStatusClosed, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 0)
	if result {
		t.Error("exchangeID=0 should be treated as open")
	}
}

func TestIsExchangeClosed_ReturnsClosed(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusClosed, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 1)
	if !result {
		t.Error("expected true when exchange returns CLOSED")
	}
}

func TestIsExchangeClosed_ReturnsOpen(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusOpen, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 1)
	if result {
		t.Error("expected false when exchange returns OPEN")
	}
}

func TestIsExchangeClosed_Error_TreatedAsOpen(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusClosed, errors.New("redis down")
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 1)
	if result {
		t.Error("on error, exchange should be treated as open (conservative)")
	}
}

func TestIsExchangeClosed_PreMarket_NotClosed(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusPreMarket, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 1)
	if result {
		t.Error("PRE_MARKET should not be considered closed")
	}
}

func TestIsExchangeClosed_AfterHours_NotClosed(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusAfterHours, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	result := e.isExchangeClosed(context.Background(), 1)
	if result {
		t.Error("AFTER_HOURS should not be considered closed")
	}
}

// ─── waitForExchangeOpen ──────────────────────────────────────────────────────

func TestWaitForExchangeOpen_NilExchange(t *testing.T) {
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, nil)
	open, err := e.waitForExchangeOpen(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !open {
		t.Error("nil exchange checker should return open=true immediately")
	}
}

func TestWaitForExchangeOpen_ZeroExchangeID(t *testing.T) {
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, &stubExchangeChecker{})
	open, err := e.waitForExchangeOpen(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !open {
		t.Error("exchangeID=0 should return open=true immediately")
	}
}

func TestWaitForExchangeOpen_ExchangeOpen_ReturnsImmediately(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusOpen, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	open, err := e.waitForExchangeOpen(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !open {
		t.Error("OPEN exchange should return (true, nil)")
	}
}

func TestWaitForExchangeOpen_ExchangePreMarket_ReturnsImmediately(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusPreMarket, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)
	open, err := e.waitForExchangeOpen(context.Background(), 1)
	if err != nil || !open {
		t.Errorf("PRE_MARKET should return (true, nil), got open=%v err=%v", open, err)
	}
}

func TestWaitForExchangeOpen_ContextCancelled_ReturnsFalse(t *testing.T) {
	checker := &stubExchangeChecker{
		statusFn: func(ctx context.Context, exchangeID int64) (domain.MarketStatus, error) {
			return domain.MarketStatusClosed, nil
		},
	}
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, checker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	open, err := e.waitForExchangeOpen(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if open {
		t.Error("cancelled context should result in open=false")
	}
}

// ─── executeOrder early exit ──────────────────────────────────────────────────

func TestExecuteOrder_OrderNotActive_ReturnsNil(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return &trading.Order{ID: id, Status: trading.OrderStatusCanceled, IsDone: false}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{
		ID: 10, ListingID: 1,
		Status:    trading.OrderStatusApproved,
		OrderType: trading.OrderTypeMarket,
		Direction: trading.OrderDirectionBuy,
	}
	err := e.executeOrder(context.Background(), order, trading.OrderTypeMarket)
	if err != nil {
		t.Errorf("expected nil error for non-active order, got: %v", err)
	}
}

func TestExecuteOrder_OrderIsDone_ReturnsNil(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return &trading.Order{ID: id, Status: trading.OrderStatusApproved, IsDone: true}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{ID: 11, ListingID: 1, OrderType: trading.OrderTypeLimit}
	err := e.executeOrder(context.Background(), order, trading.OrderTypeLimit)
	if err != nil {
		t.Errorf("expected nil error for done order, got: %v", err)
	}
}

func TestExecuteOrder_SnapshotError_ReturnsError(t *testing.T) {
	orders := &stubOrderRepo{}
	market := &stubMarketProvider{
		getSnapshotFn: func(ctx context.Context, listingID int64) (MarketSnapshot, error) {
			return MarketSnapshot{}, errors.New("snapshot error")
		},
	}
	e := newTestEngine(orders, market, nil)

	order := &trading.Order{ID: 12, ListingID: 1, OrderType: trading.OrderTypeMarket}
	err := e.executeOrder(context.Background(), order, trading.OrderTypeMarket)
	if err == nil {
		t.Error("expected error when market snapshot fails")
	}
}

func TestExecuteOrder_GetByIDError_ReturnsError(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return nil, errors.New("db error")
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{ID: 13, ListingID: 1, OrderType: trading.OrderTypeMarket}
	err := e.executeOrder(context.Background(), order, trading.OrderTypeMarket)
	if err == nil {
		t.Error("expected error when GetByID fails")
	}
}

// ─── executeForexOrder early exit ─────────────────────────────────────────────

func TestExecuteForexOrder_OrderNotActive_ReturnsNil(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return &trading.Order{ID: id, Status: trading.OrderStatusCanceled}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{ID: 20, ListingID: 1}
	snap := MarketSnapshot{ListingType: domain.ListingTypeForex}
	err := e.executeForexOrder(context.Background(), order, snap)
	if err != nil {
		t.Errorf("expected nil for non-active forex order, got: %v", err)
	}
}

func TestExecuteForexOrder_GetByIDError_ReturnsError(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return nil, errors.New("db error")
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{ID: 21, ListingID: 1}
	snap := MarketSnapshot{ListingType: domain.ListingTypeForex}
	err := e.executeForexOrder(context.Background(), order, snap)
	if err == nil {
		t.Error("expected error when GetByID fails in forex order")
	}
}

// ─── runOrder ─────────────────────────────────────────────────────────────────

func TestRunOrder_UnknownOrderType_ExitsGracefully(t *testing.T) {
	e := newTestEngine(&stubOrderRepo{}, &stubMarketProvider{}, nil)

	order := &trading.Order{ID: 30, OrderType: "UNKNOWN_TYPE", Direction: trading.OrderDirectionBuy}
	done := make(chan struct{})
	go func() {
		e.runOrder(context.Background(), order)
		close(done)
	}()

	select {
	case <-done:
		// Good: exited without panic
	case <-time.After(time.Second):
		t.Error("runOrder with unknown order type should exit immediately")
	}
}

func TestRunOrder_MarketOrder_CancelledOrder_ExitsGracefully(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			return &trading.Order{ID: id, Status: trading.OrderStatusCanceled}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	order := &trading.Order{
		ID:           31,
		OrderType:    trading.OrderTypeMarket,
		Direction:    trading.OrderDirectionBuy,
		ListingID:    1,
		Quantity:     1,
		ContractSize: 1,
	}
	done := make(chan struct{})
	go func() {
		e.runOrder(context.Background(), order)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Error("runOrder with market order on cancelled order should exit quickly")
	}
}

func TestRunOrder_StopOrder_NilStopPrice_TriggersImmediately(t *testing.T) {
	orders := &stubOrderRepo{
		getByIDFn: func(ctx context.Context, id int64) (*trading.Order, error) {
			// Return cancelled so executeOrder returns early
			return &trading.Order{ID: id, Status: trading.OrderStatusCanceled}, nil
		},
	}
	e := newTestEngine(orders, &stubMarketProvider{}, nil)

	// STOP with nil StopPrice: waitForStopActivation returns (true, nil) immediately
	// Then executeOrder sees cancelled order and returns nil
	order := &trading.Order{
		ID:           32,
		OrderType:    trading.OrderTypeStop,
		Direction:    trading.OrderDirectionBuy,
		ListingID:    1,
		Quantity:     1,
		ContractSize: 1,
		StopPrice:    nil, // triggers immediately
	}
	done := make(chan struct{})
	go func() {
		e.runOrder(context.Background(), order)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Error("runOrder with STOP order (nil stop price) should trigger immediately and exit")
	}
}
