package memora

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// newFakeMemora 起一个最小 memora 假服务：记录收到的 Authorization 头与
// X-Project-Id，按路径回假响应。返回 (server, 录制器)。
type recorded struct {
	authHeader string
	projectID  string
	tenantID   string
	addBody    map[string]any
	searchBody map[string]any
}

func newFakeMemora(t *testing.T, status int, body string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		rec.projectID = r.Header.Get("X-Project-Id")
		rec.tenantID = r.Header.Get("X-Tenant-Id")
		_ = json.NewDecoder(r.Body).Decode(&rec.addBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mem_123","content":"hello"}`))
	})
	mux.HandleFunc("/api/search/hybrid", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&rec.searchBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"results":[{"id":"m1","memory":"note body","score":0.9,"bm25_score":0.5,"rrf_score":0.03,"final_score":0.7}],"strategy":"bm25_rrf_mmr","total":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestNewClientEmptyBaseURL(t *testing.T) {
	if c := NewClient("", "", "", "", "", "", time.Second); c != nil {
		t.Fatalf("空 baseURL 应返回 nil（未配置降级），got %v", c)
	}
}

func TestAddAndSearchWithJWT(t *testing.T) {
	srv, rec := newFakeMemora(t, http.StatusOK, `{"status":"ok"}`)
	c := NewClient(srv.URL, "", "secret-123", "tenant-a", "proj-b", "pocketd", 5*time.Second)
	if c == nil {
		t.Fatal("client 不应为 nil")
	}
	ctx := context.Background()
	if err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if !strings.HasPrefix(rec.authHeader, "Bearer ") {
		t.Fatalf("应带 Bearer JWT，got %q", rec.authHeader)
	}
	token := strings.TrimPrefix(rec.authHeader, "Bearer ")
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte("secret-123"), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("JWT 应可用 secret-123 验证: %v", err)
	}
	if claims["tenant_id"] != "tenant-a" || claims["user_id"] != "pocketd" {
		t.Fatalf("claims 缺 tenant/user: %v", claims)
	}

	add, err := c.Add(ctx, AddRequest{Content: "hello", Tags: []string{"pocket-note"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if add.ID != "mem_123" {
		t.Fatalf("add.id = %q", add.ID)
	}
	if rec.addBody["content"] != "hello" || rec.addBody["user_id"] != "pocketd" {
		t.Fatalf("add body 不符: %v", rec.addBody)
	}
	if rec.projectID != "proj-b" || rec.tenantID != "tenant-a" {
		t.Fatalf("project/tenant 头不符: %q/%q", rec.projectID, rec.tenantID)
	}

	res, err := c.SearchHybrid(ctx, "note body", 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.OK || res.Total != 1 || res.Results[0].ID != "m1" {
		t.Fatalf("search 响应不符: %+v", res)
	}
	if rec.searchBody["top_k"] != float64(5) {
		t.Fatalf("top_k 未透传: %v", rec.searchBody)
	}
}

func TestStaticAPIKeyPreferred(t *testing.T) {
	srv, rec := newFakeMemora(t, http.StatusOK, `{}`)
	c := NewClient(srv.URL, "sk-static", "secret-should-not-be-used", "", "", "", 5*time.Second)
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	if rec.authHeader != "Bearer sk-static" {
		t.Fatalf("静态 key 优先: %q", rec.authHeader)
	}
}

func TestUpstreamErrorSurfaced(t *testing.T) {
	srv, _ := newFakeMemora(t, http.StatusUnauthorized, `{"error":"bad token"}`)
	c := NewClient(srv.URL, "", "secret", "", "", "", 5*time.Second)
	err := c.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应带出 HTTP 401，got %v", err)
	}
}
