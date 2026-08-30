package scheduledtask

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("POCKET_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_POSTGRES_DSN not set")
	}
	
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New failed: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	
	store, err := NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	
	return store, ctx
}

func TestMaintenanceDryRun(t *testing.T) {
	store, ctx := setupTestStore(t)
	
	// 创建一个测试任务
	task := &Task{
		ID:           NewID(),
		WorkspaceID:  "test-ws",
		UserID:       "test-user",
		Name:         "Maintenance Test Task",
		Kind:         KindWebhook,
		ScheduleKind: ScheduleInterval,
		ScheduleExpr: "1h",
		Timezone:     "UTC",
		Enabled:      true,
		TimeoutSec:   10,
		Payload:      []byte(`{"url":"https://example.com"}`),
		CreatedAt:    time.Now().Unix(),
		UpdatedAt:    time.Now().Unix(),
	}
	
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	defer store.DeleteTaskScoped(ctx, task.ID, task.UserID, task.WorkspaceID)
	
	// 创建一个"陈旧"的运行记录（手动设置旧时间戳）
	staleStartTime := time.Now().Add(-2 * time.Hour).Unix()
	run := &Run{
		ID:        NewID(),
		TaskID:    task.ID,
		Status:    RunStatusRunning,
		StartedAt: staleStartTime,
	}
	
	if err := store.InsertRun(ctx, run); err != nil {
		t.Fatalf("InsertRun failed: %v", err)
	}
	
	// 运行维护（dry run）
	cfg := MaintenanceConfig{
		StaleRunThreshold: 1 * time.Hour,
		DryRun:           true,
		Logger:           log.New(os.Stdout, "[MAINTENANCE TEST] ", log.LstdFlags),
	}
	
	result, err := store.RunMaintenance(ctx, cfg)
	if err != nil {
		t.Fatalf("RunMaintenance failed: %v", err)
	}
	
	if result.StaleRunsFound == 0 {
		t.Errorf("expected to find at least 1 stale run, found %d", result.StaleRunsFound)
	}
	
	if result.StaleRunsMarked != 0 {
		t.Errorf("dry run should not mark runs, but marked %d", result.StaleRunsMarked)
	}
	
	if result.StaleRunsSkipped != result.StaleRunsFound {
		t.Errorf("dry run should skip all found runs, found=%d skipped=%d", 
			result.StaleRunsFound, result.StaleRunsSkipped)
	}
	
	// 验证 run 仍然是 running 状态
	runs, err := store.ListRuns(ctx, task.ID, task.UserID, task.WorkspaceID, 10)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	
	if runs[0].Status != RunStatusRunning {
		t.Errorf("dry run should not change status, got %s", runs[0].Status)
	}
}

func TestMaintenanceMarkStaleRuns(t *testing.T) {
	store, ctx := setupTestStore(t)
	
	// 创建测试任务
	task := &Task{
		ID:           NewID(),
		WorkspaceID:  "test-ws",
		UserID:       "test-user",
		Name:         "Stale Run Test",
		Kind:         KindWebhook,
		ScheduleKind: ScheduleInterval,
		ScheduleExpr: "1h",
		Timezone:     "UTC",
		Enabled:      true,
		TimeoutSec:   10,
		Payload:      []byte(`{"url":"https://example.com"}`),
		CreatedAt:    time.Now().Unix(),
		UpdatedAt:    time.Now().Unix(),
	}
	
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	defer store.DeleteTaskScoped(ctx, task.ID, task.UserID, task.WorkspaceID)
	
	// 创建一个陈旧的 running run
	staleStartTime := time.Now().Add(-90 * time.Minute).Unix()
	run := &Run{
		ID:        NewID(),
		TaskID:    task.ID,
		Status:    RunStatusRunning,
		StartedAt: staleStartTime,
	}
	
	if err := store.InsertRun(ctx, run); err != nil {
		t.Fatalf("InsertRun failed: %v", err)
	}
	
	// 运行维护（实际执行）
	cfg := MaintenanceConfig{
		StaleRunThreshold: 1 * time.Hour,
		DryRun:           false,
		Logger:           log.New(os.Stdout, "[MAINTENANCE] ", log.LstdFlags),
	}
	
	result, err := store.RunMaintenance(ctx, cfg)
	if err != nil {
		t.Fatalf("RunMaintenance failed: %v", err)
	}
	
	if result.StaleRunsFound == 0 {
		t.Errorf("expected to find stale runs, found %d", result.StaleRunsFound)
	}
	
	if result.StaleRunsMarked == 0 {
		t.Errorf("expected to mark stale runs, marked %d", result.StaleRunsMarked)
	}
	
	// 验证 run 已被标记为 failed
	runs, err := store.ListRuns(ctx, task.ID, task.UserID, task.WorkspaceID, 10)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	
	if runs[0].Status != RunStatusFailed {
		t.Errorf("expected status=failed, got %s", runs[0].Status)
	}
	
	if runs[0].Error == "" {
		t.Errorf("expected error message to be set")
	}
	
	if runs[0].FinishedAt == 0 {
		t.Errorf("expected finished_at to be set")
	}
}

func TestMaintenanceLeaseRecovery(t *testing.T) {
	store, ctx := setupTestStore(t)
	
	// 创建带有过期 lease 的任务
	now := time.Now().Unix()
	expiredLease := now - 3600 // 1 小时前
	
	task := &Task{
		ID:           NewID(),
		WorkspaceID:  "test-ws",
		UserID:       "test-user",
		Name:         "Lease Recovery Test",
		Kind:         KindWebhook,
		ScheduleKind: ScheduleInterval,
		ScheduleExpr: "1h",
		Timezone:     "UTC",
		Enabled:      true,
		TimeoutSec:   10,
		Payload:      []byte(`{"url":"https://example.com"}`),
		LeaseUntil:   expiredLease,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	defer store.DeleteTaskScoped(ctx, task.ID, task.UserID, task.WorkspaceID)
	
	// 创建陈旧的 running run
	staleStartTime := now - 7200 // 2 小时前
	run := &Run{
		ID:        NewID(),
		TaskID:    task.ID,
		Status:    RunStatusRunning,
		StartedAt: staleStartTime,
	}
	
	if err := store.InsertRun(ctx, run); err != nil {
		t.Fatalf("InsertRun failed: %v", err)
	}
	
	// 运行维护
	cfg := MaintenanceConfig{
		StaleRunThreshold: 1 * time.Hour,
		DryRun:           false,
		Logger:           log.New(os.Stdout, "[LEASE RECOVERY] ", log.LstdFlags),
	}
	
	result, err := store.RunMaintenance(ctx, cfg)
	if err != nil {
		t.Fatalf("RunMaintenance failed: %v", err)
	}
	
	if result.TasksRecovered == 0 {
		t.Errorf("expected to recover at least 1 task, recovered %d", result.TasksRecovered)
	}
	
	// 验证 lease 已被清除
	recovered, err := store.GetTaskScoped(ctx, task.ID, task.UserID, task.WorkspaceID)
	if err != nil {
		t.Fatalf("GetTaskScoped failed: %v", err)
	}
	
	if recovered.LeaseUntil != 0 {
		t.Errorf("expected lease_until to be cleared, got %d", recovered.LeaseUntil)
	}
}
