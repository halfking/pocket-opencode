package redclaw

import (
	"testing"
)

func TestTenantContextFromJWT(t *testing.T) {
	claims := map[string]interface{}{
		"sub":       "user-123",
		"tenant_id": "pocket-enterprise",
		"role":      "developer",
	}

	ctx := ExtractTenantContext(claims)
	if ctx == nil {
		t.Fatal("expected non-nil tenant context")
	}
	if ctx.TenantID != "pocket-enterprise" {
		t.Errorf("expected tenant_id pocket-enterprise, got %s", ctx.TenantID)
	}
	if ctx.UserID != "user-123" {
		t.Errorf("expected user_id user-123, got %s", ctx.UserID)
	}
	if ctx.Role != "developer" {
		t.Errorf("expected role developer, got %s", ctx.Role)
	}
}

func TestTenantContextFromJWT_MissingTenant(t *testing.T) {
	claims := map[string]interface{}{
		"sub":  "user-123",
		"role": "developer",
	}

	ctx := ExtractTenantContext(claims)
	if ctx == nil {
		t.Fatal("expected non-nil tenant context")
	}
	if ctx.TenantID != "default" {
		t.Errorf("expected default tenant_id, got %s", ctx.TenantID)
	}
}

func TestTenantContextFromJWT_NilClaims(t *testing.T) {
	ctx := ExtractTenantContext(nil)
	if ctx == nil {
		t.Fatal("expected non-nil tenant context")
	}
	if ctx.TenantID != "default" {
		t.Errorf("expected default tenant_id, got %s", ctx.TenantID)
	}
}

func TestAttachTenantHeaders(t *testing.T) {
	ctx := &TenantContext{
		TenantID: "pocket-enterprise",
		UserID:   "user-123",
		Role:     "developer",
	}

	headers, err := AttachTenantHeaders(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if headers["X-Tenant-ID"] != "pocket-enterprise" {
		t.Errorf("expected X-Tenant-ID pocket-enterprise, got %s", headers["X-Tenant-ID"])
	}
	if headers["X-User-ID"] != "user-123" {
		t.Errorf("expected X-User-ID user-123, got %s", headers["X-User-ID"])
	}
}

func TestAttachTenantHeaders_NilContext(t *testing.T) {
	_, err := AttachTenantHeaders(nil)
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestTenantContextFromJWT_EmptyTenantString(t *testing.T) {
	claims := map[string]interface{}{
		"sub":       "user-123",
		"tenant_id": "",
		"role":      "developer",
	}

	ctx := ExtractTenantContext(claims)
	if ctx.TenantID != "default" {
		t.Errorf("expected default tenant_id for empty string, got %s", ctx.TenantID)
	}
}

func TestTenantContextFromJWT_InvalidTypes(t *testing.T) {
	claims := map[string]interface{}{
		"sub":       123, // should be string
		"tenant_id": "tenant-1",
		"role":      "developer",
	}

	ctx := ExtractTenantContext(claims)
	if ctx.UserID != "" {
		t.Errorf("expected empty UserID for invalid type, got %s", ctx.UserID)
	}
}