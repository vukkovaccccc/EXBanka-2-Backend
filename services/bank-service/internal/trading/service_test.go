package trading

import (
	"context"
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ─── Minimal inline mocks ─────────────────────────────────────────────────────

// mockOrderRepository implements OrderRepository with optional function overrides.
type mockOrderRepository struct {
	createFn         func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error)
	getByIDFn        func(ctx context.Context, id int64) (*Order, error)
	updateStatusFn   func(ctx context.Context, id int64, status OrderStatus, approvedBy *string) (*Order, error)
	listByStatusFn   func(ctx context.Context, status *OrderStatus) ([]Order, error)
	listByUserFn     func(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error)
	cancelFn         func(ctx context.Context, id int64) (*Order, error)
	getNetHoldingsFn func(ctx context.Context, userID, listingID int64) (int64, error)
}

func (m *mockOrderRepository) Create(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
	if m.createFn != nil {
		return m.createFn(ctx, req, status)
	}
	return &Order{ID: 1, Status: status}, nil
}
func (m *mockOrderRepository) GetByID(ctx context.Context, id int64) (*Order, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return nil, ErrOrderNotFound
}
func (m *mockOrderRepository) UpdateStatus(ctx context.Context, id int64, status OrderStatus, approvedBy *string) (*Order, error) {
	if m.updateStatusFn != nil {
		return m.updateStatusFn(ctx, id, status, approvedBy)
	}
	return &Order{ID: id, Status: status}, nil
}
func (m *mockOrderRepository) UpdateRemainingPortions(ctx context.Context, id int64, remaining int32, isDone bool) (*Order, error) {
	return &Order{ID: id}, nil
}
func (m *mockOrderRepository) ListByUserID(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error) {
	if m.listByUserFn != nil {
		return m.listByUserFn(ctx, userID, statusFilter)
	}
	return nil, nil
}
func (m *mockOrderRepository) ListByStatus(ctx context.Context, status *OrderStatus) ([]Order, error) {
	if m.listByStatusFn != nil {
		return m.listByStatusFn(ctx, status)
	}
	return nil, nil
}
func (m *mockOrderRepository) ListActiveByListing(ctx context.Context, listingID int64) ([]Order, error) {
	return nil, nil
}
func (m *mockOrderRepository) CreateTransaction(ctx context.Context, orderID int64, qty int32, price decimal.Decimal) (*OrderTransaction, error) {
	return &OrderTransaction{}, nil
}
func (m *mockOrderRepository) GetTransactionsByOrderID(ctx context.Context, orderID int64) ([]OrderTransaction, error) {
	return nil, nil
}
func (m *mockOrderRepository) MarkDone(ctx context.Context, id int64) (*Order, error) {
	return &Order{ID: id}, nil
}
func (m *mockOrderRepository) Cancel(ctx context.Context, id int64) (*Order, error) {
	if m.cancelFn != nil {
		return m.cancelFn(ctx, id)
	}
	return &Order{ID: id, Status: OrderStatusCanceled}, nil
}
func (m *mockOrderRepository) GetNetHoldings(ctx context.Context, userID, listingID int64) (int64, error) {
	if m.getNetHoldingsFn != nil {
		return m.getNetHoldingsFn(ctx, userID, listingID)
	}
	return 100, nil
}
func (m *mockOrderRepository) WithDB(db *gorm.DB) OrderRepository { return m }

// mockListingService implements domain.ListingService.
type mockListingService struct {
	getByIDFn func(ctx context.Context, id int64) (*domain.ListingCalculated, error)
}

func (m *mockListingService) GetListingByID(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return &domain.ListingCalculated{
		Listing: domain.Listing{ID: id, Ask: 10.0, Bid: 9.5},
	}, nil
}
func (m *mockListingService) ListListings(ctx context.Context, filter domain.ListingFilter) ([]domain.ListingCalculated, int64, error) {
	return nil, 0, nil
}
func (m *mockListingService) GetListingHistory(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error) {
	return nil, nil
}

// mockActuaryRepository implements domain.ActuaryRepository.
type mockActuaryRepository struct {
	getByEmployeeIDFn          func(ctx context.Context, employeeID int64) (*domain.Actuary, error)
	incrementUsedLimitAlwaysFn func(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, bool, error)
}

func (m *mockActuaryRepository) Create(ctx context.Context, input domain.CreateActuaryInput) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (m *mockActuaryRepository) GetByID(ctx context.Context, id int64) (*domain.Actuary, error) {
	return nil, domain.ErrActuaryNotFound
}
func (m *mockActuaryRepository) GetByEmployeeID(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
	if m.getByEmployeeIDFn != nil {
		return m.getByEmployeeIDFn(ctx, employeeID)
	}
	return nil, domain.ErrActuaryNotFound
}
func (m *mockActuaryRepository) List(ctx context.Context, actuaryType string) ([]domain.Actuary, error) {
	return nil, nil
}
func (m *mockActuaryRepository) Update(ctx context.Context, input domain.UpdateActuaryInput) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (m *mockActuaryRepository) Delete(ctx context.Context, id int64) error { return nil }
func (m *mockActuaryRepository) DeleteByEmployeeID(ctx context.Context, employeeID int64) error {
	return nil
}
func (m *mockActuaryRepository) ResetAllUsedLimits(ctx context.Context) error { return nil }
func (m *mockActuaryRepository) IncrementUsedLimitIfWithin(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (m *mockActuaryRepository) IncrementUsedLimitAlways(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, bool, error) {
	if m.incrementUsedLimitAlwaysFn != nil {
		return m.incrementUsedLimitAlwaysFn(ctx, employeeID, amount)
	}
	return &domain.Actuary{}, false, nil
}
func (m *mockActuaryRepository) InsertActuaryLimitAudit(ctx context.Context, actorEmployeeID, targetEmployeeID int64, oldLimit, newLimit decimal.Decimal) error {
	return nil
}

// stubMarginChecker is a no-op MarginChecker.
type stubMarginChecker struct{}

func (s *stubMarginChecker) HasSufficientMargin(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *stubMarginChecker) HasApprovedCreditForMargin(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
	return false, nil
}
func (s *stubMarginChecker) HasSufficientMarginTrezor(ctx context.Context, currency string, required decimal.Decimal) (bool, error) {
	return true, nil
}

// stubFundsManager is a no-op FundsManager.
type stubFundsManager struct{}

func (s *stubFundsManager) ReserveFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) ReleaseFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *stubFundsManager) HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *stubFundsManager) ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error) {
	return usdAmount, nil
}
func (s *stubFundsManager) ReserveForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) ReleaseForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManager) ForexSwap(ctx context.Context, userID int64, fromAccountID int64, baseCurrency, quoteCurrency string, nominalBase, rate decimal.Decimal, direction OrderDirection) error {
	return nil
}
func (s *stubFundsManager) WithDB(db *gorm.DB) FundsManager { return s }

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newTestTradingService(orders OrderRepository, listings domain.ListingService) TradingService {
	return NewTradingService(orders, listings, &mockActuaryRepository{}, &stubMarginChecker{}, &stubFundsManager{})
}

// ─── validateOrderTypeFields ──────────────────────────────────────────────────

func TestValidateOrderTypeFields_Market_NoPrice(t *testing.T) {
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeMarket})
	if err != nil {
		t.Errorf("MARKET should not require price fields, got: %v", err)
	}
}

func TestValidateOrderTypeFields_Limit_MissingPrice(t *testing.T) {
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeLimit, PricePerUnit: nil})
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got: %v", err)
	}
}

func TestValidateOrderTypeFields_Limit_WithPrice(t *testing.T) {
	price := decimal.NewFromFloat(50.0)
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeLimit, PricePerUnit: &price})
	if err != nil {
		t.Errorf("LIMIT with price should pass, got: %v", err)
	}
}

func TestValidateOrderTypeFields_Stop_MissingStopPrice(t *testing.T) {
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeStop, StopPrice: nil})
	if !errors.Is(err, ErrStopPriceRequired) {
		t.Errorf("expected ErrStopPriceRequired, got: %v", err)
	}
}

func TestValidateOrderTypeFields_Stop_WithStopPrice(t *testing.T) {
	stop := decimal.NewFromFloat(30.0)
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeStop, StopPrice: &stop})
	if err != nil {
		t.Errorf("STOP with stop price should pass, got: %v", err)
	}
}

func TestValidateOrderTypeFields_StopLimit_MissingStopPrice(t *testing.T) {
	price := decimal.NewFromFloat(50.0)
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeStopLimit, PricePerUnit: &price, StopPrice: nil})
	if !errors.Is(err, ErrStopPriceRequired) {
		t.Errorf("expected ErrStopPriceRequired, got: %v", err)
	}
}

func TestValidateOrderTypeFields_StopLimit_MissingLimitPrice(t *testing.T) {
	stop := decimal.NewFromFloat(30.0)
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeStopLimit, StopPrice: &stop, PricePerUnit: nil})
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got: %v", err)
	}
}

func TestValidateOrderTypeFields_StopLimit_BothPresent(t *testing.T) {
	stop := decimal.NewFromFloat(30.0)
	price := decimal.NewFromFloat(32.0)
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: OrderTypeStopLimit, StopPrice: &stop, PricePerUnit: &price})
	if err != nil {
		t.Errorf("STOP_LIMIT with both prices should pass, got: %v", err)
	}
}

func TestValidateOrderTypeFields_UnknownType(t *testing.T) {
	err := validateOrderTypeFields(&CreateOrderRequest{OrderType: "UNKNOWN"})
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

// ─── CalculateOrderDetails ────────────────────────────────────────────────────

func TestCalculateOrderDetails_InvalidDirection(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, &mockListingService{})
	_, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType: OrderTypeLimit,
		Direction: "INVALID",
	})
	if !errors.Is(err, ErrInvalidDirection) {
		t.Errorf("expected ErrInvalidDirection, got: %v", err)
	}
}

func TestCalculateOrderDetails_Limit_Success(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	price := decimal.NewFromFloat(10.0)
	resp, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType:    OrderTypeLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     2,
		PricePerUnit: &price,
		Margin:       false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	// notional = 1 × 10 × 2 = 20; 24% = 4.8
	if !resp.ApproximatePrice.Equal(decimal.NewFromFloat(20.0)) {
		t.Errorf("expected notional 20, got %s", resp.ApproximatePrice)
	}
	if !resp.Commission.Equal(decimal.NewFromFloat(4.8)) {
		t.Errorf("expected commission 4.8, got %s", resp.Commission)
	}
}

func TestCalculateOrderDetails_Limit_MissingPrice(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType: OrderTypeLimit,
		Direction: OrderDirectionBuy,
	})
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got: %v", err)
	}
}

func TestCalculateOrderDetails_Stop_Success(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	stopPrice := decimal.NewFromFloat(10.0)
	resp, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType:    OrderTypeStop,
		Direction:    OrderDirectionSell,
		ContractSize: 1,
		Quantity:     2,
		StopPrice:    &stopPrice,
		Margin:       false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Uses MARKET schedule; notional = 20; 14% = 2.8
	if !resp.ApproximatePrice.Equal(decimal.NewFromFloat(20.0)) {
		t.Errorf("expected notional 20, got %s", resp.ApproximatePrice)
	}
}

func TestCalculateOrderDetails_Stop_MissingPrice(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType: OrderTypeStop,
		Direction: OrderDirectionSell,
	})
	if !errors.Is(err, ErrStopPriceRequired) {
		t.Errorf("expected ErrStopPriceRequired, got: %v", err)
	}
}

func TestCalculateOrderDetails_StopLimit_Success(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	stop := decimal.NewFromFloat(9.0)
	limit := decimal.NewFromFloat(10.0)
	resp, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType:    OrderTypeStopLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     3,
		StopPrice:    &stop,
		PricePerUnit: &limit,
		Margin:       false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// notional = 1 × 10 × 3 = 30; 24% = 7.2
	if !resp.ApproximatePrice.Equal(decimal.NewFromFloat(30.0)) {
		t.Errorf("expected notional 30, got %s", resp.ApproximatePrice)
	}
}

func TestCalculateOrderDetails_Market_Success(t *testing.T) {
	listings := &mockListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 20.0, Bid: 19.0},
			}, nil
		},
	}
	svc := newTestTradingService(&mockOrderRepository{}, listings)
	resp, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionBuy, // uses Ask = 20
		ContractSize: 1,
		Quantity:     2,
		ListingID:    1,
		Margin:       false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// notional = 1 × 20 × 2 = 40; 14% = 5.6 < 7 → 5.6
	if !resp.ApproximatePrice.Equal(decimal.NewFromFloat(40.0)) {
		t.Errorf("expected notional 40, got %s", resp.ApproximatePrice)
	}
}

func TestCalculateOrderDetails_Market_ListingError(t *testing.T) {
	listings := &mockListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return nil, domain.ErrListingNotFound
		},
	}
	svc := newTestTradingService(&mockOrderRepository{}, listings)
	_, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType: OrderTypeMarket,
		Direction: OrderDirectionBuy,
		ListingID: 99,
	})
	if err == nil {
		t.Error("expected error when listing not found")
	}
}

func TestCalculateOrderDetails_UnknownOrderType(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType: "UNKNOWN",
		Direction: OrderDirectionBuy,
	})
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

// ─── ListOrders ───────────────────────────────────────────────────────────────

func TestListOrders_Success(t *testing.T) {
	expected := []Order{{ID: 1}, {ID: 2}}
	orders := &mockOrderRepository{
		listByStatusFn: func(ctx context.Context, status *OrderStatus) ([]Order, error) {
			return expected, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.ListOrders(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 orders, got %d", len(got))
	}
}

func TestListOrders_Error(t *testing.T) {
	orders := &mockOrderRepository{
		listByStatusFn: func(ctx context.Context, status *OrderStatus) ([]Order, error) {
			return nil, errors.New("db error")
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.ListOrders(context.Background(), nil)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestListOrders_WithFilter(t *testing.T) {
	status := OrderStatusPending
	orders := &mockOrderRepository{
		listByStatusFn: func(ctx context.Context, s *OrderStatus) ([]Order, error) {
			if s == nil || *s != OrderStatusPending {
				return nil, errors.New("wrong filter")
			}
			return []Order{{ID: 3, Status: OrderStatusPending}}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.ListOrders(context.Background(), &status)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 order, got %d", len(got))
	}
}

// ─── ListOrdersByUser ─────────────────────────────────────────────────────────

func TestListOrdersByUser_Success(t *testing.T) {
	expected := []Order{{ID: 10, UserID: 5}}
	orders := &mockOrderRepository{
		listByUserFn: func(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error) {
			if userID != 5 {
				return nil, errors.New("wrong userID")
			}
			return expected, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.ListOrdersByUser(context.Background(), 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 order, got %d", len(got))
	}
}

func TestListOrdersByUser_Error(t *testing.T) {
	orders := &mockOrderRepository{
		listByUserFn: func(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error) {
			return nil, errors.New("repo error")
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.ListOrdersByUser(context.Background(), 1, nil)
	if err == nil {
		t.Error("expected error")
	}
}

// ─── ApproveOrder ─────────────────────────────────────────────────────────────

func TestApproveOrder_OrderNotFound(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.ApproveOrder(context.Background(), 999, 1)
	if !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("expected ErrOrderNotFound, got: %v", err)
	}
}

func TestApproveOrder_NotPending(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusApproved}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.ApproveOrder(context.Background(), 1, 10)
	if !errors.Is(err, ErrInvalidOrderState) {
		t.Errorf("expected ErrInvalidOrderState, got: %v", err)
	}
}

func TestApproveOrder_Success(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusPending}, nil
		},
		updateStatusFn: func(ctx context.Context, id int64, status OrderStatus, approvedBy *string) (*Order, error) {
			return &Order{ID: id, Status: status}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.ApproveOrder(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED, got %s", got.Status)
	}
}

// ─── DeclineOrder ─────────────────────────────────────────────────────────────

func TestDeclineOrder_OrderNotFound(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.DeclineOrder(context.Background(), 999, 1)
	if !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("expected ErrOrderNotFound, got: %v", err)
	}
}

func TestDeclineOrder_NotPending(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusDone}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.DeclineOrder(context.Background(), 1, 10)
	if !errors.Is(err, ErrInvalidOrderState) {
		t.Errorf("expected ErrInvalidOrderState, got: %v", err)
	}
}

func TestDeclineOrder_Success(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusPending, Direction: OrderDirectionSell}, nil
		},
		updateStatusFn: func(ctx context.Context, id int64, status OrderStatus, approvedBy *string) (*Order, error) {
			return &Order{ID: id, Status: status}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.DeclineOrder(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusDeclined {
		t.Errorf("expected DECLINED, got %s", got.Status)
	}
}

// ─── CreateOrder ──────────────────────────────────────────────────────────────

func TestCreateOrder_SettlementExpired(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			if status != OrderStatusDeclined {
				return nil, errors.New("expected DECLINED status")
			}
			return &Order{ID: 1, Status: OrderStatusDeclined}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		SettlementExpired: true,
		Direction:         OrderDirectionBuy,
		OrderType:         OrderTypeMarket,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusDeclined {
		t.Errorf("expected DECLINED, got %s", got.Status)
	}
}

func TestCreateOrder_InvalidDirection(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction: "INVALID",
		OrderType: OrderTypeMarket,
	})
	if !errors.Is(err, ErrInvalidDirection) {
		t.Errorf("expected ErrInvalidDirection, got %v", err)
	}
}

func TestCreateOrder_InvalidOrderType(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction: OrderDirectionBuy,
		OrderType: "UNKNOWN",
	})
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got %v", err)
	}
}

func TestCreateOrder_MarginOnSell(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, &mockListingService{})
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction: OrderDirectionSell,
		OrderType: OrderTypeMarket,
		Margin:    true,
	})
	if err == nil {
		t.Error("expected error for margin on SELL order")
	}
}

func TestCreateOrder_SellOwnership_Success(t *testing.T) {
	orders := &mockOrderRepository{
		getNetHoldingsFn: func(ctx context.Context, userID, listingID int64) (int64, error) {
			return 50, nil // has 50, needs 5
		},
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	svc := NewTradingService(orders, &mockListingService{}, &mockActuaryRepository{}, &stubMarginChecker{}, &stubFundsManager{})
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionSell,
		OrderType:    OrderTypeMarket,
		Quantity:     5,
		ListingID:    1,
		IsForex:      false,
		IsClient:     false,
		IsSupervisor: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED, got %s", got.Status)
	}
}

func TestCreateOrder_SellOwnership_Insufficient(t *testing.T) {
	orders := &mockOrderRepository{
		getNetHoldingsFn: func(ctx context.Context, userID, listingID int64) (int64, error) {
			return 3, nil // has 3, needs 10
		},
	}
	svc := NewTradingService(orders, &mockListingService{}, &mockActuaryRepository{}, &stubMarginChecker{}, &stubFundsManager{})
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction: OrderDirectionSell,
		OrderType: OrderTypeMarket,
		Quantity:  10,
		ListingID: 1,
		IsForex:   false,
	})
	if !errors.Is(err, ErrInsufficientHoldings) {
		t.Errorf("expected ErrInsufficientHoldings, got %v", err)
	}
}

func TestCreateOrder_BuyNonMargin_InsufficientFunds(t *testing.T) {
	price := decimal.NewFromFloat(100.0)
	fundsManager := &stubFundsManagerWithControl{hasFunds: false}
	svc := NewTradingService(&mockOrderRepository{}, &mockListingService{}, &mockActuaryRepository{}, &stubMarginChecker{}, fundsManager)
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		Quantity:     1,
		ContractSize: 1,
		PricePerUnit: &price,
		ListingID:    1,
		IsClient:     true,
		IsForex:      false,
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected ErrInsufficientFunds, got %v", err)
	}
}

// stubFundsManagerWithControl allows controlling HasSufficientFunds result.
type stubFundsManagerWithControl struct {
	hasFunds bool
}

func (s *stubFundsManagerWithControl) ReserveFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) ReleaseFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error) {
	return s.hasFunds, nil
}
func (s *stubFundsManagerWithControl) HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	return s.hasFunds, nil
}
func (s *stubFundsManagerWithControl) ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error) {
	return usdAmount, nil
}
func (s *stubFundsManagerWithControl) ReserveForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) ReleaseForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	return nil
}
func (s *stubFundsManagerWithControl) ForexSwap(ctx context.Context, userID int64, fromAccountID int64, baseCurrency, quoteCurrency string, nominalBase, rate decimal.Decimal, direction OrderDirection) error {
	return nil
}
func (s *stubFundsManagerWithControl) WithDB(db *gorm.DB) FundsManager { return s }

// ─── CancelOrder ──────────────────────────────────────────────────────────────

func TestCancelOrder_OrderNotFound(t *testing.T) {
	svc := newTestTradingService(&mockOrderRepository{}, nil)
	_, err := svc.CancelOrder(context.Background(), 999, 1, false)
	if !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestCancelOrder_PermissionDenied_NotOwner_NotSupervisor(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, UserID: 10, Status: OrderStatusPending}, nil
		},
	}
	// caller is user 99, not owner (10), not supervisor
	svc := newTestTradingService(orders, nil)
	_, err := svc.CancelOrder(context.Background(), 1, 99, false)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestCancelOrder_OwnerCancel_PendingSell_Success(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, UserID: 5, Status: OrderStatusPending, Direction: OrderDirectionSell}, nil
		},
		cancelFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusCanceled}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	got, err := svc.CancelOrder(context.Background(), 1, 5, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusCanceled {
		t.Errorf("expected CANCELED, got %s", got.Status)
	}
}

func TestCancelOrder_SupervisorFlag_Cancel_Success(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, UserID: 10, Status: OrderStatusApproved, Direction: OrderDirectionSell}, nil
		},
		cancelFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusCanceled}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	// caller 99 is not owner but has supervisor JWT flag
	got, err := svc.CancelOrder(context.Background(), 1, 99, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusCanceled {
		t.Errorf("expected CANCELED, got %s", got.Status)
	}
}

func TestCancelOrder_NonCancelableState_Done(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, UserID: 5, Status: OrderStatusDone, IsDone: true}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.CancelOrder(context.Background(), 1, 5, false)
	if !errors.Is(err, ErrInvalidOrderState) {
		t.Errorf("expected ErrInvalidOrderState, got %v", err)
	}
}

func TestCancelOrder_NonCancelableState_AlreadyCanceled(t *testing.T) {
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, UserID: 5, Status: OrderStatusCanceled}, nil
		},
	}
	svc := newTestTradingService(orders, nil)
	_, err := svc.CancelOrder(context.Background(), 1, 5, false)
	if !errors.Is(err, ErrInvalidOrderState) {
		t.Errorf("expected ErrInvalidOrderState, got %v", err)
	}
}

// ─── resolveStatus (via CreateOrder) ─────────────────────────────────────────

func newFullTradingService(
	orders OrderRepository,
	listings domain.ListingService,
	actuaries domain.ActuaryRepository,
	funds FundsManager,
) TradingService {
	return NewTradingService(orders, listings, actuaries, &stubMarginChecker{}, funds)
}

func TestCreateOrder_ResolveStatus_SupervisorFlag_Approved(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
		getNetHoldingsFn: func(ctx context.Context, userID, listingID int64) (int64, error) {
			return 50, nil
		},
	}
	svc := newFullTradingService(orders, &mockListingService{}, &mockActuaryRepository{}, &stubFundsManager{})
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionSell,
		OrderType:    OrderTypeMarket,
		Quantity:     1,
		ContractSize: 1,
		ListingID:    1,
		IsSupervisor: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED (supervisor fast-path), got %s", got.Status)
	}
}

func TestCreateOrder_ResolveStatus_ClientNoActuaryRow_Approved(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	// actuary repo returns ErrActuaryNotFound → client path → APPROVED
	actuaries := &mockActuaryRepository{}
	svc := newFullTradingService(orders, &mockListingService{}, actuaries, &stubFundsManager{})
	price := decimal.NewFromFloat(10.0)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		Quantity:     1,
		ContractSize: 1,
		PricePerUnit: &price,
		ListingID:    1,
		IsClient:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED (no actuary row), got %s", got.Status)
	}
}

func TestCreateOrder_ResolveStatus_Agent_NeedApproval_Pending(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	actuaries := &mockActuaryRepository{
		getByEmployeeIDFn: func(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
			return &domain.Actuary{
				ActuaryType:  domain.ActuaryTypeAgent,
				NeedApproval: true,
				Limit:        decimal.NewFromFloat(1000.0),
			}, nil
		},
	}
	svc := newFullTradingService(orders, &mockListingService{}, actuaries, &stubFundsManager{})
	price := decimal.NewFromFloat(10.0)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		Quantity:     1,
		ContractSize: 1,
		PricePerUnit: &price,
		ListingID:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusPending {
		t.Errorf("expected PENDING (need_approval), got %s", got.Status)
	}
}

func TestCreateOrder_ResolveStatus_Agent_ExceededLimit_Pending(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	actuaries := &mockActuaryRepository{
		getByEmployeeIDFn: func(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
			return &domain.Actuary{
				ActuaryType:  domain.ActuaryTypeAgent,
				NeedApproval: false,
				Limit:        decimal.NewFromFloat(1000.0),
			}, nil
		},
		incrementUsedLimitAlwaysFn: func(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, bool, error) {
			return &domain.Actuary{}, true, nil // exceeded = true
		},
	}
	svc := newFullTradingService(orders, &mockListingService{}, actuaries, &stubFundsManager{})
	price := decimal.NewFromFloat(10.0)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		Quantity:     1,
		ContractSize: 1,
		PricePerUnit: &price,
		ListingID:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusPending {
		t.Errorf("expected PENDING (exceeded limit), got %s", got.Status)
	}
}

func TestCreateOrder_ResolveStatus_Agent_WithinLimit_Approved(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	actuaries := &mockActuaryRepository{
		getByEmployeeIDFn: func(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
			return &domain.Actuary{
				ActuaryType:  domain.ActuaryTypeAgent,
				NeedApproval: false,
				Limit:        decimal.NewFromFloat(10000.0),
			}, nil
		},
		incrementUsedLimitAlwaysFn: func(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, bool, error) {
			return &domain.Actuary{}, false, nil // not exceeded
		},
	}
	svc := newFullTradingService(orders, &mockListingService{}, actuaries, &stubFundsManager{})
	price := decimal.NewFromFloat(10.0)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		Quantity:     1,
		ContractSize: 1,
		PricePerUnit: &price,
		ListingID:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED (within limit), got %s", got.Status)
	}
}

// ─── initialMarginCostForListing (via CalculateOrderDetails + Margin) ─────────

func TestCalculateOrderDetails_Margin_FetchesIMC(t *testing.T) {
	listings := &mockListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing:           domain.Listing{ID: id, Ask: 10.0, Bid: 9.5},
				MaintenanceMargin: 50.0,
			}, nil
		},
	}
	svc := newTestTradingService(&mockOrderRepository{}, listings)
	price := decimal.NewFromFloat(10.0)
	resp, err := svc.CalculateOrderDetails(context.Background(), &OrderCalculationRequest{
		OrderType:    OrderTypeLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     1,
		PricePerUnit: &price,
		ListingID:    1,
		Margin:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InitialMarginCost == nil {
		t.Fatal("expected InitialMarginCost to be set with Margin=true")
	}
}

// ─── validateForexSellBalance ─────────────────────────────────────────────────

func TestCreateOrder_ForexSell_NotClient_Skips(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	svc := newFullTradingService(orders, &mockListingService{}, &mockActuaryRepository{}, &stubFundsManager{})
	// Non-client FOREX SELL: validateForexSellBalance should be skipped
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionSell,
		OrderType:    OrderTypeMarket,
		Quantity:     5,
		ContractSize: 1,
		ListingID:    1,
		IsForex:      true,
		IsClient:     false,
		IsSupervisor: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED, got %s", got.Status)
	}
}

func TestCreateOrder_ForexSell_Client_InsufficientFreeBalance(t *testing.T) {
	fundsManager := &stubFundsManagerWithControl{hasFunds: false}
	svc := NewTradingService(&mockOrderRepository{}, &mockListingService{}, &mockActuaryRepository{}, &stubMarginChecker{}, fundsManager)
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionSell,
		OrderType:    OrderTypeMarket,
		Quantity:     5,
		ContractSize: 100,
		ListingID:    1,
		IsForex:      true,
		IsClient:     true,
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected ErrInsufficientFunds, got %v", err)
	}
}

func TestCreateOrder_ForexSell_Client_SufficientFreeBalance(t *testing.T) {
	fundsManager := &stubFundsManagerWithControl{hasFunds: true}
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	svc := NewTradingService(orders, &mockListingService{}, &mockActuaryRepository{}, &stubMarginChecker{}, fundsManager)
	got, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionSell,
		OrderType:    OrderTypeMarket,
		Quantity:     5,
		ContractSize: 1,
		ListingID:    1,
		IsForex:      true,
		IsClient:     true,
		IsSupervisor: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusApproved {
		t.Errorf("expected APPROVED, got %s", got.Status)
	}
}

// ─── validateMargin ───────────────────────────────────────────────────────────

func TestCreateOrder_Margin_NonClient_SufficientTrezor(t *testing.T) {
	orders := &mockOrderRepository{
		createFn: func(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error) {
			return &Order{ID: 1, Status: status}, nil
		},
	}
	listings := &mockListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing:           domain.Listing{ID: id, Ask: 20.0},
				MaintenanceMargin: 100.0,
			}, nil
		},
	}
	marginChecker := &stubMarginChecker{} // HasSufficientMarginTrezor returns true
	svc := NewTradingService(orders, listings, &mockActuaryRepository{}, marginChecker, &stubFundsManager{})
	_, err := svc.CreateOrder(context.Background(), &CreateOrderRequest{
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeMarket,
		Quantity:     1,
		ContractSize: 1,
		ListingID:    1,
		IsClient:     false,
		IsSupervisor: true,
		Margin:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── computeNotional + computeNotionalPlusCommission via CancelOrder BUY ─────

func TestCancelOrder_BuyLimit_Agent_ReleaseFunds(t *testing.T) {
	price := decimal.NewFromFloat(10.0)
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{
				ID:                id,
				UserID:            5,
				Status:            OrderStatusPending,
				Direction:         OrderDirectionBuy,
				OrderType:         OrderTypeLimit,
				PricePerUnit:      &price,
				ContractSize:      1,
				RemainingPortions: 3,
				IsClient:          false,
			}, nil
		},
		cancelFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusCanceled}, nil
		},
	}
	svc := newTestTradingService(orders, &mockListingService{})
	got, err := svc.CancelOrder(context.Background(), 1, 5, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusCanceled {
		t.Errorf("expected CANCELED, got %s", got.Status)
	}
}

func TestCancelOrder_BuyLimit_Client_ReleaseFunds_WithCommission(t *testing.T) {
	price := decimal.NewFromFloat(10.0)
	orders := &mockOrderRepository{
		getByIDFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{
				ID:                id,
				UserID:            7,
				Status:            OrderStatusApproved,
				Direction:         OrderDirectionBuy,
				OrderType:         OrderTypeLimit,
				PricePerUnit:      &price,
				ContractSize:      1,
				RemainingPortions: 5,
				IsClient:          true,
			}, nil
		},
		cancelFn: func(ctx context.Context, id int64) (*Order, error) {
			return &Order{ID: id, Status: OrderStatusCanceled}, nil
		},
	}
	svc := newTestTradingService(orders, &mockListingService{})
	got, err := svc.CancelOrder(context.Background(), 1, 7, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != OrderStatusCanceled {
		t.Errorf("expected CANCELED, got %s", got.Status)
	}
}
