package cache

import (
	"context"
	"sync"
	"time"
)

type memItem struct {
	value     []byte
	expiresAt time.Time
}

// memoryStore is the default in-process TTL cache. It shields the GitHub API
// from rate limits for a single instance and is the fallback when Redis is
// unavailable.
type memoryStore struct {
	mu    sync.RWMutex
	items map[string]memItem
	ttl   time.Duration
	now   func() time.Time
}

func newMemoryStore(ttl time.Duration) *memoryStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &memoryStore{
		items: make(map[string]memItem),
		ttl:   ttl,
		now:   time.Now,
	}
}

func (m *memoryStore) ttlOr(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return m.ttl
	}
	return ttl
}

func (m *memoryStore) Get(ctx context.Context, key string, dest any) error {
	m.mu.RLock()
	it, ok := m.items[key]
	m.mu.RUnlock()

	if !ok {
		return ErrNotFound
	}
	if m.now().After(it.expiresAt) {
		m.mu.Lock()
		delete(m.items, key)
		m.mu.Unlock()
		return ErrNotFound
	}
	return decode(it.value, dest)
}

func (m *memoryStore) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := encode(value)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.items[key] = memItem{value: raw, expiresAt: m.now().Add(m.ttlOr(ttl))}
	m.mu.Unlock()
	return nil
}

func (m *memoryStore) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
	return nil
}

func (m *memoryStore) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	window := m.ttlOr(ttl)

	current, ok := m.items[key]
	if !ok || now.After(current.expiresAt) {
		next, err := encode(int64(1))
		if err != nil {
			return 0, err
		}
		m.items[key] = memItem{value: next, expiresAt: now.Add(window)}
		return 1, nil
	}

	var n int64
	if err := decode(current.value, &n); err != nil {
		n = 0
	}
	n++

	raw, err := encode(n)
	if err != nil {
		return 0, err
	}
	m.items[key] = memItem{value: raw, expiresAt: current.expiresAt}
	return n, nil
}

func (m *memoryStore) Ping(ctx context.Context) error { return nil }

func (m *memoryStore) Close() error { return nil }

// Cleanup drops expired entries; call periodically to bound memory.
func (m *memoryStore) Cleanup() {
	now := m.now()

	m.mu.Lock()
	for k, it := range m.items {
		if now.After(it.expiresAt) {
			delete(m.items, k)
		}
	}
	m.mu.Unlock()
}

// Len reports the number of tracked keys, expired ones included.
func (m *memoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}
