package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

func memStore(t *testing.T, ttl time.Duration) *memoryStore {
	t.Helper()
	return newMemoryStore(ttl)
}

func TestMemorySetGetExpiry(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, 50*time.Millisecond)

	if err := c.Set(ctx, "torvalds", "cached", 0); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := c.Get(ctx, "torvalds", &got); err != nil {
		t.Fatalf("expected cache hit, got %v", err)
	}
	if got != "cached" {
		t.Errorf("got %q, want %q", got, "cached")
	}

	time.Sleep(70 * time.Millisecond)

	if err := c.Get(ctx, "torvalds", &got); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after TTL, got %v", err)
	}
}

func TestMemoryMissingKey(t *testing.T) {
	c := memStore(t, time.Minute)

	var v int
	if err := c.Get(context.Background(), "nope", &v); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemorySetTTLOverride(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, time.Hour)

	if err := c.Set(ctx, "stars", 1, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	var v int
	if err := c.Get(ctx, "stars", &v); err != nil {
		t.Fatal("expected hit before custom TTL elapsed")
	}

	time.Sleep(50 * time.Millisecond)
	if err := c.Get(ctx, "stars", &v); err != ErrNotFound {
		t.Error("expected expiry at custom TTL, not the default")
	}
}

func TestMemoryDeleteAndCleanup(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, 20*time.Millisecond)

	_ = c.Set(ctx, "a", 1, 0)
	_ = c.Set(ctx, "b", 2, 0)

	if err := c.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}

	var v int
	if err := c.Get(ctx, "a", &v); err != ErrNotFound {
		t.Error("expected deleted key to be gone")
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}

	time.Sleep(40 * time.Millisecond)
	c.Cleanup()
	if c.Len() != 0 {
		t.Errorf("Len after cleanup = %d, want 0", c.Len())
	}
}

func TestMemoryDeleteMissingIsNotAnError(t *testing.T) {
	if err := memStore(t, time.Minute).Delete(context.Background(), "ghost"); err != nil {
		t.Errorf("deleting a missing key should not error, got %v", err)
	}
}

func TestMemoryIncrCountsAndResetsAfterTTL(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, time.Minute)

	if got, _ := c.Incr(ctx, "ip", time.Minute); got != 1 {
		t.Errorf("first Incr = %d, want 1", got)
	}
	if got, _ := c.Incr(ctx, "ip", time.Minute); got != 2 {
		t.Errorf("second Incr = %d, want 2", got)
	}

	short := memStore(t, time.Minute)
	if got, _ := short.Incr(ctx, "k", 20*time.Millisecond); got != 1 {
		t.Fatal("expected first count")
	}
	time.Sleep(40 * time.Millisecond)
	if got, _ := short.Incr(ctx, "k", 20*time.Millisecond); got != 1 {
		t.Errorf("window should reset after TTL, got %d", got)
	}
}

// TestMemoryStoresStructs exercises the JSON round trip used for API snapshots.
func TestMemoryStoresStructs(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, time.Minute)

	type payload struct {
		Login  string   `json:"login"`
		Stars  int      `json:"stars"`
		Skills []string `json:"skills"`
	}

	in := payload{Login: "torvalds", Stars: 10, Skills: []string{"C", "Rust"}}
	if err := c.Set(ctx, "snap:torvalds", in, 0); err != nil {
		t.Fatal(err)
	}

	var out payload
	if err := c.Get(ctx, "snap:torvalds", &out); err != nil {
		t.Fatal(err)
	}
	if out.Login != in.Login || out.Stars != in.Stars || len(out.Skills) != 2 {
		t.Errorf("round trip mismatch: %+v", out)
	}
}

func TestMemoryConcurrentAccessIsRaceFree(t *testing.T) {
	ctx := context.Background()
	c := memStore(t, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune('a' + n%26))
			_ = c.Set(ctx, key, n, 0)
			var v int
			_ = c.Get(ctx, key, &v)
			_, _ = c.Incr(ctx, key, time.Minute)
			c.Len()
		}(i)
	}
	wg.Wait()
}

func TestNewSelectsDriver(t *testing.T) {
	ctx := context.Background()

	mem, err := New(ctx, Config{Driver: DriverMemory})
	if err != nil {
		t.Fatalf("memory driver: %v", err)
	}
	defer mem.Close()
	if _, ok := mem.(*memoryStore); !ok {
		t.Errorf("DriverMemory returned %T", mem)
	}

	def, err := New(ctx, Config{})
	if err != nil {
		t.Fatalf("empty driver should default to memory: %v", err)
	}
	defer def.Close()
	if _, ok := def.(*memoryStore); !ok {
		t.Errorf("empty driver returned %T", def)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(context.Background(), Config{Driver: "memcached"}); err == nil {
		t.Error("expected an error for an unknown driver")
	}
}

// TestNewFallsBackWhenRedisUnreachable is the important guarantee: a dead cache
// backend must not stop the application from starting.
func TestNewFallsBackWhenRedisUnreachable(t *testing.T) {
	var reported error
	SetFallback(func(err error) { reported = err })
	t.Cleanup(func() { SetFallback(nil) })

	store, err := New(context.Background(), Config{
		Driver:     DriverRedis,
		RedisURL:   "redis://127.0.0.1:1", // unroutable port
		DialTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}
	defer store.Close()

	if _, ok := store.(*memoryStore); !ok {
		t.Errorf("expected memoryStore fallback, got %T", store)
	}
	if reported == nil {
		t.Error("fallback reason should be reported so it lands in the logs")
	}
}
