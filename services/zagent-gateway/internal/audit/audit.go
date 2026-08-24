// Package audit provides a fail-closed audit wrapper around an
// underlying audit sink.
//
// Per ADR-0019 (ZAG Audit Fail-Closed), ZAG must guarantee that no
// privileged action returns a success to the caller until an audit
// record is durably persisted. If the underlying sink is unavailable,
// the wrapper returns an error and the caller must NOT proceed.
package audit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Event is a single audit record.
type Event struct {
	ID         string                 `json:"id"`
	OccurredAt time.Time              `json:"occurred_at"`
	Actor      string                 `json:"actor"`
	Tenant     string                 `json:"tenant"`
	Action     string                 `json:"action"`
	Resource   string                 `json:"resource"`
	Decision   string                 `json:"decision"`
	Reason     string                 `json:"reason,omitempty"`
	Attributes map[string]any         `json:"attributes,omitempty"`
	Chain      map[string]string      `json:"chain,omitempty"`
}

// Sink is the persistence contract for audit events. Implementations
// MUST be synchronous and durable; the wrapper relies on Write
// returning only after the event has been flushed.
type Sink interface {
	Write(ctx context.Context, ev Event) error
	Close() error
}

// Recorder is the fail-closed wrapper. It blocks until the underlying
// sink acknowledges the write. When wrapped with a circuit breaker
// (see WithBreaker), repeated sink failures short-circuit Write to
// avoid hanging on a broken dependency.
type Recorder struct {
	sink    Sink
	breaker *Breaker
}

// ErrSinkUnavailable is returned when the breaker is open or the sink
// cannot accept writes. Callers MUST treat this as a hard failure
// and refuse to proceed with the privileged action.
var ErrSinkUnavailable = errors.New("audit sink unavailable")

// New constructs a Recorder around the given sink.
func New(sink Sink) *Recorder {
	return &Recorder{sink: sink, breaker: NewBreaker(5, 30*time.Second)}
}

// WithBreaker replaces the default circuit breaker. Useful for tests.
func (r *Recorder) WithBreaker(b *Breaker) *Recorder {
	r.breaker = b
	return r
}

// Record persists an audit event. On any sink failure it returns
// ErrSinkUnavailable and increments the breaker counters. Callers
// must propagate the error to abort the underlying privileged
// operation.
func (r *Recorder) Record(ctx context.Context, ev Event) error {
	if err := r.breaker.Allow(); err != nil {
		return err
	}
	if err := r.sink.Write(ctx, ev); err != nil {
		r.breaker.Fail()
		return errors.Join(ErrSinkUnavailable, err)
	}
	r.breaker.Success()
	return nil
}

// Close releases any underlying sink resources.
func (r *Recorder) Close() error { return r.sink.Close() }

// Sink returns the wrapped sink (for tests / diagnostics).
func (r *Recorder) Sink() Sink { return r.sink }

// Breaker is a small circuit breaker. It opens after `threshold`
// consecutive failures and stays open for `cooldown`. While open,
// every Allow returns ErrSinkUnavailable without invoking the sink.
type Breaker struct {
	threshold int
	cooldown  time.Duration

	mu           sync.Mutex
	failures     int
	openUntil    time.Time
}

// NewBreaker constructs a Breaker.
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{threshold: threshold, cooldown: cooldown}
}

// Allow checks the breaker state. If the breaker is closed (or the
// cooldown has elapsed) it returns nil; otherwise ErrSinkUnavailable.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures >= b.threshold && time.Now().Before(b.openUntil) {
		return ErrSinkUnavailable
	}
	if b.failures >= b.threshold && !time.Now().Before(b.openUntil) {
		// half-open: allow next attempt and reset failure count
		b.failures = 0
	}
	return nil
}

// Success registers a successful sink operation.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
}

// Fail registers a failed sink operation.
func (b *Breaker) Fail() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = time.Now().Add(b.cooldown)
	}
}
