package server

// mobile_events_handler_test.go — GET /api/mobile/events/snapshot 端点行为测试
// （鉴权 fail-closed、未装配降级 503、装配后 200 形状）。快照数据的深层逻辑
// 在 internal/opencode/session_event_broadcaster_test.go 覆盖。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

func TestHandleMobileEventsSnapshot(t *testing.T) {
	s := &Server{}

	do := func(claims *authClaims) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/mobile/events/snapshot", nil)
		if claims != nil {
			req = req.WithContext(setClaimsForTest(req.Context(), claims))
		}
		rec := httptest.NewRecorder()
		s.handleMobileEventsSnapshot(rec, req)
		return rec
	}

	// 未认证 → 401。
	if rec := do(nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: status = %d, want 401", rec.Code)
	}
	// 无 workspace → 400（fail-closed，与 approvals 一致）。
	if rec := do(&authClaims{UserID: "u1"}); rec.Code != http.StatusBadRequest {
		t.Errorf("no workspace: status = %d, want 400", rec.Code)
	}
	// 已认证但未装配 broadcaster → 503。
	if rec := do(&authClaims{UserID: "u1", WorkspaceID: "ws-a"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no broadcaster: status = %d, want 503", rec.Code)
	}

	// 装配后 → 200 + §3 JSON 形状。
	t.Cleanup(func() { mobileEventsBroadcaster.Store(nil) })
	b := opencode.NewSessionEventBroadcaster(nil, nil, nil, nil)
	s.SetMobileEventsBroadcaster(b)

	rec := do(&authClaims{UserID: "u1", WorkspaceID: "ws-a"})
	if rec.Code != http.StatusOK {
		t.Fatalf("configured: status = %d, want 200", rec.Code)
	}
	var snap struct {
		Sessions    []json.RawMessage `json:"sessions"`
		GeneratedAt int64             `json:"generated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v (%s)", err, rec.Body.String())
	}
	if snap.Sessions == nil || snap.GeneratedAt == 0 {
		t.Errorf("snapshot shape = %s, want non-nil sessions + generated_at", rec.Body.String())
	}

	// 非 GET → 405。
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/events/snapshot", nil)
	req = req.WithContext(setClaimsForTest(context.Background(), &authClaims{UserID: "u1", WorkspaceID: "ws-a"}))
	post := httptest.NewRecorder()
	s.handleMobileEventsSnapshot(post, req)
	if post.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", post.Code)
	}
}

// TestMobileEventsSnapshotRealtimeRows 验证注入真实广播器后，活跃会话行
// 按 workspace 过滤出现在快照里。
func TestMobileEventsSnapshotRealtimeRows(t *testing.T) {
	t.Cleanup(func() { mobileEventsBroadcaster.Store(nil) })

	reg := registry.NewRegistry()
	if err := reg.RegisterInstance(&model.PocketInstance{
		ID: "inst-http", DisplayName: "inst-http", Health: "healthy", WorkspaceID: "ws-a",
		Capabilities: []string{"session"},
	}); err != nil {
		t.Fatalf("register instance: %v", err)
	}

	b := opencode.NewSessionEventBroadcaster(reg, nil, nil, nil)
	s := &Server{}
	s.SetMobileEventsBroadcaster(b)

	b.Ingest(opencode.DomainEvent{
		InstanceID: "inst-http",
		SessionID:  "ses-http-1",
		Type:       "session.next.prompted",
		Raw:        adapter.OpenCodeEvent{ID: "evt-1", Type: "session.next.prompted", Data: map[string]any{}},
		ReceivedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/events/snapshot", nil)
	req = req.WithContext(setClaimsForTest(req.Context(), &authClaims{UserID: "u1", WorkspaceID: "ws-a"}))
	rec := httptest.NewRecorder()
	s.handleMobileEventsSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var snap opencode.EventsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != "ses-http-1" {
		t.Errorf("sessions = %+v, want ses-http-1 row", snap.Sessions)
	}
	if snap.GeneratedAt == 0 {
		t.Error("generated_at must be epoch ms")
	}

	// 其他 workspace 的请求看不到该会话（租户隔离）。
	req = httptest.NewRequest(http.MethodGet, "/api/mobile/events/snapshot", nil)
	req = req.WithContext(setClaimsForTest(req.Context(), &authClaims{UserID: "u2", WorkspaceID: "ws-b"}))
	rec = httptest.NewRecorder()
	s.handleMobileEventsSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ws-b status = %d, want 200", rec.Code)
	}
	var other opencode.EventsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode ws-b: %v", err)
	}
	if len(other.Sessions) != 0 {
		t.Errorf("ws-b sessions = %+v, want empty (workspace isolation)", other.Sessions)
	}
}
