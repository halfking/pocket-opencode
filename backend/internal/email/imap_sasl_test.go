package email

// Tests for the IMAP XOAUTH2 / OAUTHBEARER SASL payload builders.
//
// References:
//   - https://developers.google.com/gmail/imap/xoauth2-protocol
//   - RFC 4959 (IMAP Extension for Simple SASL)
//   - RFC 7628 (A Set of Simple Authentication and Security Layer (SASL) Mechanisms for OAuth)

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildOAuthBearerPayloadEmptyUser(t *testing.T) {
	payload := buildOAuthBearerPayload("", "ya29.token")
	// With an empty username we emit `n,,` (no `a=` block) — IMAP servers
	// tolerate this and use the empty authzid.
	want := "n,,\x01auth=Bearer ya29.token\x01\x01"
	if payload != want {
		t.Fatalf("payload = %q, want %q", payload, want)
	}
}

func TestBuildOAuthBearerPayloadWithUser(t *testing.T) {
	payload := buildOAuthBearerPayload("user@example.com", "tok")
	want := "n,a=user@example.com,\x01auth=Bearer tok\x01\x01"
	if payload != want {
		t.Fatalf("payload = %q, want %q", payload, want)
	}
}

func TestOAuthBearerClientStart(t *testing.T) {
	c := NewOAuthBearerClient("user@example.com", "tok123", "imap.example.com", 993)
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mech != "OAUTHBEARER" {
		t.Fatalf("mech = %q, want OAUTHBEARER", mech)
	}
	got := string(ir)
	if !strings.HasPrefix(got, "n,a=user@example.com,") {
		t.Fatalf("missing authzid prefix: %s", got)
	}
	if !strings.Contains(got, "host=imap.example.com") {
		t.Fatalf("missing host: %s", got)
	}
	if !strings.Contains(got, "port=993") {
		t.Fatalf("missing port: %s", got)
	}
	if !strings.HasSuffix(got, "\x01auth=Bearer tok123\x01\x01") {
		t.Fatalf("missing trailing auth block: %s", got)
	}
}

func TestOAuthBearerClientEmptyToken(t *testing.T) {
	c := NewOAuthBearerClient("user@example.com", "", "", 0)
	if _, _, err := c.Start(); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestXOAuth2ClientStart(t *testing.T) {
	c := NewXOAuth2Client("user@example.com", "abc")
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Fatalf("mech = %q, want XOAUTH2", mech)
	}
	got := string(ir)
	want := "n,a=user@example.com,\x01auth=Bearer abc\x01\x01"
	if got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestXOAuth2ClientNextDecodesServerChallenge(t *testing.T) {
	c := NewXOAuth2Client("user@example.com", "abc")
	encoded := base64.StdEncoding.EncodeToString([]byte("{\"status\":\"invalid_token\"}"))
	if _, err := c.Next([]byte(encoded)); err == nil || !strings.Contains(err.Error(), "invalid_token") {
		t.Fatalf("expected invalid_token error, got %v", err)
	}
}

func TestXOAuth2ClientNextAcceptsEmpty(t *testing.T) {
	c := NewXOAuth2Client("user@example.com", "abc")
	out, err := c.Next(nil)
	if err != nil || out != nil {
		t.Fatalf("empty challenge should be no-op, got out=%v err=%v", out, err)
	}
}