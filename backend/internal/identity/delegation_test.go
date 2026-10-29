package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var delegationTestSecret = []byte("delegation-contract-test-secret-32-bytes")

func delegationTestClaims(now time.Time) DelegationClaims {
	return DelegationClaims{TenantID: "tenant-a", Actor: "redclaw.admin", Scope: "workspace-membership:read", Method: "GET", Path: "/internal/workspace-authority/workspaces", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "redclaw", Subject: "user-a", Audience: jwt.ClaimStrings{"pocket-workspace-authority"}, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Second)),
	}}
}

func signDelegationForTest(t *testing.T, c DelegationClaims, domainSeparated bool) string {
	t.Helper()
	key := delegationTestSecret
	if domainSeparated {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("openpocket-workspace-delegation:v1"))
		key = mac.Sum(nil)
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWorkspaceDelegationBindsRequestAndTrustDomain(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	base := delegationTestClaims(now)
	raw := signDelegationForTest(t, base, true)
	got, err := VerifyDelegation(delegationTestSecret, raw, "tenant-a", "GET", base.Path, now)
	if err != nil || got.Subject != "user-a" {
		t.Fatalf("valid delegation: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*DelegationClaims)
	}{
		{"issuer", func(c *DelegationClaims) { c.Issuer = "pocket" }},
		{"audience", func(c *DelegationClaims) { c.Audience = jwt.ClaimStrings{"pocket-api"} }},
		{"multiple audiences", func(c *DelegationClaims) { c.Audience = append(c.Audience, "pocket-api") }},
		{"scope", func(c *DelegationClaims) { c.Scope = "admin" }},
		{"actor", func(c *DelegationClaims) { c.Actor = "browser" }},
		{"tenant", func(c *DelegationClaims) { c.TenantID = "tenant-b" }},
		{"path", func(c *DelegationClaims) { c.Path += "/other/binding" }},
		{"method", func(c *DelegationClaims) { c.Method = "POST" }},
		{"expired", func(c *DelegationClaims) { c.ExpiresAt = jwt.NewNumericDate(now) }},
		{"long ttl", func(c *DelegationClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute)) }},
		{"missing nonce", func(c *DelegationClaims) { c.ID = "" }},
		{"missing issued at", func(c *DelegationClaims) { c.IssuedAt = nil }},
		{"missing not before", func(c *DelegationClaims) { c.NotBefore = nil }},
		{"future", func(c *DelegationClaims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Second)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			if _, err := VerifyDelegation(delegationTestSecret, signDelegationForTest(t, c, true), "tenant-a", "GET", base.Path, now); err == nil {
				t.Fatal("invalid delegation accepted")
			}
		})
	}
	if _, err := VerifyDelegation(delegationTestSecret, signDelegationForTest(t, base, false), "tenant-a", "GET", base.Path, now); err == nil {
		t.Fatal("ordinary shared-secret JWT accepted as delegated request")
	}
}

func TestWorkspaceDelegationNonceIsAtomicAndPersistent(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	now := time.Now().Truncate(time.Second)
	claims := delegationTestClaims(now)
	var accepted atomic.Int32
	done := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() {
			err := store.ConsumeDelegation(context.Background(), &claims, now)
			if err == nil {
				accepted.Add(1)
			}
			done <- err
		}()
	}
	for i := 0; i < 12; i++ {
		err := <-done
		if err != nil && err != ErrDelegationReplay {
			t.Fatal(err)
		}
	}
	if accepted.Load() != 1 {
		t.Fatalf("same nonce accepted %d times", accepted.Load())
	}
	restarted := &Store{pool: store.pool}
	if err := restarted.ConsumeDelegation(context.Background(), &claims, now); err != ErrDelegationReplay {
		t.Fatalf("nonce lost across store instances: %v", err)
	}
}
