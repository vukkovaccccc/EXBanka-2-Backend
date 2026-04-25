package config_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/config"
)

// setRequired sets the five mandatory DB env vars using t.Setenv (auto-cleanup).
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_USER", "banka")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_NAME", "banka_db")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0:8082", cfg.HTTPAddr)
	assert.Equal(t, "0.0.0.0:50052", cfg.GRPCAddr)
	assert.Equal(t, "user-service:50051", cfg.UserServiceAddr)
	assert.Equal(t, 24, cfg.WorkerIntervalHours)
	assert.Equal(t, 72, cfg.RetryAfterHours)
	assert.InDelta(t, 0.05, cfg.LatePaymentPenalty, 1e-9)
	assert.Equal(t, "https://v6.exchangerate-api.com/v6", cfg.ExchangeRateAPIBaseURL)
	assert.Equal(t, "notification-service:50053", cfg.NotificationServiceAddr)
}

func TestLoad_EnvOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("HTTP_ADDR", "0.0.0.0:9090")
	t.Setenv("GRPC_ADDR", "0.0.0.0:50099")
	t.Setenv("WORKER_INTERVAL_HOURS", "12")
	t.Setenv("RETRY_AFTER_HOURS", "48")
	t.Setenv("LATE_PAYMENT_PENALTY_PCT", "0.10")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0:9090", cfg.HTTPAddr)
	assert.Equal(t, "0.0.0.0:50099", cfg.GRPCAddr)
	assert.Equal(t, 12, cfg.WorkerIntervalHours)
	assert.Equal(t, 48, cfg.RetryAfterHours)
	assert.InDelta(t, 0.10, cfg.LatePaymentPenalty, 1e-9)
}

func TestLoad_MissingRequiredVars(t *testing.T) {
	required := []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME"}
	for _, key := range required {
		t.Run("missing_"+key, func(t *testing.T) {
			setRequired(t)
			t.Setenv(key, "") // clear this one
			_, err := config.Load()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestLoad_DBFieldsPopulated(t *testing.T) {
	setRequired(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "localhost", cfg.DBHost)
	assert.Equal(t, "5432", cfg.DBPort)
	assert.Equal(t, "banka", cfg.DBUser)
	assert.Equal(t, "secret", cfg.DBPassword)
	assert.Equal(t, "banka_db", cfg.DBName)
}

func TestConfig_DSN(t *testing.T) {
	setRequired(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	dsn := cfg.DSN()
	assert.Contains(t, dsn, "host=localhost")
	assert.Contains(t, dsn, "port=5432")
	assert.Contains(t, dsn, "user=banka")
	assert.Contains(t, dsn, "dbname=banka_db")
	assert.Contains(t, dsn, "sslmode=disable")

	// Verify the DSN format more completely.
	expected := fmt.Sprintf(
		"host=localhost port=5432 user=banka password=secret dbname=banka_db sslmode=disable TimeZone=UTC",
	)
	assert.Equal(t, expected, dsn)
}

func TestLoad_InvalidIntFallsBackToDefault(t *testing.T) {
	setRequired(t)
	t.Setenv("WORKER_INTERVAL_HOURS", "not-a-number")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 24, cfg.WorkerIntervalHours, "invalid int should use default")
}

func TestLoad_InvalidFloatFallsBackToDefault(t *testing.T) {
	setRequired(t)
	t.Setenv("LATE_PAYMENT_PENALTY_PCT", "not-a-float")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.InDelta(t, 0.05, cfg.LatePaymentPenalty, 1e-9, "invalid float should use default")
}

func TestLoad_ValidInt64EnvVar(t *testing.T) {
	setRequired(t)
	t.Setenv("STATE_REVENUE_ACCOUNT_ID", "12345")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.StateRevenueAccountID)
}

func TestLoad_InvalidInt64FallsBackToDefault(t *testing.T) {
	setRequired(t)
	t.Setenv("STATE_REVENUE_ACCOUNT_ID", "not-a-number")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.StateRevenueAccountID)
}

func TestLoad_ListingStrictExternalTrue(t *testing.T) {
	setRequired(t)
	t.Setenv("LISTING_STRICT_EXTERNAL", "true")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.ListingRequireLiveQuotes)
}

func TestLoad_ListingStrictExternalOne(t *testing.T) {
	setRequired(t)
	t.Setenv("LISTING_STRICT_EXTERNAL", "1")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.ListingRequireLiveQuotes)
}

func TestLoad_ListingRequireLiveQuotesFalse(t *testing.T) {
	setRequired(t)
	t.Setenv("LISTING_REQUIRE_LIVE_QUOTES", "false")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.ListingRequireLiveQuotes)
}

func TestLoad_ListingRequireLiveQuotesTrue(t *testing.T) {
	setRequired(t)
	t.Setenv("LISTING_REQUIRE_LIVE_QUOTES", "true")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.ListingRequireLiveQuotes)
}
