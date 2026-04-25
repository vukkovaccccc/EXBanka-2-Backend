// White-box tests for listing_service.go — package service gives access to
// unexported helpers (calculate, contractSizeAndMargin, parseFuture*, parseOption*).
package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// floatEq returns true when a and b differ by less than 1e-9.
func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// ─── Inline mock for ListingRepository ───────────────────────────────────────

type mockListingRepo struct {
	listFn            func(ctx context.Context, filter domain.ListingFilter) ([]domain.Listing, int64, error)
	getByIDFn         func(ctx context.Context, id int64) (*domain.Listing, error)
	getHistoryFn      func(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error)
	getLatestChangeFn func(ctx context.Context, id int64) (float64, error)
}

func (m *mockListingRepo) List(ctx context.Context, filter domain.ListingFilter) ([]domain.Listing, int64, error) {
	if m.listFn != nil {
		return m.listFn(ctx, filter)
	}
	return nil, 0, nil
}
func (m *mockListingRepo) GetByID(ctx context.Context, id int64) (*domain.Listing, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return &domain.Listing{ID: id}, nil
}
func (m *mockListingRepo) GetHistory(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error) {
	if m.getHistoryFn != nil {
		return m.getHistoryFn(ctx, id, from, to)
	}
	return nil, nil
}
func (m *mockListingRepo) GetLatestDailyChange(ctx context.Context, id int64) (float64, error) {
	if m.getLatestChangeFn != nil {
		return m.getLatestChangeFn(ctx, id)
	}
	return 0, nil
}
func (m *mockListingRepo) Create(ctx context.Context, l domain.Listing) (*domain.Listing, error) {
	return &l, nil
}
func (m *mockListingRepo) UpdatePrices(ctx context.Context, id int64, price, ask, bid float64, volume int64, at time.Time) error {
	return nil
}
func (m *mockListingRepo) UpdateDetails(ctx context.Context, id int64, detailsJSON string) error {
	return nil
}
func (m *mockListingRepo) AppendDailyPrice(ctx context.Context, info domain.ListingDailyPriceInfo) error {
	return nil
}
func (m *mockListingRepo) ListAll(ctx context.Context) ([]domain.Listing, error) {
	return nil, nil
}

// ─── calculate ────────────────────────────────────────────────────────────────

func TestCalculate_Stock(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeStock,
		Price:       100.0,
		Volume:      500,
	}
	result := calculate(l, 5.0)

	// ChangePercent: prev = 100 - 5 = 95; (100-95)/95*100 ≈ 5.26%
	if result.ChangePercent <= 0 {
		t.Errorf("expected positive change percent, got %f", result.ChangePercent)
	}
	// DollarVolume = 500 * 100 = 50000
	if result.DollarVolume != 50000.0 {
		t.Errorf("expected dollar volume 50000, got %f", result.DollarVolume)
	}
	// Stock: contractSize=1, maintenanceMargin=0.5*100=50
	if result.ContractSize != 1 {
		t.Errorf("expected contract size 1, got %f", result.ContractSize)
	}
	if result.MaintenanceMargin != 50.0 {
		t.Errorf("expected maintenance margin 50, got %f", result.MaintenanceMargin)
	}
	// InitialMarginCost = 50 * 1.1 ≈ 55 (allow float imprecision)
	if !floatEq(result.InitialMarginCost, 55.0) {
		t.Errorf("expected initial margin ≈55, got %v", result.InitialMarginCost)
	}
	// NominalValue = 1 * 100 = 100
	if result.NominalValue != 100.0 {
		t.Errorf("expected nominal value 100, got %f", result.NominalValue)
	}
}

func TestCalculate_Stock_ZeroChange(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeStock,
		Price:       100.0,
	}
	result := calculate(l, 0.0)
	// change=0 → previousClose = 100 - 0 = 100; (100-100)/100 = 0%
	if result.ChangePercent != 0.0 {
		t.Errorf("expected 0%% change, got %f", result.ChangePercent)
	}
}

func TestCalculate_Stock_ZeroPrice(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeStock,
		Price:       0.0,
	}
	// previousClose = 0 - 0 = 0 → guard against division by zero
	result := calculate(l, 0.0)
	if result.ChangePercent != 0.0 {
		t.Errorf("expected 0%% change for zero price, got %f", result.ChangePercent)
	}
}

func TestCalculate_Forex(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeForex,
		Price:       1.1,
		Volume:      1000,
	}
	result := calculate(l, 0.0)
	// Forex: contractSize=1000, maintenanceMargin=1000*1.1*0.1=110
	if result.ContractSize != 1000 {
		t.Errorf("expected contract size 1000, got %f", result.ContractSize)
	}
	if result.MaintenanceMargin != 110.0 {
		t.Errorf("expected maintenance margin 110, got %f", result.MaintenanceMargin)
	}
}

func TestCalculate_Future_WithJSON(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeFuture,
		Price:       50.0,
		DetailsJSON: `{"contract_size": 5}`,
	}
	result := calculate(l, 0.0)
	// contractSize=5, maintenanceMargin=5*50*0.1=25
	if result.ContractSize != 5 {
		t.Errorf("expected contract size 5, got %f", result.ContractSize)
	}
	if result.MaintenanceMargin != 25.0 {
		t.Errorf("expected maintenance margin 25, got %f", result.MaintenanceMargin)
	}
}

func TestCalculate_Future_WithoutJSON(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeFuture,
		Price:       50.0,
		DetailsJSON: `{}`,
	}
	result := calculate(l, 0.0)
	// contract_size missing → defaults to 1; maintenanceMargin=1*50*0.1=5
	if result.ContractSize != 1 {
		t.Errorf("expected contract size 1 (fallback), got %f", result.ContractSize)
	}
}

func TestCalculate_Option_WithJSON(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeOption,
		Price:       5.0,
		DetailsJSON: `{"underlying_price": 100.0}`,
	}
	result := calculate(l, 0.0)
	// Option: contractSize=100, maintenanceMargin=100*0.5*100=5000
	if result.ContractSize != 100 {
		t.Errorf("expected contract size 100, got %f", result.ContractSize)
	}
	if result.MaintenanceMargin != 5000.0 {
		t.Errorf("expected maintenance margin 5000, got %f", result.MaintenanceMargin)
	}
}

func TestCalculate_Option_MissingUnderlyingPrice(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeOption,
		Price:       5.0,
		DetailsJSON: `{}`,
	}
	result := calculate(l, 0.0)
	// underlying_price=0 → maintenanceMargin=100*0.5*0=0
	if result.MaintenanceMargin != 0.0 {
		t.Errorf("expected maintenance margin 0, got %f", result.MaintenanceMargin)
	}
}

// ─── contractSizeAndMargin ────────────────────────────────────────────────────

func TestContractSizeAndMargin_Stock(t *testing.T) {
	l := domain.Listing{ListingType: domain.ListingTypeStock, Price: 200.0}
	cs, mm := contractSizeAndMargin(l)
	if cs != 1 {
		t.Errorf("stock: expected cs=1, got %f", cs)
	}
	if mm != 100.0 {
		t.Errorf("stock: expected mm=100, got %f", mm)
	}
}

func TestContractSizeAndMargin_Forex(t *testing.T) {
	l := domain.Listing{ListingType: domain.ListingTypeForex, Price: 2.0}
	cs, mm := contractSizeAndMargin(l)
	if cs != 1000 {
		t.Errorf("forex: expected cs=1000, got %f", cs)
	}
	// 1000 * 2.0 * 0.1 = 200
	if mm != 200.0 {
		t.Errorf("forex: expected mm=200, got %f", mm)
	}
}

func TestContractSizeAndMargin_Future_ValidJSON(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeFuture,
		Price:       10.0,
		DetailsJSON: `{"contract_size": 50}`,
	}
	cs, mm := contractSizeAndMargin(l)
	if cs != 50 {
		t.Errorf("future: expected cs=50, got %f", cs)
	}
	// 50 * 10 * 0.1 = 50
	if mm != 50.0 {
		t.Errorf("future: expected mm=50, got %f", mm)
	}
}

func TestContractSizeAndMargin_Future_InvalidJSON(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeFuture,
		Price:       10.0,
		DetailsJSON: `not-json`,
	}
	cs, _ := contractSizeAndMargin(l)
	// Parses to 0 → fallback to 1
	if cs != 1 {
		t.Errorf("future invalid JSON: expected cs=1 fallback, got %f", cs)
	}
}

func TestContractSizeAndMargin_Option(t *testing.T) {
	l := domain.Listing{
		ListingType: domain.ListingTypeOption,
		Price:       5.0,
		DetailsJSON: `{"underlying_price": 50.0}`,
	}
	cs, mm := contractSizeAndMargin(l)
	if cs != 100 {
		t.Errorf("option: expected cs=100, got %f", cs)
	}
	// 100 * 0.5 * 50 = 2500
	if mm != 2500.0 {
		t.Errorf("option: expected mm=2500, got %f", mm)
	}
}

// ─── parseFutureContractSize ──────────────────────────────────────────────────

func TestParseFutureContractSize_ValidJSON(t *testing.T) {
	result := parseFutureContractSize(`{"contract_size": 42.5}`)
	if result != 42.5 {
		t.Errorf("expected 42.5, got %f", result)
	}
}

func TestParseFutureContractSize_MissingField(t *testing.T) {
	result := parseFutureContractSize(`{}`)
	if result != 0 {
		t.Errorf("expected 0 for missing field, got %f", result)
	}
}

func TestParseFutureContractSize_InvalidJSON(t *testing.T) {
	result := parseFutureContractSize(`not-json`)
	if result != 0 {
		t.Errorf("expected 0 for invalid JSON, got %f", result)
	}
}

func TestParseFutureContractSize_EmptyString(t *testing.T) {
	result := parseFutureContractSize(``)
	if result != 0 {
		t.Errorf("expected 0 for empty string, got %f", result)
	}
}

// ─── parseOptionUnderlyingPrice ───────────────────────────────────────────────

func TestParseOptionUnderlyingPrice_ValidJSON(t *testing.T) {
	result := parseOptionUnderlyingPrice(`{"underlying_price": 123.45}`)
	if result != 123.45 {
		t.Errorf("expected 123.45, got %f", result)
	}
}

func TestParseOptionUnderlyingPrice_MissingField(t *testing.T) {
	result := parseOptionUnderlyingPrice(`{}`)
	if result != 0 {
		t.Errorf("expected 0 for missing field, got %f", result)
	}
}

func TestParseOptionUnderlyingPrice_InvalidJSON(t *testing.T) {
	result := parseOptionUnderlyingPrice(`invalid`)
	if result != 0 {
		t.Errorf("expected 0 for invalid JSON, got %f", result)
	}
}

// ─── NewListingService / ListListings / GetListingByID ────────────────────────

func TestNewListingService_NotNil(t *testing.T) {
	svc := NewListingService(&mockListingRepo{}, nil, "test-key")
	if svc == nil {
		t.Error("expected non-nil service")
	}
}

func TestListListings_Success(t *testing.T) {
	repo := &mockListingRepo{
		listFn: func(ctx context.Context, filter domain.ListingFilter) ([]domain.Listing, int64, error) {
			return []domain.Listing{
				{ID: 1, ListingType: domain.ListingTypeStock, Price: 10.0},
				{ID: 2, ListingType: domain.ListingTypeForex, Price: 5.0},
			}, 2, nil
		},
	}
	svc := NewListingService(repo, nil, "")
	results, total, err := svc.ListListings(context.Background(), domain.ListingFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestListListings_RepoError(t *testing.T) {
	repo := &mockListingRepo{
		listFn: func(ctx context.Context, filter domain.ListingFilter) ([]domain.Listing, int64, error) {
			return nil, 0, errors.New("db error")
		},
	}
	svc := NewListingService(repo, nil, "")
	_, _, err := svc.ListListings(context.Background(), domain.ListingFilter{})
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetListingByID_Success(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{ID: id, ListingType: domain.ListingTypeStock, Price: 50.0}, nil
		},
	}
	svc := NewListingService(repo, nil, "")
	result, err := svc.GetListingByID(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != 5 {
		t.Errorf("expected ID 5, got %d", result.ID)
	}
}

func TestGetListingByID_NotFound(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return nil, domain.ErrListingNotFound
		},
	}
	svc := NewListingService(repo, nil, "")
	_, err := svc.GetListingByID(context.Background(), 99)
	if !errors.Is(err, domain.ErrListingNotFound) {
		t.Errorf("expected ErrListingNotFound, got: %v", err)
	}
}

func TestGetListingHistory_FallsBackToRepo(t *testing.T) {
	expected := []domain.ListingDailyPriceInfo{{ID: 1, Price: 100.0}}
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{ID: id, Ticker: "TEST", ListingType: domain.ListingTypeStock}, nil
		},
		getHistoryFn: func(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error) {
			return expected, nil
		},
	}
	// httpClient=nil causes NewListingService to use default; no EODHD key so
	// FetchListingHistoryFromMarkets will fail, falling back to repo.GetHistory.
	svc := NewListingService(repo, nil, "")
	now := time.Now()
	result, err := svc.GetListingHistory(context.Background(), 1, now.AddDate(0, -1, 0), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected at least one result from repo fallback")
	}
}
