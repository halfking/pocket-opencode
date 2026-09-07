package redclaw

import "testing"

func TestDecodeRedClawError_NestedGatewayJSON(t *testing.T) {
	body := []byte(`{"error":{"code":"not_found","message":"resource not found"}}`)
	err := decodeRedClawError(404, body)
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if got != "RedClaw HTTP 404 (not_found): resource not found" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeRedClawError_FlatLegacyJSON(t *testing.T) {
	body := []byte(`{"code":401,"message":"invalid token"}`)
	err := decodeRedClawError(401, body)
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if got != "RedClaw error (code=401): invalid token" {
		t.Fatalf("got %q", got)
	}
}
