package agentbridge

// bridge_test.go — unit tests for the Bridge orchestration.
//
// Verifies the S0 §6.2 fix: when a task_id is supplied, the new session is
// auto-attached via the TaskAttacher. Uses fake deps — no live opencode
// instance, no PG.

import (
	"context"
	"errors"
	"testing"
)

// fakeCreator records calls and returns scripted results.
type fakeCreator struct {
	createSessionID string
	createErr       error
	sendErr         error
	lastCreateInput *CreateSessionInput
	lastSendSession string
	lastSendInput   *SendPromptInput
}

func (f *fakeCreator) CreateSessionOnInstance(_ context.Context, _ string, in *CreateSessionInput) (*SessionInfo, error) {
	f.lastCreateInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &SessionInfo{ID: f.createSessionID}, nil
}

func (f *fakeCreator) SendPromptToSession(_ context.Context, _, sessionID string, in *SendPromptInput) error {
	f.lastSendSession = sessionID
	f.lastSendInput = in
	return f.sendErr
}

type fakeResolver struct {
	url string
	err error
}

func (r *fakeResolver) ResolveAPIBase(_ string) (string, error) { return r.url, r.err }

// recordingAttacher records AttachSession calls.
type recordingAttacher struct {
	calls []attachCall
	err   error
}

type attachCall struct {
	taskID, instanceID, sessionID, role string
}

func (a *recordingAttacher) AttachSession(_ context.Context, taskID, instanceID, sessionID, role string) error {
	a.calls = append(a.calls, attachCall{taskID, instanceID, sessionID, role})
	return a.err
}

// fakeStore is a minimal StoreLike for tests.
type fakeStore struct {
	agents        map[string]*Agent
	statusUpdates []string
}

func (s *fakeStore) Get(_ context.Context, id string) (*Agent, error) {
	a, ok := s.agents[id]
	if !ok {
		return nil, ErrNotFound
	}
	return a, nil
}

func (s *fakeStore) UpdateStatus(_ context.Context, id string, status Status) error {
	s.statusUpdates = append(s.statusUpdates, id+"="+string(status))
	return nil
}

func TestSend_AutoAttachesTask(t *testing.T) {
	// THE S0 §6.2 FIX: dispatch with task_id must auto-attach the new session.
	store := &fakeStore{agents: map[string]*Agent{
		"a1": {ID: "a1", WorkspaceID: "ws", InstanceID: "inst1", Name: "dev"},
	}}
	creator := &fakeCreator{createSessionID: "ses_123"}
	resolver := &fakeResolver{url: "http://inst1:4096"}
	attacher := &recordingAttacher{}

	bridge := NewBridge(store, creator, resolver, attacher)

	res, err := bridge.Send(context.Background(), "a1", "请重构 auth 模块", SendOptions{
		TaskID: "task_abc", AgentName: "build",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.SessionID != "ses_123" {
		t.Errorf("session = %s, want ses_123", res.SessionID)
	}
	if !res.Attached {
		t.Error("expected Attached=true")
	}
	if len(attacher.calls) != 1 {
		t.Fatalf("expected 1 attach call, got %d", len(attacher.calls))
	}
	c := attacher.calls[0]
	if c.taskID != "task_abc" || c.instanceID != "inst1" || c.sessionID != "ses_123" || c.role != "primary" {
		t.Errorf("attach call mismatch: %+v", c)
	}
	// Agent should have been marked busy after a successful dispatch.
	foundBusy := false
	for _, u := range store.statusUpdates {
		if u == "a1=busy" {
			foundBusy = true
		}
	}
	if !foundBusy {
		t.Error("agent should be marked busy after dispatch")
	}
}

func TestSend_NoTaskID_NoAttach(t *testing.T) {
	store := &fakeStore{agents: map[string]*Agent{
		"a1": {ID: "a1", WorkspaceID: "ws", InstanceID: "inst1"},
	}}
	bridge := NewBridge(store, &fakeCreator{createSessionID: "ses_x"}, &fakeResolver{url: "http://x"}, &recordingAttacher{})
	res, err := bridge.Send(context.Background(), "a1", "hello", SendOptions{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Attached {
		t.Error("without task_id, should not attach")
	}
}

func TestSend_AttachFailureNonFatal(t *testing.T) {
	// Attach error must NOT fail the dispatch (session was already created).
	store := &fakeStore{agents: map[string]*Agent{
		"a1": {ID: "a1", WorkspaceID: "ws", InstanceID: "inst1"},
	}}
	attacher := &recordingAttacher{err: errors.New("db down")}
	bridge := NewBridge(store, &fakeCreator{createSessionID: "ses_y"}, &fakeResolver{url: "http://y"}, attacher)
	res, err := bridge.Send(context.Background(), "a1", "hi", SendOptions{TaskID: "t1"})
	if err != nil {
		t.Fatalf("dispatch should succeed even if attach fails, got %v", err)
	}
	if res.Attached {
		t.Error("Attached should be false on attach error")
	}
}

func TestSend_InstanceOffline(t *testing.T) {
	store := &fakeStore{agents: map[string]*Agent{
		"a1": {ID: "a1", WorkspaceID: "ws", InstanceID: "inst_dead"},
	}}
	bridge := NewBridge(store, &fakeCreator{}, &fakeResolver{err: errors.New("instance not found")}, &recordingAttacher{})
	_, err := bridge.Send(context.Background(), "a1", "x", SendOptions{})
	if err == nil {
		t.Fatal("expected error on unresolvable instance")
	}
	found := false
	for _, u := range store.statusUpdates {
		if u == "a1=offline" {
			found = true
		}
	}
	if !found {
		t.Error("agent should be marked offline after resolve failure")
	}
}

func TestSend_AgentNotFound(t *testing.T) {
	store := &fakeStore{agents: map[string]*Agent{}}
	bridge := NewBridge(store, &fakeCreator{}, &fakeResolver{}, &recordingAttacher{})
	_, err := bridge.Send(context.Background(), "nope", "x", SendOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSend_EmptyPrompt(t *testing.T) {
	store := &fakeStore{agents: map[string]*Agent{"a1": {ID: "a1", InstanceID: "i"}}}
	bridge := NewBridge(store, &fakeCreator{}, &fakeResolver{}, &recordingAttacher{})
	_, err := bridge.Send(context.Background(), "a1", "", SendOptions{})
	if err == nil {
		t.Error("empty prompt should error")
	}
}
