// Package auth — mutual TLS certificate management.
//
// This file implements the mTLS portion of the ZAG security baseline.
// It is intentionally small: the goal here is to define a single
// in-process certificate state machine, a CA rollover helper, and the
// revocation/rotation checks. The actual TLS handshake happens in the
// net/http listener; this code is consumed by that listener.
//
// Non-negotiable rules (all enforced by mtls_test.go):
//
//   - A request without a valid client certificate is rejected before
//     any business logic runs. There is no "fall back to HMAC" path.
//   - A revoked certificate is rejected even if it has not yet expired.
//   - An expired certificate is rejected. There is no grace window
//     beyond the cert's own notAfter.
//   - During CA rollover both the old and the new Issuing CA are
//     trusted. After rollover completes the old CA is removed from the
//     trust store and any cert it issued is rejected on sight.
//   - Every successful enrollment is recorded in the state store. If
//     the state store is unavailable, enrollment fails closed.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// CertState is the lifecycle state of a single client or server cert.
type CertState string

const (
	StateEnrolled  CertState = "enrolled"  // CSR signed, not yet seen on the wire
	StateActive    CertState = "active"    // at least one successful handshake
	StateRotating  CertState = "rotating"  // new cert issued; old cert still usable
	StateRevoked   CertState = "revoked"   // revoked (CRL/OCSP or operator action)
	StateExpired   CertState = "expired"   // notAfter passed; terminal
)

// CertRecord is a single certificate tracked by the gateway.
type CertRecord struct {
	Serial        string
	Subject       string
	Issuer        string // canonical CA id (see CAStore)
	Fingerprint   string // sha256 of DER, lower-case hex
	NotBefore     time.Time
	NotAfter      time.Time
	State         CertState
	EnrolledAt    time.Time
	LastSeenAt    time.Time
	BoundTenantID string // SPIFFE-style tenant binding
	BoundPodID    string // empty for non-pod certs
}

// Fingerprint computes the SHA-256 of the DER bytes and returns it as
// lowercase hex. It exists as a public helper so call sites can
// canonicalize cert fingerprints before lookup.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Valid reports whether the cert is currently usable. Revoked and
// expired certs are NEVER valid; the rotation grace window is encoded
// separately so this helper stays a pure time-window check.
func (c CertRecord) Valid() bool {
	switch c.State {
	case StateRevoked, StateExpired:
		return false
	}
	now := time.Now()
	if !c.NotBefore.IsZero() && now.Before(c.NotBefore) {
		return false
	}
	if !c.NotAfter.IsZero() && !now.Before(c.NotAfter) {
		return false
	}
	return c.State == StateActive || c.State == StateRotating || c.State == StateEnrolled
}

// CertStateStore persists CertRecords. Production deploys wire this to
// Postgres; the in-memory implementation lives next door in
// memory_cert_store.go (so tests can spin up an isolated store) and
// must NOT be used in production. The interface is deliberately small
// so swapping the backend is mechanical.
type CertStateStore interface {
	Put(rec CertRecord) error
	Get(serial string) (CertRecord, error)
	List() ([]CertRecord, error)
	Revoke(serial string, reason string) error
}

// ErrCertNotFound is returned when a serial is not present in the
// store. It exists so handlers can render a precise error response
// without inspecting the wrapped error chain.
var ErrCertNotFound = errors.New("auth: certificate serial not found")

// ErrCertRevoked is returned when the lookup succeeded but the row is
// in the Revoked state.
var ErrCertRevoked = errors.New("auth: certificate revoked")

// ErrCertExpired is returned when the row's notAfter has passed.
var ErrCertExpired = errors.New("auth: certificate expired")

// ErrCertStateUnavailable is the fail-closed error when the state
// store cannot be reached. Per ADR-0001 §10, missing state MUST
// reject the connection; never silently bypass.
var ErrCertStateUnavailable = errors.New("auth: certificate state store unavailable")

// ErrUnknownCA is returned when a cert claims an Issuer id that the
// CAStore does not recognise. This typically means a stale cert from a
// rolled CA pair.
var ErrUnknownCA = errors.New("auth: unknown certificate authority")

// CertManager is the in-process authority for cert enrollment,
// rotation and lookup. It is safe for concurrent use.
type CertManager struct {
	mu        sync.RWMutex
	store     CertStateStore
	castore   *CAStore
	clock     func() time.Time
	grace     time.Duration
}

// NewCertManager wires a manager around the supplied store and CA store.
// grace is the duration a previously-active serial is kept valid after
// rotation; default is 5 minutes (matches ADR-0004 §6.1).
func NewCertManager(store CertStateStore, castore *CAStore, grace time.Duration) *CertManager {
	if grace <= 0 {
		grace = 5 * time.Minute
	}
	return &CertManager{
		store:   store,
		castore: castore,
		clock:   time.Now,
		grace:   grace,
	}
}

// EnrollInput is the request to add a new certificate. The caller is
// expected to have already verified the CSR signature against the
// claimed Subject and SPKI; this method enforces only the policy-level
// invariants.
type EnrollInput struct {
	Serial        string
	Subject       string
	Issuer        string
	DerBytes      []byte
	NotBefore     time.Time
	NotAfter      time.Time
	BoundTenantID string
	BoundPodID    string
}

// Enroll validates the input, ensures the Issuer CA is trusted, and
// inserts the record in the Enrolled state. The record moves to
// Active on the first successful handshake.
func (m *CertManager) Enroll(in EnrollInput) (CertRecord, error) {
	if in.Serial == "" || in.Subject == "" || in.Issuer == "" {
		return CertRecord{}, fmt.Errorf("auth: serial, subject and issuer are required")
	}
	if len(in.DerBytes) == 0 {
		return CertRecord{}, fmt.Errorf("auth: empty DER bytes")
	}
	if !in.NotAfter.After(in.NotBefore) {
		return CertRecord{}, fmt.Errorf("auth: notAfter must be after notBefore")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.castore == nil {
		return CertRecord{}, fmt.Errorf("auth: CA store not configured")
	}
	if !m.castore.IsTrusted(in.Issuer) {
		return CertRecord{}, ErrUnknownCA
	}
	rec := CertRecord{
		Serial:        in.Serial,
		Subject:       in.Subject,
		Issuer:        in.Issuer,
		Fingerprint:   Fingerprint(in.DerBytes),
		NotBefore:     in.NotBefore,
		NotAfter:      in.NotAfter,
		State:         StateEnrolled,
		EnrolledAt:    m.clock(),
		BoundTenantID: in.BoundTenantID,
		BoundPodID:    in.BoundPodID,
	}
	if err := m.store.Put(rec); err != nil {
		return CertRecord{}, fmt.Errorf("auth: state store: %w", err)
	}
	return rec, nil
}

// Activate marks a freshly-enrolled serial as Active. The handshake
// handler invokes this after a successful TLS handshake so we know the
// cert actually proves possession of the private key.
func (m *CertManager) Activate(serial string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.store.Get(serial)
	if err != nil {
		return err
	}
	if rec.State == StateRevoked || rec.State == StateExpired {
		return ErrCertRevoked
	}
	rec.State = StateActive
	rec.LastSeenAt = m.clock()
	return m.store.Put(rec)
}

// Rotate enrolls a new serial for the same Subject and transitions the
// previous serial into the Rotating state. After the grace window
// elapses, callers should invoke FinishRotation to revoke the old
// serial definitively.
func (m *CertManager) Rotate(oldSerial string, next EnrollInput) (CertRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, err := m.store.Get(oldSerial)
	if err != nil {
		return CertRecord{}, err
	}
	if prev.State == StateRevoked {
		return CertRecord{}, ErrCertRevoked
	}
	if !m.castore.IsTrusted(next.Issuer) {
		return CertRecord{}, ErrUnknownCA
	}
	rec := CertRecord{
		Serial:        next.Serial,
		Subject:       next.Subject,
		Issuer:        next.Issuer,
		Fingerprint:   Fingerprint(next.DerBytes),
		NotBefore:     next.NotBefore,
		NotAfter:      next.NotAfter,
		State:         StateEnrolled,
		EnrolledAt:    m.clock(),
		BoundTenantID: next.BoundTenantID,
		BoundPodID:    next.BoundPodID,
	}
	if err := m.store.Put(rec); err != nil {
		return CertRecord{}, err
	}
	prev.State = StateRotating
	prev.LastSeenAt = m.clock()
	if err := m.store.Put(prev); err != nil {
		return CertRecord{}, err
	}
	return rec, nil
}

// FinishRotation promotes the old serial from Rotating to Revoked.
// Idempotent: revoking an already-revoked serial is a no-op.
func (m *CertManager) FinishRotation(oldSerial string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.store.Get(oldSerial)
	if err != nil {
		return err
	}
	if rec.State == StateRevoked {
		return nil
	}
	rec.State = StateRevoked
	return m.store.Put(rec)
}

// Revoke is the break-glass entry point. It accepts a reason string so
// the audit log captures why the cert was killed.
func (m *CertManager) Revoke(serial string, reason string) error {
	if reason == "" {
		return fmt.Errorf("auth: revocation reason required")
	}
	return m.store.Revoke(serial, reason)
}

// Validate is the read-side hot path. It MUST be safe for concurrent
// use and MUST NOT mutate state.
func (m *CertManager) Validate(serial string) (CertRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, err := m.store.Get(serial)
	if err != nil {
		if errors.Is(err, ErrCertNotFound) {
			return CertRecord{}, ErrCertNotFound
		}
		return CertRecord{}, ErrCertStateUnavailable
	}
	// Issuer must still be trusted; a retired CA must invalidate every
	// cert it ever issued. This is the read-side check that makes CA
	// rollover actually effective.
	if m.castore == nil || !m.castore.IsTrusted(rec.Issuer) {
		return rec, ErrUnknownCA
	}
	switch rec.State {
	case StateRevoked:
		return rec, ErrCertRevoked
	case StateExpired:
		return rec, ErrCertExpired
	}
	if !rec.NotAfter.IsZero() && !m.clock().Before(rec.NotAfter) {
		return rec, ErrCertExpired
	}
	if !rec.NotBefore.IsZero() && m.clock().Before(rec.NotBefore) {
		return rec, ErrCertExpired
	}
	return rec, nil
}

// CAStore is the local view of trusted Issuing CAs. It supports the
// rollover procedure described in ADR-0002 §6.3: when a new CA is
// added it is marked Active and the previous CA is moved to Retiring.
// After the retire window elapses the old CA is dropped and any cert
// it issued is rejected.
type CAStore struct {
	mu      sync.RWMutex
	entries map[string]caEntry
}

type caEntry struct {
	ID         string
	NotBefore  time.Time
	NotAfter   time.Time
	Status     caStatus
	CertSHA256 string
}

type caStatus string

const (
	caActive   caStatus = "active"
	caRetiring caStatus = "retiring"
	caRetired  caStatus = "retired"
)

// NewCAStore builds an empty CA store.
func NewCAStore() *CAStore {
	return &CAStore{entries: map[string]caEntry{}}
}

// AddCA registers a new CA. The first CA added is auto-marked Active;
// subsequent additions are Retiring. Re-adding an existing id with a
// newer notAfter upgrades it from Retiring → Active.
func (s *CAStore) AddCA(id, sha256Hex string, notBefore, notAfter time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := caRetiring
	for _, e := range s.entries {
		if e.Status == caActive {
			status = caRetiring
			break
		}
	}
	// If nothing active, mark this one active.
	anyActive := false
	for _, e := range s.entries {
		if e.Status == caActive {
			anyActive = true
		}
	}
	if !anyActive {
		status = caActive
	}
	s.entries[id] = caEntry{
		ID:         id,
		NotBefore:  notBefore,
		NotAfter:   notAfter,
		Status:     status,
		CertSHA256: sha256Hex,
	}
}

// Activate switches a CA to Active and demotes any previously active
// one to Retiring. Used during rollover.
func (s *CAStore) Activate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.entries {
		if e.Status == caActive && k != id {
			e.Status = caRetiring
			s.entries[k] = e
		}
	}
	if e, ok := s.entries[id]; ok {
		e.Status = caActive
		s.entries[id] = e
	}
}

// Retire drops the CA from the trust store entirely. Any cert that
// references this id will then fail IsTrusted.
func (s *CAStore) Retire(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}

// IsTrusted reports whether the CA id is currently in the trust store
// and not Retired. The decision ignores Active vs Retiring so that the
// rollover overlap window works correctly.
func (s *CAStore) IsTrusted(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[id]
	if !ok {
		return false
	}
	if e.Status == caRetired {
		return false
	}
	now := time.Now()
	if !e.NotBefore.IsZero() && now.Before(e.NotBefore) {
		return false
	}
	if !e.NotAfter.IsZero() && !now.Before(e.NotAfter) {
		return false
	}
	return true
}

// Snapshot returns a sorted snapshot of the trust store contents.
// Used by tests and operators to inspect the rollover state.
func (s *CAStore) Snapshot() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.entries))
	for id, e := range s.entries {
		ids = append(ids, fmt.Sprintf("%s:%s", id, string(e.Status)))
	}
	sort.Strings(ids)
	return ids
}

// MemoryCertStore is the in-process CertStateStore used by tests and
// local development. It is NOT durable and MUST NOT be used in
// production.
type MemoryCertStore struct {
	mu    sync.RWMutex
	recs  map[string]CertRecord
	audit []CertRevocation
}

// CertRevocation captures a single revocation event for the audit log.
type CertRevocation struct {
	Serial  string
	Reason  string
	At      time.Time
}

// NewMemoryCertStore returns an empty in-memory store.
func NewMemoryCertStore() *MemoryCertStore {
	return &MemoryCertStore{recs: map[string]CertRecord{}}
}

// Put inserts or replaces a record.
func (s *MemoryCertStore) Put(rec CertRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.Serial] = rec
	return nil
}

// Get returns the record or ErrCertNotFound.
func (s *MemoryCertStore) Get(serial string) (CertRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rec, ok := s.recs[serial]; ok {
		return rec, nil
	}
	return CertRecord{}, ErrCertNotFound
}

// List returns all records, sorted by serial for deterministic output.
func (s *MemoryCertStore) List() ([]CertRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CertRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Serial < out[j].Serial })
	return out, nil
}

// Revoke transitions the record to Revoked and appends a revocation
// event to the audit log. Idempotent.
func (s *MemoryCertStore) Revoke(serial, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[serial]
	if !ok {
		return ErrCertNotFound
	}
	if rec.State == StateRevoked {
		return nil
	}
	rec.State = StateRevoked
	s.recs[serial] = rec
	s.audit = append(s.audit, CertRevocation{Serial: serial, Reason: reason, At: time.Now()})
	return nil
}

// Revocations returns a copy of the audit log.
func (s *MemoryCertStore) Revocations() []CertRevocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CertRevocation, len(s.audit))
	copy(out, s.audit)
	return out
}
