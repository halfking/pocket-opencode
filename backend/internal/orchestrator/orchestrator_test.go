package orchestrator

import (
	"context"
	"errors"
	"testing"
)

// fakeDispatcher is a test double implementing both LocalDispatcher and
// CloudDispatcher so tests can control success/failure/availability.
type fakeDispatcher struct {
	available bool
	err       error
	label     string
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, task *Task) (*Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &Result{
		TaskID:     task.ID,
		Status:     "success",
		Output:     "ok:" + f.label,
		ExecutedBy: f.label,
	}, nil
}

func (f *fakeDispatcher) IsAvailable() bool {
	return f.available
}

func TestDispatch_LocalFirst_LocalSucceeds(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, LocalFirst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "local" {
		t.Fatalf("expected local execution, got %s", result.ExecutedBy)
	}
	if result.FallbackUsed {
		t.Fatalf("expected no fallback when local succeeds")
	}
}

func TestDispatch_LocalFirst_FallsBackToCloud(t *testing.T) {
	local := &fakeDispatcher{available: true, err: errors.New("local boom")}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, LocalFirst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "cloud" {
		t.Fatalf("expected cloud fallback, got %s", result.ExecutedBy)
	}
	if !result.FallbackUsed {
		t.Fatalf("expected FallbackUsed=true")
	}
}

func TestDispatch_LocalFirst_NoFallbackWhenDisabled(t *testing.T) {
	local := &fakeDispatcher{available: true, err: errors.New("local boom")}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	cfg := DefaultConfig()
	cfg.EnableFallback = false
	o := New(local, cloud, cfg)

	_, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, LocalFirst)
	if err == nil {
		t.Fatalf("expected error when fallback disabled and local fails")
	}
}

func TestDispatch_CloudFirst_FallsBackToLocal(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, err: errors.New("cloud boom")}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, CloudFirst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "local" {
		t.Fatalf("expected local fallback, got %s", result.ExecutedBy)
	}
	if !result.FallbackUsed {
		t.Fatalf("expected FallbackUsed=true")
	}
}

func TestDispatch_LocalOnly_IgnoresCloud(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, LocalOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "local" {
		t.Fatalf("expected local, got %s", result.ExecutedBy)
	}
}

func TestDispatch_CloudOnly_IgnoresLocal(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, CloudOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "cloud" {
		t.Fatalf("expected cloud, got %s", result.ExecutedBy)
	}
}

func TestDispatch_Hybrid_SimpleTaskPrefersLocal(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi", Type: "text"}, Hybrid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "local" {
		t.Fatalf("expected local for simple task, got %s", result.ExecutedBy)
	}
}

func TestDispatch_Hybrid_ComplexTaskPrefersCloud(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil)

	longPrompt := make([]byte, 2000)
	for i := range longPrompt {
		longPrompt[i] = 'a'
	}
	task := &Task{
		ID:     "t2",
		Prompt: string(longPrompt),
		Type:   "workflow",
		Skills: []string{"s1", "s2", "s3"},
	}

	result, err := o.Dispatch(context.Background(), task, Hybrid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "cloud" {
		t.Fatalf("expected cloud for complex task, got %s", result.ExecutedBy)
	}
}

func TestDispatch_NoDispatcherAvailable(t *testing.T) {
	local := &fakeDispatcher{available: false}
	cloud := &fakeDispatcher{available: false}
	o := New(local, cloud, nil)

	_, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, LocalFirst)
	if err == nil {
		t.Fatalf("expected error when no dispatcher available")
	}
}

func TestDispatch_NilTask(t *testing.T) {
	o := New(nil, nil, nil)
	_, err := o.Dispatch(context.Background(), nil, LocalFirst)
	if err == nil {
		t.Fatalf("expected error for nil task")
	}
}

func TestDispatch_UnknownStrategy(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	o := New(local, nil, nil)
	_, err := o.Dispatch(context.Background(), &Task{ID: "t1"}, Strategy("bogus"))
	if err == nil {
		t.Fatalf("expected error for unknown strategy")
	}
}

func TestDispatch_DefaultStrategyWhenEmpty(t *testing.T) {
	local := &fakeDispatcher{available: true, label: "local"}
	cloud := &fakeDispatcher{available: true, label: "cloud"}
	o := New(local, cloud, nil) // DefaultConfig uses LocalFirst

	result, err := o.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hi"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutedBy != "local" {
		t.Fatalf("expected default strategy (local_first) to pick local, got %s", result.ExecutedBy)
	}
}

func TestMockCloudDispatcher(t *testing.T) {
	d := NewMockCloudDispatcher(true)
	if !d.IsAvailable() {
		t.Fatalf("expected available")
	}
	result, err := d.Dispatch(context.Background(), &Task{ID: "t1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success status")
	}

	unavailable := NewMockCloudDispatcher(false)
	if unavailable.IsAvailable() {
		t.Fatalf("expected unavailable")
	}
	if _, err := unavailable.Dispatch(context.Background(), &Task{ID: "t1"}); err == nil {
		t.Fatalf("expected error when unavailable")
	}
}

func TestACCDispatcher_IsAvailable(t *testing.T) {
	d := NewACCDispatcher("", "")
	if d.IsAvailable() {
		t.Fatalf("expected unavailable with empty config")
	}
	d2 := NewACCDispatcher("https://acc.example.com", "key")
	if !d2.IsAvailable() {
		t.Fatalf("expected available with base URL and key set")
	}
	if _, err := d2.Dispatch(context.Background(), &Task{ID: "t1"}); err == nil {
		t.Fatalf("expected not-implemented error from ACCDispatcher.Dispatch")
	}
}

func TestLocalAgentDispatcher_NilRuntime(t *testing.T) {
	d := NewLocalAgentDispatcher(nil)
	if d.IsAvailable() {
		t.Fatalf("expected unavailable with nil runtime")
	}
	if _, err := d.Dispatch(context.Background(), &Task{ID: "t1"}); err == nil {
		t.Fatalf("expected error with nil runtime")
	}
}
