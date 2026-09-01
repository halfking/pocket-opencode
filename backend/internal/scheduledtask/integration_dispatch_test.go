package scheduledtask_test

// integration_dispatch_test.go — Phase 4 端到端装配验证：
//   scheduler.store.C → scheduler.scan → scheduler.dispatch →
//     cloud_dispatch executor → orchestrator.CloudDispatcher → run.Success
//
// 这个测试只覆盖 Phase 4 装配注入路径（cloud_dispatch），既不依赖 PG 也不依赖
// 真实 ACC HTTP；用 capturing dispatcher 验证：payload 被传给
// orchestrator.Task，Task.{ID,WorkspaceID,UserID,Prompt,Type,Skills,Metadata} 完整，
// Result 被 marshal 进 run.Output，broadcast 序列正确。

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/orchestrator"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	scheduledexecutors "github.com/halfking/pocket-opencode/backend/internal/scheduledtask/executors"
)

// waitForCondition polls until pred returns true or deadline elapses.
func waitForCondition(timeout time.Duration, pred func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("timeout waiting for condition")
}

// integrationStubDispatcher 既实现 LocalDispatcher 又实现 CloudDispatcher；
// 用 capturing 字段断言 executor→dispatcher 之间 contract。
type integrationStubDispatcher struct {
	mu        sync.Mutex
	available bool
	captured  *orchestrator.Task
	result    *orchestrator.Result
	err       error
}

func (s *integrationStubDispatcher) Dispatch(_ context.Context, t *orchestrator.Task) (*orchestrator.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = t
	if s.err != nil {
		return nil, s.err
	}
	if !s.available {
		return nil, errors.New("integration stub: dispatcher unavailable")
	}
	return s.result, nil
}

func (s *integrationStubDispatcher) IsAvailable() bool { return s.available }

func (s *integrationStubDispatcher) capturedTask() *orchestrator.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captured
}

type integrationCloudProvider struct{ d *integrationStubDispatcher }

func (p *integrationCloudProvider) Cloud() orchestrator.CloudDispatcher { return p.d }

// integrationMemoryStore 是 integration 用的最小 SchedulerStore 实现。
type integrationMemoryStore struct {
	mu       sync.Mutex
	due      []*scheduledtask.Task
	runs     []*scheduledtask.Run
	finished []scheduledtask.RunStatus
	updates  []int64
	outputs  []json.RawMessage
}

func (s *integrationMemoryStore) Available() bool { return true }

func (s *integrationMemoryStore) ClaimDue(_ context.Context, _ int64, _ int) ([]*scheduledtask.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.due
	s.due = nil
	return out, nil
}

func (s *integrationMemoryStore) ClaimTaskNow(_ context.Context, _, _, _ string, _ int64) (*scheduledtask.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.due) == 0 {
		return nil, scheduledtask.ErrTaskNotFound
	}
	t := s.due[0]
	s.due = s.due[1:]
	return t, nil
}

func (s *integrationMemoryStore) GetTaskScoped(_ context.Context, _, _, _ string) (*scheduledtask.Task, error) {
	return nil, scheduledtask.ErrTaskNotFound
}

func (s *integrationMemoryStore) InsertRun(_ context.Context, r *scheduledtask.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	return nil
}

func (s *integrationMemoryStore) FinishRun(_ context.Context, _ string, status scheduledtask.RunStatus, output json.RawMessage, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, status)
	s.outputs = append(s.outputs, output)
	return nil
}

func (s *integrationMemoryStore) UpdateTaskAfterRun(_ context.Context, _ string, _ scheduledtask.RunStatus, _ string, next int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, next)
	return nil
}

type integrationBroadcaster struct {
	mu     sync.Mutex
	events []string
}

func (b *integrationBroadcaster) BroadcastToWorkspace(_ string, event string, _ interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

func TestSchedulerCloudDispatchIntegration(t *testing.T) {
	stub := &integrationStubDispatcher{
		available: true,
		result: &orchestrator.Result{
			TaskID:      "acc-1",
			Status:      "success",
			Output:      "{\"reply\":\"ok\"}",
			ExecutedBy:  "cloud",
			UsageTokens: 42,
		},
	}
	provider := &integrationCloudProvider{d: stub}

	store := &integrationMemoryStore{
		due: []*scheduledtask.Task{{
			ID: "sched-cloud-1", Name: "daily-summary",
			UserID:      "user-cloud",
			WorkspaceID: "workspace-cloud",
			Kind:        scheduledtask.KindCloudDispatch,
			ScheduleKind: scheduledtask.ScheduleInterval,
			ScheduleExpr: "1h",
			Timezone:     "UTC",
			TimeoutSec:   30,
			Payload:      json.RawMessage(`{"prompt":"summarize inbox","type":"text","skills":["summarize"],"max_tokens":512}`),
		}},
	}
	broadcaster := &integrationBroadcaster{}

	s := scheduledtask.NewScheduler(store, true)
	s.SetMaxParallel(1)
	s.SetBroadcaster(broadcaster)
	if err := s.Register(scheduledexecutors.NewCloudDispatchExecutor(provider)); err != nil {
		t.Fatalf("register cloud executor: %v", err)
	}

	s.TriggerNow(context.Background(), "sched-cloud-1", "user-cloud", "workspace-cloud")
	if err := waitForCondition(2*time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.finished) == 1
	}); err != nil {
		t.Fatalf("dispatch did not complete: %v", err)
	}
	s.Stop()

	// 1. 状态机：1 个 run + 1 次 FinishRun(Success) + 1 次 UpdateTaskAfterRun
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(store.runs))
	}
	if len(store.finished) != 1 || store.finished[0] != scheduledtask.RunStatusSuccess {
		t.Fatalf("finished = %v, want one Success", store.finished)
	}
	if len(store.updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(store.updates))
	}

	// 2. Run.Output 是 cloud Result 的合法 JSON 编码
	if len(store.outputs) != 1 || !json.Valid(store.outputs[0]) {
		t.Fatalf("output invalid: %v", store.outputs)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(store.outputs[0], &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if decoded["status"] != "success" || decoded["executed_by"] != "cloud" || decoded["task_id"] != "acc-1" {
		t.Fatalf("unexpected output payload: %v", decoded)
	}
	if usage, _ := decoded["usage_tokens"].(float64); usage != 42 {
		t.Fatalf("usage_tokens = %v, want 42", decoded["usage_tokens"])
	}

	// 3. cloud dispatcher 收到的 Task contract 完整
	captured := stub.capturedTask()
	if captured == nil {
		t.Fatal("cloud dispatcher did not receive task")
	}
	if captured.ID != "sched-cloud-1" || captured.WorkspaceID != "workspace-cloud" || captured.UserID != "user-cloud" {
		t.Fatalf("identity mismatch: %+v", captured)
	}
	if captured.Prompt != "summarize inbox" || captured.Type != "text" {
		t.Fatalf("prompt/type mismatch: %+v", captured)
	}
	if len(captured.Skills) != 1 || captured.Skills[0] != "summarize" {
		t.Fatalf("skills mismatch: %+v", captured.Skills)
	}
	if captured.Metadata["source"] != "scheduled_task" {
		t.Fatalf("metadata.source missing: %+v", captured.Metadata)
	}
	if captured.Metadata["task_name"] != "daily-summary" {
		t.Fatalf("metadata.task_name missing: %+v", captured.Metadata)
	}
	if got := captured.Metadata["max_tokens"]; got != 512 {
		t.Fatalf("metadata.max_tokens = %v, want 512", got)
	}

	// 4. broadcast 事件序列 = [started, succeeded]
	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	if len(broadcaster.events) != 2 || broadcaster.events[0] != "scheduledtask.started" || broadcaster.events[1] != "scheduledtask.succeeded" {
		t.Fatalf("events = %v", broadcaster.events)
	}
}

// TestSchedulerCloudDispatchUnavailableIntegration — 装配阶段 IsAvailable=false 时
// 整条链路应让 run 进 RunStatusFailed 而不是 panic 或挂起。
func TestSchedulerCloudDispatchUnavailableIntegration(t *testing.T) {
	stub := &integrationStubDispatcher{available: false}
	provider := &integrationCloudProvider{d: stub}

	store := &integrationMemoryStore{
		due: []*scheduledtask.Task{{
			ID: "sched-1", UserID: "u", WorkspaceID: "w", Kind: scheduledtask.KindCloudDispatch,
			ScheduleKind: scheduledtask.ScheduleInterval, ScheduleExpr: "1h", Timezone: "UTC", TimeoutSec: 5,
			Payload: json.RawMessage(`{"prompt":"hi","type":"text"}`),
		}},
	}
	s := scheduledtask.NewScheduler(store, true)
	if err := s.Register(scheduledexecutors.NewCloudDispatchExecutor(provider)); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.TriggerNow(context.Background(), "sched-cloud-1", "user-cloud", "workspace-cloud")
	if err := waitForCondition(2*time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.finished) == 1
	}); err != nil {
		t.Fatalf("dispatch did not complete: %v", err)
	}
	s.Stop()

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.finished[0] != scheduledtask.RunStatusFailed {
		t.Fatalf("status = %s, want Failed", store.finished[0])
	}
	if stub.capturedTask() != nil {
		t.Fatal("unavailable dispatcher should never be called")
	}
}
