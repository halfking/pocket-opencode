package acchttp

// signer_test.go — the Authorization header must come from the SIGNER
// when one is configured.
//
// The regression behind this (2026-10-22, live local ACC): ACC's
// /api/v2/* surface accepts ONLY an HS256-signed service JWT; a static
// bearer token is answered with 401 "invalid bearer token". So a client
// that can only send a static token is permanently 401 against that
// surface.
//
// The ordering is the contract: when both a Signer and an APIKey are
// configured, the SIGNER wins. The reverse order would send the static
// token, get 401, and leave a perfectly good signature sitting unused in
// the configuration — a failure that reads like a server problem.
//
// A signer that ERRORS must fail the request, not degrade to an
// unauthenticated (or static-token) request: on a fail-closed permission
// gate, "the token could not be minted" has to stop the request, because
// the alternative is exactly the bypass the gate exists to prevent.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fixedSigner struct {
	token string
	err   error
}

func (s fixedSigner) Token() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

func TestSignerTakesPriorityOverStaticAPIKey(t *testing.T) {
	var captured string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c, err := New(Config{
		BaseURL: ts.URL,
		APIKey:  "static-token-abc",
		Signer:  fixedSigner{token: "signed.jwt.value"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), "/probe", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "Bearer signed.jwt.value"; captured != want {
		t.Errorf("Authorization = %q, want %q (the signer must win over the static key)", captured, want)
	}
}

func TestStaticAPIKeyStillUsedWithoutSigner(t *testing.T) {
	var captured string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c, err := New(Config{BaseURL: ts.URL, APIKey: "static-token-abc"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), "/probe", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "Bearer static-token-abc"; captured != want {
		t.Errorf("Authorization = %q, want %q", captured, want)
	}
}

func TestSignerErrorFailsTheRequest(t *testing.T) {
	reached := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	boom := errors.New("signing failed")
	c, err := New(Config{
		BaseURL: ts.URL,
		APIKey:  "static-token-abc",
		Signer:  fixedSigner{err: boom},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), "/probe", nil); err == nil {
		t.Fatal("expected the request to fail when the signer errors")
	}
	if reached {
		t.Error("the request reached the server — a signing failure must not fall back to another credential")
	}
}

func TestSignerReturningEmptyTokenFailsTheRequest(t *testing.T) {
	reached := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(Config{BaseURL: ts.URL, Signer: fixedSigner{token: "   "}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), "/probe", nil); err == nil {
		t.Fatal("expected the request to fail on an empty signed token")
	}
	if reached {
		t.Error("the request reached the server — an empty token must not become an unauthenticated request")
	}
}

func TestSignerIsUsedOnEveryRequest(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c, err := New(Config{BaseURL: ts.URL, Signer: fixedSigner{token: "signed.jwt.value"}, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "/probe", nil); err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d requests, want 3", len(seen))
	}
	for i, h := range seen {
		if h != "Bearer signed.jwt.value" {
			t.Errorf("request %d Authorization = %q", i, h)
		}
	}
}
