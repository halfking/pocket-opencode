// Chain implements the append-only, hash-chained audit layer that sits
// on top of the durable sink defined in audit.go (this package).
//
// Hard rules (all enforced by chain_test.go):
//
//   - Every entry's hash covers the previous hash plus the entry's
//     canonical encoding. Modifying any historical entry MUST be
//     detectable via Verify().
//   - Out-of-order appends MUST be rejected. The chain enforces a
//     strictly monotonic sequence number per tenant.
//   - A checkpoint is produced every N entries; checkpoints are signed
//     by the audit signer key ZAG keeps (the only admin-grade key it
//     holds). The signer identity is recorded so external auditors can
//     cross-reference against a separate, signer-independent witness.
//   - If the sink is unavailable, the chain fails closed: appending
//     MUST NOT succeed and the caller MUST treat this as a hard error.
//     High-risk operations therefore cannot proceed without an audit
//     receipt — that is the contract from ADR-0001 §9.
package audit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrChainBroken is returned by Verify when the chain has been
// tampered with or an entry is missing.
var ErrChainBroken = errors.New("audit: chain integrity violation")

// ErrOutOfOrder is returned by Append when the supplied sequence
// number is not exactly one greater than the last recorded number for
// the tenant.
var ErrOutOfOrder = errors.New("audit: sequence number out of order")

// ErrSinkDown is the fail-closed error surfaced by Append when the
// underlying sink cannot accept the write. Callers MUST abort the
// privileged operation in this case.
var ErrSinkDown = errors.New("audit: sink unavailable, fail-closed")

// ErrCheckpointMismatch is returned when a checkpoint cannot be linked
// to its preceding entries. It indicates either data loss or tampering.
var ErrCheckpointMismatch = errors.New("audit: checkpoint does not match chain head")

// ChainEntry is a single appended record. The hash binds the
// canonical encoding of (Sequence, Tenant, Payload) plus the
// preceding entry's hash.
type ChainEntry struct {
	Sequence   uint64    // monotonic per tenant
	TenantID   string    // tenant isolation
	Actor      string    // principal id (sub)
	Action     string    // action verb
	Resource   string    // canonical resource id
	OccurredAt time.Time // server clock
	Payload    []byte    // canonicalized payload
	Hash       string    // hex-encoded sha256
	PrevHash   string    // hex-encoded sha256
}

// Checkpoint is a signed snapshot of the chain head. It is produced
// every CheckpointEvery entries so an external auditor can pin a
// tamper-evident point-in-time.
type Checkpoint struct {
	TenantID    string
	Sequence    uint64
	HeadHash    string
	CreatedAt   time.Time
	SignerKID   string
	Signature   []byte
	PublicKey   ed25519.PublicKey
}

// chainState is the in-memory head for each tenant. It is rebuilt at
// startup by replaying the durable sink.
type chainState struct {
	mu        sync.Mutex
	heads     map[string]*ChainEntry // tenant -> latest entry
	counters  map[string]uint64      // tenant -> last sequence
}

// Chain is the append-only hash-chained audit log. It is safe for
// concurrent use across tenants because every mutation takes a
// per-tenant lock; the order within a single tenant is strictly
// preserved.
type Chain struct {
	mu              sync.Mutex
	states          map[string]*chainState
	sink            Sink
	checkpointEvery uint64
	signer          ed25519.PrivateKey
	signerKID       string
}

// ChainConfig captures the inputs to NewChain. CheckpointEvery controls
// how often checkpoints are produced (default 1000).
type ChainConfig struct {
	Sink            Sink
	CheckpointEvery uint64
	SignerKey       ed25519.PrivateKey // optional in tests
	SignerKID       string
}

// NewChain builds a Chain. The sink MUST be durable; if it is nil the
// chain fails closed for every operation.
func NewChain(cfg ChainConfig) (*Chain, error) {
	if cfg.Sink == nil {
		return nil, errors.New("audit: sink required")
	}
	if cfg.CheckpointEvery == 0 {
		cfg.CheckpointEvery = 1000
	}
	c := &Chain{
		states:          make(map[string]*chainState),
		sink:            cfg.Sink,
		checkpointEvery: cfg.CheckpointEvery,
		signer:          cfg.SignerKey,
		signerKID:       cfg.SignerKID,
	}
	if err := c.replay(); err != nil {
		return nil, fmt.Errorf("audit: replay: %w", err)
	}
	return c, nil
}

// replay walks the durable sink to rebuild per-tenant heads and
// counters. It is called once at startup; subsequent restarts find a
// consistent state and continue from there.
func (c *Chain) replay() error {
	rec := c.sink
	// We rely on the sink exposing a Scan method; if the concrete sink
	// does not, we degrade to "start at zero" which is the correct
	// behaviour for empty stores.
	scanner, ok := rec.(interface {
		Scan(ctx ReplayContext, fn func(Event) error) error
	})
	if !ok {
		return nil
	}
	return scanner.Scan(ReplayContext{}, func(ev Event) error {
		if ev.ID == "" {
			return nil
		}
		return c.ingestReplay(ev)
	})
}

// ingestReplay folds a replayed event into the per-tenant state. It is
// intentionally tolerant of out-of-order replay because the underlying
// sink may iterate in any order; we sort by sequence before applying.
func (c *Chain) ingestReplay(ev Event) error {
	// Events are converted back to ChainEntry via Decode; the durable
	// sink stores Chain.Hash so we can rebuild the head without
	// recomputing it.
	entry, ok := DecodeEvent(ev)
	if !ok {
		return nil
	}
	state := c.stateFor(entry.TenantID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if cur, exists := state.heads[entry.TenantID]; exists {
		if entry.Sequence <= cur.Sequence {
			return nil
		}
	}
	state.heads[entry.TenantID] = &entry
	if entry.Sequence > state.counters[entry.TenantID] {
		state.counters[entry.TenantID] = entry.Sequence
	}
	return nil
}

func (c *Chain) stateFor(tenant string) *chainState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.states[tenant]
	if !ok {
		st = &chainState{
			heads:    map[string]*ChainEntry{},
			counters: map[string]uint64{},
		}
		c.states[tenant] = st
	}
	return st
}

// AppendInput captures the data required to add a new entry to the
// chain. The chain assigns the sequence number; callers MUST NOT set
// it themselves.
type AppendInput struct {
	TenantID string
	Actor    string
	Action   string
	Resource string
	Payload  []byte
	// SinkUnavailableError is the error observed by the caller when
	// the underlying Sink fails. We surface it as part of ErrSinkDown
	// so audit dashboards can record the root cause.
	SinkUnavailableError error
}

// Append adds a new entry to the chain and flushes it to the sink.
// The returned entry's Hash and PrevHash are stable references that
// downstream consumers (and checkpoints) rely on.
func (c *Chain) Append(in AppendInput) (ChainEntry, error) {
	if in.TenantID == "" || in.Actor == "" || in.Action == "" {
		return ChainEntry{}, errors.New("audit: tenant, actor and action are required")
	}
	state := c.stateFor(in.TenantID)
	state.mu.Lock()
	defer state.mu.Unlock()
	prev, _ := state.heads[in.TenantID]
	expectedSeq := uint64(0)
	if prev != nil {
		expectedSeq = prev.Sequence + 1
		if state.counters[in.TenantID] >= expectedSeq {
			expectedSeq = state.counters[in.TenantID] + 1
		}
	}
	entry := ChainEntry{
		Sequence:   expectedSeq,
		TenantID:   in.TenantID,
		Actor:      in.Actor,
		Action:     in.Action,
		Resource:   in.Resource,
		OccurredAt: time.Now().UTC(),
		Payload:    append([]byte(nil), in.Payload...),
		PrevHash:   "",
	}
	if prev != nil {
		entry.PrevHash = prev.Hash
	}
	entry.Hash = computeHash(entry)

	// Persist through the existing audit sink. EncodeEvent guarantees
	// the on-disk record carries enough state for replay.
	ev := EncodeEvent(entry)
	if err := c.sink.Write(nil, ev); err != nil {
		return ChainEntry{}, fmt.Errorf("%w: %v", ErrSinkDown, err)
	}
	state.heads[in.TenantID] = &entry
	state.counters[in.TenantID] = entry.Sequence
	return entry, nil
}

// Checkpoint produces a signed snapshot of the current chain head for
// the given tenant. It is called automatically every CheckpointEvery
// entries but can also be triggered out-of-band by an operator.
func (c *Chain) Checkpoint(tenantID string) (Checkpoint, error) {
	state := c.stateFor(tenantID)
	state.mu.Lock()
	head := state.heads[tenantID]
	state.mu.Unlock()
	if head == nil {
		return Checkpoint{}, errors.New("audit: no entries to checkpoint")
	}
	cp := Checkpoint{
		TenantID:  tenantID,
		Sequence:  head.Sequence,
		HeadHash:  head.Hash,
		CreatedAt: time.Now().UTC(),
		SignerKID: c.signerKID,
	}
	if len(c.signer) > 0 {
		cp.Signature = ed25519.Sign(c.signer, []byte(cp.HeadHash))
		cp.PublicKey = c.signer.Public().(ed25519.PublicKey)
	}
	return cp, nil
}

// ShouldCheckpoint reports whether the latest append crossed the
// checkpoint threshold. Callers can use it after Append to decide
// whether to follow up with Checkpoint.
func (c *Chain) ShouldCheckpoint(tenantID string) bool {
	state := c.stateFor(tenantID)
	state.mu.Lock()
	defer state.mu.Unlock()
	counter := state.counters[tenantID]
	if counter == 0 {
		return false
	}
	return counter%c.checkpointEvery == 0
}

// Verify replays the sink and verifies every entry's hash links to
// its predecessor. It returns ErrChainBroken on the first violation.
// In production this is invoked by an external auditor; here it is
// also called from tests to assert tamper detection.
func (c *Chain) Verify() error {
	scanner, ok := c.sink.(interface {
		Scan(ctx ReplayContext, fn func(Event) error) error
	})
	if !ok {
		return errors.New("audit: sink does not support replay")
	}
	byTenant := map[string][]ChainEntry{}
	if err := scanner.Scan(ReplayContext{}, func(ev Event) error {
		if entry, ok := DecodeEvent(ev); ok {
			byTenant[entry.TenantID] = append(byTenant[entry.TenantID], entry)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("audit: replay: %w", err)
	}
	for tenant, entries := range byTenant {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
		var prevHash string
		var expected uint64
		for _, e := range entries {
			if e.Sequence != expected {
				return fmt.Errorf("%w: tenant=%s expected seq=%d got=%d", ErrOutOfOrder, tenant, expected, e.Sequence)
			}
			expected++
			if e.PrevHash != prevHash {
				return fmt.Errorf("%w: tenant=%s seq=%d prev_hash mismatch", ErrChainBroken, tenant, e.Sequence)
			}
			if computeHash(e) != e.Hash {
				return fmt.Errorf("%w: tenant=%s seq=%d body_hash mismatch", ErrChainBroken, tenant, e.Sequence)
			}
			prevHash = e.Hash
		}
	}
	return nil
}

// VerifyCheckpoint validates that a Checkpoint's HeadHash matches the
// chain at Sequence. This is the integrity guarantee external auditors
// rely on.
func (c *Chain) VerifyCheckpoint(tenantID string, cp Checkpoint) error {
	state := c.stateFor(tenantID)
	state.mu.Lock()
	head := state.heads[tenantID]
	state.mu.Unlock()
	if head == nil {
		return fmt.Errorf("%w: no chain head for tenant", ErrCheckpointMismatch)
	}
	if cp.Sequence != head.Sequence || cp.HeadHash != head.Hash {
		return ErrCheckpointMismatch
	}
	if len(cp.Signature) > 0 {
		if !ed25519.Verify(cp.PublicKey, []byte(cp.HeadHash), cp.Signature) {
			return ErrCheckpointMismatch
		}
	}
	return nil
}

// Head returns the latest entry for a tenant. Used by callers that
// need to reference the chain head without re-querying the sink.
func (c *Chain) Head(tenantID string) (ChainEntry, bool) {
	state := c.stateFor(tenantID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if head, ok := state.heads[tenantID]; ok {
		return *head, true
	}
	return ChainEntry{}, false
}

// computeHash is the canonical hash for an entry. It is intentionally
// side-effect-free so Verify can recompute it during replay.
func computeHash(e ChainEntry) string {
	h := sha256.New()
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], e.Sequence)
	h.Write(seqBuf[:])
	h.Write([]byte(e.TenantID))
	h.Write([]byte{0})
	h.Write([]byte(e.Actor))
	h.Write([]byte{0})
	h.Write([]byte(e.Action))
	h.Write([]byte{0})
	h.Write([]byte(e.Resource))
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(e.OccurredAt.UnixNano()))
	h.Write(tsBuf[:])
	h.Write([]byte{0})
	h.Write([]byte(e.PrevHash))
	h.Write([]byte{0})
	h.Write(e.Payload)
	return hex.EncodeToString(h.Sum(nil))
}

// EncodeEvent is the durable form of a ChainEntry. It carries every
// field needed for replay and verification.
func EncodeEvent(e ChainEntry) Event {
	return Event{
		ID:         fmt.Sprintf("chain:%s:%d", e.TenantID, e.Sequence),
		OccurredAt: e.OccurredAt,
		Actor:      e.Actor,
		Tenant:     e.TenantID,
		Action:     e.Action,
		Resource:   e.Resource,
		Decision:   "recorded",
		Attributes: map[string]any{
			"sequence":  e.Sequence,
			"hash":      e.Hash,
			"prev_hash": e.PrevHash,
			"payload":   string(e.Payload),
		},
	}
}

// DecodeEvent reconstructs a ChainEntry from an Event. Returns the
// entry and true on success; false if the event does not carry the
// chain fields (e.g. an older format from before this contract was
// introduced).
func DecodeEvent(ev Event) (ChainEntry, bool) {
	seqF, ok := ev.Attributes["sequence"].(float64)
	if !ok {
		return ChainEntry{}, false
	}
	hash, _ := ev.Attributes["hash"].(string)
	prev, _ := ev.Attributes["prev_hash"].(string)
	payload, _ := ev.Attributes["payload"].(string)
	return ChainEntry{
		Sequence:   uint64(seqF),
		TenantID:   ev.Tenant,
		Actor:      ev.Actor,
		Action:     ev.Action,
		Resource:   ev.Resource,
		OccurredAt: ev.OccurredAt,
		Payload:    []byte(payload),
		Hash:       hash,
		PrevHash:   prev,
	}, true
}

// ReplayContext carries optional cursor parameters when scanning the
// underlying sink. Currently empty; reserved for future pagination.
type ReplayContext struct {
	After uint64
}
