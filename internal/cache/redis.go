package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore keeps cached values in Redis so they survive restarts and deploys.
//
// Redis is only ever a cache here: losing it must degrade to the in-process
// store, never take the site down. Sessions are deliberately NOT stored here —
// an LRU eviction policy could silently drop them and log users out at random.
type redisStore struct {
	client    *redis.Client
	prefix    string
	ttl       time.Duration
	opTimeout time.Duration
}

func newRedisStore(ctx context.Context, cfg Config) (*redisStore, error) {
	opts := &redis.Options{
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}

	if cfg.RedisURL != "" {
		parsed, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		// An explicit REDIS_PASSWORD wins over an embedded one.
		if cfg.RedisPassword != "" {
			parsed.Password = cfg.RedisPassword
		}
		opts = parsed
	}

	if cfg.DialTimeout > 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if cfg.OpTimeout > 0 {
		opts.ReadTimeout = cfg.OpTimeout
		opts.WriteTimeout = cfg.OpTimeout
	}

	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	s := &redisStore{
		client:    client,
		prefix:    cfg.KeyPrefix,
		ttl:       cfg.TTL,
		opTimeout: cfg.OpTimeout,
	}
	if s.ttl <= 0 {
		s.ttl = 10 * time.Minute
	}
	if s.opTimeout <= 0 {
		s.opTimeout = 2 * time.Second
	}
	return s, nil
}

func (r *redisStore) key(k string) string { return r.prefix + k }

func (r *redisStore) ttlOr(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return r.ttl
	}
	return ttl
}

func (r *redisStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, r.opTimeout)
}

func (r *redisStore) Get(ctx context.Context, key string, dest any) error {
	opCtx, cancel := r.ctx(ctx)
	defer cancel()

	raw, err := r.client.Get(opCtx, r.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return decode(raw, dest)
}

func (r *redisStore) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := encode(value)
	if err != nil {
		return err
	}

	opCtx, cancel := r.ctx(ctx)
	defer cancel()

	return r.client.Set(opCtx, r.key(key), raw, r.ttlOr(ttl)).Err()
}

func (r *redisStore) Delete(ctx context.Context, key string) error {
	opCtx, cancel := r.ctx(ctx)
	defer cancel()

	return r.client.Del(opCtx, r.key(key)).Err()
}

// Incr is atomic server-side, which is what makes it safe to use for rate
// limiting across concurrent requests.
func (r *redisStore) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	opCtx, cancel := r.ctx(ctx)
	defer cancel()

	full := r.key(key)

	// INCR creates the key with no expiry, so set one only when it was just
	// created (value == 1). This keeps the window fixed rather than sliding
	// on every request, which would let a client extend its own window.
	count, err := r.client.Incr(opCtx, full).Result()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		if err := r.client.Expire(opCtx, full, r.ttlOr(ttl)).Err(); err != nil {
			return count, err
		}
	}
	return count, nil
}

func (r *redisStore) Ping(ctx context.Context) error {
	opCtx, cancel := r.ctx(ctx)
	defer cancel()

	return r.client.Ping(opCtx).Err()
}

func (r *redisStore) Close() error { return r.client.Close() }
