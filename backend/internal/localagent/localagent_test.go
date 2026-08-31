package localagent

import (
	"context"
	"testing"
	"time"
)

func TestNewRuntime(t *testing.T) {
	backend := NewMockBackend("test")
	runtime := NewRuntime(backend)
	if runtime == nil {
		t.Fatal("expected non-nil runtime")
	}
}

func TestRuntime_LoadSkill(t *testing.T) {
	backend := NewMockBackend("test")
	runtime := NewRuntime(backend)

	if err := runtime.LoadSkill(context.Background(), "skill-1", "/path/to/skill"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded := runtime.LoadedSkills()
	if len(loaded) != 1 || loaded[0] != "skill-1" {
		t.Fatalf("expected skill-1 registered, got %v", loaded)
	}
}

func TestRuntime_LoadSkill_NilBackend(t *testing.T) {
	runtime := NewRuntime(nil)
	if err := runtime.LoadSkill(context.Background(), "skill-1", "/path"); err == nil {
		t.Fatal("expected error with nil backend")
	}
}

func TestRuntime_Execute(t *testing.T) {
	backend := NewMockBackend("test")
	runtime := NewRuntime(backend)

	task := &AgentTask{ID: "task-1", Prompt: "Hello"}

	result, err := runtime.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskID != task.ID {
		t.Fatalf("expected task ID %s, got %s", task.ID, result.TaskID)
	}
	if result.Status != "success" {
		t.Fatalf("expected success status, got %s", result.Status)
	}
	if result.Output == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestRuntime_ExecuteWithNilBackend(t *testing.T) {
	runtime := NewRuntime(nil)
	task := &AgentTask{ID: "task-1", Prompt: "Hello"}

	if _, err := runtime.Execute(context.Background(), task); err == nil {
		t.Fatal("expected error with nil backend")
	}
}

func TestRuntime_ExecuteWithNilTask(t *testing.T) {
	runtime := NewRuntime(NewMockBackend("test"))
	if _, err := runtime.Execute(context.Background(), nil); err == nil {
		t.Fatal("expected error with nil task")
	}
}

func TestRuntime_Close(t *testing.T) {
	backend := NewMockBackend("test")
	runtime := NewRuntime(backend)
	if err := runtime.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntime_Close_NilBackend(t *testing.T) {
	runtime := NewRuntime(nil)
	if err := runtime.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMockBackend_Name(t *testing.T) {
	backend := NewMockBackend("test-backend")
	if backend.Name() != "test-backend" {
		t.Fatalf("expected name 'test-backend', got %s", backend.Name())
	}
}

func TestMockBackend_Execute(t *testing.T) {
	backend := NewMockBackend("mock")
	task := &AgentTask{ID: "task-1", Prompt: "Test prompt"}

	eventChan, err := backend.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []AgentEvent
	for event := range eventChan {
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected events from mock backend")
	}

	var hasThinking, hasTextDelta, hasDone bool
	for _, event := range events {
		switch event.Type {
		case "thinking":
			hasThinking = true
		case "text_delta":
			hasTextDelta = true
		case "done":
			hasDone = true
		}
	}
	if !hasThinking {
		t.Error("expected thinking event")
	}
	if !hasTextDelta {
		t.Error("expected text_delta event")
	}
	if !hasDone {
		t.Error("expected done event")
	}
}

func TestMockBackend_Execute_CancelledContext(t *testing.T) {
	backend := NewMockBackend("mock")
	task := &AgentTask{ID: "task-1", Prompt: "Test"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	eventChan, err := backend.Execute(ctx, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The channel must still close promptly even though the context is
	// already cancelled — otherwise Runtime.Execute would hang forever.
	done := make(chan struct{})
	go func() {
		for range eventChan {
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event channel did not close after context cancellation")
	}
}

func TestMockBackend_LoadSkill(t *testing.T) {
	backend := NewMockBackend("mock")
	if err := backend.LoadSkill(context.Background(), "skill-1", "/path"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWASMBackend_NotImplemented(t *testing.T) {
	backend := NewWASMBackend()
	if backend.Name() != "wasm" {
		t.Fatalf("expected name 'wasm', got %s", backend.Name())
	}
	if _, err := backend.Execute(context.Background(), &AgentTask{ID: "t1"}); err == nil {
		t.Fatal("expected not implemented error")
	}
	if err := backend.LoadSkill(context.Background(), "s1", "/path"); err == nil {
		t.Fatal("expected not implemented error")
	}
}

func TestPythonBackend_NotImplemented(t *testing.T) {
	backend := NewPythonBackend()
	if backend.Name() != "python" {
		t.Fatalf("expected name 'python', got %s", backend.Name())
	}
	if _, err := backend.Execute(context.Background(), &AgentTask{ID: "t1"}); err == nil {
		t.Fatal("expected not implemented error")
	}
	if err := backend.LoadSkill(context.Background(), "s1", "/path"); err == nil {
		t.Fatal("expected not implemented error")
	}
}

func TestACPStdioBackend_NotImplemented(t *testing.T) {
	backend := NewACPStdioBackend("/bin/agent")
	if backend.Name() != "acp-stdio" {
		t.Fatalf("expected name 'acp-stdio', got %s", backend.Name())
	}
	if _, err := backend.Execute(context.Background(), &AgentTask{ID: "t1"}); err == nil {
		t.Fatal("expected not implemented error")
	}
	if err := backend.LoadSkill(context.Background(), "s1", "/path"); err == nil {
		t.Fatal("expected not implemented error")
	}
}

func TestNewSkillLoader(t *testing.T) {
	runtime := NewRuntime(NewMockBackend("mock"))
	loader := NewSkillLoader("https://market.example.com", runtime)
	if loader == nil {
		t.Fatal("expected non-nil loader")
	}
}

func TestSkillLoader_LoadSkillFromMarketplace_NotImplemented(t *testing.T) {
	runtime := NewRuntime(NewMockBackend("mock"))
	loader := NewSkillLoader("https://market.example.com", runtime)

	if err := loader.LoadSkillFromMarketplace(context.Background(), "skill-1"); err == nil {
		t.Fatal("expected not implemented error")
	}
}
