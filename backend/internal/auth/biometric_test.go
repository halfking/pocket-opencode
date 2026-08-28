package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// TestBiometricStoreInitNilPool 验证 nil pool 时 Init 返 not configured（PG schema 创建失败）。
//
// 注意：nil pool 下 CRUD 操作会降级到内存 map，不再返回 ErrBiometricNotConfigured。
// 这里只验证 Init 行为（启动期应当 fail-fast）。
func TestBiometricStoreInitNilPool(t *testing.T) {
	s := NewBiometricStore(nil)
	if err := s.Init(context.Background()); err == nil {
		t.Fatal("Init on nil pool should error")
	}
}

// TestBiometricStoreMemoryFallback 验证 nil pool 下 CRUD 走内存路径。
func TestBiometricStoreMemoryFallback(t *testing.T) {
	s := NewBiometricStore(nil)
	ctx := context.Background()
	c := &BiometricCredential{
		ID:          "cred-1",
		UserID:      "u-1",
		WorkspaceID: "w-1",
		DeviceName:  "test",
		PublicKey:   []byte("pk"),
		Counter:     0,
	}
	if err := s.Register(ctx, c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := s.Get(ctx, "cred-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u-1" || got.WorkspaceID != "w-1" {
		t.Errorf("Get returned wrong fields: %+v", got)
	}
	// Get 不可达 credential
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrBiometricNotFound) {
		t.Errorf("expected ErrBiometricNotFound, got %v", err)
	}
	// ListByUser
	list, err := s.ListByUser(ctx, "u-1", "w-1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 credential, got %d", len(list))
	}
	// Touch
	if err := s.Touch(ctx, "cred-1", 5); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, _ = s.Get(ctx, "cred-1")
	if got.Counter != 5 {
		t.Errorf("expected counter=5, got %d", got.Counter)
	}
	// Delete
	ok, err := s.Delete(ctx, "cred-1")
	if err != nil || !ok {
		t.Fatalf("Delete: ok=%v err=%v", ok, err)
	}
	if _, err := s.Get(ctx, "cred-1"); !errors.Is(err, ErrBiometricNotFound) {
		t.Errorf("expected ErrBiometricNotFound after delete, got %v", err)
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
