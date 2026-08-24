package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

// helpers -------------------------------------------------------------------

func newChain(t *testing.T) (*Chain, *MemorySink) {
	t.Helper()
	sink := NewMemorySink()
	c, err := NewChain(ChainConfig{Sink: sink, CheckpointEvery: 3})
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	return c, sink
}

func appendEntry(t *testing.T, c *Chain, tenant, actor, action, resource string, payload []byte) ChainEntry {
	t.Helper()
	e, err := c.Append(AppendInput{
		TenantID: tenant,
		Actor:    actor,
		Action:   action,
		Resource: resource,
		Payload:  payload,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return e
}

// tests ---------------------------------------------------------------------

func TestAppendAssignsMonotonicSequence(t *testing.T) {
	c, _ := newChain(t)
	for i := uint64(1); i <= 5; i++ {
		e := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("hello"))
		if e.Sequence != i {
			t.Fatalf("expected sequence %d, got %d", i, e.Sequence)
		}
		if e.PrevHash == "" && i != 1 {
			t.Fatalf("expected non-empty PrevHash for non-genesis entry")
		}
	}
}

func TestAppendChainLinksViaPrevHash(t *testing.T) {
	c, _ := newChain(t)
	a := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	b := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("b"))
	if b.PrevHash != a.Hash {
		t.Fatalf("expected prev_hash=%s, got %s", a.Hash, b.PrevHash)
	}
}

func TestTenantIsolation(t *testing.T) {
	c, _ := newChain(t)
	a := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	b := appendEntry(t, c, "tenant-2", "actor", "session.read", "s1", []byte("a"))
	if b.Sequence != 1 {
		t.Fatalf("tenant-2 should start at 1, got %d", b.Sequence)
	}
	if b.PrevHash != "" {
		t.Fatalf("tenant-2 genesis entry must have empty prev_hash, got %s", b.PrevHash)
	}
	if a.Hash == b.Hash {
		t.Fatalf("different tenants must produce different hashes")
	}
}

func TestVerifyCleanChain(t *testing.T) {
	c, _ := newChain(t)
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("b"))
	if err := c.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyDetectsTamper(t *testing.T) {
	c, sink := newChain(t)
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("b"))

	// Mutate the second entry's payload without recomputing the hash.
	sink.mu.Lock()
	sink.events[1].Attributes["payload"] = "tampered"
	sink.mu.Unlock()

	if err := c.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken, got %v", err)
	}
}

func TestOutOfOrderSequenceRejected(t *testing.T) {
	c, _ := newChain(t)
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	// We cannot directly forge an Append sequence number because the
	// chain assigns it. Instead we inject an out-of-order event via the
	// sink and call Verify: replay should detect it.
	sink := c.sink.(*MemorySink)
	sink.mu.Lock()
	sink.events = append(sink.events, Event{
		ID:         "chain:tenant-1:99",
		OccurredAt: nowFn(),
		Actor:      "actor",
		Tenant:     "tenant-1",
		Action:     "session.read",
		Resource:   "s1",
		Decision:   "recorded",
		Attributes: map[string]any{
			"sequence":  float64(99),
			"hash":      "deadbeef",
			"prev_hash": "",
			"payload":   "x",
		},
	})
	sink.mu.Unlock()
	if err := c.Verify(); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("expected ErrOutOfOrder, got %v", err)
	}
}

func TestFailClosedOnSinkUnavailable(t *testing.T) {
	c, sink := newChain(t)
	sink.SetFail(true)
	if _, err := c.Append(AppendInput{
		TenantID: "tenant-1",
		Actor:    "actor",
		Action:   "session.read",
		Resource: "s1",
		Payload:  []byte("a"),
	}); !errors.Is(err, ErrSinkDown) {
		t.Fatalf("expected ErrSinkDown, got %v", err)
	}
}

func TestCheckpointSignsAndVerifies(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	sink := NewMemorySink()
	c, err := NewChain(ChainConfig{Sink: sink, CheckpointEvery: 2, SignerKey: priv, SignerKID: "kid-1"})
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("b"))
	if !c.ShouldCheckpoint("tenant-1") {
		t.Fatalf("expected ShouldCheckpoint to be true at sequence 2")
	}
	cp, err := c.Checkpoint("tenant-1")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if !ed25519.Verify(pub, []byte(cp.HeadHash), cp.Signature) {
		t.Fatalf("checkpoint signature did not verify")
	}
	if err := c.VerifyCheckpoint("tenant-1", cp); err != nil {
		t.Fatalf("verify checkpoint: %v", err)
	}
}

func TestCheckpointRejectsTamperedHeadHash(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	sink := NewMemorySink()
	c, err := NewChain(ChainConfig{Sink: sink, SignerKey: priv, SignerKID: "kid-1"})
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	cp, err := c.Checkpoint("tenant-1")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	cp.HeadHash = strings.Repeat("0", 64)
	if err := c.VerifyCheckpoint("tenant-1", cp); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("expected ErrCheckpointMismatch, got %v", err)
	}
}

func TestEmptyTenantCheckpointRejected(t *testing.T) {
	c, _ := newChain(t)
	if _, err := c.Checkpoint("nope"); err == nil {
		t.Fatalf("expected error checkpointing unknown tenant")
	}
}

func TestHeadReturnsLatest(t *testing.T) {
	c, _ := newChain(t)
	a := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("a"))
	b := appendEntry(t, c, "tenant-1", "actor", "session.read", "s1", []byte("b"))
	head, ok := c.Head("tenant-1")
	if !ok {
		t.Fatalf("expected head")
	}
	if head.Sequence != b.Sequence {
		t.Fatalf("expected head seq=%d, got %d", b.Sequence, head.Sequence)
	}
	if head.Hash != b.Hash {
		t.Fatalf("expected head hash=%s, got %s", b.Hash, head.Hash)
	}
	if head.Sequence == a.Sequence {
		t.Fatalf("head did not advance")
	}
}

// nowFn is a small helper for tests that need a deterministic-ish
// timestamp. Returning time.Time{} keeps the hash stable across runs.
func nowFn() (t time.Time) { return time.Time{} }
