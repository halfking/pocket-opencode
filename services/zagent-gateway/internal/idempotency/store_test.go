package idempotency

import (
	"errors"
	"testing"
	"time"
)

// helpers -------------------------------------------------------------------

func fastStore() *MemoryStore {
	s := NewMemoryStore()
	// Set a deterministic clock so expiry tests are reproducible.
	s.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return s
}

// tests ---------------------------------------------------------------------

func TestReserveFreshReturnsInFlight(t *testing.T) {
	s := fastStore()
	entry, err := s.Reserve("k1", "tenant-1", "actor-1", HashBody([]byte("hello")), time.Hour)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if entry.Status != StatusInFlight {
		t.Fatalf("expected in_flight, got %q", entry.Status)
	}
}

func TestCompleteThenReserveReplaysResponse(t *testing.T) {
	s := fastStore()
	body := HashBody([]byte("hello"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.Complete("k1", "tenant-1", "actor-1", body, 200, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	entry, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour)
	if err != nil {
		t.Fatalf("re-reserve: %v", err)
	}
	if entry.Status != StatusComplete {
		t.Fatalf("expected complete, got %q", entry.Status)
	}
	if string(entry.Response) != `{"ok":true}` {
		t.Fatalf("response not replayed: %q", string(entry.Response))
	}
}

func TestSameKeyDifferentBodyRejected(t *testing.T) {
	s := fastStore()
	body1 := HashBody([]byte("hello"))
	body2 := HashBody([]byte("world"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body1, time.Hour); err != nil {
		t.Fatalf("reserve1: %v", err)
	}
	if err := s.Complete("k1", "tenant-1", "actor-1", body1, 200, []byte("{}")); err != nil {
		t.Fatalf("complete: %v", err)
	}
	_, err := s.Reserve("k1", "tenant-1", "actor-1", body2, time.Hour)
	if !errors.Is(err, ErrBodyMismatch) {
		t.Fatalf("expected ErrBodyMismatch, got %v", err)
	}
}

func TestInFlightReportedAsInFlight(t *testing.T) {
	s := fastStore()
	body := HashBody([]byte("hello"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	_, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour)
	if !errors.Is(err, ErrInFlight) {
		t.Fatalf("expected ErrInFlight, got %v", err)
	}
}

func TestTenantScopePreventsCollision(t *testing.T) {
	s := fastStore()
	body := HashBody([]byte("hello"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve1: %v", err)
	}
	entry, err := s.Reserve("k1", "tenant-2", "actor-1", body, time.Hour)
	if err != nil {
		t.Fatalf("different tenant must not collide: %v", err)
	}
	if entry.TenantID != "tenant-2" {
		t.Fatalf("scope leaked: %q", entry.TenantID)
	}
}

func TestExpiredEntryTreatedAsFresh(t *testing.T) {
	s := fastStore()
	body := HashBody([]byte("hello"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// Advance the clock past the expiry window.
	s.now = func() time.Time { return time.Unix(1_700_000_000+2*3600, 0) }
	entry, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour)
	if err != nil {
		t.Fatalf("re-reserve after expiry: %v", err)
	}
	if entry.Status != StatusInFlight {
		t.Fatalf("expected fresh in_flight, got %q", entry.Status)
	}
}

func TestSweepRemovesExpired(t *testing.T) {
	s := fastStore()
	body := HashBody([]byte("hello"))
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := s.Reserve("k2", "tenant-1", "actor-1", body, time.Hour); err != nil {
		t.Fatalf("reserve2: %v", err)
	}
	s.now = func() time.Time { return time.Unix(1_700_000_000+2*3600, 0) }
	if removed := s.Sweep(); removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	if len(s.Snapshot()) != 0 {
		t.Fatalf("expected empty store after sweep")
	}
}

func TestFailingStoreFailsClosed(t *testing.T) {
	var s Store = FailingStore{}
	if _, err := s.Reserve("k1", "tenant-1", "actor-1", "h", time.Hour); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("expected ErrStoreUnavailable, got %v", err)
	}
	if err := s.Complete("k1", "tenant-1", "actor-1", "h", 200, nil); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("expected ErrStoreUnavailable, got %v", err)
	}
}

func TestRejectsEmptyFields(t *testing.T) {
	s := fastStore()
	if _, err := s.Reserve("", "tenant", "actor", "h", time.Hour); err == nil {
		t.Fatalf("expected error for empty key")
	}
	if _, err := s.Reserve("k", "", "actor", "h", time.Hour); err == nil {
		t.Fatalf("expected error for empty tenant")
	}
	if _, err := s.Reserve("k", "tenant", "", "h", time.Hour); err == nil {
		t.Fatalf("expected error for empty actor")
	}
}

func TestRejectsNonPositiveTTL(t *testing.T) {
	s := fastStore()
	if _, err := s.Reserve("k", "t", "a", "h", 0); err == nil {
		t.Fatalf("expected error for zero ttl")
	}
}

func TestCompleteWithoutReserveFails(t *testing.T) {
	s := fastStore()
	if err := s.Complete("missing", "tenant-1", "actor-1", "h", 200, nil); err == nil {
		t.Fatalf("expected error completing unknown key")
	}
}

func TestHashBodyDeterministic(t *testing.T) {
	if HashBody([]byte("a")) == HashBody([]byte("b")) {
		t.Fatalf("hash collision")
	}
	if HashBody([]byte("a")) != HashBody([]byte("a")) {
		t.Fatalf("hash non-deterministic")
	}
}
