package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// TestBiometricStoreErrorsOnNilPool 验证 nil pool 时 store 返 not configured。
func TestBiometricStoreErrorsOnNilPool(t *testing.T) {
	s := NewBiometricStore(nil)
	if err := s.Init(context.Background()); err == nil {
		t.Fatal("Init on nil pool should error")
	}
	if _, err := s.Get(context.Background(), "x"); !errors.Is(err, ErrBiometricNotConfigured) {
		t.Fatalf("expected ErrBiometricNotConfigured, got %v", err)
	}
	if _, err := s.ListByUser(context.Background(), "u", "w"); !errors.Is(err, ErrBiometricNotConfigured) {
		t.Fatalf("expected ErrBiometricNotConfigured, got %v", err)
	}
}

// TestNewChallengeID 验证 challenge 生成是 base64url、无 padding、长度合理。
func TestNewChallengeID(t *testing.T) {
	id, err := NewChallengeID()
	if err != nil {
		t.Fatalf("NewChallengeID: %v", err)
	}
	// 32 字节 base64url（无 padding）长度应为 43。
	if len(id) != 43 {
		t.Fatalf("unexpected challenge length: %d (want 43)", len(id))
	}
	if strings.ContainsAny(id, "+/=") {
		t.Fatalf("challenge must be base64url (no + / or =): %q", id)
	}
	// 应能解回原 32 字节
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded length: %d (want 32)", len(decoded))
	}
	// 两次生成应不同
	id2, _ := NewChallengeID()
	if id == id2 {
		t.Fatal("two challenges must differ")
	}
}
