package audit

import (
	"context"
	"sync"
)

// MemorySink is an in-memory audit sink for tests and local
// development. It is NOT durable and MUST NOT be used in production.
// Production deployments MUST swap this for the postgres-backed sink
// (see services/zagent-gateway/internal/audit/postgres in ADR-0019).
type MemorySink struct {
	mu     sync.Mutex
	events []Event
	fail   bool // when true, Write returns an error to simulate sink downtime
}

// NewMemorySink returns an empty MemorySink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// SetFail toggles the sink into a failing mode used by the chain
// fail-closed tests. Production code MUST NOT call this.
func (m *MemorySink) SetFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = fail
}

// Write appends the event to the in-memory buffer.
func (m *MemorySink) Write(ctx context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errSinkDown
	}
	m.events = append(m.events, ev)
	return nil
}

// Close is a no-op for the memory sink.
func (m *MemorySink) Close() error { return nil }

// Events returns a snapshot of the recorded events.
func (m *MemorySink) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Scan walks the recorded events in insertion order. The implementation
// here is intentionally minimal — a real Postgres-backed sink would
// page by Sequence. The replay/verify path treats the order as
// authoritative because DecodeEvent sorts by tenant+sequence.
func (m *MemorySink) Scan(ctx ReplayContext, fn func(Event) error) error {
	m.mu.Lock()
	events := append([]Event(nil), m.events...)
	m.mu.Unlock()
	for _, ev := range events {
		if err := fn(ev); err != nil {
			return err
		}
	}
	return nil
}

// errSinkDown is the sentinel error returned by the failing sink. It
// is intentionally unexported so the public API stays small; callers
// identify it via errors.Is against ErrSinkDown.
var errSinkDown = chainErrSinkDown

// chainErrSinkDown is exposed only to the chain package so that
// ErrSinkDown can be used in errors.Is comparisons.
var chainErrSinkDown = errSinkDownSentinel{}

type errSinkDownSentinel struct{}

func (errSinkDownSentinel) Error() string { return "memory sink forced down" }

// Is makes errSinkDown match ErrSinkDown via errors.Is.
func (errSinkDownSentinel) Is(target error) bool { return target == ErrSinkDown }
