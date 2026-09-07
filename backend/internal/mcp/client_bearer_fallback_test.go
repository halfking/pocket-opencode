package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Node ACC 校验 api_keys 静态 Bearer；JWT 会 401。客户端必须回退到 raw secret。
func TestDoRaw_FallsBackToRawBearerOn401(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		auths = append(auths, auth)
		if strings.Count(auth, ".") == 2 {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		if auth != "acc-api-key" {
			http.Error(w, "forbidden", http.StatusUnauthorized)
			return
		}
		w.Header().Set("mcp-session-id", "sess-node")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "acc-api-key", false)
	body, headers, err := c.doRaw(context.Background(), []byte(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
	if err != nil {
		t.Fatalf("doRaw: %v", err)
	}
	if headers.Get("mcp-session-id") != "sess-node" {
		t.Fatalf("session header = %q", headers.Get("mcp-session-id"))
	}
	if !strings.Contains(string(body), `"result"`) {
		t.Fatalf("body = %s", body)
	}
	if len(auths) < 2 {
		t.Fatalf("expected JWT then raw bearer, got %d auths", len(auths))
	}
	if strings.Count(auths[0], ".") != 2 {
		t.Fatalf("first auth should be JWT, got %q", auths[0])
	}
	if auths[1] != "acc-api-key" {
		t.Fatalf("second auth should be raw key")
	}
}

func TestGetRemoteTasks_NodeACCBearerFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.Count(auth, ".") == 2 {
			http.Error(w, "jwt rejected", http.StatusUnauthorized)
			return
		}
		if auth != "acc-api-key" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Method == "initialize" {
			w.Header().Set("mcp-session-id", "s1")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"))
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"[running] t1: from-acc (owner: acc)\\n"}]}}`
		_, _ = w.Write([]byte("event: message\ndata: " + payload + "\n\n"))
	}))
	t.Cleanup(srv.Close)

	tasks, err := NewClient(srv.URL, "acc-api-key", false).GetRemoteTasks(context.Background(), "running", 2)
	if err != nil {
		t.Fatalf("GetRemoteTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("tasks = %+v", tasks)
	}
}
