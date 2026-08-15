package facade_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/facade"
)

// mockServiceJWT is a JWT-shaped token whose payload carries tenant_id. The
// client must derive tenant identity from this token and must NOT send a bare
// X-User-Id / X-Tenant-ID header. (Signature is fake: the mock provider — and
// the real one — verifies it, the client never does.)
func mockServiceJWT(tenantID string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"iss":        "openpocket",
		"aud":        "redclaw-facade",
		"sub":        "service:openpocket",
		"tenant_id":  tenantID,
		"actor_id":   "user-1",
		"actor_type": "user",
	})
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body map[string]interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode mock response: %v", err)
	}
}

func newClient(t *testing.T, baseURL string) *facade.Client {
	t.Helper()
	c, err := facade.NewClient(facade.Config{
		BaseURL: baseURL,
		Token:   mockServiceJWT("tenant-123"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// Scenario 3.1: task create success.
func TestContract_TaskCreateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/tasks" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+mockServiceJWT("tenant-123") {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "pocket-task-123" {
			t.Errorf("Idempotency-Key = %q, want pocket-task-123", got)
		}
		if got := r.Header.Get("X-Correlation-ID"); got != "corr-123" {
			t.Errorf("X-Correlation-ID = %q, want corr-123", got)
		}
		// The client must never send a bare identity header: tenant derives
		// from the JWT claims.
		if r.Header.Get("X-User-Id") != "" || r.Header.Get("X-Tenant-ID") != "" {
			t.Errorf("client must not send bare identity headers, got X-User-Id=%q X-Tenant-ID=%q",
				r.Header.Get("X-User-Id"), r.Header.Get("X-Tenant-ID"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["project_id"] != "project-123" || body["title"] != "Test task" {
			t.Errorf("unexpected body: %v", body)
		}
		tc, _ := body["task_contract"].(map[string]interface{})
		if tc == nil || tc["type"] != "agent_task" || tc["risk_level"] != "low" {
			t.Errorf("unexpected task_contract: %v", body["task_contract"])
		}
		writeJSON(t, w, 202, map[string]interface{}{
			"data": map[string]interface{}{
				"task_id":      "acc-task-456",
				"status":       "accepted",
				"status_url":   "/api/v2/tasks/acc-task-456",
				"operation_id": "op-789",
			},
			"request_id":     "req-123",
			"correlation_id": "corr-123",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.CreateTask(context.Background(),
		facade.CreateTaskRequest{
			ProjectID:    "project-123",
			Title:        "Test task",
			TaskContract: &facade.TaskContract{Type: "agent_task", RiskLevel: "low"},
		},
		facade.WithIdempotencyKey("pocket-task-123"),
		facade.WithCorrelationID("corr-123"),
	)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// assertion: status 202 implied by success; body fields:
	if resp.Data.TaskID != "acc-task-456" {
		t.Errorf("task_id = %q, want acc-task-456", resp.Data.TaskID)
	}
	if resp.Data.Status != "accepted" {
		t.Errorf("status = %q, want accepted", resp.Data.Status)
	}
	if resp.Data.StatusURL != "/api/v2/tasks/acc-task-456" {
		t.Errorf("status_url = %q", resp.Data.StatusURL)
	}
	if resp.Data.OperationID != "op-789" {
		t.Errorf("operation_id = %q, want op-789", resp.Data.OperationID)
	}
	if resp.CorrelationID != "corr-123" {
		t.Errorf("correlation_id = %q, want corr-123", resp.CorrelationID)
	}
	// side effect (caller bookkeeping): mapping inputs are available.
	if resp.RequestID != "req-123" {
		t.Errorf("request_id = %q", resp.RequestID)
	}
}

// Scenario 3.2: task list success with cursor pagination envelope.
func TestContract_TaskListSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/tasks" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got := r.URL.Query().Get("project_id"); got != "project-123" {
			t.Errorf("project_id = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("limit = %q, want 10", got)
		}
		writeJSON(t, w, 200, map[string]interface{}{
			"data": []map[string]interface{}{{
				"task_id":          "acc-task-456",
				"project_id":       "project-123",
				"title":            "Test task",
				"status":           "running",
				"resource_version": 3,
				"updated_at":       "2026-08-14T00:00:00Z",
			}},
			"page": map[string]interface{}{
				"limit":       10,
				"next_cursor": nil,
			},
			"request_id": "req-124",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.ListTasks(context.Background(), facade.ListTasksQuery{ProjectID: "project-123", Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
	if resp.Data[0].TaskID != "acc-task-456" {
		t.Errorf("data[0].task_id = %q, want acc-task-456", resp.Data[0].TaskID)
	}
	if resp.Data[0].Status != "running" || resp.Data[0].ResourceVersion != 3 {
		t.Errorf("unexpected item: %+v", resp.Data[0])
	}
	if resp.Page.HasMore() {
		t.Errorf("next_cursor should be empty, got %q", resp.Page.NextCursor)
	}
}

// Scenario 3.3: approval decision.
func TestContract_ApprovalDecisionApprove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/approvals/gate-123/decision" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got := r.Header.Get("Idempotency-Key"); got != "pocket-approval-123" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		if got := r.Header.Get("X-Correlation-ID"); got != "corr-124" {
			t.Errorf("X-Correlation-ID = %q", got)
		}
		var body facade.ApprovalDecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Decision != "approve" || body.Reason != "LGTM" || body.ExpectedGateVersion != 1 {
			t.Errorf("unexpected body: %+v", body)
		}
		if len(body.CandidateDecisions) != 1 ||
			body.CandidateDecisions[0].CandidateID != "cand-123" ||
			body.CandidateDecisions[0].Decision != "promote" {
			t.Errorf("unexpected candidate_decisions: %+v", body.CandidateDecisions)
		}
		writeJSON(t, w, 202, map[string]interface{}{
			"data": map[string]interface{}{
				"approval_id": "approval-456",
				"gate_id":     "gate-123",
				"status":      "accepted",
				"status_url":  "/api/v2/gates/gate-123",
			},
			"request_id":     "req-125",
			"correlation_id": "corr-124",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.SubmitApprovalDecision(context.Background(), "gate-123",
		facade.ApprovalDecisionRequest{
			Decision:            "approve",
			Reason:              "LGTM",
			ExpectedGateVersion: 1,
			CandidateDecisions: []facade.CandidateDecision{
				{CandidateID: "cand-123", Decision: "promote"},
			},
		},
		facade.WithIdempotencyKey("pocket-approval-123"),
		facade.WithCorrelationID("corr-124"),
	)
	if err != nil {
		t.Fatalf("SubmitApprovalDecision: %v", err)
	}
	if resp.Data.GateID != "gate-123" {
		t.Errorf("data.gate_id = %q, want gate-123", resp.Data.GateID)
	}
	if resp.Data.ApprovalID != "approval-456" || resp.Data.Status != "accepted" {
		t.Errorf("unexpected data: %+v", resp.Data)
	}
	if resp.CorrelationID != "corr-124" {
		t.Errorf("correlation_id = %q, want corr-124", resp.CorrelationID)
	}
}

// Scenario 3.4: memory search.
func TestContract_MemorySearchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/memory/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got := r.Header.Get("X-Correlation-ID"); got != "corr-125" {
			t.Errorf("X-Correlation-ID = %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["query"] != "project summary" {
			t.Errorf("query = %v", body["query"])
		}
		scope, _ := body["scope_chain"].(map[string]interface{})
		if scope == nil || scope["tenant_id"] != "tenant-123" || scope["project_id"] != "project-123" {
			t.Errorf("unexpected scope_chain: %v", body["scope_chain"])
		}
		if body["top_k"] != float64(5) || body["token_budget"] != float64(2000) {
			t.Errorf("unexpected top_k/token_budget: %v/%v", body["top_k"], body["token_budget"])
		}
		policy, _ := body["policy"].(map[string]interface{})
		if policy == nil || policy["on_degraded"] != "degraded_with_warning" {
			t.Errorf("unexpected policy: %v", body["policy"])
		}
		writeJSON(t, w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"items": []map[string]interface{}{{
					"source":          "memory",
					"memory_id":       "memory-123",
					"scope":           "project:project-123",
					"score":           0.92,
					"token_count":     128,
					"policy_decision": "allow",
					"snippet":         "summary text",
				}},
				"degraded": false,
			},
			"request_id":     "req-126",
			"correlation_id": "corr-125",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.SearchMemory(context.Background(),
		facade.MemorySearchRequest{
			Query:       "project summary",
			ScopeChain:  &facade.MemoryScopeChain{TenantID: "tenant-123", ProjectID: "project-123"},
			TopK:        5,
			TokenBudget: 2000,
			Policy:      &facade.MemorySearchPolicy{OnDegraded: "degraded_with_warning"},
		},
		facade.WithCorrelationID("corr-125"),
	)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(resp.Data.Items))
	}
	if resp.Data.Items[0].MemoryID != "memory-123" {
		t.Errorf("items[0].memory_id = %q, want memory-123", resp.Data.Items[0].MemoryID)
	}
	if resp.Data.Items[0].Score != 0.92 || resp.Data.Items[0].PolicyDecision != "allow" {
		t.Errorf("unexpected item: %+v", resp.Data.Items[0])
	}
	if resp.Data.Degraded {
		t.Error("degraded should be false")
	}
}

// Scenario 3.5: notification ack.
func TestContract_NotificationAckSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/notifications/noti-123/ack" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got := r.Header.Get("Idempotency-Key"); got != "pocket-ack-123" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		writeJSON(t, w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"notification_id": "noti-123",
				"ack_at":          "2026-08-14T00:00:00Z",
			},
			"request_id": "req-127",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.AckNotification(context.Background(), "noti-123",
		facade.WithIdempotencyKey("pocket-ack-123"))
	if err != nil {
		t.Fatalf("AckNotification: %v", err)
	}
	if resp.Data.NotificationID != "noti-123" {
		t.Errorf("notification_id = %q", resp.Data.NotificationID)
	}
	if resp.Data.AckAt != "2026-08-14T00:00:00Z" {
		t.Errorf("ack_at = %q", resp.Data.AckAt)
	}
}

// Scenario 3.6: error envelope — tenant mismatch (422).
func TestContract_TaskCreateTenantMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 422, map[string]interface{}{
			"error": map[string]interface{}{
				"code":      "tenant_mismatch",
				"message":   "project does not belong to JWT tenant",
				"retryable": false,
			},
			"request_id": "req-128",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.CreateTask(context.Background(),
		facade.CreateTaskRequest{
			ProjectID:    "other-tenant-project",
			Title:        "Test",
			TaskContract: &facade.TaskContract{Type: "agent_task"},
		})
	if err == nil {
		t.Fatalf("expected error, got resp %+v", resp)
	}
	var apiErr *facade.APIError
	if e, ok := err.(*facade.APIError); ok {
		apiErr = e
	} else {
		t.Fatalf("error should be *facade.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 422 {
		t.Errorf("status = %d, want 422", apiErr.Status)
	}
	if apiErr.Code != "tenant_mismatch" {
		t.Errorf("error.code = %q, want tenant_mismatch", apiErr.Code)
	}
	if apiErr.Retryable {
		t.Error("retryable should be false")
	}
	if apiErr.Message != "project does not belong to JWT tenant" {
		t.Errorf("unexpected message: %q", apiErr.Message)
	}
	if apiErr.RequestID != "req-128" {
		t.Errorf("request_id = %q", apiErr.RequestID)
	}
}

// Scenario 3.7: idempotency replay — same Idempotency-Key returns 200 with
// the original data; the client must surface it as success.
func TestContract_TaskCreateIdempotencyReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "pocket-task-123" {
			t.Errorf("Idempotency-Key = %q, want pocket-task-123", got)
		}
		writeJSON(t, w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"task_id": "acc-task-456",
				"status":  "accepted",
			},
			"request_id": "req-129",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	resp, err := client.CreateTask(context.Background(),
		facade.CreateTaskRequest{
			ProjectID:    "project-123",
			Title:        "Test task",
			TaskContract: &facade.TaskContract{Type: "agent_task"},
		},
		facade.WithIdempotencyKey("pocket-task-123"),
	)
	if err != nil {
		t.Fatalf("CreateTask replay: %v", err)
	}
	if resp.Data.TaskID != "acc-task-456" {
		t.Errorf("data.task_id = %q, want acc-task-456", resp.Data.TaskID)
	}
	if resp.Data.Status != "accepted" {
		t.Errorf("data.status = %q, want accepted", resp.Data.Status)
	}
}

// Scenario 3.8: SSE reconnect with cursor (Last-Event-ID + after).
func TestContract_SSEReconnectWithCursor(t *testing.T) {
	var connects int32
	var mu sync.Mutex
	seenRequests := []map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/runs/run-123/events" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		n := atomic.AddInt32(&connects, 1)
		mu.Lock()
		seenRequests = append(seenRequests, map[string]string{
			"after":       r.URL.Query().Get("after"),
			"lastEventID": r.Header.Get("Last-Event-ID"),
			"auth":        r.Header.Get("Authorization"),
			"accept":      r.Header.Get("Accept"),
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		if n == 1 {
			// deliver evt-101 then abruptly drop the connection
			fmt.Fprint(w, "id: evt-101\nevent: task.state.changed.v1\ndata: {\"task_id\":\"task-123\",\"status\":\"completed\"}\n\n")
			flusher.Flush()
			panic(http.ErrAbortHandler)
		}
		// second connection: deliver evt-102 (with explicit envelope fields),
		// then keep the stream open until the client goes away.
		fmt.Fprint(w, "id: evt-102\nevent: memory.candidate.proposed.v1\ndata: {\"event_id\":\"evt-102\",\"correlation_id\":\"corr-200\",\"schema_version\":\"1.0\",\"type\":\"memory.candidate.proposed.v1\",\"payload\":{\"candidate_id\":\"cand-9\"}}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)

	var muEv sync.Mutex
	gotEvents := []facade.EventEnvelope{}
	var cursor string

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamDone := make(chan error, 1)
	go func() {
		streamDone <- client.StreamRunEvents(ctx, "run-123", "evt-100", facade.EventStreamConfig{
			RetryBackoff: 10 * time.Millisecond,
		}, func(ev facade.Event) error {
			env := ev.Envelope()
			muEv.Lock()
			gotEvents = append(gotEvents, env)
			cursor = env.EventID
			muEv.Unlock()
			if env.EventID == "evt-102" {
				cancel() // test is done once we verified the resume worked
			}
			return nil
		})
	}()

	select {
	case err := <-streamDone:
		if err != nil && err != context.Canceled {
			t.Fatalf("StreamRunEvents: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish in time")
	}

	if n := atomic.LoadInt32(&connects); n != 2 {
		t.Fatalf("connects = %d, want 2", n)
	}

	mu.Lock()
	first, second := seenRequests[0], seenRequests[1]
	mu.Unlock()
	if first["after"] != "evt-100" || first["lastEventID"] != "evt-100" {
		t.Errorf("first connect after=%q lastEventID=%q, want evt-100", first["after"], first["lastEventID"])
	}
	if second["after"] != "evt-101" || second["lastEventID"] != "evt-101" {
		t.Errorf("reconnect after=%q lastEventID=%q, want evt-101 (resume after last event)", second["after"], second["lastEventID"])
	}
	if !strings.Contains(first["auth"], "Bearer ") {
		t.Errorf("SSE request missing Authorization: %q", first["auth"])
	}
	if first["accept"] != "text/event-stream" {
		t.Errorf("Accept = %q", first["accept"])
	}

	muEv.Lock()
	defer muEv.Unlock()
	if len(gotEvents) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(gotEvents), gotEvents)
	}
	e1, e2 := gotEvents[0], gotEvents[1]
	if e1.EventID != "evt-101" || e1.Type != "task.state.changed.v1" {
		t.Errorf("event 1 envelope: %+v", e1)
	}
	if e1.SchemaVersion != facade.SchemaVersionV1 {
		t.Errorf("event 1 schema_version = %q, want 1.0 (default)", e1.SchemaVersion)
	}
	var p1 map[string]string
	if err := json.Unmarshal(e1.Payload, &p1); err != nil {
		t.Fatalf("event 1 payload not JSON: %v", err)
	}
	if p1["status"] != "completed" {
		t.Errorf("event 1 payload = %v", p1)
	}
	if e2.EventID != "evt-102" || e2.CorrelationID != "corr-200" || e2.SchemaVersion != "1.0" {
		t.Errorf("event 2 envelope: %+v", e2)
	}
	// side effect: cursor advanced to the last received event
	if cursor != "evt-102" {
		t.Errorf("cursor = %q, want evt-102", cursor)
	}
}

// Extra contract checks beyond the 8 scenarios.

func TestClientRequiresAuth(t *testing.T) {
	if _, err := facade.NewClient(facade.Config{BaseURL: "http://localhost:1"}); err == nil {
		t.Error("expected error when Token empty")
	}
	if _, err := facade.NewClient(facade.Config{Token: "t"}); err == nil {
		t.Error("expected error when BaseURL empty")
	}
}

func TestAutoGeneratedHeaders(t *testing.T) {
	var gotIdem, gotCorr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdem = r.Header.Get("Idempotency-Key")
		gotCorr = r.Header.Get("X-Correlation-ID")
		writeJSON(t, w, 200, map[string]interface{}{
			"data":       map[string]interface{}{"notification_id": "n1", "ack_at": "now"},
			"request_id": "r1",
		})
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	if _, err := client.AckNotification(context.Background(), "n1"); err != nil {
		t.Fatalf("AckNotification: %v", err)
	}
	if gotIdem == "" {
		t.Error("write without explicit key must auto-generate Idempotency-Key")
	}
	if gotCorr == "" {
		t.Error("X-Correlation-ID must always be sent")
	}
}

func TestTenantDerivedFromToken(t *testing.T) {
	client := newClient(t, httptest.NewServer(nil).URL)
	if got := client.TenantID(); got != "tenant-123" {
		t.Errorf("TenantID() = %q, want tenant-123", got)
	}
}
