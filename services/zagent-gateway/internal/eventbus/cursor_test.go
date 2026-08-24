package eventbus

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// helpers -------------------------------------------------------------------

func mkEvent(seq uint64, id string) Event {
	return Event{
		EventID:     id,
		TenantID:    "tenant-1",
		AggregateID: "sess-1",
		Sequence:    seq,
		Type:        "session.message",
		OccurredAt:  time.Now(),
		Payload:     []byte("p"),
	}
}

// tests ---------------------------------------------------------------------

func TestDeliverAcceptsNewEvents(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	for i := uint64(1); i <= 5; i++ {
		act, err := c.Deliver("consumer-1", mkEvent(i, fmt.Sprintf("id-%d", i)))
		if err != nil {
			t.Fatalf("seq %d: %v", i, err)
		}
		if act != DeliverActionAccept {
			t.Fatalf("seq %d: expected accept, got %q", i, act)
		}
	}
	if s := c.State("consumer-1"); s.HighSeq != 5 {
		t.Fatalf("expected high seq 5, got %d", s.HighSeq)
	}
}

func TestReplayDedupsByEventID(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	ev := mkEvent(1, "id-1")
	if act, _ := c.Deliver("consumer-1", ev); act != DeliverActionAccept {
		t.Fatalf("first delivery must accept")
	}
	act, err := c.Deliver("consumer-1", ev)
	if err != nil {
		t.Fatalf("replay error: %v", err)
	}
	if act != DeliverActionDedup {
		t.Fatalf("expected dedup, got %q", act)
	}
}

func TestOutOfOrderSequenceDeduped(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	if _, _ = c.Deliver("consumer-1", mkEvent(2, "id-2")); false {
	}
	if _, _ = c.Deliver("consumer-1", mkEvent(3, "id-3")); false {
	}
	// Now deliver an event with sequence <= high (which is 3). This
	// must be deduped silently.
	act, err := c.Deliver("consumer-1", mkEvent(2, "id-2-replay"))
	if err != nil {
		t.Fatalf("out-of-order err: %v", err)
	}
	if act != DeliverActionDedup {
		t.Fatalf("expected dedup for out-of-order, got %q", act)
	}
}

func TestSequenceGapSurfaced(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	if _, _ = c.Deliver("consumer-1", mkEvent(1, "id-1")); false {
	}
	act, err := c.Deliver("consumer-1", mkEvent(5, "id-5"))
	if !errors.Is(err, ErrGap) {
		t.Fatalf("expected ErrGap, got %v", err)
	}
	if act != DeliverActionGap {
		t.Fatalf("expected gap action, got %q", act)
	}
}

func TestSlowConsumerBackpressure(t *testing.T) {
	var backfired int
	var mu sync.Mutex
	c := NewCursor(CursorConfig{
		TenantID:    "tenant-1",
		AggregateID: "sess-1",
		MaxQueue:    2,
		OnBackpressure: func(string, int) {
			mu.Lock()
			defer mu.Unlock()
			backfired++
		},
	})
	for i := uint64(1); i <= 3; i++ {
		if _, err := c.Deliver("consumer-1", mkEvent(i, "id")); err != nil {
			if !errors.Is(err, ErrBackpressure) {
				t.Fatalf("seq %d: %v", i, err)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if backfired == 0 {
		t.Fatalf("expected backpressure callback to fire")
	}
}

func TestAckReducesPending(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	ev := mkEvent(1, "id")
	if _, err := c.Deliver("consumer-1", ev); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := c.State("consumer-1").QueueDepth; got != 1 {
		t.Fatalf("expected queue depth 1, got %d", got)
	}
	if err := c.Ack("consumer-1", ev); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := c.State("consumer-1").QueueDepth; got != 0 {
		t.Fatalf("expected queue depth 0, got %d", got)
	}
}

func TestTenantMismatchRejected(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	ev := mkEvent(1, "id")
	ev.TenantID = "tenant-2"
	_, err := c.Deliver("consumer-1", ev)
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}
}

func TestAggregateMismatchRejected(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	ev := mkEvent(1, "id")
	ev.AggregateID = "sess-2"
	_, err := c.Deliver("consumer-1", ev)
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}
}

func TestResumeReturnsHighSeq(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	for i := uint64(1); i <= 4; i++ {
		c.Deliver("consumer-1", mkEvent(i, "id"))
	}
	if got := c.Resume("consumer-1"); got != 4 {
		t.Fatalf("expected high seq 4, got %d", got)
	}
	if got := c.FormatResume("consumer-1"); got != "tenant-1/sess-1/4" {
		t.Fatalf("unexpected resume string: %s", got)
	}
}

func TestGCReducesDedupWindow(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	for i := uint64(1); i <= 10; i++ {
		c.Deliver("consumer-1", mkEvent(i, "id"))
	}
	if removed := c.GC(5); removed != 5 {
		t.Fatalf("expected 5 removed, got %d", removed)
	}
	if removed := c.GC(5); removed != 0 {
		t.Fatalf("expected 0 removed on second pass, got %d", removed)
	}
}

func TestManagerReturnsSharedCursor(t *testing.T) {
	m := NewManager()
	a := m.Get("tenant-1", "sess-1")
	b := m.Get("tenant-1", "sess-1")
	if a != b {
		t.Fatalf("Manager.Get must return the same instance")
	}
	c := m.Get("tenant-2", "sess-1")
	if c == a {
		t.Fatalf("different tenants must produce different cursors")
	}
}

func TestEventIDDedupIndependentOfSequence(t *testing.T) {
	c := NewCursor(CursorConfig{TenantID: "tenant-1", AggregateID: "sess-1"})
	// Two events with the same event_id but different sequence numbers
	// must still be deduped.
	if _, _ = c.Deliver("consumer-1", mkEvent(1, "shared")); false {
	}
	act, _ := c.Deliver("consumer-1", mkEvent(2, "shared"))
	if act != DeliverActionDedup {
		t.Fatalf("expected dedup on id collision, got %q", act)
	}
}
