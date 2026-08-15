package opencode

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// recordedApprovalBroadcast captures one BroadcastToWorkspace call.
type recordedApprovalBroadcast struct {
	workspaceID string
	msgType     string
	envelope    WsEnvelopeV1
}

// recordingApprovalHub implements ApprovalBroadcastHub for tests.
type recordingApprovalHub struct {
	mu       sync.Mutex
	received []recordedApprovalBroadcast
}

func (h *recordingApprovalHub) BroadcastToWorkspace(workspaceID, msgType string, payload interface{}) {
	env, ok := payload.(WsEnvelopeV1)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received = append(h.received, recordedApprovalBroadcast{
		workspaceID: workspaceID,
		msgType:     msgType,
		envelope:    env,
	})
}

func (h *recordingApprovalHub) events() []recordedApprovalBroadcast {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]recordedApprovalBroadcast, len(h.received))
	copy(out, h.received)
	return out
}

// waitForApprovalEvent polls the recorder until a broadcast with the given
// type and cause.approval_id shows up, or the timeout elapses.
func waitForApprovalEvent(hub *recordingApprovalHub, msgType, approvalID string, timeout time.Duration) *recordedApprovalBroadcast {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, evt := range hub.events() {
			if evt.msgType != msgType {
				continue
			}
			if evt.envelope.Cause == nil || evt.envelope.Cause.ApprovalID != approvalID {
				continue
			}
			evtCopy := evt
			return &evtCopy
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// newApprovalBroadcasterFixture builds a registry + both managers + a wired
// broadcaster pointing at the recording hub. The returned cleanup stops the
// managers and the broadcaster loop.
func newApprovalBroadcasterFixture(t *testing.T, instances ...*model.PocketInstance) (*registry.Registry, *PermissionManager, *QuestionManager, *ApprovalBroadcaster, *recordingApprovalHub, func()) {
	t.Helper()

	reg := registry.NewRegistry()
	for _, inst := range instances {
		if err := reg.RegisterInstance(inst); err != nil {
			t.Fatalf("register instance %s: %v", inst.ID, err)
		}
		reg.SetInstanceAPIBase(inst.ID, "http://"+inst.ID+".fake")
	}

	ad := newFakePermissionAdapter()
	permMgr := NewPermissionManager(reg, ad, PermissionManagerOptions{PollInterval: time.Hour}, nil)
	quesMgr := NewQuestionManager(reg, ad, QuestionManagerOptions{PollInterval: time.Hour}, nil)

	hub := &recordingApprovalHub{}
	bc := NewApprovalBroadcaster(reg, permMgr, quesMgr)
	bc.SetBroadcaster(hub)

	ctx, cancel := context.WithCancel(context.Background())
	go bc.Run(ctx)
	cleanup := func() {
		cancel()
		permMgr.Close()
		quesMgr.Close()
	}
	return reg, permMgr, quesMgr, bc, hub, cleanup
}

// workspaceInstance builds an instance bound to a workspace.
func workspaceInstance(id, workspaceID string) *model.PocketInstance {
	return &model.PocketInstance{
		ID:           id,
		DisplayName:  id,
		Health:       "healthy",
		WorkspaceID:  workspaceID,
		Capabilities: []string{"session", "permission", "question"},
	}
}

// triggerPermissionNew feeds a new-permission event through the real manager
// pipeline (event-driven path) and waits for the pending broadcast. It
// retries with fresh request IDs because the broadcaster's Subscribe may not
// be registered yet when the first event is published (publish drops instead
// of blocking).
func triggerPermissionNew(t *testing.T, mgr *PermissionManager, hub *recordingApprovalHub, instanceID, sessionID, prefix string) string {
	t.Helper()
	for i := 0; i < 30; i++ {
		requestID := fmt.Sprintf("%s_%d", prefix, i)
		mgr.handleNewPermissionFromEvent(instanceID, sessionID, map[string]any{
			"id":        requestID,
			"action":    "bash",
			"resources": []interface{}{"ls -la"},
		})
		if evt := waitForApprovalEvent(hub, ApprovalEventPermissionPending, requestID, 150*time.Millisecond); evt != nil {
			return requestID
		}
	}
	t.Fatalf("permission pending broadcast never arrived for %s", prefix)
	return ""
}

// triggerQuestionNew feeds a question.asked event through the real question
// manager pipeline and waits for the pending broadcast.
func triggerQuestionNew(t *testing.T, mgr *QuestionManager, hub *recordingApprovalHub, instanceID, sessionID, prefix string) string {
	t.Helper()
	for i := 0; i < 30; i++ {
		requestID := fmt.Sprintf("%s_%d", prefix, i)
		mgr.handleNewQuestionFromEvent(instanceID, sessionID, map[string]any{
			"id": requestID,
			"questions": []interface{}{
				map[string]interface{}{
					"question": "continue?",
					"header":   "Confirm",
					"options": []interface{}{
						map[string]interface{}{"label": "Yes", "description": "proceed"},
						map[string]interface{}{"label": "No", "description": "abort"},
					},
				},
			},
		})
		if evt := waitForApprovalEvent(hub, ApprovalEventQuestionPending, requestID, 150*time.Millisecond); evt != nil {
			return requestID
		}
	}
	t.Fatalf("question pending broadcast never arrived for %s", prefix)
	return ""
}

// TestApprovalBroadcaster_PermissionPendingThenResolved covers the full
// pipeline: a permission request enters pending state (broadcast
// approval.permission.pending), the user replies via the manager (broadcast
// approval.resolved), and both envelopes follow WsEnvelopeV1 with
// cause.approval_id for frontend dedupe.
func TestApprovalBroadcaster_PermissionPendingThenResolved(t *testing.T) {
	_, permMgr, _, _, hub, cleanup := newApprovalBroadcasterFixture(t, workspaceInstance("inst-a", "ws-a"))
	defer cleanup()

	requestID := triggerPermissionNew(t, permMgr, hub, "inst-a", "ses-1", "per-1")

	pending := waitForApprovalEvent(hub, ApprovalEventPermissionPending, requestID, time.Second)
	if pending == nil {
		t.Fatalf("pending broadcast lost")
	}
	if pending.workspaceID != "ws-a" {
		t.Errorf("pending broadcast workspace = %q, want ws-a", pending.workspaceID)
	}
	env := pending.envelope
	if env.V != 1 {
		t.Errorf("envelope v = %d, want 1", env.V)
	}
	if env.ID == "" {
		t.Error("envelope id must be non-empty for idempotent dedupe")
	}
	if env.Channel != "approvals" {
		t.Errorf("envelope channel = %q, want approvals", env.Channel)
	}
	if env.Topic != "inst-a" {
		t.Errorf("envelope topic = %q, want inst-a", env.Topic)
	}
	if env.Cause == nil || env.Cause.ApprovalID != requestID {
		t.Errorf("envelope cause.approval_id = %+v, want %s", env.Cause, requestID)
	}
	data, ok := env.Data.(*ApprovalPermissionPendingPayload)
	if !ok {
		t.Fatalf("pending data type = %T, want *ApprovalPermissionPendingPayload", env.Data)
	}
	if data.InstanceID != "inst-a" || data.SessionID != "ses-1" {
		t.Errorf("pending routing fields = %s/%s, want inst-a/ses-1", data.InstanceID, data.SessionID)
	}
	if data.Request == nil || data.Request.ID != requestID || data.Request.Action != "bash" {
		t.Errorf("pending request payload = %+v, want id=%s action=bash", data.Request, requestID)
	}

	// Resolve via the manager reply path → approval.resolved broadcast.
	if err := permMgr.Reply(context.Background(), "inst-a", "ses-1", requestID, "once", "ok"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	resolved := waitForApprovalEvent(hub, ApprovalEventResolved, requestID, 2*time.Second)
	if resolved == nil {
		t.Fatalf("no approval.resolved broadcast seen")
	}
	if resolved.workspaceID != "ws-a" {
		t.Errorf("resolved broadcast workspace = %q, want ws-a", resolved.workspaceID)
	}
	rdata, ok := resolved.envelope.Data.(*ApprovalResolvedPayload)
	if !ok {
		t.Fatalf("resolved data type = %T", resolved.envelope.Data)
	}
	if rdata.Kind != "permission" || rdata.RequestID != requestID {
		t.Errorf("resolved payload = %+v, want kind=permission requestID=%s", rdata, requestID)
	}
	if rdata.Resolution != "approved" || rdata.Decision != "once" {
		t.Errorf("resolved resolution/decision = %s/%s, want approved/once", rdata.Resolution, rdata.Decision)
	}
}

// TestApprovalBroadcaster_QuestionPendingThenResolved covers the question
// pipeline: question.asked → approval.question.pending, question answered →
// approval.resolved.
func TestApprovalBroadcaster_QuestionPendingThenResolved(t *testing.T) {
	_, _, quesMgr, _, hub, cleanup := newApprovalBroadcasterFixture(t, workspaceInstance("inst-q", "ws-q"))
	defer cleanup()

	requestID := triggerQuestionNew(t, quesMgr, hub, "inst-q", "ses-2", "que-1")

	pending := waitForApprovalEvent(hub, ApprovalEventQuestionPending, requestID, time.Second)
	if pending == nil {
		t.Fatalf("question pending broadcast lost")
	}
	if pending.workspaceID != "ws-q" {
		t.Errorf("question pending workspace = %q, want ws-q", pending.workspaceID)
	}
	qdata, ok := pending.envelope.Data.(*ApprovalQuestionPendingPayload)
	if !ok {
		t.Fatalf("question pending data type = %T", pending.envelope.Data)
	}
	if qdata.Request == nil || qdata.Request.ID != requestID {
		t.Errorf("question payload = %+v, want id=%s", qdata.Request, requestID)
	}
	if len(qdata.Request.Questions) != 1 || qdata.Request.Questions[0].Question != "continue?" {
		t.Errorf("question payload questions = %+v", qdata.Request.Questions)
	}
	if pending.envelope.Cause == nil || pending.envelope.Cause.ApprovalID != requestID {
		t.Errorf("question pending cause = %+v, want approval_id=%s", pending.envelope.Cause, requestID)
	}

	// Answered upstream → approval.resolved broadcast.
	quesMgr.handleResolvedQuestionFromEvent("inst-q", "ses-2", map[string]any{"id": requestID})

	resolved := waitForApprovalEvent(hub, ApprovalEventResolved, requestID, 2*time.Second)
	if resolved == nil {
		t.Fatalf("no approval.resolved broadcast for question")
	}
	rdata, ok := resolved.envelope.Data.(*ApprovalResolvedPayload)
	if !ok {
		t.Fatalf("resolved data type = %T", resolved.envelope.Data)
	}
	if rdata.Kind != "question" || rdata.Resolution != "answered" {
		t.Errorf("question resolved payload = %+v, want kind=question resolution=answered", rdata)
	}
	if resolved.envelope.Cause == nil || resolved.envelope.Cause.ApprovalID != requestID {
		t.Errorf("question resolved cause = %+v, want approval_id=%s", resolved.envelope.Cause, requestID)
	}
}

// TestApprovalBroadcaster_WorkspaceIsolation verifies broadcasts are
// workspace-targeted: each instance's events only go to its owning workspace,
// and shared (workspace-less) instances are not broadcast at all.
func TestApprovalBroadcaster_WorkspaceIsolation(t *testing.T) {
	_, permMgr, _, _, hub, cleanup := newApprovalBroadcasterFixture(t,
		workspaceInstance("inst-a", "ws-a"),
		workspaceInstance("inst-b", "ws-b"),
		workspaceInstance("inst-shared", ""), // shared operator instance
	)
	defer cleanup()

	triggerPermissionNew(t, permMgr, hub, "inst-a", "ses-a", "per-a")
	triggerPermissionNew(t, permMgr, hub, "inst-b", "ses-b", "per-b")

	// Shared instance: feed several events, expect zero broadcasts.
	for i := 0; i < 10; i++ {
		permMgr.handleNewPermissionFromEvent("inst-shared", "ses-s", map[string]any{
			"id":     fmt.Sprintf("per-shared-%d", i),
			"action": "edit",
		})
		time.Sleep(10 * time.Millisecond)
	}

	events := hub.events()
	if len(events) != 2 {
		t.Fatalf("expected exactly 2 broadcasts (one per owned workspace), got %d: %+v", len(events), events)
	}
	byWorkspace := map[string]int{}
	for _, evt := range events {
		byWorkspace[evt.workspaceID]++
	}
	if byWorkspace["ws-a"] != 1 || byWorkspace["ws-b"] != 1 {
		t.Errorf("workspace distribution = %v, want one broadcast each to ws-a and ws-b", byWorkspace)
	}
	for _, evt := range events {
		if evt.workspaceID == "" {
			t.Errorf("broadcast with empty workspace (global) is forbidden: %+v", evt)
		}
	}
}

// TestApprovalBroadcaster_NoHubIsSafe verifies a nil broadcaster hub does not
// panic (managers still function without WS push).
func TestApprovalBroadcaster_NoHubIsSafe(t *testing.T) {
	reg := registry.NewRegistry()
	inst := workspaceInstance("inst-a", "ws-a")
	if err := reg.RegisterInstance(inst); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	ad := newFakePermissionAdapter()
	permMgr := NewPermissionManager(reg, ad, PermissionManagerOptions{PollInterval: time.Hour}, nil)
	quesMgr := NewQuestionManager(reg, ad, QuestionManagerOptions{PollInterval: time.Hour}, nil)
	defer permMgr.Close()
	defer quesMgr.Close()

	bc := NewApprovalBroadcaster(reg, permMgr, quesMgr)
	// hub left nil — must be a no-op, not a panic.
	bc.forwardPermission(PermissionEvent{Type: "new", InstanceID: "inst-a", SessionID: "ses-1", RequestID: "per-1"})
}
