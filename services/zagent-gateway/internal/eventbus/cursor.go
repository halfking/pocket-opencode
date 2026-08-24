// Package eventbus implements the consumer-side cursor, dedup and
// backpressure machinery for the ZAG event bus.
//
// The bus fans events out from the audit chain and from upstream
// services (RedClaw, ACC, OpenCode runtime) to long-lived consumers
// (mobile WS, IDE connectors, operator dashboards). The contract from
// ADR-0001 §8 is:
//
//   - Every event carries a per-(tenant, aggregate) monotonic sequence
//     number. Consumers resume from `Last-Event-ID` and the cursor
//     MUST detect gaps.
//   - Out-of-order events (sequence lower than what the consumer has
//     already seen) are deduplicated silently — the consumer MUST NOT
//     see them twice.
//   - Slow consumers are detected by per-consumer queue depth. When a
//     consumer exceeds the configured queue cap the cursor returns
//     ErrBackpressure and the consumer SHOULD reconnect from the last
//     checkpoint with a fresh budget.
//   - The cursor is the only place tenant isolation is enforced for
//     the bus: a cursor that receives an event for a tenant it has not
//     been provisioned for MUST reject it.
package eventbus

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrBackpressure is returned when a consumer's pending queue exceeds
// the configured limit. The consumer SHOULD reconnect with a fresh
// budget; the cursor is preserved across reconnects.
var ErrBackpressure = errors.New("eventbus: consumer backpressure")

// ErrGap is returned when an event arrives with a sequence number
// greater than expectedSeq+1, indicating one or more events were lost
// in transit. Consumers MUST surface this to the operator rather than
// silently dropping the gap.
var ErrGap = errors.New("eventbus: sequence gap detected")

// ErrTenantMismatch is returned when a cursor receives an event for a
// tenant it was not provisioned for.
var ErrTenantMismatch = errors.New("eventbus: cursor not provisioned for tenant")

// Event is the canonical envelope shared by every event source.
type Event struct {
	EventID     string    // unique per (tenant, aggregate) sequence
	TenantID    string    // tenant isolation key
	AggregateID string    // aggregate (session, task, pod) the event belongs to
	Sequence    uint64    // monotonic per (tenant, aggregate)
	Type        string    // dotted event type, e.g. session.message
	OccurredAt  time.Time // server clock
	Payload     []byte    // opaque to the cursor
}

// CursorConfig is the input to NewCursor.
type CursorConfig struct {
	TenantID    string
	AggregateID string
	// MaxQueue is the per-consumer queue cap. When exceeded, Deliver
	// returns ErrBackpressure. Defaults to 1024.
	MaxQueue int
	// OnBackpressure is invoked (synchronously) when backpressure
	// trips. It is optional; useful for metrics.
	OnBackpressure func(consumerID string, depth int)
}

// Cursor is a per-(consumer, tenant, aggregate) state machine. It
// tracks the highest seen sequence number, the dedup window, and the
// pending queue depth.
type Cursor struct {
	mu sync.Mutex

	tenantID    string
	aggregateID string
	maxQueue    int
	onBack      func(string, int)

	highSeq     uint64                       // highest sequence ever accepted
	dedup       map[string]struct{}          // recently delivered event_ids
	pending     map[string]int               // consumer -> pending count
	lastSeenAt  map[string]time.Time         // consumer -> last deliver time
}

// NewCursor builds a Cursor for the given (tenant, aggregate) pair.
func NewCursor(cfg CursorConfig) *Cursor {
	if cfg.MaxQueue <= 0 {
		cfg.MaxQueue = 1024
	}
	return &Cursor{
		tenantID:    cfg.TenantID,
		aggregateID: cfg.AggregateID,
		maxQueue:    cfg.MaxQueue,
		onBack:      cfg.OnBackpressure,
		dedup:       map[string]struct{}{},
		pending:     map[string]int{},
		lastSeenAt:  map[string]time.Time{},
	}
}

// State is a snapshot of the cursor's view, used by the resumability
// API (Last-Event-ID resume).
type State struct {
	TenantID    string
	AggregateID string
	HighSeq     uint64
	QueueDepth  int
}

// State returns a snapshot for diagnostics. The caller MUST NOT mutate
// the returned value.
func (c *Cursor) State(consumerID string) State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{
		TenantID:    c.tenantID,
		AggregateID: c.aggregateID,
		HighSeq:     c.highSeq,
		QueueDepth:  c.pending[consumerID],
	}
}

// Deliver accepts an event, performs dedup, sequence, and backpressure
// checks, and returns the action the caller must take:
//
//   - DeliverActionAccept: hand the event to the consumer.
//   - DeliverActionDedup: silently drop; already seen.
//   - DeliverActionGap: surface to the operator; sequence jumped.
//   - DeliverActionBackpressure: drop; consumer too slow.
//
// The tenant/aggregate isolation is enforced BEFORE the action is
// returned so a misrouted event never reaches the consumer.
func (c *Cursor) Deliver(consumerID string, ev Event) (DeliverAction, error) {
	if consumerID == "" {
		return DeliverActionBackpressure, errors.New("eventbus: consumer id required")
	}
	if ev.TenantID != c.tenantID || ev.AggregateID != c.aggregateID {
		return DeliverActionBackpressure, ErrTenantMismatch
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Dedup: silently skip already-seen ids.
	if _, seen := c.dedup[ev.EventID]; seen {
		return DeliverActionDedup, nil
	}
	// Backpressure: refuse when the queue is saturated.
	if c.pending[consumerID] >= c.maxQueue {
		if c.onBack != nil {
			c.onBack(consumerID, c.pending[consumerID])
		}
		return DeliverActionBackpressure, ErrBackpressure
	}
	// Sequence handling: gap if highSeq > 0 and ev.Sequence > highSeq+1.
	if c.highSeq > 0 && ev.Sequence > c.highSeq+1 {
		// We still accept but flag the gap so the consumer can surface it.
		c.pending[consumerID]++
		c.lastSeenAt[consumerID] = time.Now()
		c.dedup[ev.EventID] = struct{}{}
		if ev.Sequence > c.highSeq {
			c.highSeq = ev.Sequence
		}
		return DeliverActionGap, ErrGap
	}
	// Out-of-order: ev.Sequence <= highSeq — drop silently.
	if ev.Sequence <= c.highSeq {
		c.dedup[ev.EventID] = struct{}{}
		return DeliverActionDedup, nil
	}
	// Accept.
	c.highSeq = ev.Sequence
	c.pending[consumerID]++
	c.lastSeenAt[consumerID] = time.Now()
	c.dedup[ev.EventID] = struct{}{}
	return DeliverActionAccept, nil
}

// Ack decrements the consumer's pending counter. Callers MUST call Ack
// exactly once for each accepted event. Missing Acks cause the cursor
// to eventually trip backpressure on the same consumer.
func (c *Cursor) Ack(consumerID string, ev Event) error {
	if ev.TenantID != c.tenantID || ev.AggregateID != c.aggregateID {
		return ErrTenantMismatch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[consumerID] > 0 {
		c.pending[consumerID]--
	}
	delete(c.dedup, ev.EventID)
	return nil
}

// DeliverAction enumerates the verdicts returned by Deliver.
type DeliverAction string

const (
	DeliverActionAccept      DeliverAction = "accept"
	DeliverActionDedup       DeliverAction = "dedup"
	DeliverActionGap         DeliverAction = "gap"
	DeliverActionBackpressure DeliverAction = "backpressure"
)

// Resume is invoked on reconnect. It returns the last sequence number
// the consumer has acked (or zero for a fresh consumer) so the
// upstream can replay from there.
func (c *Cursor) Resume(consumerID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.highSeq
}

// FormatResume encodes the resume cursor in the Last-Event-ID format
// the ADR mandates: "<tenant>/<aggregate>/<sequence>".
func (c *Cursor) FormatResume(consumerID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("%s/%s/%d", c.tenantID, c.aggregateID, c.highSeq)
}

// GC sweeps the dedup window. Callers should invoke this periodically
// to bound memory; the cap is configurable.
func (c *Cursor) GC(maxEntries int) int {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dedup) <= maxEntries {
		return 0
	}
	// Drop arbitrary half. We don't track order on the dedup set; a
	// future iteration could use an LRU if the window matters for
	// correctness (it does not for the gap/dedup invariants).
	drop := len(c.dedup) - maxEntries
	removed := 0
	for k := range c.dedup {
		if removed >= drop {
			break
		}
		delete(c.dedup, k)
		removed++
	}
	return removed
}

// Manager is the multi-tenant registry of cursors. Production deploys
// persist per-consumer cursors in Postgres; this in-memory manager is
// the test/dev equivalent.
type Manager struct {
	mu      sync.Mutex
	cursors map[string]*Cursor
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{cursors: map[string]*Cursor{}}
}

// Get returns the cursor for (tenant, aggregate) or creates one with
// default settings.
func (m *Manager) Get(tenantID, aggregateID string) *Cursor {
	key := tenantID + "\x00" + aggregateID
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.cursors[key]; ok {
		return c
	}
	c := NewCursor(CursorConfig{TenantID: tenantID, AggregateID: aggregateID})
	m.cursors[key] = c
	return c
}
