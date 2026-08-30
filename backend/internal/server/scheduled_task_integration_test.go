package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestScheduledTaskEndToEnd 验证完整的 create → claim → execute → audit → WebSocket → history 闭环。
// 这是一个集成测试，需要 PostgreSQL 可用才能运行。
func TestScheduledTaskEndToEnd(t *testing.T) {
	dsn := os.Getenv("POCKET_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_POSTGRES_DSN not set; skipping integration test")
	}

	srv, token := newTestServerWithAuth(t)
	
	// 初始化 scheduled task store 和 scheduler
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to PostgreSQL: %v", err)
	}
	defer pool.Close()
	
	store, err := scheduledtask.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	srv.scheduledTaskStore = store
	
	scheduler := scheduledtask.NewScheduler(store, true)
	scheduler.SetTickInterval(1 * time.Second)
	scheduler.SetMaxParallel(2)
	
	// 注册一个测试 executor
	testExec := &testWebhookExecutor{
		responses: make(map[string]int),
	}
	if err := scheduler.Register(testExec); err != nil {
		t.Fatalf("failed to register executor: %v", err)
	}
	
	// 设置 broadcaster 和 auditor
	broadcaster := &testBroadcaster{}
	scheduler.SetBroadcaster(broadcaster)
	
	auditor := &testAuditor{}
	scheduler.SetAuditWriter(auditor)
	
	srv.scheduledTaskScheduler = scheduler
	
	// 启动 scheduler
	scheduler.Start(ctx)
	defer scheduler.Stop()
	
	h := srv.Handler()
	
	// Step 1: Create a scheduled task
	taskInput := map[string]interface{}{
		"name":          "E2E Test Task",
		"description":   "Integration test webhook task",
		"kind":          "webhook",
		"schedule_kind": "interval",
		"schedule_expr": "30s",
		"timezone":      "UTC",
		"enabled":       true,
		"timeout_sec":   10,
		"payload": map[string]interface{}{
			"url":    "https://httpbin.org/post",
			"method": "POST",
			"body":   map[string]string{"test": "e2e"},
		},
	}
	
	body, _ := json.Marshal(taskInput)
	req := httptest.NewRequest(http.MethodPost, "/api/scheduled-tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusCreated {
		t.Fatalf("create task failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	
	var createdTask scheduledtask.Task
	if err := json.Unmarshal(rr.Body.Bytes(), &createdTask); err != nil {
		t.Fatalf("failed to unmarshal created task: %v", err)
	}
	
	t.Logf("Created task ID: %s", createdTask.ID)
	
	// Step 2: List tasks to verify persistence
	req = httptest.NewRequest(http.MethodGet, "/api/scheduled-tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks failed: status=%d", rr.Code)
	}
	
	var listResp struct {
		Tasks []*scheduledtask.Task `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal task list: %v", err)
	}
	
	found := false
	for _, task := range listResp.Tasks {
		if task.ID == createdTask.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created task not found in list")
	}
	
	// Step 3: Trigger manual execution
	req = httptest.NewRequest(http.MethodPost, "/api/scheduled-tasks/"+createdTask.ID+"/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusAccepted {
		t.Fatalf("manual trigger failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	
	t.Logf("Triggered manual run, waiting for execution...")
	
	// Step 4: Wait for execution and verify run history
	time.Sleep(3 * time.Second)
	
	req = httptest.NewRequest(http.MethodGet, "/api/scheduled-tasks/"+createdTask.ID+"/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusOK {
		t.Fatalf("list runs failed: status=%d", rr.Code)
	}
	
	var runsResp struct {
		Runs []*scheduledtask.Run `json:"runs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("failed to unmarshal runs: %v", err)
	}
	
	if len(runsResp.Runs) == 0 {
		t.Fatalf("no runs found after manual trigger")
	}
	
	lastRun := runsResp.Runs[0]
	if lastRun.Status != scheduledtask.RunStatusSuccess && lastRun.Status != scheduledtask.RunStatusFailed {
		t.Fatalf("run status = %s, expected terminal state", lastRun.Status)
	}
	
	t.Logf("Run completed with status: %s", lastRun.Status)
	
	// Step 5: Verify WebSocket events were broadcast
	broadcaster.mu.Lock()
	eventCount := len(broadcaster.events)
	broadcaster.mu.Unlock()
	
	if eventCount < 2 {
		t.Fatalf("expected at least 2 WebSocket events (started + terminal), got %d", eventCount)
	}
	
	// Step 6: Verify audit trail
	auditor.mu.Lock()
	auditCount := auditor.writes
	auditor.mu.Unlock()
	
	if auditCount < 2 {
		t.Fatalf("expected at least 2 audit events (create + run), got %d", auditCount)
	}
	
	// Step 7: Update the task
	updateInput := map[string]interface{}{
		"enabled": false,
	}
	body, _ = json.Marshal(updateInput)
	req = httptest.NewRequest(http.MethodPatch, "/api/scheduled-tasks/"+createdTask.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusOK {
		t.Fatalf("update task failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	
	// Step 8: Delete the task
	req = httptest.NewRequest(http.MethodDelete, "/api/scheduled-tasks/"+createdTask.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete task failed: status=%d", rr.Code)
	}
	
	t.Logf("E2E test completed successfully")
}

// testWebhookExecutor 是一个简化的 webhook executor 用于测试
type testWebhookExecutor struct {
	mu        sync.Mutex
	responses map[string]int
}

func (e *testWebhookExecutor) Kind() scheduledtask.Kind {
	return scheduledtask.KindWebhook
}

func (e *testWebhookExecutor) Execute(ctx context.Context, task *scheduledtask.Task) (*scheduledtask.Result, error) {
	e.mu.Lock()
	e.responses[task.ID]++
	e.mu.Unlock()
	
	// Simulate webhook call
	time.Sleep(100 * time.Millisecond)
	
	return &scheduledtask.Result{
		Output: json.RawMessage(`{"status":"ok","test":true}`),
	}, nil
}

// testBroadcaster 记录所有 WebSocket 广播事件
type testBroadcaster struct {
	mu     sync.Mutex
	events []string
}

func (b *testBroadcaster) BroadcastToWorkspace(workspaceID, msgType string, payload interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, msgType)
}

// testAuditor 记录所有审计事件
type testAuditor struct {
	mu     sync.Mutex
	writes int
}

func (a *testAuditor) Write(userID, tenantID, action, resource string, fields scheduledtask.AuditFields) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writes++
}

// TestScheduledTaskTenantIsolation 验证 workspace 隔离：用户只能看到自己 workspace 的任务
func TestScheduledTaskTenantIsolation(t *testing.T) {
	dsn := os.Getenv("POCKET_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_POSTGRES_DSN not set; skipping integration test")
	}

	srv, _ := newTestServerWithAuth(t)
	
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to PostgreSQL: %v", err)
	}
	defer pool.Close()
	
	store, err := scheduledtask.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	srv.scheduledTaskStore = store
	
	h := srv.Handler()
	
	// 创建两个不同 workspace 的 token
	token1, _ := srv.jwtSigner.SignWithWorkspace("user1", "member", "workspace-1")
	token2, _ := srv.jwtSigner.SignWithWorkspace("user2", "member", "workspace-2")
	
	// workspace-1 创建任务
	taskInput := map[string]interface{}{
		"name":          "Workspace 1 Task",
		"kind":          "webhook",
		"schedule_kind": "interval",
		"schedule_expr": "1h",
		"enabled":       true,
		"payload": map[string]interface{}{
			"url":    "https://example.com",
			"method": "GET",
		},
	}
	
	body, _ := json.Marshal(taskInput)
	req := httptest.NewRequest(http.MethodPost, "/api/scheduled-tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusCreated {
		t.Fatalf("create task failed: %d", rr.Code)
	}
	
	var task1 scheduledtask.Task
	json.Unmarshal(rr.Body.Bytes(), &task1)
	
	// workspace-2 尝试列出任务，不应看到 workspace-1 的任务
	req = httptest.NewRequest(http.MethodGet, "/api/scheduled-tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks failed: %d", rr.Code)
	}
	
	var listResp struct {
		Tasks []*scheduledtask.Task `json:"tasks"`
	}
	json.Unmarshal(rr.Body.Bytes(), &listResp)
	
	for _, task := range listResp.Tasks {
		if task.ID == task1.ID {
			t.Fatalf("workspace-2 should not see workspace-1's task")
		}
	}
	
	// workspace-2 尝试访问 workspace-1 的任务，应被拒绝
	req = httptest.NewRequest(http.MethodGet, "/api/scheduled-tasks/"+task1.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when accessing other workspace's task, got %d", rr.Code)
	}
	
	// 清理
	req = httptest.NewRequest(http.MethodDelete, "/api/scheduled-tasks/"+task1.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token1)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	
	t.Logf("Tenant isolation test passed")
}
