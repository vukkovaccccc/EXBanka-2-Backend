package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/redis/go-redis/v9"
)

const cardRequestKeyPrefix = "card_req_"

// RedisCardRequestStore implementira domain.CardRequestStore koristeći Redis.
// Ključ format: card_req_{ownerID}
// TTL: 5 minuta (enforced pozivačem — servis prosleđuje ttl parametar).
type RedisCardRequestStore struct {
	client *redis.Client
}

// NewRedisCardRequestStore parsira Redis URL i vraća gotov store.
// Podržani formati: "redis://localhost:6379", "redis://:password@host:6379/0"
func NewRedisCardRequestStore(redisURL string) (*RedisCardRequestStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse URL: %w", err)
	}
	client := redis.NewClient(opts)

	// Ping pri startu da bi main.go odmah znao da li je Redis dostupan.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	log.Printf("[redis] konekcija uspostavljena na %s", redisURL)
	return &RedisCardRequestStore{client: client}, nil
}

// SaveCardRequest serijalizuje state u JSON i upisuje ga u Redis.
// Koristi SET sa EX opcijom — automatski overwrite-uje postojeći ključ (Edge Case 5).
func (s *RedisCardRequestStore) SaveCardRequest(
	ctx context.Context,
	ownerID int64,
	state domain.CardRequestState,
	ttl time.Duration,
) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal card request state: %w", err)
	}
	key := fmt.Sprintf("%s%d", cardRequestKeyPrefix, ownerID)
	return s.client.Set(ctx, key, data, ttl).Err()
}

// GetCardRequest čita state iz Redisa i deserijalizuje ga.
// Vraća domain.ErrCardRequestNotFound ako ključ ne postoji (TTL istekao ili nikad kreiran).
func (s *RedisCardRequestStore) GetCardRequest(ctx context.Context, ownerID int64) (*domain.CardRequestState, error) {
	key := fmt.Sprintf("%s%d", cardRequestKeyPrefix, ownerID)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, domain.ErrCardRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get card request: %w", err)
	}
	var state domain.CardRequestState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal card request state: %w", err)
	}
	return &state, nil
}

// DeleteCardRequest briše ključ iz Redisa (rollback — Edge Case 6, ili po uspešnom kreiranju).
func (s *RedisCardRequestStore) DeleteCardRequest(ctx context.Context, ownerID int64) error {
	key := fmt.Sprintf("%s%d", cardRequestKeyPrefix, ownerID)
	return s.client.Del(ctx, key).Err()
}

// ─── NoOp implementacija ──────────────────────────────────────────────────────

// NoOpCardRequestStore se koristi kada REDIS_URL nije konfigurisan.
// SaveCardRequest vraća grešku — RequestKartica će failovati sa jasnom porukom.
type NoOpCardRequestStore struct{}

func (s *NoOpCardRequestStore) SaveCardRequest(_ context.Context, ownerID int64, _ domain.CardRequestState, _ time.Duration) error {
	log.Printf("[redis] NoOp — CardRequest za owner_id=%d nije sačuvan (Redis nije konfigurisan)", ownerID)
	return fmt.Errorf("redis nije konfigurisan — postavi REDIS_URL env varijablu")
}

func (s *NoOpCardRequestStore) GetCardRequest(_ context.Context, _ int64) (*domain.CardRequestState, error) {
	return nil, fmt.Errorf("redis nije konfigurisan — postavi REDIS_URL env varijablu")
}

func (s *NoOpCardRequestStore) DeleteCardRequest(_ context.Context, _ int64) error {
	return nil
}
