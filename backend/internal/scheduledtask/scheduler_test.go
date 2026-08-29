package scheduledtask

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSchedulerStore struct {
	mu       sync.Mutex
	due      []*Task
	runs     []*Run
	finished []RunStatus
	updated  []int64
}

func (f *fakeSchedulerStore) Available() bool { return true }
func (f *fakeSchedulerStore) ClaimTaskNow(_ context.Context, _ string, _ string, _ string, _ int64) (*Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.due) == 0 {
		return nil, ErrTaskNotFound
	}
	return f.due[0], nil
}
func (f *fakeSchedulerStore) ClaimDue(context.Context, int64, int) ([]*Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.due
	f.due = nil
	return out, nil
}
func (f *fakeSchedulerStore) GetTaskScoped(context.Context, string, string, string) (*Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.due) == 0 {
		return nil, ErrTaskNotFound
	}
	return f.due[0], nil
}
func (f *fakeSchedulerStore) InsertRun(_ context.Context, r *Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, r)
	return nil
}
func (f *fakeSchedulerStore) FinishRun(_ context.Context, _ string, status RunStatus, _ json.RawMessage, _ string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, status)
	return nil
}
func (f *fakeSchedulerStore) UpdateTaskAfterRun(_ context.Context, _ string, _ RunStatus, _ string, next int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, next)
	return nil
}

type fakeExecutor struct {
	kind  Kind
	calls atomic.Int32
	fn    func(context.Context, *Task) (*Result, error)
}

func (e *fakeExecutor) Kind() Kind { return e.kind }
func (e *fakeExecutor) Execute(ctx context.Context, t *Task) (*Result, error) {
	e.calls.Add(1)
	if e.fn != nil {
		return e.fn(ctx, t)
	}
	return &Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}

type fakeBroadcaster struct {
	mu     sync.Mutex
	events []string
}

func (b *fakeBroadcaster) BroadcastToWorkspace(_ string, event string, _ interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

type fakeAuditor struct {
	mu     sync.Mutex
	writes int
}

func (a *fakeAuditor) Write(string, string, string, string, AuditFields) {
	a.mu.Lock()
	a.writes++
	a.mu.Unlock()
}

func (a *fakeAuditor) WriteCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes
}

func TestSchedulerDispatchSuccess(t *testing.T) {
	store := &fakeSchedulerStore{due: []*Task{{
		ID: "task-1", UserID: "user-1", WorkspaceID: "workspace-1", Kind: "test",
		ScheduleKind: ScheduleInterval, ScheduleExpr: "1h", Timezone: "UTC", TimeoutSec: 1,
	}}}
	exec := &fakeExecutor{kind: "test"}
	broadcaster := &fakeBroadcaster{}
	auditor := &fakeAuditor{}
	s := NewScheduler(store, true)
	s.SetMaxParallel(1)
	if err := s.Register(exec); err != nil {
		t.Fatal(err)
	}
	s.SetBroadcaster(broadcaster)
	s.SetAuditWriter(auditor)
	s.scan(context.Background())
	waitFor(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.finished) == 1
	})
	if exec.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.calls.Load())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.finished[0] != RunStatusSuccess {
		t.Fatalf("status = %s, want success", store.finished[0])
	}
	if len(store.runs) != 1 || len(store.updated) != 1 {
		t.Fatalf("runs=%d updates=%d, want one each", len(store.runs), len(store.updated))
	}
	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	if len(broadcaster.events) != 2 || broadcaster.events[0] != "scheduledtask.started" || broadcaster.events[1] != "scheduledtask.succeeded" {
		t.Fatalf("events = %v", broadcaster.events)
	}
	waitFor(t, func() bool { return auditor.WriteCount() == 1 })
}

func TestSchedulerDispatchErrorAndPanicAreRecorded(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(context.Context, *Task) (*Result, error)
	}{
		{"error", func(context.Context, *Task) (*Result, error) { return nil, errors.New("boom") }},
		{"panic", func(context.Context, *Task) (*Result, error) { panic("boom") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeSchedulerStore{}
			s := NewScheduler(store, true)
			if err := s.Register(&fakeExecutor{kind: "test", fn: tc.fn}); err != nil {
				t.Fatal(err)
			}
			s.dispatch(context.Background(), &Task{ID: "t", UserID: "u", WorkspaceID: "w", Kind: "test", ScheduleKind: ScheduleAt, ScheduleExpr: "2099-01-01T00:00:00Z", Timezone: "UTC"})
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.finished) != 1 || store.finished[0] != RunStatusFailed {
				t.Fatalf("finished = %v", store.finished)
			}
		})
	}
}

func TestTriggerNowOutlivesCallerContext(t *testing.T) {
	store := &fakeSchedulerStore{due: []*Task{{
		ID: "task-1", UserID: "user-1", WorkspaceID: "workspace-1", Kind: "test",
		ScheduleKind: ScheduleInterval, ScheduleExpr: "1h", Timezone: "UTC", TimeoutSec: 1,
	}}}
	executed := make(chan struct{}, 1)
	s := NewScheduler(store, true)
	if err := s.Register(&fakeExecutor{kind: "test", fn: func(context.Context, *Task) (*Result, error) {
		executed <- struct{}{}
		return &Result{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.TriggerNow(ctx, "task-1", "user-1", "workspace-1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-executed:
	case <-time.After(time.Second):
		t.Fatal("manual run was canceled with its HTTP caller context")
	}
	s.Stop()
}

func TestSchedulerDisabledAndNilStoreNoOp(t *testing.T) {
	s := NewScheduler(nil, false)
	s.Start(context.Background())
	if err := s.TriggerNow(context.Background(), "id", "u", "w"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("TriggerNow disabled = %v", err)
	}
	s.Stop()

	s = NewScheduler(nil, true)
	s.Start(context.Background())
	if err := s.TriggerNow(context.Background(), "id", "u", "w"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("TriggerNow nil store = %v", err)
	}
	s.Stop()
}

func TestSchedulerRegisterRejectsNilAndEmptyKind(t *testing.T) {
	s := NewScheduler(nil, true)
	if err := s.Register(nil); err == nil {
		t.Fatal("Register(nil) should fail")
	}
	if err := s.Register(&fakeExecutor{}); err == nil {
		t.Fatal("Register(empty kind) should fail")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
