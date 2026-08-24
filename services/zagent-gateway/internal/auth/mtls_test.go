package auth

import (
	"errors"
	"sort"
	"testing"
	"time"
)

// helpers -------------------------------------------------------------------

func newCertManager(t *testing.T) (*CertManager, *MemoryCertStore, *CAStore) {
	t.Helper()
	store := NewMemoryCertStore()
	cas := NewCAStore()
	cas.AddCA("ca-prod-2026-q3", "deadbeef", time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour))
	return NewCertManager(store, cas, time.Minute), store, cas
}

func sampleEnroll(serial string) EnrollInput {
	return EnrollInput{
		Serial:        serial,
		Subject:       "CN=pod-1,OU=pod",
		Issuer:        "ca-prod-2026-q3",
		DerBytes:      []byte("not-a-real-der-but-good-enough-for-fixture"),
		NotBefore:     time.Now().Add(-time.Minute),
		NotAfter:      time.Now().Add(24 * time.Hour),
		BoundTenantID: "tenant-1",
		BoundPodID:    "pod-1",
	}
}

// tests ---------------------------------------------------------------------

func TestEnrollAndValidateHappyPath(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	rec, err := mgr.Enroll(sampleEnroll("01"))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if rec.State != StateEnrolled {
		t.Fatalf("expected Enrolled, got %q", rec.State)
	}
	if err := mgr.Activate("01"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	got, err := mgr.Validate("01")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !got.Valid() {
		t.Fatalf("expected valid cert")
	}
}

func TestRevokedCertRejected(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	if _, err := mgr.Enroll(sampleEnroll("01")); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := mgr.Activate("01"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := mgr.Revoke("01", "compromised"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := mgr.Validate("01"); !errorsIs(err, ErrCertRevoked) {
		t.Fatalf("expected ErrCertRevoked, got %v", err)
	}
}

func TestExpiredCertRejected(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	in := sampleEnroll("01")
	in.NotBefore = time.Now().Add(-2 * time.Hour)
	in.NotAfter = time.Now().Add(-time.Hour)
	if _, err := mgr.Enroll(in); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := mgr.Activate("01"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := mgr.Validate("01"); !errorsIs(err, ErrCertExpired) {
		t.Fatalf("expected ErrCertExpired, got %v", err)
	}
}

func TestRotationKeepsOldSerialValidDuringGrace(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	if _, err := mgr.Enroll(sampleEnroll("01")); err != nil {
		t.Fatalf("enroll1: %v", err)
	}
	if err := mgr.Activate("01"); err != nil {
		t.Fatalf("activate1: %v", err)
	}
	if _, err := mgr.Rotate("01", sampleEnroll("02")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := mgr.Activate("02"); err != nil {
		t.Fatalf("activate2: %v", err)
	}
	// Old serial must still verify during grace.
	if _, err := mgr.Validate("01"); err != nil {
		t.Fatalf("expected old serial to verify during grace, got %v", err)
	}
	// New serial must also verify.
	if _, err := mgr.Validate("02"); err != nil {
		t.Fatalf("expected new serial to verify, got %v", err)
	}
}

func TestFinishRotationRevokesOldSerial(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	if _, err := mgr.Enroll(sampleEnroll("01")); err != nil {
		t.Fatalf("enroll1: %v", err)
	}
	if err := mgr.Activate("01"); err != nil {
		t.Fatalf("activate1: %v", err)
	}
	if _, err := mgr.Rotate("01", sampleEnroll("02")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := mgr.FinishRotation("01"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := mgr.Validate("01"); !errorsIs(err, ErrCertRevoked) {
		t.Fatalf("expected ErrCertRevoked after finish, got %v", err)
	}
}

func TestCARolloverAcceptsBothCAs(t *testing.T) {
	mgr, _, cas := newCertManager(t)
	cas.AddCA("ca-prod-2026-q4", "f00dface", time.Now().Add(-time.Minute), time.Now().Add(365*24*time.Hour))
	// The new AddCA call promoted the second one to Retiring because
	// there was already an Active entry. Activate it now.
	cas.Activate("ca-prod-2026-q4")

	inOld := sampleEnroll("01")
	inNew := sampleEnroll("02")
	inNew.Issuer = "ca-prod-2026-q4"
	if _, err := mgr.Enroll(inOld); err != nil {
		t.Fatalf("enroll old: %v", err)
	}
	if _, err := mgr.Enroll(inNew); err != nil {
		t.Fatalf("enroll new: %v", err)
	}
	// Both must validate.
	if _, err := mgr.Validate("01"); err != nil {
		t.Fatalf("validate old: %v", err)
	}
	if _, err := mgr.Validate("02"); err != nil {
		t.Fatalf("validate new: %v", err)
	}

	// Now retire the old CA. Certs from the retired CA must be
	// rejected.
	cas.Retire("ca-prod-2026-q3")
	if _, err := mgr.Validate("01"); !errorsIs(err, ErrUnknownCA) && !errorsIs(err, ErrCertRevoked) {
		t.Fatalf("expected retired-CA rejection, got %v", err)
	}
}

func TestUnknownIssuerRejected(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	in := sampleEnroll("01")
	in.Issuer = "ca-not-trusted"
	if _, err := mgr.Enroll(in); !errorsIs(err, ErrUnknownCA) {
		t.Fatalf("expected ErrUnknownCA, got %v", err)
	}
}

func TestStateStoreUnavailableFailsClosed(t *testing.T) {
	mgr := NewCertManager(unavailableStore{}, NewCAStore(), time.Minute)
	if _, err := mgr.Enroll(sampleEnroll("01")); err == nil {
		t.Fatalf("expected error when state store unavailable")
	}
	if _, err := mgr.Validate("01"); err == nil {
		t.Fatalf("expected error when state store unavailable")
	}
}

func TestRevokeRequiresReason(t *testing.T) {
	mgr, _, _ := newCertManager(t)
	if _, err := mgr.Enroll(sampleEnroll("01")); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := mgr.Revoke("01", ""); err == nil {
		t.Fatalf("expected revocation to require a reason")
	}
}

func TestListSorted(t *testing.T) {
	store := NewMemoryCertStore()
	mgr, _, _ := newCertManager(t)
	rec, err := mgr.Enroll(sampleEnroll("b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(rec); err != nil {
		t.Fatal(err)
	}
	rec, err = mgr.Enroll(sampleEnroll("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(rec); err != nil {
		t.Fatal(err)
	}
	rec, err = mgr.Enroll(sampleEnroll("c"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(rec); err != nil {
		t.Fatal(err)
	}
	recs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	got := []string{recs[0].Serial, recs[1].Serial, recs[2].Serial}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("expected sorted, got %v", got)
	}
}

// fixtures ------------------------------------------------------------------

type unavailableStore struct{}

func (unavailableStore) Put(CertRecord) error                  { return errors.New("down") }
func (unavailableStore) Get(string) (CertRecord, error)        { return CertRecord{}, errors.New("down") }
func (unavailableStore) List() ([]CertRecord, error)           { return nil, errors.New("down") }
func (unavailableStore) Revoke(string, string) error           { return errors.New("down") }
