package transport

import (
	"context"
	"fmt"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/redis/go-redis/v9"
)

const marketTestModeKey = "market:test_mode"

// RedisMarketModeStore implementira domain.MarketModeStore koristeći Redis.
// Ključ: market:test_mode — vrednost: "true" | "false" (bez TTL).
type RedisMarketModeStore struct {
	client *redis.Client
}

// NewRedisMarketModeStore parsira Redis URL i vraća gotov store.
// Podržani formati: "redis://localhost:6379", "redis://:password@host:6379/0"
func NewRedisMarketModeStore(redisURL string) (*RedisMarketModeStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse URL: %w", err)
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	log.Printf("[redis] market mode store konekcija uspostavljena na %s", redisURL)
	return &RedisMarketModeStore{client: client}, nil
}

// SetTestMode upisuje zastavicu u Redis (bez TTL — ostaje dok se ne promeni).
func (s *RedisMarketModeStore) SetTestMode(ctx context.Context, enabled bool) error {
	val := "false"
	if enabled {
		val = "true"
	}
	return s.client.Set(ctx, marketTestModeKey, val, 0).Err()
}

// IsTestMode čita zastavicu iz Redisa.
// Ako ključ ne postoji, vraća false (default: radno vreme se poštuje).
func (s *RedisMarketModeStore) IsTestMode(ctx context.Context) (bool, error) {
	val, err := s.client.Get(ctx, marketTestModeKey).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis get %s: %w", marketTestModeKey, err)
	}
	return val == "true", nil
}

// ─── NoOp implementacija ──────────────────────────────────────────────────────

// NoOpMarketModeStore se koristi kada REDIS_URL nije konfigurisan.
// IsTestMode uvek vraća false — radno vreme se uvek poštuje.
// SetTestMode vraća grešku sa jasnom porukom.
type NoOpMarketModeStore struct{}

var _ domain.MarketModeStore = (*NoOpMarketModeStore)(nil)

func (s *NoOpMarketModeStore) SetTestMode(_ context.Context, _ bool) error {
	return fmt.Errorf("redis nije konfigurisan — postavi REDIS_URL env varijablu")
}

func (s *NoOpMarketModeStore) IsTestMode(_ context.Context) (bool, error) {
	log.Printf("[redis] NoOp — market:test_mode nije dostupan (Redis nije konfigurisan); berza se tretira kao zatvorena")
	return false, nil
}
