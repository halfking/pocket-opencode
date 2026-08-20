package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// syncTestAdapter extends mobileRouteAdapter with mobile-sync behaviors:
// unique session ids per create and a deterministic session list with
// upstream time.updated values.
type syncTestAdapter struct {
	mobileRouteAdapter
	mu       sync.Mutex
	creates  int
	sessions []adapter.OpenCodeSession
}

func (a *syncTestAdapter) CreateSession(context.Context, string, *adapter.CreateSessionRequest) (*adapter.OpenCodeSessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creates++
	id := "ses_upstream_" + string(rune('a'+a.creates-1))
	updated := time.Now().UnixMilli()
	a.sessions = append(a.sessions, adapter.OpenCodeSession{
		ID:          id,
		Title:       "sync test",
		Status:      "idle",
		TimeUpdated: updated,
	})
	return &adapter.OpenCodeSessionInfo{ID: id}, nil
}

func (a *syncTestAdapter) ListSessions(context.Context, string) ([]adapter.OpenCodeSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]adapter.OpenCodeSession, len(a.sessions))
	copy(out, a.sessions)
	return out, nil
}

func newSyncTestServer(t *testing.T) (*Server, *syncTestAdapter, map[string]string) {
	t.Helper()
	srv, _, signer, tokens := newMobileRouteServer(t)
	ad := &syncTestAdapter{}
	srv.opencode = ad
	// newMobileRouteServer signs ws-a/ws-b/"" tokens; reuse them.
	_ = signer
	return srv, ad, tokens
}

func TestMobileSessionCreateIdempotentReplay(t *testing.T) {
	srv, ad, tokens := newSyncTestServer(t)
	h := srv.Handler()

	create := func(key string) (*httptest.ResponseRecorder, map[string]any) {
		req := mobileRequest(http.MethodPost, "/api/mobile/sessions?instance_id=owned-a", tokens["ws-a"], `{}`)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create failed: %d %s", rr.Code, rr.Body.String())
		}
		var parsed map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		return rr, parsed
	}

	// 首次创建：进入上游，缓存结果。
	rr1, body1 := create("idem-1")
	if rr1.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatal("first create must not be marked replayed")
	}

	// 相同幂等键重放：不重复创建上游，返回同一 session。
	rr2, body2 := create("idem-1")
	if rr2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replay must set Idempotency-Replayed header")
	}
	if body1["id"] != body2["id"] {
		t.Fatalf("replay returned different session: %v vs %v", body1["id"], body2["id"])
	}
	if ad.creates != 1 {
		t.Fatalf("expected exactly 1 upstream create, got %d", ad.creates)
	}

	// 不同幂等键：新创建。
	_, body3 := create("idem-2")
	if body3["id"] == body1["id"] {
		t.Fatal("different idempotency key must create a new session")
	}
	if ad.creates != 2 {
		t.Fatalf("expected 2 upstream creates, got %d", ad.creates)
	}
}

func TestMobileSessionCreateIdempotencyIsWorkspaceScoped(t *testing.T) {
	srv, ad, tokens := newSyncTestServer(t)
	h := srv.Handler()

	do := func(token, instance, key string) int {
		req := mobileRequest(http.MethodPost, "/api/mobile/sessions?instance_id="+instance, token, `{}`)
		req.Header.Set("Idempotency-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := do(tokens["ws-a"], "owned-a", "shared-key"); code != http.StatusOK {
		t.Fatalf("ws-a create failed: %d", code)
	}
	// 另一 workspace 使用相同 key：必须重新创建（不能读到 ws-a 的缓存）。
	if code := do(tokens["ws-b"], "owned-b", "shared-key"); code != http.StatusOK {
		t.Fatalf("ws-b create failed: %d", code)
	}
	if ad.creates != 2 {
		t.Fatalf("workspace scoping violated: expected 2 creates, got %d", ad.creates)
	}
}

func TestMobileSessionListSinceFilter(t *testing.T) {
	srv, ad, tokens := newSyncTestServer(t)
	h := srv.Handler()

	// 预置两个已知时间戳的会话。
	old := adapter.OpenCodeSession{ID: "ses_old", Title: "old", Status: "idle", TimeUpdated: 1_000}
	recent := adapter.OpenCodeSession{ID: "ses_recent", Title: "recent", Status: "idle", TimeUpdated: 2_000}
	ad.mu.Lock()
	ad.sessions = []adapter.OpenCodeSession{old, recent}
	ad.mu.Unlock()

	list := func(since string) map[string]any {
		target := "/api/mobile/sessions?instance_id=owned-a"
		if since != "" {
			target += "&since=" + since
		}
		req := mobileRequest(http.MethodGet, target, tokens["ws-a"], "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("list failed: %d %s", rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		return body
	}

	full := list("")
	if got := int(full["total"].(float64)); got != 2 {
		t.Fatalf("full list expected 2 sessions, got %d", got)
	}

	inc := list("1500")
	if got := int(inc["total"].(float64)); got != 1 {
		t.Fatalf("since=1500 expected 1 session, got %d", got)
	}
	rows := inc["data"].([]any)
	row := rows[0].(map[string]any)
	if row["id"] != "ses_recent" {
		t.Fatalf("since filter returned wrong session: %v", row)
	}
	if row["timeUpdatedMs"].(float64) != 2000 {
		t.Fatalf("sync view must expose timeUpdatedMs, got %v", row["timeUpdatedMs"])
	}
	if inc["sinceMs"].(float64) != 1500 {
		t.Fatalf("response must echo sinceMs, got %v", inc["sinceMs"])
	}
}

func TestMobileSessionListSinceValidation(t *testing.T) {
	srv, _, tokens := newSyncTestServer(t)
	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/mobile/sessions?instance_id=owned-a&since=abc", tokens["ws-a"], "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric since must 400, got %d", rr.Code)
	}
	if code := structuredCode(t, rr); code != CodeInvalidRequest {
		t.Fatalf("expected invalid_request code, got %s", code)
	}
}

func TestMobileCreateCacheEmptyKey(t *testing.T) {
	cache := newMobileCreateCache()
	info := &adapter.OpenCodeSessionInfo{ID: "ses_x"}
	cache.Put("ws", "inst", "", info)
	if _, hit := cache.Get("ws", "inst", ""); hit {
		t.Fatal("empty idempotency key must never hit the cache")
	}
}
