package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
)

type resultCacheStore struct{ rdb *redis.Client }

func NewResultCacheStore(rdb *redis.Client) service.ResultCacheStore {
	// Use the same Redis instance with an independently owned pool. The shared
	// client's context timeout setting cannot be changed without affecting other
	// repositories, and WithTimeout does not enable context-aware socket I/O.
	options := *rdb.Options()
	options.PushNotificationProcessor = nil // Do not reuse the shared client's push handlers.
	options.ContextTimeoutEnabled = true
	options.MaxRetries = -1 // Cache failures must not multiply the caller's budget.
	return &resultCacheStore{rdb: redis.NewClient(&options)}
}

// Close only the result cache's pool, never the application's shared client.
func (s *resultCacheStore) Close() error { return s.rdb.Close() }

func resultCacheLockKey(key string) string {
	return strings.Replace(key, "result-cache:v1:", "result-cache-lock:v1:", 1)
}
func (s *resultCacheStore) Get(ctx context.Context, key string) (*service.ResultCacheEntry, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e service.ResultCacheEntry
	if len(raw) > (2<<20)+16384 || json.Unmarshal(raw, &e) != nil || e.Version != 1 || !json.Valid(e.Body) || e.CreatedAt.IsZero() || !e.ExpiresAt.After(time.Now()) || e.CreatedAt.After(time.Now().Add(time.Second)) {
		_ = s.rdb.Eval(ctx, `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`, []string{key}, raw).Err()
		return nil, nil
	}
	return &e, nil
}
func (s *resultCacheStore) Acquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, resultCacheLockKey(key), token, ttl).Result()
}
func (s *resultCacheStore) Renew(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	n, e := s.rdb.Eval(ctx, `if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE',KEYS[1],ARGV[2]) end return 0`, []string{resultCacheLockKey(key)}, token, ttl.Milliseconds()).Int()
	return n == 1, e
}
func (s *resultCacheStore) Publish(ctx context.Context, key, token string, e *service.ResultCacheEntry) (bool, error) {
	if e == nil || len(e.Body) > 2<<20 || !json.Valid(e.Body) {
		return false, fmt.Errorf("invalid result cache entry")
	}
	ttl := time.Until(e.ExpiresAt)
	if ttl <= 0 {
		return false, nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	n, err := s.rdb.Eval(ctx, `if redis.call('GET', KEYS[2]) ~= ARGV[1] then return 0 end redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[3]); redis.call('DEL',KEYS[2]); return 1`, []string{key, resultCacheLockKey(key)}, token, ttl.Milliseconds(), raw).Int()
	return n == 1, err
}
func (s *resultCacheStore) Release(ctx context.Context, key, token string) error {
	return s.rdb.Eval(ctx, `if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{resultCacheLockKey(key)}, token).Err()
}
