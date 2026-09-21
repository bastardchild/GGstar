// Package cache provides the TTL cache used by ggstar.
//
// The Store interface keeps the application independent of the backing
// implementation: the default is an in-process store, and Redis is opt-in via
// CACHE_DRIVER=redis. Swapping drivers must never require touching call sites.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Get when the key is absent or expired.
var ErrNotFound = errors.New("cache: key not found")

// Store is the minimal surface ggstar needs. Implementations must be safe for
// concurrent use.
type Store interface {
	// Get unmarshals the value stored at key into dest.
	Get(ctx context.Context, key string, dest any) error
	// Set stores value at key for the given ttl. A ttl <= 0 means "use default".
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	// Delete removes key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Incr increments an integer counter, creating it with the given ttl when
	// absent, and returns the new value.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	// Ping verifies the backend is reachable.
	Ping(ctx context.Context) error
	// Close releases resources.
	Close() error
}

// Encode/decode helpers shared by the drivers.

func encode(value any) ([]byte, error) {
	if raw, ok := value.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

func decode(raw []byte, dest any) error {
	if dest == nil {
		return nil
	}
	if buf, ok := dest.(*[]byte); ok {
		*buf = raw
		return nil
	}
	return json.Unmarshal(raw, dest)
}

// Driver names accepted by New.
const (
	DriverMemory = "memory"
	DriverRedis  = "redis"
)

// Config describes how to build a Store.
type Config struct {
	Driver string
	TTL    time.Duration

	// Redis settings, unused by the memory driver.
	RedisURL      string
	RedisPassword string
	RedisDB       int
	KeyPrefix     string
	DialTimeout   time.Duration
	OpTimeout     time.Duration
}

// New builds a Store for cfg.Driver.
//
// When the Redis driver fails to connect, New logs the failure and returns a
// working in-process store instead of an error: a cache outage must never take
// the site down.
func New(ctx context.Context, cfg Config) (Store, error) {
	switch cfg.Driver {
	case DriverRedis:
		store, err := newRedisStore(ctx, cfg)
		if err != nil {
			if fallback == nil {
				return nil, fmt.Errorf("cache: redis driver failed and no fallback configured: %w", err)
			}
			fallback(fmt.Errorf("cache: falling back to in-process store: %w", err))
			return newMemoryStore(cfg.TTL), nil
		}
		return store, nil

	case DriverMemory, "":
		return newMemoryStore(cfg.TTL), nil

	default:
		return nil, fmt.Errorf("cache: unknown driver %q", cfg.Driver)
	}
}

// fallback receives the reason Redis was abandoned. main sets it to a logger so
// the message lands in structured startup logs.
var fallback func(error)

// SetFallback registers the callback invoked when Redis cannot be reached.
func SetFallback(fn func(error)) { fallback = fn }
