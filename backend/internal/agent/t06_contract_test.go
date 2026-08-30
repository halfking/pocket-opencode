// This file (t06_contract_test.go) locks the external wire contract between
// OpenPocket and its agent adapters. The tests do not exercise the production
// transport (stdio/HTTP/WS) end-to-end; instead they inject a minimal
// in-memory Transport mock and assert:
//
//  1. Each ACPStdioAdapter method emits the correct JSON-RPC method name
//     and parameter shape (session/new, session/prompt, session/set_mode,
//     session/permission/reply, session/question/reply, …).
//  2. WSTransport serialises requests as JSON-RPC 2.0 text frames, sends
//     them on the /acp path, and propagates the configured Authorization
//     + custom headers.
//
// If any ACP parameter name or RPC method name in adapter_acp_stdio.go /
// transport_ws.go changes, the assertions below must be updated in lockstep —
// that coupling is the point. Do not refactor production code past these
// tests without re-running the full integration probe.
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type recordedCall struct {
	method string
	params map[string]any
}

type contractTransport struct {
	mu    sync.Mutex
	calls []recordedCall
}

func (t *contractTransport) Start(context.Context) error               { return nil }
func (t *contractTransport) Close() error                              { return nil }
func (t *contractTransport) Recv(context.Context) ([]byte, error)      { return nil, context.Canceled }
func (t *contractTransport) Notify(context.Context, string, any) error { return nil }
func (t *contractTransport) Call(_ context.Context, method string, params any, out any) error {
	encoded, _ := json.Marshal(params)
	var got map[string]any
	_ = json.Unmarshal(encoded, &got)
	t.mu.Lock()
	t.calls = append(t.calls, recordedCall{method: method, params: got})
	t.mu.Unlock()
	if target, ok := out.(*map[string]any); ok && target != nil {
		*target = map[string]any{"id": "sess-1", "title": "Created", "messageId": "msg-1"}
	}
	return nil
}

func TestContract_ACPStdioAdapterMethodShapes(t *testing.T) {
	ref := AgentRef{Type: "acp-stdio", Target: "contract-agent"}
	tr := &contractTransport{}
	adapter := &ACPStdioAdapter{transports: map[AgentRef]Transport{ref: tr}}
	ctx := context.Background()

	if err := adapter.HealthCheck(ctx, ref); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if _, err := adapter.CreateSession(ctx, ref, &CreateSessionRequest{Title: "New", Agent: "claude", Model: "m", WorkingDir: "/work"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := adapter.SendPrompt(ctx, ref, "sess-1", &SendPromptRequest{Text: "hello"}); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	if err := adapter.SetSessionMode(ctx, ref, "sess-1", "plan"); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}
	if err := adapter.ReplyPermission(ctx, ref, "sess-1", "perm-1", PermissionDecision{OptionID: "allow_once"}); err != nil {
		t.Fatalf("ReplyPermission: %v", err)
	}
	if err := adapter.ReplyQuestion(ctx, ref, "sess-1", "q-1", []QuestionAnswer{{OptionIDs: []string{"a"}}}); err != nil {
		t.Fatalf("ReplyQuestion: %v", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	want := []string{"initialize", "session/new", "session/prompt", "session/set_mode", "session/permission/reply", "session/question/reply"}
	if len(tr.calls) != len(want) {
		t.Fatalf("calls = %+v", tr.calls)
	}
	for i, method := range want {
		if tr.calls[i].method != method {
			t.Errorf("call %d = %q, want %q", i, tr.calls[i].method, method)
		}
	}
	if tr.calls[1].params["workingDir"] != "/work" || tr.calls[2].params["sessionId"] != "sess-1" || tr.calls[2].params["text"] != "hello" || tr.calls[3].params["modeId"] != "plan" || tr.calls[4].params["requestId"] != "perm-1" || tr.calls[5].params["requestId"] != "q-1" {
		t.Fatalf("unexpected ACP parameter shapes: %+v", tr.calls)
	}
}

func TestContract_WSTransportACPPathJSONRPCAndHeaders(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acp" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Contract") != "yes" {
			t.Errorf("missing auth/custom headers: %+v", r.Header)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			t.Errorf("read frame: kind=%d err=%v", kind, err)
			return
		}
		var req Request
		if err := json.Unmarshal(data, &req); err != nil || req.JSONRPC != "2.0" || req.Method != "session/new" || len(req.ID) == 0 {
			t.Errorf("request = %+v, err=%v", req, err)
			return
		}
		_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{"id": "sess-ws"}})
	}))
	defer srv.Close()

	baseURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	tr := NewWSTransport(TransportConfig{BaseURL: baseURL, AuthToken: "token", Headers: map[string]string{"X-Contract": "yes"}})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()
	var out struct {
		ID string `json:"id"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Call(ctx, "session/new", map[string]any{"title": "WS"}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.ID != "sess-ws" {
		t.Fatalf("response = %+v", out)
	}
}
