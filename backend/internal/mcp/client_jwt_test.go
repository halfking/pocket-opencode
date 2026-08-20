package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestNewClientWithAuth_DefaultScopes 验证缺省 scopes 回退为 tasks,sessions，
// 且 tenantID 被正确保存（供签发的 JWT claim 使用）。
func TestNewClientWithAuth_DefaultScopes(t *testing.T) {
	c := NewClientWithAuth("http://x", "secret", "tenant-1", nil, false)
	if len(c.scopes) != 2 || c.scopes[0] != "tasks" || c.scopes[1] != "sessions" {
		t.Fatalf("default scopes should be [tasks sessions], got %v", c.scopes)
	}
	if c.tenantID != "tenant-1" {
		t.Fatalf("tenantID not stored: %q", c.tenantID)
	}
	if c.secret != "secret" {
		t.Fatalf("secret not stored")
	}
}

// TestNewClient_WrapperDefaults 验证旧 NewClient 包装仍可用：
// 把 apiKey 当作 secret，tenant 留空、scopes 走默认。
func TestNewClient_WrapperDefaults(t *testing.T) {
	c := NewClient("http://x", "legacy-key", false)
	if c.secret != "legacy-key" {
		t.Fatalf("NewClient must treat apiKey as secret, got %q", c.secret)
	}
	if len(c.scopes) != 2 {
		t.Fatalf("wrapper must default scopes, got %v", c.scopes)
	}
}

// captureACC 起最小 MCP 服务，记录收到的 Authorization 头。
func captureACC(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "sess-test")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": `{"id":"wi-1"}`}}}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &authz
}

// TestClient_SendsHS256JWTWithClaims 验证写调用（CreateTask）确实把 HMAC 内部
// JWT 作为 Bearer 发送，且 claims 含非空 tenant_id 与 scopes={tasks,sessions}，
// 算法为 HS256——与 ACC auth.ResolveToken / scopeAllowed 期望一致。
func TestClient_SendsHS256JWTWithClaims(t *testing.T) {
	srv, authz := captureACC(t)
	c := NewClientWithAuth(srv.URL, "topsecret", "tenant-wake", []string{"tasks", "sessions"}, false)

	if _, err := c.CreateTask(context.Background(), map[string]any{"title": "t"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !strings.HasPrefix(*authz, "Bearer ") {
		t.Fatalf("Authorization must be Bearer, got %q", *authz)
	}
	token := strings.TrimPrefix(*authz, "Bearer ")

	parsed := &mcpClaims{}
	got, err := jwt.ParseWithClaims(token, parsed, func(tok *jwt.Token) (interface{}, error) {
		if tok.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			t.Fatalf("unexpected alg %s", tok.Method.Alg())
		}
		return []byte("topsecret"), nil
	})
	if err != nil {
		t.Fatalf("parse JWT: %v", err)
	}
	if !got.Valid {
		t.Fatal("token invalid")
	}
	if parsed.TenantID != "tenant-wake" {
		t.Fatalf("tenant_id claim = %q, want tenant-wake", parsed.TenantID)
	}
	scopes, ok := parsed.Custom["scopes"].([]any)
	if !ok || len(scopes) != 2 {
		t.Fatalf("Custom.scopes = %v, want [tasks sessions]", parsed.Custom["scopes"])
	}
	if scopes[0] != "tasks" || scopes[1] != "sessions" {
		t.Fatalf("scopes = %v", scopes)
	}
}
