package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockOpenCodeServer simulates a minimal OpenCode HTTP API for testing the
// HTTP adapter. It tracks which routes were called and returns canned
// responses that match the real OpenCode JSON envelope.
type mockOpenCodeServer struct {
	mu             sync.Mutex
	permissionList []PermissionRequest
	questionList   []QuestionRequest
	messages       []opencodeMessage // exported via /api/session/:id/message
	events         []string                  // newline-separated "data: {...}" SSE payloads
	replyCalls     []map[string]any
	rejectCalls    []map[string]string
	healthOK       bool
}

func newMockOpenCodeServer() *mockOpenCodeServer {
	return &mockOpenCodeServer{
		healthOK:   true,
		permissionList: []PermissionRequest{
			{
				ID:        "per_test_1",
				SessionID: "ses_test_1",
				Action:    "bash",
				Resources: []string{"rm -rf /tmp/test"},
				Source: &PermissionSource{
					Type:      "tool",
					MessageID: "msg_1",
					CallID:    "call_1",
				},
			},
		},
		questionList: []QuestionRequest{
			{
				ID:        "que_test_1",
				SessionID: "ses_test_1",
				Questions: []QuestionInfo{
					{
						Question: "Which framework?",
						Header:   "Framework",
						Options: []QuestionOption{
							{Label: "React", Description: "Meta's UI library"},
							{Label: "Vue", Description: "Progressive framework"},
						},
					},
				},
			},
		},
		messages: []opencodeMessage{
			{ID: "msg_1", Type: "user"},
			{ID: "msg_2", Type: "assistant"},
		},
		events: []string{
			`{"id":"evt_1","type":"test","data":{"foo":"bar"}}`,
			`{"id":"evt_2","type":"permission.v2.asked","data":{"sessionID":"ses_test_1","id":"per_test_1"}}`,
		},
	}
}

func (m *mockOpenCodeServer) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": m.healthOK})
	})

	mux.HandleFunc("/api/session/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/session/")
		parts := strings.Split(path, "/")
		if len(parts) < 1 || parts[0] == "" {
			http.Error(w, "missing session id", http.StatusBadRequest)
			return
		}
		sessionID := parts[0]

		// /api/session/:id/permission[/:reqID/reply]
		if len(parts) >= 2 && parts[1] == "permission" {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": m.permissionList})
				return
			}
			if r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "reply" {
				body, _ := io.ReadAll(r.Body)
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)
				payload["__sessionID"] = sessionID
				payload["__requestID"] = parts[2]
				m.mu.Lock()
				m.replyCalls = append(m.replyCalls, payload)
				m.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// /api/session/:id/question[/:reqID/{reply,reject}]
		if len(parts) >= 2 && parts[1] == "question" {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": m.questionList})
				return
			}
			if r.Method == http.MethodPost && len(parts) == 4 {
				body, _ := io.ReadAll(r.Body)
				switch parts[3] {
				case "reply":
					var payload map[string]any
					_ = json.Unmarshal(body, &payload)
					payload["__sessionID"] = sessionID
					payload["__requestID"] = parts[2]
					m.mu.Lock()
					m.replyCalls = append(m.replyCalls, payload)
					m.mu.Unlock()
					w.WriteHeader(http.StatusNoContent)
				case "reject":
					m.mu.Lock()
					m.rejectCalls = append(m.rejectCalls, map[string]string{
						"__sessionID": sessionID,
						"__requestID": parts[2],
					})
					m.mu.Unlock()
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
				return
			}
		}

		// /api/session/:id/message
		if len(parts) >= 2 && parts[1] == "message" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": m.messages,
			})
			return
		}

		// /api/session/:id (GET detail)
		if len(parts) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":    sessionID,
					"title": "test session",
				},
			})
			return
		}

		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/permission/request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m.permissionList})
	})

	mux.HandleFunc("/api/question/request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m.questionList})
	})

	mux.HandleFunc("/api/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		for _, evt := range m.events {
			fmt.Fprintf(w, "data: %s\n\n", evt)
			flusher.Flush()
		}
		// Block until client disconnects to simulate a long-lived stream
		<-r.Context().Done()
	})

	return mux
}

// =============================================================================
// Tests
// =============================================================================

func TestHTTPAdapter_GetPermissionRequests(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	reqs, err := ad.GetPermissionRequests(context.Background(), srv.URL, "ses_test_1")
	if err != nil {
		t.Fatalf("GetPermissionRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(reqs))
	}
	if reqs[0].ID != "per_test_1" || reqs[0].Action != "bash" {
		t.Errorf("unexpected permission: %+v", reqs[0])
	}
	if reqs[0].Source == nil || reqs[0].Source.Type != "tool" {
		t.Errorf("source not parsed: %+v", reqs[0].Source)
	}
}

func TestHTTPAdapter_ReplyPermission(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	err := ad.ReplyPermission(context.Background(), srv.URL, "ses_test_1", "per_test_1", PermissionReplyAlways, "trusted command")
	if err != nil {
		t.Fatalf("ReplyPermission: %v", err)
	}
	if len(mock.replyCalls) != 1 {
		t.Fatalf("expected 1 reply call, got %d", len(mock.replyCalls))
	}
	if mock.replyCalls[0]["reply"] != "always" {
		t.Errorf("expected reply=always, got %v", mock.replyCalls[0]["reply"])
	}
	if mock.replyCalls[0]["message"] != "trusted command" {
		t.Errorf("expected message=trusted command, got %v", mock.replyCalls[0]["message"])
	}
}

func TestHTTPAdapter_GetQuestionRequests(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	reqs, err := ad.GetQuestionRequests(context.Background(), srv.URL, "ses_test_1")
	if err != nil {
		t.Fatalf("GetQuestionRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 question, got %d", len(reqs))
	}
	if len(reqs[0].Questions) != 1 || len(reqs[0].Questions[0].Options) != 2 {
		t.Errorf("questions/options not parsed: %+v", reqs[0])
	}
}

func TestHTTPAdapter_ReplyQuestion(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	answers := []QuestionAnswer{{"Vue"}}
	err := ad.ReplyQuestion(context.Background(), srv.URL, "ses_test_1", "que_test_1", answers)
	if err != nil {
		t.Fatalf("ReplyQuestion: %v", err)
	}
	if len(mock.replyCalls) != 1 {
		t.Fatalf("expected 1 reply call, got %d", len(mock.replyCalls))
	}
	answers2, ok := mock.replyCalls[0]["answers"].([]any)
	if !ok || len(answers2) != 1 {
		t.Fatalf("answers not parsed: %+v", mock.replyCalls[0]["answers"])
	}
}

func TestHTTPAdapter_RejectQuestion(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	err := ad.RejectQuestion(context.Background(), srv.URL, "ses_test_1", "que_test_1")
	if err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	if len(mock.rejectCalls) != 1 {
		t.Fatalf("expected 1 reject call, got %d", len(mock.rejectCalls))
	}
	if mock.rejectCalls[0]["__requestID"] != "que_test_1" {
		t.Errorf("wrong request id: %+v", mock.rejectCalls[0])
	}
}

func TestHTTPAdapter_GetSessionMessages(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	resp, err := ad.GetSessionMessages(context.Background(), srv.URL, "ses_test_1", 10, "desc", "")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 messages, got %d", len(resp.Data))
	}
}

func TestHTTPAdapter_HealthCheck(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	if err := ad.HealthCheck(context.Background(), srv.URL); err != nil {
		t.Errorf("HealthCheck on healthy: %v", err)
	}

	mock.mu.Lock()
	mock.healthOK = false
	mock.mu.Unlock()

	if err := ad.HealthCheck(context.Background(), srv.URL); err == nil {
		t.Errorf("HealthCheck on unhealthy: expected error, got nil")
	}
}

func TestHTTPAdapter_SubscribeEvents(t *testing.T) {
	mock := newMockOpenCodeServer()
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	ad := NewOpenCodeHTTPAdapter(5000)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, cancelSub, err := ad.SubscribeEvents(ctx, srv.URL, "", "")
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	defer cancelSub()

	// We expect 2 events from the mock
	received := 0
	for received < 2 {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatalf("channel closed early after %d events", received)
			}
			if evt.Type == "" {
				t.Errorf("event missing type: %+v", evt)
			}
			received++
		case <-ctx.Done():
			t.Fatalf("timed out after %d events", received)
		}
	}

	if received != 2 {
		t.Errorf("expected 2 events, got %d", received)
	}
}
