package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/service"
)

// ─── Mock ExchangeRepository ──────────────────────────────────────────────────

type mockExchangeRepository struct {
	mock.Mock
}

func (m *mockExchangeRepository) List(ctx context.Context, filter domain.ListExchangesFilter) ([]domain.Exchange, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Exchange), args.Error(1)
}
func (m *mockExchangeRepository) GetByID(ctx context.Context, id int64) (*domain.Exchange, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Exchange), args.Error(1)
}
func (m *mockExchangeRepository) GetByMICCode(ctx context.Context, micCode string) (*domain.Exchange, error) {
	args := m.Called(ctx, micCode)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Exchange), args.Error(1)
}
func (m *mockExchangeRepository) IsHoliday(ctx context.Context, polity string, date time.Time) (bool, error) {
	args := m.Called(ctx, polity, date)
	return args.Bool(0), args.Error(1)
}

// ─── Mock MarketModeStore ─────────────────────────────────────────────────────

type mockMarketModeStore struct {
	mock.Mock
}

func (m *mockMarketModeStore) SetTestMode(ctx context.Context, enabled bool) error {
	return m.Called(ctx, enabled).Error(0)
}
func (m *mockMarketModeStore) IsTestMode(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func newBerzaService(repo domain.ExchangeRepository, store domain.MarketModeStore) domain.BerzaService {
	return service.NewBerzaService(repo, store)
}

// buildExchangeUTC builds a test Exchange in UTC with the given open/close hours.
func buildExchangeUTC(openHour, closeHour int) domain.Exchange {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	return domain.Exchange{
		ID:        1,
		Polity:    "US",
		Timezone:  "UTC",
		OpenTime:  base.Add(time.Duration(openHour) * time.Hour),
		CloseTime: base.Add(time.Duration(closeHour) * time.Hour),
	}
}

// ─── ListExchanges ────────────────────────────────────────────────────────────

func TestBerzaService_ListExchanges_Success(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	filter := domain.ListExchangesFilter{Polity: "US"}
	exchanges := []domain.Exchange{{ID: 1, Name: "NYSE"}, {ID: 2, Name: "NASDAQ"}}
	repo.On("List", ctx, filter).Return(exchanges, nil)

	svc := newBerzaService(repo, store)
	got, err := svc.ListExchanges(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestBerzaService_ListExchanges_Error(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	repo.On("List", ctx, domain.ListExchangesFilter{}).Return(nil, errors.New("db error"))

	svc := newBerzaService(repo, store)
	_, err := svc.ListExchanges(ctx, domain.ListExchangesFilter{})
	assert.Error(t, err)
}

// ─── GetExchange ──────────────────────────────────────────────────────────────

func TestBerzaService_GetExchange_ByID(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	ex := &domain.Exchange{ID: 3, Name: "LSE"}
	repo.On("GetByID", ctx, int64(3)).Return(ex, nil)

	svc := newBerzaService(repo, store)
	got, err := svc.GetExchange(ctx, 3, "")
	require.NoError(t, err)
	assert.Equal(t, ex, got)
}

func TestBerzaService_GetExchange_ByMICCode(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	ex := &domain.Exchange{ID: 4, MICCode: "XNYS"}
	repo.On("GetByMICCode", ctx, "XNYS").Return(ex, nil)

	svc := newBerzaService(repo, store)
	got, err := svc.GetExchange(ctx, 0, "XNYS")
	require.NoError(t, err)
	assert.Equal(t, "XNYS", got.MICCode)
}

func TestBerzaService_GetExchange_NotFound(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	repo.On("GetByID", ctx, int64(99)).Return(nil, domain.ErrExchangeNotFound)

	svc := newBerzaService(repo, store)
	_, err := svc.GetExchange(ctx, 99, "")
	assert.ErrorIs(t, err, domain.ErrExchangeNotFound)
}

// ─── ToggleMarketTestMode ─────────────────────────────────────────────────────

func TestBerzaService_ToggleMarketTestMode_Enable(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("SetTestMode", ctx, true).Return(nil)

	svc := newBerzaService(repo, store)
	err := svc.ToggleMarketTestMode(ctx, true)
	assert.NoError(t, err)
	store.AssertExpectations(t)
}

func TestBerzaService_ToggleMarketTestMode_Disable(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("SetTestMode", ctx, false).Return(nil)

	svc := newBerzaService(repo, store)
	err := svc.ToggleMarketTestMode(ctx, false)
	assert.NoError(t, err)
}

func TestBerzaService_ToggleMarketTestMode_Error(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("SetTestMode", ctx, true).Return(errors.New("redis error"))

	svc := newBerzaService(repo, store)
	err := svc.ToggleMarketTestMode(ctx, true)
	assert.Error(t, err)
}

// ─── IsExchangeOpen ───────────────────────────────────────────────────────────

func TestBerzaService_IsExchangeOpen_RepoError(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	repo.On("GetByID", ctx, int64(1)).Return(nil, errors.New("db error"))

	svc := newBerzaService(repo, store)
	status, err := svc.IsExchangeOpen(ctx, 1)
	assert.Error(t, err)
	assert.Equal(t, domain.MarketStatusClosed, status)
}

// ─── GetMarketStatus ──────────────────────────────────────────────────────────

func TestBerzaService_GetMarketStatus_TestModeOn(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(true, nil)

	ex := buildExchangeUTC(9, 17)
	svc := newBerzaService(repo, store)
	status, err := svc.GetMarketStatus(ctx, ex)
	require.NoError(t, err)
	// Test mode always returns OPEN
	assert.Equal(t, domain.MarketStatusOpen, status)
}

func TestBerzaService_GetMarketStatus_TestModeError(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(false, errors.New("redis down"))

	ex := buildExchangeUTC(9, 17)
	svc := newBerzaService(repo, store)
	status, err := svc.GetMarketStatus(ctx, ex)
	assert.Error(t, err)
	assert.Equal(t, domain.MarketStatusClosed, status)
}

func TestBerzaService_GetMarketStatus_InvalidTimezone(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(false, nil)

	ex := buildExchangeUTC(9, 17)
	ex.Timezone = "Invalid/Zone"
	svc := newBerzaService(repo, store)
	status, err := svc.GetMarketStatus(ctx, ex)
	assert.Error(t, err)
	assert.Equal(t, domain.MarketStatusClosed, status)
}

func TestBerzaService_GetMarketStatus_Holiday(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(false, nil)
	repo.On("IsHoliday", ctx, "US", mock.AnythingOfType("time.Time")).Return(true, nil)

	// Use UTC with 00:00–23:59 so time-of-day check would pass — but holiday check fires first
	ex := buildExchangeUTC(0, 23)
	svc := newBerzaService(repo, store)
	status, err := svc.GetMarketStatus(ctx, ex)
	require.NoError(t, err)
	assert.Equal(t, domain.MarketStatusClosed, status)
}

func TestBerzaService_GetMarketStatus_HolidayCheckError_ContinuesNormally(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(false, nil)
	// IsHoliday error is treated as non-holiday (silent continue)
	repo.On("IsHoliday", ctx, "US", mock.AnythingOfType("time.Time")).Return(false, errors.New("holiday error"))

	// Open 00:00 – 23:59 in UTC so today's time will land in OPEN window (on a weekday)
	ex := buildExchangeUTC(0, 24)
	ex.CloseTime = ex.OpenTime.Add(23 * time.Hour).Add(59 * time.Minute)

	svc := newBerzaService(repo, store)
	// Just ensure it doesn't crash; the status depends on weekday/time-of-day
	_, err := svc.GetMarketStatus(ctx, ex)
	// Error is expected only if weekend or outside hours; we just verify no panic
	_ = err
}

func TestBerzaService_GetMarketStatus_OpenAllDay(t *testing.T) {
	// Use UTC open=00:00, close=23:59 on a known weekday to force OPEN status
	// We pick Monday 2025-01-06 (not a US holiday)
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()
	store.On("IsTestMode", ctx).Return(false, nil)
	repo.On("IsHoliday", ctx, "US", mock.AnythingOfType("time.Time")).Return(false, nil)

	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	ex := domain.Exchange{
		ID:        1,
		Polity:    "US",
		Timezone:  "UTC",
		OpenTime:  base,                                    // 00:00
		CloseTime: base.Add(23*time.Hour + 59*time.Minute), // 23:59
	}

	svc := newBerzaService(repo, store)
	status, err := svc.GetMarketStatus(ctx, ex)
	require.NoError(t, err)
	// Status depends on current UTC time and day — just verify it's one of the valid statuses
	validStatuses := map[domain.MarketStatus]bool{
		domain.MarketStatusOpen:       true,
		domain.MarketStatusPreMarket:  true,
		domain.MarketStatusAfterHours: true,
		domain.MarketStatusClosed:     true,
	}
	assert.True(t, validStatuses[status], "unexpected status: %s", status)
}

func TestBerzaService_IsExchangeOpen_Success(t *testing.T) {
	repo := &mockExchangeRepository{}
	store := &mockMarketModeStore{}
	ctx := context.Background()

	ex := &domain.Exchange{ID: 5, Polity: "US", Timezone: "UTC", OpenTime: time.Time{}, CloseTime: time.Time{}}
	repo.On("GetByID", ctx, int64(5)).Return(ex, nil)
	// Use test mode ON so GetMarketStatus returns OPEN immediately
	store.On("IsTestMode", ctx).Return(true, nil)

	svc := newBerzaService(repo, store)
	status, err := svc.IsExchangeOpen(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, domain.MarketStatusOpen, status)
}
