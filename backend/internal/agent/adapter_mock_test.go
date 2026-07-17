package agent

// mock_test.go + registry_test.go — Mock + Registry 单测

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockAgentAdapter_CreateAndListSessions(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx := context.Background()
	ref := AgentRef{Type: "mock", Target: "test"}

	s1, err := m.CreateSession(ctx, ref, &CreateSessionRequest{Title: "first"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s1.ID == "" {
		t.Fatal("session ID empty")
	}
	if s1.Agent != "mock" {
		t.Errorf("agent = %q, want mock", s1.Agent)
	}

	s2, _ := m.CreateSession(ctx, ref, &CreateSessionRequest{Title: "second"})

	list, err := m.ListSessions(ctx, ref, ListOptions{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("sessions = %d, want 2", len(list))
	}
	if list[0].ID != s1.ID || list[1].ID != s2.ID {
		t.Errorf("session order wrong")
	}
}

func TestMockAgentAdapter_SendPrompt(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx := context.Background()
	ref := AgentRef{Type: "mock", Target: "test"}

	s, _ := m.CreateSession(ctx, ref, &CreateSessionRequest{})

	res, err := m.SendPrompt(ctx, ref, s.ID, &SendPromptRequest{Text: "hello world"})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	if !res.Enqueued {
		t.Error("enqueued should be true")
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stopReason = %q", res.StopReason)
	}

	msgs, _ := m.GetMessages(ctx, ref, s.ID, ListOptions{})
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (user + assistant)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Parts[0].Text != "hello world" {
		t.Errorf("user message wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Parts[0].Text != "echo: hello world" {
		t.Errorf("assistant echo wrong: %+v", msgs[1])
	}
}

func TestMockAgentAdapter_DeleteSession(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx := context.Background()
	ref := AgentRef{Type: "mock", Target: "test"}

	s, _ := m.CreateSession(ctx, ref, nil)
	if err := m.DeleteSession(ctx, ref, s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	list, _ := m.ListSessions(ctx, ref, ListOptions{})
	if len(list) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(list))
	}
}

func TestMockAgentAdapter_LoadSessionNotFound(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx := context.Background()
	ref := AgentRef{Type: "mock", Target: "test"}

	_, err := m.LoadSession(ctx, ref, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("err should be AgentError, got %T", err)
	}
	if ae.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", ae.StatusCode)
	}
}

func TestMockAgentAdapter_ForceError(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx := context.Background()
	ref := AgentRef{Type: "mock", Target: "test"}

	m.SetForceErr(NewUnreachableError(nil))

	if err := m.HealthCheck(ctx, ref); err == nil {
		t.Fatal("expected forced error")
	}

	m.SetForceErr(nil)
	if err := m.HealthCheck(ctx, ref); err != nil {
		t.Errorf("after clear, expected no error: %v", err)
	}
}

func TestMockAgentAdapter_Capabilities(t *testing.T) {
	m := NewMockAgentAdapter()
	caps, err := m.Capabilities(context.Background(), AgentRef{Type: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	if !caps.ListSessions || !caps.LoadSession || !caps.Streaming {
		t.Error("mock should support all basic capabilities")
	}
}

func TestMockAgentAdapter_SubscribeEvents(t *testing.T) {
	m := NewMockAgentAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ref := AgentRef{Type: "mock", Target: "test"}

	events, cleanup, err := m.SubscribeEvents(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got := 0
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return
			}
			got++
			if evt.Type == "done" {
				return // 收到 done 事件就结束
			}
		case <-ctx.Done():
			if got < 2 {
				t.Errorf("only got %d events before timeout", got)
			}
			return
		}
	}
}

// ---- Registry tests ----

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	m1 := NewMockAgentAdapter()
	m2 := NewMockAgentAdapter()

	if err := reg.Register(AgentRef{Type: "opencode", Target: "http://a"}, m1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(AgentRef{Type: "acp-stdio", Target: "/bin/codex"}, m2, "codex-1"); err != nil {
		t.Fatal(err)
	}

	got, ok := reg.Get(AgentRef{Type: "opencode", Target: "http://a"})
	if !ok || got != m1 {
		t.Fatal("opencode lookup failed")
	}

	// by instance ID
	got, ref, ok := reg.GetByInstanceID("codex-1")
	if !ok || got != m2 {
		t.Fatal("instance_id lookup failed")
	}
	if ref.Type != "acp-stdio" {
		t.Errorf("ref.Type = %q", ref.Type)
	}
}

func TestRegistry_InvalidRef(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(AgentRef{}, NewMockAgentAdapter())
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestRegistry_DuplicateRef(t *testing.T) {
	reg := NewRegistry()
	ref := AgentRef{Type: "opencode", Target: "http://a"}
	_ = reg.Register(ref, NewMockAgentAdapter())
	// 重复注册应覆盖（不报错）
	if err := reg.Register(ref, NewMockAgentAdapter()); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	ref := AgentRef{Type: "opencode", Target: "http://a"}
	m := NewMockAgentAdapter()
	_ = reg.Register(ref, m, "inst-1")

	reg.Unregister(ref)
	if _, ok := reg.Get(ref); ok {
		t.Error("adapter should be gone after Unregister")
	}
	if _, _, ok := reg.GetByInstanceID("inst-1"); ok {
		t.Error("instance_id mapping should be gone after Unregister")
	}
}

func TestRegistry_All(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(AgentRef{Type: "a", Target: "1"}, NewMockAgentAdapter())
	_ = reg.Register(AgentRef{Type: "b", Target: "2"}, NewMockAgentAdapter())

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
}

func TestRegistry_HealthCheckAll(t *testing.T) {
	reg := NewRegistry()
	good := NewMockAgentAdapter()
	bad := NewMockAgentAdapter()
	bad.SetForceErr(NewUnreachableError(nil))

	_ = reg.Register(AgentRef{Type: "good", Target: "1"}, good)
	_ = reg.Register(AgentRef{Type: "bad", Target: "2"}, bad)

	statuses := reg.HealthCheckAll(context.Background())
	if !statuses[AgentRef{Type: "good", Target: "1"}].Up {
		t.Error("good should be up")
	}
	if statuses[AgentRef{Type: "bad", Target: "2"}].Up {
		t.Error("bad should be down")
	}
	if statuses[AgentRef{Type: "bad", Target: "2"}].Error == "" {
		t.Error("bad should have error message")
	}
}
