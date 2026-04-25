package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// ─── Minimal mock for domain.ListingRepository ────────────────────────────────

type mockListingRepo struct {
	getByIDFn func(ctx context.Context, id int64) (*domain.Listing, error)
}

func (m *mockListingRepo) GetByID(ctx context.Context, id int64) (*domain.Listing, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return &domain.Listing{ID: id}, nil
}
func (m *mockListingRepo) List(ctx context.Context, filter domain.ListingFilter) ([]domain.Listing, int64, error) {
	return nil, 0, nil
}
func (m *mockListingRepo) GetHistory(ctx context.Context, id int64, from, to time.Time) ([]domain.ListingDailyPriceInfo, error) {
	return nil, nil
}
func (m *mockListingRepo) GetLatestDailyChange(ctx context.Context, id int64) (float64, error) {
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

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestNewListingMarketDataProvider_NotNil(t *testing.T) {
	p := NewListingMarketDataProvider(&mockListingRepo{})
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestGetMarketSnapshot_RepoError(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return nil, errors.New("not found")
		},
	}
	p := NewListingMarketDataProvider(repo)
	_, err := p.GetMarketSnapshot(context.Background(), 1)
	if err == nil {
		t.Error("expected error from repo")
	}
}

func TestGetMarketSnapshot_Stock_NoDetailsJSON(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				Ask:         10.5,
				Bid:         10.0,
				Volume:      1000,
				ListingType: domain.ListingTypeStock,
				DetailsJSON: "",
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	snap, err := p.GetMarketSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Ask != 10.5 {
		t.Errorf("expected Ask 10.5, got %f", snap.Ask)
	}
	if snap.Bid != 10.0 {
		t.Errorf("expected Bid 10.0, got %f", snap.Bid)
	}
	if snap.Volume != 1000 {
		t.Errorf("expected Volume 1000, got %d", snap.Volume)
	}
}

func TestGetMarketSnapshot_Forex_WithDetails(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				ListingType: domain.ListingTypeForex,
				DetailsJSON: `{"base_currency":"EUR","quote_currency":"USD"}`,
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	snap, err := p.GetMarketSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.ForexBaseCurrency != "EUR" {
		t.Errorf("expected base EUR, got %s", snap.ForexBaseCurrency)
	}
	if snap.ForexQuoteCurrency != "USD" {
		t.Errorf("expected quote USD, got %s", snap.ForexQuoteCurrency)
	}
}

func TestGetMarketSnapshot_Forex_InvalidJSON(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				ListingType: domain.ListingTypeForex,
				DetailsJSON: `not-json`,
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	// Should not error — invalid JSON is silently ignored for ForexDetails
	snap, err := p.GetMarketSnapshot(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Base and quote should be empty since JSON parse failed
	if snap.ForexBaseCurrency != "" || snap.ForexQuoteCurrency != "" {
		t.Errorf("expected empty currency on JSON parse failure")
	}
}

func TestGetMarketSnapshot_Future_WithSettlementDate(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				ListingType: domain.ListingTypeFuture,
				DetailsJSON: `{"settlement_date":"2025-12-31"}`,
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	snap, err := p.GetMarketSnapshot(context.Background(), 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.SettlementDate == nil {
		t.Error("expected SettlementDate to be set")
	} else {
		if snap.SettlementDate.Month() != time.December || snap.SettlementDate.Day() != 31 {
			t.Errorf("expected 2025-12-31, got %s", snap.SettlementDate)
		}
	}
}

func TestGetMarketSnapshot_Option_WithSettlementDate_RFC3339(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				ListingType: domain.ListingTypeOption,
				DetailsJSON: `{"settlement_date":"2025-06-15T00:00:00Z"}`,
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	snap, err := p.GetMarketSnapshot(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.SettlementDate == nil {
		t.Error("expected SettlementDate to be set from RFC3339")
	}
}

func TestGetMarketSnapshot_Future_NoSettlementDate(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return &domain.Listing{
				ID:          id,
				ListingType: domain.ListingTypeFuture,
				DetailsJSON: `{"contract_size":100}`,
			}, nil
		},
	}
	p := NewListingMarketDataProvider(repo)
	snap, err := p.GetMarketSnapshot(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.SettlementDate != nil {
		t.Errorf("expected nil SettlementDate, got %s", snap.SettlementDate)
	}
}
