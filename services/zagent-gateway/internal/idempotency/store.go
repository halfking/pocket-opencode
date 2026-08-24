// Package idempotency provides a deterministic, body-binding idempotency
// store for write operations crossing the ZAG boundary.
//
// Hard rules (enforced by store_test.go):
//
//   - The same Idempotency-Key with a different body hash MUST be
//     rejected with ErrBodyMismatch. This blocks the "replay a key
//     against a different mutation" attack that would otherwise turn
//     an idempotency token into a back-door for arbitrary writes.
//   - The same Idempotency-Key with the same body MUST return the
//     previously recorded response without re-executing the handler.
//   - Entries expire after TTL. An expired key is treated as a fresh
//     request — the contract is that clients must regenerate keys when
//     they re-issue.
//   - The store is keyed by (tenant_id, actor_id, key) so two
//     different callers cannot collide on the same key value.
package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrBodyMismatch is returned when a caller reuses an Idempotency-Key
// with a different body hash. The caller MUST NOT proceed; per
// ADR-0001 §6 this is a hard failure surfaced to the user.
var ErrBodyMismatch = errors.New("idempotency: body hash does not match stored request")

// ErrInFlight is returned when the original request is still being
// processed. The client SHOULD retry after the in-flight deadline.
var ErrInFlight = errors.New("idempotency: original request still in flight")

// ErrStoreUnavailable is the fail-closed error when the backing store
// cannot accept reads or writes. Write requests MUST NOT proceed
// without idempotency coverage; the gateway must surface a 503.
var ErrStoreUnavailable = errors.New("idempotency: store unavailable")

// Status reports the lifecycle of a stored entry.
type Status string

const (
	StatusInFlight Status = "in_flight"
	StatusComplete Status = "complete"
)

// Entry is the durable record kept by the store.
type Entry struct {
	Key        string
	TenantID   string
	ActorID    string
	BodyHash   string
	Status     Status
	Response   []byte // raw response bytes; opaque to the store
	StatusCode int
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// HashBody computes the canonical SHA-256 of a request body. Callers
// MUST pass the raw bytes as received over the wire — the gateway
// re-hashes on the server side and the two values are compared.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Store is the contract the rest of the gateway depends on. Production
// deploys wire this to Postgres; the in-memory implementation lives
// alongside the interface and MUST NOT be used in production.
type Store interface {
	// Reserve attempts to claim the key for a new request. If the key
	// already exists:
	//   - same body hash, complete -> returns the recorded response
	//   - same body hash, in flight -> returns ErrInFlight
	//   - different body hash -> returns ErrBodyMismatch
	// On success, the returned *Entry is in the InFlight state; the
	// caller MUST call Complete when the handler finishes.
	Reserve(key, tenantID, actorID, bodyHash string, ttl time.Duration) (*Entry, error)
	// Complete transitions an in-flight entry to complete, attaching
	// the recorded response. Calling Complete on a key not in flight
	// is an error.
	Complete(key, tenantID, actorID, bodyHash string, statusCode int, response []byte) error
	// Lookup returns the entry for a key without mutating state.
	Lookup(key, tenantID, actorID string) (*Entry, error)
}

// MemoryStore is the in-memory Store implementation used by tests and
// the local CLI. It is NOT durable.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]Entry
	now     func() time.Time
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]Entry),
		now:     time.Now,
	}
}

// scopeKey composes the composite key under which an entry is stored.
// Two callers may legitimately use the same Idempotency-Key value as
// long as their (tenant, actor) tuple differs.
func scopeKey(tenantID, actorID, key string) string {
	return tenantID + "\x00" + actorID + "\x00" + key
}

func (s *MemoryStore) Reserve(key, tenantID, actorID, bodyHash string, ttl time.Duration) (*Entry, error) {
	if key == "" || tenantID == "" || actorID == "" {
		return nil, errors.New("idempotency: key, tenant and actor are required")
	}
	if ttl <= 0 {
		return nil, errors.New("idempotency: ttl must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := scopeKey(tenantID, actorID, key)
	if existing, ok := s.entries[sk]; ok {
		if s.now().After(existing.ExpiresAt) {
			delete(s.entries, sk)
		} else {
			if existing.BodyHash != bodyHash {
				return &existing, ErrBodyMismatch
			}
			switch existing.Status {
			case StatusComplete:
				return &existing, nil
			case StatusInFlight:
				return &existing, ErrInFlight
			}
		}
	}
	now := s.now()
	entry := Entry{
		Key:        key,
		TenantID:   tenantID,
		ActorID:    actorID,
		BodyHash:   bodyHash,
		Status:     StatusInFlight,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		StatusCode: 0,
	}
	s.entries[sk] = entry
	return &entry, nil
}

func (s *MemoryStore) Complete(key, tenantID, actorID, bodyHash string, statusCode int, response []byte) error {
	if key == "" {
		return errors.New("idempotency: key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := scopeKey(tenantID, actorID, key)
	entry, ok := s.entries[sk]
	if !ok {
		return errors.New("idempotency: no in-flight entry to complete")
	}
	if entry.BodyHash != bodyHash {
		return ErrBodyMismatch
	}
	entry.Status = StatusComplete
	entry.StatusCode = statusCode
	entry.Response = append([]byte(nil), response...)
	s.entries[sk] = entry
	return nil
}

func (s *MemoryStore) Lookup(key, tenantID, actorID string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := scopeKey(tenantID, actorID, key)
	entry, ok := s.entries[sk]
	if !ok {
		return nil, errors.New("idempotency: not found")
	}
	if s.now().After(entry.ExpiresAt) {
		delete(s.entries, sk)
		return nil, errors.New("idempotency: expired")
	}
	return &entry, nil
}

// Snapshot returns a sorted copy of the entry set. Used by tests and
// for diagnostic dumps.
func (s *MemoryStore) Snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		if out[i].ActorID != out[j].ActorID {
			return out[i].ActorID < out[j].ActorID
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Sweep removes expired entries. It is safe to call concurrently and
// is intended to be invoked periodically by the host process. It
// returns the number of entries removed.
func (s *MemoryStore) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	removed := 0
	for k, e := range s.entries {
		if now.After(e.ExpiresAt) {
			delete(s.entries, k)
			removed++
		}
	}
	return removed
}

// FailingStore is a Store that always returns ErrStoreUnavailable. It
// exists so the gateway can be configured to fail closed in tests.
type FailingStore struct{}

// Reserve always fails closed.
func (FailingStore) Reserve(string, string, string, string, time.Duration) (*Entry, error) {
	return nil, ErrStoreUnavailable
}

// Complete always fails closed.
func (FailingStore) Complete(string, string, string, string, int, []byte) error {
	return ErrStoreUnavailable
}

// Lookup always fails closed.
func (FailingStore) Lookup(string, string, string) (*Entry, error) {
	return nil, ErrStoreUnavailable
}

// Response captures the cached response that will be replayed on
// duplicate Idempotency-Key submissions. It is intentionally
// transport-agnostic so the same store can back JSON over HTTP and
// gRPC over HTTP/2.
type Response struct {
	StatusCode int
	Body       []byte
	Headers    map[string]string
}
