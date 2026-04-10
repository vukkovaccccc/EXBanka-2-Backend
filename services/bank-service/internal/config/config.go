// Package config loads all configuration from environment variables.
// Clean Architecture: infrastructure layer — no business logic here.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for bank-service.
type Config struct {
	// HTTP
	HTTPAddr string // e.g. "0.0.0.0:8082"

	// gRPC
	GRPCAddr        string // e.g. "0.0.0.0:50052"
	UserServiceAddr string // e.g. "user-service:50051"

	// PostgreSQL
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// JWT
	JWTAccessSecret string

	// RabbitMQ — opcionalno; ako je prazno, notifikacije se samo loguju.
	RabbitMQURL string // e.g. "amqp://guest:guest@localhost:5672/"

	// Cron job — InstallmentWorker parametri.
	WorkerIntervalHours int     // koliko često se worker pokreće (default 24)
	RetryAfterHours     int     // zakašnjenje pre ponovnog pokušaja (default 72)
	LatePaymentPenalty  float64 // kazneni % koji se dodaje nominalnoj stopi (default 0.05)
	// ExchangeRate-API (https://www.exchangerate-api.com)
	ExchangeRateAPIKey     string // required for live rates; falls back to local rates if empty
	ExchangeRateAPIBaseURL string // default: https://v6.exchangerate-api.com/v6

	// Exchange rate parameters (0–1 range; 0.005 = 0.5%)
	ExchangeSpreadRate    float64 // env: EXCHANGE_SPREAD_RATE    — spread primenjen na srednji kurs
	ExchangeProvizijaRate float64 // env: EXCHANGE_PROVIZIJA_RATE — stopa provizije po konverziji

	// PCI-DSS: tajni ključ za HMAC-SHA256 hashiranje CVV kodova kartica.
	// Mora biti postavljen u produkciji — bez ovog ključa CVV je brute-forceable.
	CVVPepper string // env: CVV_PEPPER

	// Redis — koristi se za keširanje OTP state-a u Flow 2 (zahtev za karticu).
	// Format: "redis://localhost:6379" ili "redis://:password@host:6379/0"
	// Ako je prazno, RequestKartica endpoint vraća 500 (Feature Flag za dev okruženje).
	RedisURL string // env: REDIS_URL

	// NotificationServiceAddr je adresa notification-service gRPC servera.
	// Koristi se za sinhronizovano slanje OTP emaila u Flow 2.
	// Ako je prazno, RequestKartica endpoint vraća 500.
	NotificationServiceAddr string // env: NOTIFICATION_SERVICE_ADDR

	// EODHD — primarni API ključ za tržišne podatke (https://eodhd.com).
	// Real-time quotes za stocks, forex, futures; EOD istorija za stocks.
	// Ako je prazno, worker prelazi na Finnhub/AV fallback.
	EODHDAPIKey string // env: EODHD_API_KEY

	// Finnhub — sekundarni API ključ za tržišne podatke (https://finnhub.io).
	// Ako je prazno, ListingRefresherWorker koristi mock vrednosti.
	FinnhubAPIKey string // env: FINNHUB_API_KEY

	// AlphaVantage — API ključ za Company Overview (STOCK) i Forex kurseve.
	// Ako je prazno, preskače se AV logika i koriste se Finnhub/mock vrednosti.
	AlphaVantageAPIKey string // env: ALPHAVANTAGE_API_KEY

	// ListingRefreshIntervalMinutes — koliko često worker osvežava cene hartija.
	// Default: 15 minuta. Za testiranje postaviti na 1.
	ListingRefreshIntervalMinutes int // env: LISTING_REFRESH_INTERVAL_MINUTES

	// ListingRequireLiveQuotes — ako je true (podrazumevano), worker ne upisuje
	// sintetičke/mock cene; pri neuspehu API-ja preskače se ažuriranje listinga.
	// Postaviti LISTING_REQUIRE_LIVE_QUOTES=false samo za lokalni dev bez API ključeva.
	ListingRequireLiveQuotes bool // env: LISTING_REQUIRE_LIVE_QUOTES (default true)

	// StateRevenueAccountID — core_banking.racun.id tekućeg RSD računa „države” za simulaciju
	// prijema poreza (uplata istim iznosom u RSD kao što je obračunat). 0 = isključeno.
	StateRevenueAccountID int64 // env: STATE_REVENUE_ACCOUNT_ID
}

// Load reads ENV vars and returns a populated Config.
// Required vars: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME.
// Optional vars fall back to sensible defaults.
func Load() (*Config, error) {
	required := []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME"}
	for _, key := range required {
		if os.Getenv(key) == "" {
			return nil, fmt.Errorf("missing required env var: %s", key)
		}
	}

	return &Config{
		HTTPAddr: getEnv("HTTP_ADDR", "0.0.0.0:8082"),
		GRPCAddr:        getEnv("GRPC_ADDR", "0.0.0.0:50052"),
		UserServiceAddr: getEnv("USER_SERVICE_ADDR", "user-service:50051"),

		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),

		JWTAccessSecret: getEnv("JWT_ACCESS_SECRET", "change-me-access-secret"),

		RabbitMQURL: os.Getenv("RABBITMQ_URL"), // prazno = NoOp publisher

		WorkerIntervalHours: getEnvInt("WORKER_INTERVAL_HOURS", 24),
		RetryAfterHours:     getEnvInt("RETRY_AFTER_HOURS", 72),
		LatePaymentPenalty:  getEnvFloat("LATE_PAYMENT_PENALTY_PCT", 0.05),
		ExchangeRateAPIKey:     os.Getenv("EXCHANGE_RATE_API_KEY"),
		ExchangeRateAPIBaseURL: getEnv("EXCHANGE_RATE_API_BASE_URL", "https://v6.exchangerate-api.com/v6"),
		ExchangeSpreadRate:    getEnvFloat("EXCHANGE_SPREAD_RATE", 0.005),
		ExchangeProvizijaRate: getEnvFloat("EXCHANGE_PROVIZIJA_RATE", 0.005),

		CVVPepper: getEnv("CVV_PEPPER", "change-me-cvv-pepper-in-production"),

		RedisURL:                os.Getenv("REDIS_URL"),
		NotificationServiceAddr: getEnv("NOTIFICATION_SERVICE_ADDR", "notification-service:50053"),

		EODHDAPIKey:                   os.Getenv("EODHD_API_KEY"),
		FinnhubAPIKey:                 os.Getenv("FINNHUB_API_KEY"),
		AlphaVantageAPIKey:            os.Getenv("ALPHAVANTAGE_API_KEY"),
		ListingRefreshIntervalMinutes: getEnvInt("LISTING_REFRESH_INTERVAL_MINUTES", 15),
		ListingRequireLiveQuotes: loadListingRequireLiveQuotes(),
		StateRevenueAccountID:    getEnvInt64("STATE_REVENUE_ACCOUNT_ID", 0),
	}, nil
}

// loadListingRequireLiveQuotes: default true — bez lažnih tržišnih cena u produkciji.
// LISTING_REQUIRE_LIVE_QUOTES=false eksplicitno dozvoljava sintetiku ako je i ALLOW_SYNTHETIC.
// LISTING_STRICT_EXTERNAL=true (zastarelo) tretira se kao require live.
func loadListingRequireLiveQuotes() bool {
	if os.Getenv("LISTING_STRICT_EXTERNAL") == "true" || os.Getenv("LISTING_STRICT_EXTERNAL") == "1" {
		return true
	}
	v := os.Getenv("LISTING_REQUIRE_LIVE_QUOTES")
	if v == "" {
		return true
	}
	return v == "true" || v == "1"
}

// DSN returns the PostgreSQL connection string accepted by GORM's postgres driver.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
