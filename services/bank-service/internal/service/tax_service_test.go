// White-box tests for tax_service.go — uses package service to access unexported
// helper functions (advisoryKey, nativeToRSD, rsdToNative) alongside exported window functions.
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ─── PreviousMonthWindow ──────────────────────────────────────────────────────

func TestPreviousMonthWindow_MiddleOfYear(t *testing.T) {
	now := time.Date(2025, time.July, 15, 12, 0, 0, 0, time.UTC)
	start, end := PreviousMonthWindow(now)

	if start.Month() != time.June || start.Year() != 2025 || start.Day() != 1 {
		t.Errorf("expected start 2025-06-01, got %s", start)
	}
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		t.Errorf("start should be at midnight, got %s", start)
	}
	// end = 2025-07-01 00:00 - 1µs = 2025-06-30 23:59:59.999999
	if end.Month() != time.June || end.Day() != 30 || end.Year() != 2025 {
		t.Errorf("expected end 2025-06-30, got %s", end)
	}
	if end.Before(start) {
		t.Error("end must be after start")
	}
}

func TestPreviousMonthWindow_January(t *testing.T) {
	// January → previous = December of previous year
	now := time.Date(2025, time.January, 10, 0, 0, 0, 0, time.UTC)
	start, end := PreviousMonthWindow(now)

	if start.Month() != time.December || start.Year() != 2024 {
		t.Errorf("expected start December 2024, got %s", start)
	}
	if end.Month() != time.December || end.Year() != 2024 {
		t.Errorf("expected end December 2024, got %s", end)
	}
}

func TestPreviousMonthWindow_EndIsBeforeStart(t *testing.T) {
	now := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	start, end := PreviousMonthWindow(now)
	if !end.After(start) {
		t.Error("end must be strictly after start")
	}
}

// ─── CurrentMonthWindow ───────────────────────────────────────────────────────

func TestCurrentMonthWindow_Basic(t *testing.T) {
	now := time.Date(2025, time.June, 15, 10, 30, 0, 0, time.UTC)
	start, end := CurrentMonthWindow(now)

	if start.Month() != time.June || start.Year() != 2025 || start.Day() != 1 {
		t.Errorf("expected start 2025-06-01, got %s", start)
	}
	if start.Hour() != 0 {
		t.Errorf("start should be midnight, got %s", start)
	}
	// end = 2025-07-01 00:00 - 1µs = 2025-06-30 23:59:59.999999
	if end.Month() != time.June {
		t.Errorf("expected end in June, got %s", end)
	}
	if !end.After(start) {
		t.Error("end must be after start")
	}
}

func TestCurrentMonthWindow_December(t *testing.T) {
	now := time.Date(2025, time.December, 25, 0, 0, 0, 0, time.UTC)
	start, end := CurrentMonthWindow(now)

	if start.Month() != time.December {
		t.Errorf("expected start in December, got %s", start)
	}
	if end.Month() != time.December {
		t.Errorf("expected end in December, got %s", end)
	}
}

// ─── MonthWindow ──────────────────────────────────────────────────────────────

func TestMonthWindow_January(t *testing.T) {
	start, end := MonthWindow(2024, time.January, time.UTC)
	if start.Day() != 1 || start.Month() != time.January || start.Year() != 2024 {
		t.Errorf("expected 2024-01-01, got %s", start)
	}
	if end.Month() != time.January || end.Day() != 31 {
		t.Errorf("expected end Jan 31, got %s", end)
	}
}

func TestMonthWindow_February_LeapYear(t *testing.T) {
	// 2024 is a leap year → February has 29 days
	start, end := MonthWindow(2024, time.February, time.UTC)
	if start.Month() != time.February {
		t.Errorf("expected February, got %s", start)
	}
	if end.Day() != 29 {
		t.Errorf("expected end Feb 29 (leap year), got %s", end)
	}
}

func TestMonthWindow_NilLocation_UsesLocal(t *testing.T) {
	// nil loc → falls back to time.Local (should not panic)
	start, end := MonthWindow(2025, time.April, nil)
	if start.Month() != time.April {
		t.Errorf("expected April, got %s", start)
	}
	if !end.After(start) {
		t.Error("end must be after start")
	}
}

func TestMonthWindow_EndIsBeforeNextMonthStart(t *testing.T) {
	start, end := MonthWindow(2025, time.March, time.UTC)
	nextMonthStart := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	if !end.Before(nextMonthStart) {
		t.Errorf("end %s should be before April 1", end)
	}
	_ = start
}

// ─── advisoryKey ─────────────────────────────────────────────────────────────

func TestAdvisoryKey_Deterministic(t *testing.T) {
	k1 := advisoryKey("tax:period:2025-06")
	k2 := advisoryKey("tax:period:2025-06")
	if k1 != k2 {
		t.Errorf("same input must produce same key: %d vs %d", k1, k2)
	}
}

func TestAdvisoryKey_DifferentInputs(t *testing.T) {
	k1 := advisoryKey("tax:period:2025-06")
	k2 := advisoryKey("tax:period:2025-07")
	if k1 == k2 {
		t.Errorf("different inputs should (very likely) produce different keys")
	}
}

func TestAdvisoryKey_EmptyString(t *testing.T) {
	k := advisoryKey("")
	// FNV-64a of empty string is well-defined; just ensure no panic
	_ = k
}

// ─── nativeToRSD ─────────────────────────────────────────────────────────────

func TestNativeToRSD_RSD(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.0}}
	amount := decimal.NewFromFloat(100.0)
	result := nativeToRSD(rates, amount, "RSD")
	if !result.Equal(amount) {
		t.Errorf("RSD→RSD: expected unchanged %s, got %s", amount, result)
	}
}

func TestNativeToRSD_KnownCurrency(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.0}}
	amount := decimal.NewFromFloat(10.0)
	result := nativeToRSD(rates, amount, "USD")
	expected := decimal.NewFromFloat(1170.0) // 10 * 117
	if !result.Equal(expected) {
		t.Errorf("USD→RSD: expected %s, got %s", expected, result)
	}
}

func TestNativeToRSD_UnknownCurrency(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "EUR", Srednji: 117.0}}
	amount := decimal.NewFromFloat(50.0)
	// Unknown currency → returns amount unchanged
	result := nativeToRSD(rates, amount, "XYZ")
	if !result.Equal(amount) {
		t.Errorf("unknown currency: expected amount unchanged %s, got %s", amount, result)
	}
}

func TestNativeToRSD_ZeroMidRate(t *testing.T) {
	// Rate with Srednji=0 should not be used (guard: r.Srednji > 0)
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 0}}
	amount := decimal.NewFromFloat(50.0)
	result := nativeToRSD(rates, amount, "USD")
	// Should fall through to return amount unchanged
	if !result.Equal(amount) {
		t.Errorf("zero mid rate: expected amount unchanged, got %s", result)
	}
}

// ─── rsdToNative ─────────────────────────────────────────────────────────────

func TestRsdToNative_RSD(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.0}}
	rsdAmount := decimal.NewFromFloat(100.0)
	result := rsdToNative(rates, rsdAmount, "RSD")
	if !result.Equal(rsdAmount) {
		t.Errorf("RSD→RSD: expected unchanged %s, got %s", rsdAmount, result)
	}
}

func TestRsdToNative_KnownCurrency(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.0}}
	rsdAmount := decimal.NewFromFloat(1170.0)
	result := rsdToNative(rates, rsdAmount, "USD")
	expected := decimal.NewFromFloat(10.0) // 1170 / 117
	if !result.Equal(expected) {
		t.Errorf("RSD→USD: expected %s, got %s", expected, result)
	}
}

func TestRsdToNative_UnknownCurrency(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "EUR", Srednji: 117.0}}
	rsdAmount := decimal.NewFromFloat(500.0)
	result := rsdToNative(rates, rsdAmount, "XYZ")
	if !result.Equal(rsdAmount) {
		t.Errorf("unknown currency: expected amount unchanged, got %s", result)
	}
}

func TestRsdToNative_ZeroMidRate(t *testing.T) {
	rates := []domain.ExchangeRate{{Oznaka: "USD", Srednji: 0}}
	rsdAmount := decimal.NewFromFloat(500.0)
	result := rsdToNative(rates, rsdAmount, "USD")
	if !result.Equal(rsdAmount) {
		t.Errorf("zero mid rate: expected amount unchanged, got %s", result)
	}
}

// ─── NewTaxService ────────────────────────────────────────────────────────────

func TestNewTaxService_NotNil(t *testing.T) {
	svc := NewTaxService(nil, nil, nil, 0)
	if svc == nil {
		t.Error("NewTaxService should return non-nil")
	}
}

func TestNewTaxService_WithConfig(t *testing.T) {
	svc := NewTaxService(nil, nil, nil, 42)
	if svc == nil {
		t.Error("NewTaxService should return non-nil even with config ID")
	}
}

// ─── setCached + ResolveStateRevenueAccountID (cached path) ──────────────────

func TestSetCached_StoreAndRetrieve(t *testing.T) {
	svc := NewTaxService(nil, nil, nil, 0)
	svc.setCached(99)
	if svc.cachedStateID != 99 {
		t.Errorf("setCached: expected 99, got %d", svc.cachedStateID)
	}
}

func TestResolveStateRevenueAccountID_CachedPath(t *testing.T) {
	ctx := context.Background()
	svc := NewTaxService(nil, nil, nil, 0)
	svc.setCached(42)
	id, err := svc.ResolveStateRevenueAccountID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("expected cached ID 42, got %d", id)
	}
}

// ─── usdToRSD ─────────────────────────────────────────────────────────────────

type mockExchangeSvc struct {
	rates []domain.ExchangeRate
	err   error
}

func (m *mockExchangeSvc) GetRates(_ context.Context) ([]domain.ExchangeRate, error) {
	return m.rates, m.err
}
func (m *mockExchangeSvc) CalculateExchange(_ context.Context, from, to string, amount float64) (*domain.ExchangeConversionResult, error) {
	return nil, nil
}
func (m *mockExchangeSvc) ExecuteExchangeTransfer(_ context.Context, input domain.ExchangeTransferInput) (*domain.ExchangeTransferResult, error) {
	return nil, nil
}

func TestUsdToRSD_Success(t *testing.T) {
	ctx := context.Background()
	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.5}},
	}
	svc := NewTaxService(nil, exchange, nil, 0)
	rate, err := svc.usdToRSD(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(117.5)
	if !rate.Equal(expected) {
		t.Errorf("expected 117.5, got %s", rate)
	}
}

func TestUsdToRSD_MissingUSD(t *testing.T) {
	ctx := context.Background()
	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "EUR", Srednji: 117.0}},
	}
	svc := NewTaxService(nil, exchange, nil, 0)
	_, err := svc.usdToRSD(ctx)
	if err == nil {
		t.Error("expected error when USD rate not in list")
	}
}

func TestUsdToRSD_ExchangeError(t *testing.T) {
	ctx := context.Background()
	exchange := &mockExchangeSvc{
		err: errors.New("exchange down"),
	}
	svc := NewTaxService(nil, exchange, nil, 0)
	_, err := svc.usdToRSD(ctx)
	if err == nil {
		t.Error("expected error when exchange service fails")
	}
}

// ─── gorm+sqlmock helper ──────────────────────────────────────────────────────

func newGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return gormDB, mock
}

// ─── ComputeProfitForPeriod ───────────────────────────────────────────────────

func TestComputeProfitForPeriod_ExchangeError(t *testing.T) {
	ctx := context.Background()
	exchange := &mockExchangeSvc{err: errors.New("exchange down")}
	svc := NewTaxService(nil, exchange, nil, 0)
	_, err := svc.ComputeProfitForPeriod(ctx, time.Now().Add(-time.Hour), time.Now())
	if err == nil {
		t.Error("expected error when exchange service fails in ComputeProfitForPeriod")
	}
}

func TestComputeProfitForPeriod_MissingUSDRate(t *testing.T) {
	ctx := context.Background()
	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "EUR", Srednji: 117.0}},
	}
	svc := NewTaxService(nil, exchange, nil, 0)
	_, err := svc.ComputeProfitForPeriod(ctx, time.Now().Add(-time.Hour), time.Now())
	if err == nil {
		t.Error("expected error when USD rate is missing")
	}
}

// ─── UserTaxPaidForYear ───────────────────────────────────────────────────────

func TestUserTaxPaidForYear_Success(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(750.0))

	svc := NewTaxService(gormDB, nil, nil, 0)
	total, err := svc.UserTaxPaidForYear(ctx, 1, 2025)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 750.0 {
		t.Errorf("expected 750.0, got %f", total)
	}
}

func TestUserTaxPaidForYear_DBError(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, nil, nil, 0)
	_, err := svc.UserTaxPaidForYear(ctx, 1, 2025)
	if err == nil {
		t.Error("expected error when DB fails in UserTaxPaidForYear")
	}
}

func TestUserTaxPaidForYear_Zero(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0.0))

	svc := NewTaxService(gormDB, nil, nil, 0)
	total, err := svc.UserTaxPaidForYear(ctx, 99, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0.0 {
		t.Errorf("expected 0.0, got %f", total)
	}
}

// ─── UserTaxUnpaidForMonth ────────────────────────────────────────────────────

func TestUserTaxUnpaidForMonth_DBError(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, nil, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	_, err := svc.UserTaxUnpaidForMonth(ctx, 1, start, end)
	if err == nil {
		t.Error("expected error when first DB query fails in UserTaxUnpaidForMonth")
	}
}

func TestUserTaxUnpaidForMonth_ExchangeError_ReturnsRecorded(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// First DB query: recorded debt = 100.0
	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(100.0))

	// Exchange fails → ComputeProfitForPeriod returns error → function returns (recorded, nil)
	exchange := &mockExchangeSvc{err: errors.New("exchange down")}
	svc := NewTaxService(gormDB, exchange, nil, 0)

	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.UserTaxUnpaidForMonth(ctx, 1, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 100.0 {
		t.Errorf("expected 100.0 (recorded), got %f", result)
	}
}

func TestUserTaxUnpaidForMonth_Zero_ExchangeError(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	mock.ExpectQuery(`SELECT COALESCE`).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0.0))

	exchange := &mockExchangeSvc{err: errors.New("exchange down")}
	svc := NewTaxService(gormDB, exchange, nil, 0)

	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.January, 31, 23, 59, 59, 0, time.UTC)
	result, err := svc.UserTaxUnpaidForMonth(ctx, 5, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0.0 {
		t.Errorf("expected 0.0, got %f", result)
	}
}

// ─── ResolveStateRevenueAccountID — cfgStateRevenueAccountID path ─────────────

func TestResolveStateRevenueAccountID_CfgPathDBError(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Config ID > 0 but DB returns an error for the existence check
	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, nil, nil, 42)
	// Should fall through to fixed account number lookup which also returns error
	mock.ExpectQuery(`SELECT id FROM`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(0))

	_, err := svc.ResolveStateRevenueAccountID(ctx)
	// Either error or ErrStateAccountMissing — both indicate failure
	if err == nil {
		t.Error("expected error when DB lookup returns nothing")
	}
}

func TestResolveStateRevenueAccountID_CfgPathExists(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// EXISTS check returns true → cfg path succeeds
	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	svc := NewTaxService(gormDB, nil, nil, 42)
	id, err := svc.ResolveStateRevenueAccountID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("expected 42, got %d", id)
	}
}

func TestResolveStateRevenueAccountID_FixedAccountFound(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Fixed account number lookup returns an ID (cfgID=0 skips cfg path)
	mock.ExpectQuery(`SELECT id FROM`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(99)))

	svc := NewTaxService(gormDB, nil, nil, 0)
	id, err := svc.ResolveStateRevenueAccountID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 99 {
		t.Errorf("expected 99, got %d", id)
	}
}

// ─── ComputeProfitForPeriod — DB paths ────────────────────────────────────────

func TestComputeProfitForPeriod_DBError(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.5}},
	}
	mock.ExpectQuery("sell_fills").WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	_, err := svc.ComputeProfitForPeriod(ctx, start, end)
	if err == nil {
		t.Error("expected error when DB fails in ComputeProfitForPeriod")
	}
}

func TestComputeProfitForPeriod_EmptyRows(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 117.5}},
	}
	mock.ExpectQuery("sell_fills").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "account_id", "profit_native", "txn_count"}))

	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.ComputeProfitForPeriod(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d rows", len(result))
	}
}

func TestComputeProfitForPeriod_PositiveProfit(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 100.0}},
	}
	rows := sqlmock.NewRows([]string{"user_id", "account_id", "profit_native", "txn_count"}).
		AddRow(int64(1), int64(10), 200.0, 3)
	mock.ExpectQuery("sell_fills").WillReturnRows(rows)

	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.ComputeProfitForPeriod(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].UserID != 1 || result[0].AccountID != 10 || result[0].TxnCount != 3 {
		t.Errorf("unexpected result fields: %+v", result[0])
	}
	// TaxRSD = 200 * 0.15 * 100 = 3000
	expectedTaxRSD := decimal.NewFromFloat(200.0).Mul(decimal.NewFromFloat(TaxRate)).Mul(decimal.NewFromFloat(100.0))
	if !result[0].TaxRSD.Equal(expectedTaxRSD) {
		t.Errorf("expected TaxRSD %s, got %s", expectedTaxRSD, result[0].TaxRSD)
	}
}

func TestComputeProfitForPeriod_ZeroProfitRow(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 100.0}},
	}
	// Row with profit=0 → skipped by the <= 0 guard
	rows := sqlmock.NewRows([]string{"user_id", "account_id", "profit_native", "txn_count"}).
		AddRow(int64(1), int64(10), 0.0, 1)
	mock.ExpectQuery("sell_fills").WillReturnRows(rows)

	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.ComputeProfitForPeriod(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result for zero profit row, got %d", len(result))
	}
}

// ─── UserTaxUnpaidForMonth — full path ────────────────────────────────────────

func TestUserTaxUnpaidForMonth_FullPath(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Query 1: remaining_debt_rsd → 50.0
	mock.ExpectQuery("remaining_debt_rsd").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(50.0))

	// Query 2 (inside ComputeProfitForPeriod): profit SQL → row for user 1
	profitRows := sqlmock.NewRows([]string{"user_id", "account_id", "profit_native", "txn_count"}).
		AddRow(int64(1), int64(10), 400.0, 2)
	mock.ExpectQuery("sell_fills").WillReturnRows(profitRows)

	// Query 3: amount_rsd covered → 0.0
	mock.ExpectQuery("amount_rsd").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0.0))

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 100.0}},
	}
	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.UserTaxUnpaidForMonth(ctx, 1, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// estimated = 400 * 0.15 * 100 = 6000; covered = 0; uncovered = 6000
	// result = 50 + 6000 = 6050
	if result <= 0 {
		t.Errorf("expected positive result, got %f", result)
	}
}

func TestUserTaxUnpaidForMonth_NoProfitForUser(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Query 1: remaining_debt → 30.0
	mock.ExpectQuery("remaining_debt_rsd").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(30.0))

	// Query 2: ComputeProfitForPeriod → profit for different user (userID=99)
	profitRows := sqlmock.NewRows([]string{"user_id", "account_id", "profit_native", "txn_count"}).
		AddRow(int64(99), int64(20), 100.0, 1)
	mock.ExpectQuery("sell_fills").WillReturnRows(profitRows)

	// Query 3: amount_rsd → 0.0
	mock.ExpectQuery("amount_rsd").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0.0))

	exchange := &mockExchangeSvc{
		rates: []domain.ExchangeRate{{Oznaka: "USD", Srednji: 100.0}},
	}
	svc := NewTaxService(gormDB, exchange, nil, 0)
	start := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.June, 30, 23, 59, 59, 0, time.UTC)
	result, err := svc.UserTaxUnpaidForMonth(ctx, 1, start, end) // userID=1, no profit found
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// uncovered = 0 - 0 = 0 (clamped); result = 30 + 0 = 30
	if result != 30.0 {
		t.Errorf("expected 30.0 (only recorded debt), got %f", result)
	}
}

// ─── ListTaxEligibleUsers — error and empty paths ─────────────────────────────

func TestListTaxEligibleUsers_DBError_Clients(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// First query (list trading clients) fails
	mock.ExpectQuery("SELECT DISTINCT").WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, nil, nil, 0)
	_, err := svc.ListTaxEligibleUsers(ctx, TaxUserFilter{})
	if err == nil {
		t.Error("expected error when clients query fails")
	}
}

func TestListTaxEligibleUsers_DBError_Actuaries(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Clients query succeeds (empty)
	mock.ExpectQuery("SELECT DISTINCT").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
	// Actuaries query fails
	mock.ExpectQuery("SELECT employee_id FROM").WillReturnError(errors.New("db error"))

	svc := NewTaxService(gormDB, nil, nil, 0)
	_, err := svc.ListTaxEligibleUsers(ctx, TaxUserFilter{})
	if err == nil {
		t.Error("expected error when actuaries query fails")
	}
}

func TestListTaxEligibleUsers_EmptyResult(t *testing.T) {
	ctx := context.Background()
	gormDB, mock := newGormDB(t)

	// Clients — empty
	mock.ExpectQuery("SELECT DISTINCT").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
	// Actuaries — empty
	mock.ExpectQuery("SELECT employee_id FROM").
		WillReturnRows(sqlmock.NewRows([]string{"employee_id"}))
	// Debts — empty (error ignored in function)
	mock.ExpectQuery("COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "debt"}))

	// Exchange fails → ComputeProfitForPeriod returns error → profits block skipped
	exchange := &mockExchangeSvc{err: errors.New("no exchange")}

	svc := NewTaxService(gormDB, exchange, nil, 0)
	result, err := svc.ListTaxEligibleUsers(ctx, TaxUserFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}
