package sessionstore

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()

	s, err := New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSetGetRoundTrip(t *testing.T) {
	s := newStore(t)

	if err := s.Set("abc", []byte("payload"), time.Minute); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("abc")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Errorf("got %q, want payload", got)
	}
}

// TestGetMissingReturnsNilNotError matters because Fiber treats a nil slice as a
// cache miss; returning an error would surface as a request failure instead.
func TestGetMissingReturnsNilNotError(t *testing.T) {
	s := newStore(t)

	got, err := s.Get("does-not-exist")
	if err != nil {
		t.Fatalf("missing key must not error, got %v", err)
	}
	if got != nil {
		t.Errorf("missing key should return nil, got %q", got)
	}
}

func TestExpiry(t *testing.T) {
	s := newStore(t)

	if err := s.Set("temp", []byte("v"), 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Get("temp"); got == nil {
		t.Fatal("expected value before expiry")
	}

	time.Sleep(60 * time.Millisecond)

	got, err := s.Get("temp")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected expiry to hide the value")
	}
}

func TestUpdateOverwrites(t *testing.T) {
	s := newStore(t)

	_ = s.Set("k", []byte("first"), time.Minute)
	_ = s.Set("k", []byte("second"), time.Minute)

	got, err := s.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("expected the newer value, got %q", got)
	}

	n, err := s.Len()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("update must not create a second row, Len = %d", n)
	}
}

func TestDeleteAndReset(t *testing.T) {
	s := newStore(t)

	_ = s.Set("a", []byte("1"), time.Minute)
	_ = s.Set("b", []byte("2"), time.Minute)

	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got != nil {
		t.Error("deleted session should be gone")
	}
	if got, _ := s.Get("b"); got == nil {
		t.Error("unrelated session must survive a delete")
	}

	// Deleting a missing key is explicitly allowed by fiber.Storage.
	if err := s.Delete("ghost"); err != nil {
		t.Errorf("deleting a missing key must not error, got %v", err)
	}

	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Len(); n != 0 {
		t.Errorf("Reset should clear everything, Len = %d", n)
	}
}

func TestSetIgnoresEmptyInput(t *testing.T) {
	s := newStore(t)

	if err := s.Set("", []byte("v"), time.Minute); err != nil {
		t.Errorf("empty key must be ignored without error, got %v", err)
	}
	if err := s.Set("k", nil, time.Minute); err != nil {
		t.Errorf("empty value must be ignored without error, got %v", err)
	}
	if n, _ := s.Len(); n != 0 {
		t.Errorf("nothing should have been stored, Len = %d", n)
	}
}

// TestZeroExpirationMeansNoExpiry mirrors fiber.Storage's documented behaviour.
func TestZeroExpirationMeansNoExpiry(t *testing.T) {
	s := newStore(t)

	if err := s.Set("forever", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("forever")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("zero expiration should not expire immediately")
	}
}

func TestNewFromDBReusesPool(t *testing.T) {
	s := newStore(t)

	second, err := NewFromDB(s.db)
	if err != nil {
		t.Fatalf("NewFromDB: %v", err)
	}
	defer second.Close()

	// NewFromDB does not own the pool, so closing it must leave the original usable.
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Set("after", []byte("v"), time.Minute); err != nil {
		t.Errorf("original store should still work: %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close must not panic or error, got %v", err)
	}
}
