package trading

// White-box tests for private methods of tradingService.
// Uses package trading to access unexported fields and methods.

import (
	"context"
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ─── Mocks for private-method tests ──────────────────────────────────────────

type spyFundsManager struct {
	reserveFn      func(ctx context.Context, accountID int64, amount decimal.Decimal) error
	releaseFn      func(ctx context.Context, accountID int64, amount decimal.Decimal) error
	reserveForexFn func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error
	releaseForexFn func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error
}

func (s *spyFundsManager) ReserveFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	if s.reserveFn != nil {
		return s.reserveFn(ctx, accountID, amount)
	}
	return nil
}
func (s *spyFundsManager) ReleaseFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, accountID, amount)
	}
	return nil
}
func (s *spyFundsManager) SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *spyFundsManager) CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *spyFundsManager) ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error {
	return nil
}
func (s *spyFundsManager) HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *spyFundsManager) HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	return true, nil
}
func (s *spyFundsManager) ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error) {
	return usdAmount, nil
}
func (s *spyFundsManager) ReserveForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	if s.reserveForexFn != nil {
		return s.reserveForexFn(ctx, userID, fromAccountID, quoteCurrency, amount)
	}
	return nil
}
func (s *spyFundsManager) ReleaseForexFunds(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
	if s.releaseForexFn != nil {
		return s.releaseForexFn(ctx, userID, fromAccountID, quoteCurrency, amount)
	}
	return nil
}
func (s *spyFundsManager) ForexSwap(ctx context.Context, userID int64, fromAccountID int64, baseCurrency, quoteCurrency string, nominalBase, rate decimal.Decimal, direction OrderDirection) error {
	return nil
}
func (s *spyFundsManager) WithDB(db *gorm.DB) FundsManager { return s }

type spyListingService struct {
	getByIDFn func(ctx context.Context, id int64) (*domain.ListingCalculated, error)
}

func (s *spyListingService) GetListingByID(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id)
	}
	return &domain.ListingCalculated{
		Listing: domain.Listing{ID: id, Ask: 10.0, Bid: 9.5},
	}, nil
}
func (s *spyListingService) ListListings(ctx context.Context, filter domain.ListingFilter) ([]domain.ListingCalculated, int64, error) {
	return nil, 0, nil
}
func (s *spyListingService) GetListingHistory(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error) {
	return nil, nil
}

type spyActuaryRepo struct {
	getByEmployeeIDFn func(ctx context.Context, id int64) (*domain.Actuary, error)
}

func (s *spyActuaryRepo) Create(ctx context.Context, input domain.CreateActuaryInput) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (s *spyActuaryRepo) GetByID(ctx context.Context, id int64) (*domain.Actuary, error) {
	return nil, domain.ErrActuaryNotFound
}
func (s *spyActuaryRepo) GetByEmployeeID(ctx context.Context, id int64) (*domain.Actuary, error) {
	if s.getByEmployeeIDFn != nil {
		return s.getByEmployeeIDFn(ctx, id)
	}
	return nil, domain.ErrActuaryNotFound
}
func (s *spyActuaryRepo) List(ctx context.Context, actuaryType string) ([]domain.Actuary, error) {
	return nil, nil
}
func (s *spyActuaryRepo) Update(ctx context.Context, input domain.UpdateActuaryInput) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (s *spyActuaryRepo) Delete(ctx context.Context, id int64) error             { return nil }
func (s *spyActuaryRepo) DeleteByEmployeeID(ctx context.Context, id int64) error { return nil }
func (s *spyActuaryRepo) ResetAllUsedLimits(ctx context.Context) error           { return nil }
func (s *spyActuaryRepo) IncrementUsedLimitIfWithin(ctx context.Context, id int64, amount decimal.Decimal) (*domain.Actuary, error) {
	return &domain.Actuary{}, nil
}
func (s *spyActuaryRepo) IncrementUsedLimitAlways(ctx context.Context, id int64, amount decimal.Decimal) (*domain.Actuary, bool, error) {
	return &domain.Actuary{}, false, nil
}
func (s *spyActuaryRepo) InsertActuaryLimitAudit(ctx context.Context, actorID, targetID int64, old, new decimal.Decimal) error {
	return nil
}

func newConcreteService(listings domain.ListingService, actuaries domain.ActuaryRepository, funds FundsManager) *tradingService {
	return &tradingService{
		orders:    &mockOrderRepository{},
		listings:  listings,
		actuaries: actuaries,
		margin:    &stubMarginChecker{},
		funds:     funds,
	}
}

// ─── getForexQuoteCurrency ────────────────────────────────────────────────────

func TestGetForexQuoteCurrency_Success(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	currency, err := svc.getForexQuoteCurrency(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if currency != "USD" {
		t.Errorf("expected USD, got %s", currency)
	}
}

func TestGetForexQuoteCurrency_ListingNotFound(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return nil, domain.ErrListingNotFound
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	_, err := svc.getForexQuoteCurrency(ctx, 99)
	if err == nil {
		t.Error("expected error when listing not found")
	}
}

func TestGetForexQuoteCurrency_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, DetailsJSON: `not-json`},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	_, err := svc.getForexQuoteCurrency(ctx, 1)
	if err == nil {
		t.Error("expected error on invalid JSON")
	}
}

func TestGetForexQuoteCurrency_EmptyQuoteCurrency(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	_, err := svc.getForexQuoteCurrency(ctx, 1)
	if err == nil {
		t.Error("expected error when quote_currency is empty")
	}
}

// ─── isSupervisor ─────────────────────────────────────────────────────────────

func TestIsSupervisor_NotFound_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	actuaries := &spyActuaryRepo{
		getByEmployeeIDFn: func(ctx context.Context, id int64) (*domain.Actuary, error) {
			return nil, domain.ErrActuaryNotFound
		},
	}
	svc := newConcreteService(&spyListingService{}, actuaries, &spyFundsManager{})
	ok, err := svc.isSupervisor(ctx, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("non-actuary user should not be supervisor")
	}
}

func TestIsSupervisor_FoundSupervisor_ReturnsTrue(t *testing.T) {
	ctx := context.Background()
	actuaries := &spyActuaryRepo{
		getByEmployeeIDFn: func(ctx context.Context, id int64) (*domain.Actuary, error) {
			return &domain.Actuary{
				ID:          1,
				ActuaryType: domain.ActuaryTypeSupervisor,
			}, nil
		},
	}
	svc := newConcreteService(&spyListingService{}, actuaries, &spyFundsManager{})
	ok, err := svc.isSupervisor(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("supervisor actuary type should return true")
	}
}

func TestIsSupervisor_FoundAgent_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	actuaries := &spyActuaryRepo{
		getByEmployeeIDFn: func(ctx context.Context, id int64) (*domain.Actuary, error) {
			return &domain.Actuary{
				ID:          2,
				ActuaryType: domain.ActuaryTypeAgent,
			}, nil
		},
	}
	svc := newConcreteService(&spyListingService{}, actuaries, &spyFundsManager{})
	ok, err := svc.isSupervisor(ctx, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("agent actuary type should return false")
	}
}

func TestIsSupervisor_DBError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	actuaries := &spyActuaryRepo{
		getByEmployeeIDFn: func(ctx context.Context, id int64) (*domain.Actuary, error) {
			return nil, errors.New("db connection lost")
		},
	}
	svc := newConcreteService(&spyListingService{}, actuaries, &spyFundsManager{})
	_, err := svc.isSupervisor(ctx, 3)
	if err == nil {
		t.Error("expected error on DB failure")
	}
}

// ─── resolveNotional ──────────────────────────────────────────────────────────

func TestResolveNotional_Market_Buy(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 20.0, Bid: 19.0},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionBuy,
		ContractSize: 2,
		Quantity:     3,
		ListingID:    1,
	}
	notional, err := svc.resolveNotional(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 * 20.0 * 3 = 120
	expected := decimal.NewFromFloat(120.0)
	if !notional.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, notional)
	}
}

func TestResolveNotional_Market_Sell(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 20.0, Bid: 18.0},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionSell,
		ContractSize: 1,
		Quantity:     5,
		ListingID:    1,
	}
	notional, err := svc.resolveNotional(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 * 18.0 * 5 = 90
	expected := decimal.NewFromFloat(90.0)
	if !notional.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, notional)
	}
}

func TestResolveNotional_Market_ListingError(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return nil, domain.ErrListingNotFound
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{OrderType: OrderTypeMarket, Direction: OrderDirectionBuy, ListingID: 99}
	_, err := svc.resolveNotional(ctx, req)
	if err == nil {
		t.Error("expected error when listing not found")
	}
}

func TestResolveNotional_Limit(t *testing.T) {
	ctx := context.Background()
	price := decimal.NewFromFloat(15.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{
		OrderType:    OrderTypeLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 2,
		Quantity:     4,
		PricePerUnit: &price,
	}
	notional, err := svc.resolveNotional(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 * 15 * 4 = 120
	expected := decimal.NewFromFloat(120.0)
	if !notional.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, notional)
	}
}

func TestResolveNotional_StopLimit(t *testing.T) {
	ctx := context.Background()
	price := decimal.NewFromFloat(25.0)
	stop := decimal.NewFromFloat(22.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{
		OrderType:    OrderTypeStopLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     2,
		PricePerUnit: &price,
		StopPrice:    &stop,
	}
	notional, err := svc.resolveNotional(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 * 25 * 2 = 50
	expected := decimal.NewFromFloat(50.0)
	if !notional.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, notional)
	}
}

func TestResolveNotional_Stop(t *testing.T) {
	ctx := context.Background()
	stop := decimal.NewFromFloat(30.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{
		OrderType:    OrderTypeStop,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     3,
		StopPrice:    &stop,
	}
	notional, err := svc.resolveNotional(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 * 30 * 3 = 90
	expected := decimal.NewFromFloat(90.0)
	if !notional.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, notional)
	}
}

func TestResolveNotional_UnknownType_ReturnsError(t *testing.T) {
	ctx := context.Background()
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	req := &CreateOrderRequest{OrderType: "BOGUS"}
	_, err := svc.resolveNotional(ctx, req)
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

// ─── computeNotional ─────────────────────────────────────────────────────────

func TestComputeNotional_Market_Buy(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 10.0, Bid: 9.0},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ListingID:    1,
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionBuy,
		ContractSize: 2,
	}
	n, err := svc.computeNotional(ctx, order, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 * 10 * 5 = 100
	expected := decimal.NewFromFloat(100.0)
	if !n.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, n)
	}
}

func TestComputeNotional_Market_Sell(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 10.0, Bid: 8.0},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ListingID:    1,
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionSell,
		ContractSize: 1,
	}
	n, err := svc.computeNotional(ctx, order, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 * 8 * 4 = 32
	expected := decimal.NewFromFloat(32.0)
	if !n.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, n)
	}
}

func TestComputeNotional_Limit_Success(t *testing.T) {
	ctx := context.Background()
	price := decimal.NewFromFloat(12.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		OrderType:    OrderTypeLimit,
		ContractSize: 1,
		PricePerUnit: &price,
	}
	n, err := svc.computeNotional(ctx, order, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(36.0)
	if !n.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, n)
	}
}

func TestComputeNotional_Limit_NilPrice_ReturnsError(t *testing.T) {
	ctx := context.Background()
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		OrderType:    OrderTypeLimit,
		ContractSize: 1,
		PricePerUnit: nil,
	}
	_, err := svc.computeNotional(ctx, order, 1)
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got: %v", err)
	}
}

func TestComputeNotional_Stop_Success(t *testing.T) {
	ctx := context.Background()
	stop := decimal.NewFromFloat(50.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		OrderType:    OrderTypeStop,
		ContractSize: 2,
		StopPrice:    &stop,
	}
	n, err := svc.computeNotional(ctx, order, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(200.0)
	if !n.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, n)
	}
}

func TestComputeNotional_Stop_NilPrice_ReturnsError(t *testing.T) {
	ctx := context.Background()
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{OrderType: OrderTypeStop, StopPrice: nil}
	_, err := svc.computeNotional(ctx, order, 1)
	if !errors.Is(err, ErrStopPriceRequired) {
		t.Errorf("expected ErrStopPriceRequired, got: %v", err)
	}
}

func TestComputeNotional_UnknownType_ReturnsError(t *testing.T) {
	ctx := context.Background()
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{OrderType: "UNKNOWN"}
	_, err := svc.computeNotional(ctx, order, 1)
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

func TestComputeNotional_StopLimit_Success(t *testing.T) {
	ctx := context.Background()
	price := decimal.NewFromFloat(45.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		OrderType:    OrderTypeStopLimit,
		ContractSize: 1,
		PricePerUnit: &price,
	}
	n, err := svc.computeNotional(ctx, order, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(90.0)
	if !n.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, n)
	}
}

// ─── reserveFundsForOrder ─────────────────────────────────────────────────────

func TestReserveFundsForOrder_SellOrder_NoOp(t *testing.T) {
	ctx := context.Background()
	reserved := false
	funds := &spyFundsManager{
		reserveFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			reserved = true
			return nil
		},
	}
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, funds)
	order := &Order{Direction: OrderDirectionSell, OrderType: OrderTypeMarket}
	err := svc.reserveFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reserved {
		t.Error("SELL order should not reserve funds")
	}
}

func TestReserveFundsForOrder_BuyOrder_Client_ReservesWithCommission(t *testing.T) {
	ctx := context.Background()
	var reservedAmount decimal.Decimal
	funds := &spyFundsManager{
		reserveFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			reservedAmount = amount
			return nil
		},
	}
	price := decimal.NewFromFloat(10.0)
	listings := &spyListingService{}
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:           1,
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		ContractSize: 1,
		Quantity:     2,
		PricePerUnit: &price,
		IsClient:     true,
	}
	err := svc.reserveFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Notional = 1*10*2 = 20, commission for LIMIT = CalcLimitCommission(20)
	notional := decimal.NewFromFloat(20.0)
	expectedTotal := notional.Add(CalcLimitCommission(notional))
	if !reservedAmount.Equal(expectedTotal) {
		t.Errorf("expected reserved %s (notional+commission), got %s", expectedTotal, reservedAmount)
	}
}

func TestReserveFundsForOrder_BuyOrder_NonClient_ReservesWithoutCommission(t *testing.T) {
	ctx := context.Background()
	var reservedAmount decimal.Decimal
	funds := &spyFundsManager{
		reserveFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			reservedAmount = amount
			return nil
		},
	}
	price := decimal.NewFromFloat(10.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:           2,
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		ContractSize: 1,
		Quantity:     2,
		PricePerUnit: &price,
		IsClient:     false, // actuary/supervisor
	}
	err := svc.reserveFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Notional only: 1*10*2 = 20 (no commission for non-client)
	expected := decimal.NewFromFloat(20.0)
	if !reservedAmount.Equal(expected) {
		t.Errorf("expected reserved %s (notional only), got %s", expected, reservedAmount)
	}
}

func TestReserveFundsForOrder_ReserveFails_ReturnsError(t *testing.T) {
	ctx := context.Background()
	funds := &spyFundsManager{
		reserveFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			return errors.New("reserve failed")
		},
	}
	price := decimal.NewFromFloat(10.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:           3,
		Direction:    OrderDirectionBuy,
		OrderType:    OrderTypeLimit,
		ContractSize: 1,
		Quantity:     1,
		PricePerUnit: &price,
	}
	err := svc.reserveFundsForOrder(ctx, order)
	if err == nil {
		t.Error("expected error when reserve fails")
	}
}

// ─── reserveForexFundsForOrder ────────────────────────────────────────────────

func TestReserveForexFundsForOrder_Market_Success(t *testing.T) {
	ctx := context.Background()
	var capturedCurrency string
	var capturedAmount decimal.Decimal
	funds := &spyFundsManager{
		reserveForexFn: func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
			capturedCurrency = quoteCurrency
			capturedAmount = amount
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         1.1,
					Bid:         1.0,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:           10,
		ListingID:    1,
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     2,
	}
	err := svc.reserveForexFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCurrency != "USD" {
		t.Errorf("expected quote currency USD, got %s", capturedCurrency)
	}
	// amount = contractSize * quantity * rate = 1 * 2 * 1.1 = 2.2
	expected := decimal.NewFromFloat(2.2)
	if !capturedAmount.Equal(expected) {
		t.Errorf("expected amount %s, got %s", expected, capturedAmount)
	}
}

func TestReserveForexFundsForOrder_Limit_Success(t *testing.T) {
	ctx := context.Background()
	var capturedCurrency string
	funds := &spyFundsManager{
		reserveForexFn: func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
			capturedCurrency = quoteCurrency
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         1.2,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	limitPrice := decimal.NewFromFloat(1.15)
	order := &Order{
		ID:           11,
		ListingID:    1,
		OrderType:    OrderTypeLimit,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     1,
		PricePerUnit: &limitPrice,
	}
	err := svc.reserveForexFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCurrency != "USD" {
		t.Errorf("expected quote currency USD, got %s", capturedCurrency)
	}
}

func TestReserveForexFundsForOrder_Limit_NilPrice_ReturnsError(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ID:           12,
		ListingID:    1,
		OrderType:    OrderTypeLimit,
		PricePerUnit: nil,
	}
	err := svc.reserveForexFundsForOrder(ctx, order)
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got: %v", err)
	}
}

func TestReserveForexFundsForOrder_InvalidOrderType_ReturnsError(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ID:        13,
		ListingID: 1,
		OrderType: "BOGUS",
	}
	err := svc.reserveForexFundsForOrder(ctx, order)
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

func TestReserveForexFundsForOrder_ZeroRate_ReturnsError(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         0.0, // zero Ask
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ID:        14,
		ListingID: 1,
		OrderType: OrderTypeMarket,
		Direction: OrderDirectionBuy,
	}
	err := svc.reserveForexFundsForOrder(ctx, order)
	if err == nil {
		t.Error("expected error when rate is zero")
	}
}

// ─── releaseForexFundsForOrder ────────────────────────────────────────────────

func TestReleaseForexFundsForOrder_Market_Success(t *testing.T) {
	ctx := context.Background()
	released := false
	funds := &spyFundsManager{
		releaseForexFn: func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
			released = true
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         1.2,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:                20,
		ListingID:         1,
		OrderType:         OrderTypeMarket,
		Direction:         OrderDirectionBuy,
		ContractSize:      1,
		RemainingPortions: 2,
	}
	err := svc.releaseForexFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !released {
		t.Error("expected ReleaseForexFunds to be called")
	}
}

func TestReleaseForexFundsForOrder_ZeroRate_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         0.0, // zero rate → no release
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ID:                21,
		ListingID:         1,
		OrderType:         OrderTypeMarket,
		Direction:         OrderDirectionBuy,
		ContractSize:      1,
		RemainingPortions: 1,
	}
	err := svc.releaseForexFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("zero rate should return nil (nothing to release), got: %v", err)
	}
}

func TestReleaseForexFundsForOrder_Limit_Success(t *testing.T) {
	ctx := context.Background()
	var capturedAmount decimal.Decimal
	funds := &spyFundsManager{
		releaseForexFn: func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
			capturedAmount = amount
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	price := decimal.NewFromFloat(1.5)
	order := &Order{
		ID:                22,
		ListingID:         1,
		OrderType:         OrderTypeLimit,
		Direction:         OrderDirectionBuy,
		ContractSize:      2,
		RemainingPortions: 3,
		PricePerUnit:      &price,
	}
	err := svc.releaseForexFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// amount = contractSize * remainingPortions * rate = 2 * 3 * 1.5 = 9
	expected := decimal.NewFromFloat(9.0)
	if !capturedAmount.Equal(expected) {
		t.Errorf("expected release amount %s, got %s", expected, capturedAmount)
	}
}

func TestReleaseForexFundsForOrder_InvalidType_ReturnsError(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ID:        23,
		ListingID: 1,
		OrderType: "INVALID",
	}
	err := svc.releaseForexFundsForOrder(ctx, order)
	if !errors.Is(err, ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got: %v", err)
	}
}

// ─── releaseFundsForOrder ─────────────────────────────────────────────────────

func TestReleaseFundsForOrder_SellOrder_NoOp(t *testing.T) {
	ctx := context.Background()
	released := false
	funds := &spyFundsManager{
		releaseFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			released = true
			return nil
		},
	}
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, funds)
	order := &Order{Direction: OrderDirectionSell, OrderType: OrderTypeMarket}
	err := svc.releaseFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if released {
		t.Error("SELL order should not release funds")
	}
}

func TestReleaseFundsForOrder_BuyOrder_NonForex_ReleasesFromAccount(t *testing.T) {
	ctx := context.Background()
	released := false
	funds := &spyFundsManager{
		releaseFn: func(ctx context.Context, accountID int64, amount decimal.Decimal) error {
			released = true
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         10.0,
					ListingType: domain.ListingTypeStock,
				},
			}, nil
		},
	}
	price := decimal.NewFromFloat(10.0)
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:                30,
		ListingID:         1,
		Direction:         OrderDirectionBuy,
		OrderType:         OrderTypeLimit,
		ContractSize:      1,
		RemainingPortions: 2,
		PricePerUnit:      &price,
		IsClient:          false,
	}
	err := svc.releaseFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !released {
		t.Error("expected ReleaseFunds to be called for non-forex BUY order")
	}
}

func TestReleaseFundsForOrder_BuyOrder_Forex_DelegatesToForexRelease(t *testing.T) {
	ctx := context.Background()
	forexReleased := false
	funds := &spyFundsManager{
		releaseForexFn: func(ctx context.Context, userID int64, fromAccountID int64, quoteCurrency string, amount decimal.Decimal) error {
			forexReleased = true
			return nil
		},
	}
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{
					ID:          id,
					Ask:         1.2,
					ListingType: domain.ListingTypeForex,
					DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD","liquidity":"High"}`,
				},
			}, nil
		},
	}
	price := decimal.NewFromFloat(1.2)
	svc := newConcreteService(listings, &spyActuaryRepo{}, funds)
	order := &Order{
		ID:                31,
		ListingID:         1,
		Direction:         OrderDirectionBuy,
		OrderType:         OrderTypeLimit,
		ContractSize:      1,
		RemainingPortions: 1,
		PricePerUnit:      &price,
	}
	err := svc.releaseFundsForOrder(ctx, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !forexReleased {
		t.Error("expected ReleaseForexFunds to be called for forex BUY order")
	}
}

// ─── spyMarginChecker + helper ────────────────────────────────────────────────

type spyMarginChecker struct {
	hasSufficientMarginFn func(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error)
	hasApprovedCreditFn   func(ctx context.Context, userID int64, required decimal.Decimal) (bool, error)
	hasSufficientTrezorFn func(ctx context.Context, currency string, required decimal.Decimal) (bool, error)
}

func (s *spyMarginChecker) HasSufficientMargin(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
	if s.hasSufficientMarginFn != nil {
		return s.hasSufficientMarginFn(ctx, accountID, required)
	}
	return true, nil
}
func (s *spyMarginChecker) HasApprovedCreditForMargin(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
	if s.hasApprovedCreditFn != nil {
		return s.hasApprovedCreditFn(ctx, userID, required)
	}
	return false, nil
}
func (s *spyMarginChecker) HasSufficientMarginTrezor(ctx context.Context, currency string, required decimal.Decimal) (bool, error) {
	if s.hasSufficientTrezorFn != nil {
		return s.hasSufficientTrezorFn(ctx, currency, required)
	}
	return true, nil
}

func newConcreteServiceWithMargin(listings domain.ListingService, actuaries domain.ActuaryRepository, funds FundsManager, margin MarginChecker) *tradingService {
	return &tradingService{
		orders:    &mockOrderRepository{},
		listings:  listings,
		actuaries: actuaries,
		margin:    margin,
		funds:     funds,
	}
}

func listingWithMargin(maintenanceMargin float64) *spyListingService {
	return &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing:           domain.Listing{ID: id, Ask: 10.0},
				MaintenanceMargin: maintenanceMargin,
			}, nil
		},
	}
}

// ─── validateMargin ───────────────────────────────────────────────────────────

func TestValidateMargin_IsClient_False_TrezorError(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasSufficientTrezorFn: func(ctx context.Context, currency string, required decimal.Decimal) (bool, error) {
			return false, errors.New("trezor db error")
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: false, ListingID: 1})
	if err == nil {
		t.Error("expected error when trezor check fails")
	}
}

func TestValidateMargin_IsClient_False_TrezorInsufficient(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasSufficientTrezorFn: func(ctx context.Context, currency string, required decimal.Decimal) (bool, error) {
			return false, nil
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: false, ListingID: 1})
	if !errors.Is(err, ErrInsufficientMargin) {
		t.Errorf("expected ErrInsufficientMargin, got %v", err)
	}
}

func TestValidateMargin_IsClient_True_CreditApproved(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasApprovedCreditFn: func(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
			return true, nil
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: true, ListingID: 1, UserID: 5})
	if err != nil {
		t.Errorf("expected nil when credit approved, got %v", err)
	}
}

func TestValidateMargin_IsClient_True_NoCreditBalanceOK(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasApprovedCreditFn: func(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
			return false, nil
		},
		hasSufficientMarginFn: func(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
			return true, nil
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: true, ListingID: 1, AccountID: 10})
	if err != nil {
		t.Errorf("expected nil when balance OK, got %v", err)
	}
}

func TestValidateMargin_IsClient_True_NoCreditBalanceInsufficient(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasApprovedCreditFn: func(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
			return false, nil
		},
		hasSufficientMarginFn: func(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
			return false, nil
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: true, ListingID: 1, AccountID: 10})
	if !errors.Is(err, ErrInsufficientMargin) {
		t.Errorf("expected ErrInsufficientMargin, got %v", err)
	}
}

func TestValidateMargin_IsClient_True_BalanceError(t *testing.T) {
	ctx := context.Background()
	checker := &spyMarginChecker{
		hasApprovedCreditFn: func(ctx context.Context, userID int64, required decimal.Decimal) (bool, error) {
			return false, nil
		},
		hasSufficientMarginFn: func(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error) {
			return false, errors.New("balance db error")
		},
	}
	svc := newConcreteServiceWithMargin(listingWithMargin(100.0), &spyActuaryRepo{}, &spyFundsManager{}, checker)
	err := svc.validateMargin(ctx, &CreateOrderRequest{IsClient: true, ListingID: 1, AccountID: 10})
	if err == nil {
		t.Error("expected error when balance check fails")
	}
}

// ─── computeNotionalPlusCommission — missing switch paths ────────────────────

func TestComputeNotionalPlusCommission_Market(t *testing.T) {
	ctx := context.Background()
	listings := &spyListingService{
		getByIDFn: func(ctx context.Context, id int64) (*domain.ListingCalculated, error) {
			return &domain.ListingCalculated{
				Listing: domain.Listing{ID: id, Ask: 20.0, Bid: 18.0},
			}, nil
		},
	}
	svc := newConcreteService(listings, &spyActuaryRepo{}, &spyFundsManager{})
	order := &Order{
		ListingID:    1,
		OrderType:    OrderTypeMarket,
		Direction:    OrderDirectionBuy,
		ContractSize: 1,
		Quantity:     2,
	}
	total, err := svc.computeNotionalPlusCommission(ctx, order, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	notional := decimal.NewFromFloat(40.0) // 1 * 20 * 2
	expected := notional.Add(CalcMarketCommission(notional))
	if !total.Equal(expected) {
		t.Errorf("expected %s (notional+market_commission), got %s", expected, total)
	}
}

func TestComputeNotionalPlusCommission_UnknownType_ZeroCommission(t *testing.T) {
	ctx := context.Background()
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	price := decimal.NewFromFloat(10.0)
	order := &Order{
		OrderType:    OrderType("UNKNOWN"),
		ContractSize: 1,
		Quantity:     1,
		PricePerUnit: &price,
	}
	// computeNotional with unknown type returns ErrInvalidOrderType
	_, err := svc.computeNotionalPlusCommission(ctx, order, 1)
	if err == nil {
		t.Error("expected error for unknown order type")
	}
}

// ─── calculateStopLimit ───────────────────────────────────────────────────────

func TestCalculateStopLimit_NilStopPrice(t *testing.T) {
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	_, err := svc.calculateStopLimit(&OrderCalculationRequest{StopPrice: nil})
	if !errors.Is(err, ErrStopPriceRequired) {
		t.Errorf("expected ErrStopPriceRequired, got %v", err)
	}
}

func TestCalculateStopLimit_NilPricePerUnit(t *testing.T) {
	stop := decimal.NewFromFloat(50.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	_, err := svc.calculateStopLimit(&OrderCalculationRequest{StopPrice: &stop, PricePerUnit: nil})
	if !errors.Is(err, ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got %v", err)
	}
}

func TestCalculateStopLimit_Success(t *testing.T) {
	stop := decimal.NewFromFloat(50.0)
	limit := decimal.NewFromFloat(55.0)
	svc := newConcreteService(&spyListingService{}, &spyActuaryRepo{}, &spyFundsManager{})
	resp, err := svc.calculateStopLimit(&OrderCalculationRequest{
		StopPrice:    &stop,
		PricePerUnit: &limit,
		ContractSize: 2,
		Quantity:     3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	// notional = 2 * 55 * 3 = 330
	expected := decimal.NewFromFloat(330.0)
	if !resp.ApproximatePrice.Equal(expected) {
		t.Errorf("expected approximate price %s, got %s", expected, resp.ApproximatePrice)
	}
}
