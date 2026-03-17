// Package config loads all configuration from environment variables.
// Clean Architecture: infrastructure layer — no business logic here.
package config

import (
	"fmt"
	"os"
)

// Config holds all runtime configuration for bank-service.
type Config struct {
	// HTTP
	HTTPAddr string // e.g. "0.0.0.0:8082"

	// gRPC
	GRPCAddr string // e.g. "0.0.0.0:50052"

	// PostgreSQL
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// JWT
	JWTAccessSecret string
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
		GRPCAddr: getEnv("GRPC_ADDR", "0.0.0.0:50052"),

		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),

		JWTAccessSecret: getEnv("JWT_ACCESS_SECRET", "change-me-access-secret"),
	}, nil
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
