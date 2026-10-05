package task

// store_test.go — PG-backed integration tests for the task Store.
//
// Focus: the S0-A tenant boundary. tasks.workspace_id / task_session_links.
// workspace_id existed as columns but every SELECT/INSERT/UPDATE/DELETE
// ignored them, so a task ID from any tenant was readable, patchable and
// deletable by any authenticated caller. These tests execute the real SQL
// against Postgres (isolated schema per test, skipped without a DSN).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgDSN() string {
	// 只认测试专用 DSN。回退读 POCKET_POSTGRES_DSN 会让本地 `go test ./...`
	// 零配置地打到生产库——实测已在生产库留下 meeting_test_* 残留 schema。
	return os.Getenv("POCKET_TEST_POSTGRES_DSN")
}

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping task integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "task_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	store, err := NewStore(pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewStore: %v", err)
	}
	return store, func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
}

func mustCreate(t *testing.T, s *Store, id, wsID, title string) *Task {
	t.Helper()
	task := &Task{ID: id, WorkspaceID: wsID, Title: title, Status: "open", Priority: "normal"}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask %s: %v", id, err)
	}
	return task
}

func TestCreateTask_IgnoresClientPendingApprovals(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	created := &Task{
		ID:               "t-pending",
		WorkspaceID:      "ws-owner",
		Title:            "任务",
		Status:           "active",
		Priority:         "normal",
		PendingApprovals: 99,
	}
	if err := s.CreateTask(context.Background(), created); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.PendingApprovals != 0 {
		t.Fatalf("in-memory pending approvals = %d, want 0", created.PendingApprovals)
	}
	stored, err := s.GetTaskScoped(context.Background(), created.ID, created.WorkspaceID)
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if stored.PendingApprovals != 0 {
		t.Fatalf("stored pending approvals = %d, want 0", stored.PendingApprovals)
	}
}

// TestCreateTask_PersistsWorkspace 确认 workspace_id 真正写进表里并能读回。
// 之前 Task 模型没有该字段，INSERT 也不带它，所有行都落到 DEFAULT 'default'。
func TestCreateTask_PersistsWorkspace(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t1", "wsA", "任务一")

	got, err := s.GetTaskScoped(ctx, "t1", "wsA")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.WorkspaceID != "wsA" {
		t.Errorf("workspaceID = %q, want wsA", got.WorkspaceID)
	}
	if got.Title != "任务一" || got.Source != "local" {
		t.Errorf("task = %+v", got)
	}
}

// TestCreateTask_EmptyWorkspaceDefaults 空 workspace 归一到 default，
// 保证单租户/历史调用方（tasksync、migration）继续可用。
func TestCreateTask_EmptyWorkspaceDefaults(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	task := &Task{ID: "t1", Title: "no ws", Status: "open", Priority: "normal"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.WorkspaceID != DefaultWorkspaceID {
		t.Errorf("in-memory workspaceID = %q, want %q", task.WorkspaceID, DefaultWorkspaceID)
	}
	if _, err := s.GetTaskScoped(ctx, "t1", ""); err != nil {
		t.Fatalf("empty workspace should resolve to default: %v", err)
	}
}

// TestGetTaskScoped_TenantIsolation 跨 workspace 读取必须失败。
func TestGetTaskScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "私有任务")

	if _, err := s.GetTaskScoped(ctx, "t1", "wsAttacker"); err == nil {
		t.Fatal("cross-workspace GetTaskScoped should fail")
	}
}

// TestListTasksScoped_TenantIsolation 列表只返回本 workspace 的任务。
func TestListTasksScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsA", "a1")
	mustCreate(t, s, "t2", "wsA", "a2")
	mustCreate(t, s, "t3", "wsB", "b1")

	a, err := s.ListTasksScoped(ctx, "wsA")
	if err != nil {
		t.Fatalf("ListTasksScoped wsA: %v", err)
	}
	if len(a) != 2 {
		t.Errorf("wsA count = %d, want 2", len(a))
	}
	for _, task := range a {
		if task.WorkspaceID != "wsA" {
			t.Errorf("leaked task from %s", task.WorkspaceID)
		}
	}

	b, err := s.ListTasksScoped(ctx, "wsB")
	if err != nil {
		t.Fatalf("ListTasksScoped wsB: %v", err)
	}
	if len(b) != 1 {
		t.Errorf("wsB count = %d, want 1", len(b))
	}

	// 非 scoped 版本仍跨租户（保留给内部调用方），确认差异是有意的。
	all, err := s.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("unscoped count = %d, want 3", len(all))
	}
}

// TestUpdateTaskScoped_TenantIsolation 跨 workspace PATCH 必须不改任何行，
// 且不能把别人的任务当返回值回显。
func TestUpdateTaskScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "原标题")

	hacked := "被改的标题"
	if _, err := s.UpdateTaskScoped(ctx, "t1", "wsAttacker", TaskUpdate{Title: &hacked}); err == nil {
		t.Fatal("cross-workspace UpdateTaskScoped should fail")
	}
	got, err := s.GetTaskScoped(ctx, "t1", "wsOwner")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.Title != "原标题" {
		t.Errorf("title was modified across tenants: %q", got.Title)
	}

	// 本 workspace 更新正常，且 workspace_id 不被 UPDATE 破坏。
	newTitle := "新标题"
	updated, err := s.UpdateTaskScoped(ctx, "t1", "wsOwner", TaskUpdate{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdateTaskScoped own workspace: %v", err)
	}
	if updated.Title != "新标题" {
		t.Errorf("title = %q, want 新标题", updated.Title)
	}
	if updated.WorkspaceID != "wsOwner" {
		t.Errorf("workspaceID = %q, want wsOwner", updated.WorkspaceID)
	}
}

func TestUpdateTaskScoped_CompletionRequiresNoPendingApprovals(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "任务")
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET pending_approvals = 1 WHERE id = $1`, "t1"); err != nil {
		t.Fatalf("set pending approvals: %v", err)
	}

	completed := "completed"
	if _, err := s.UpdateTaskScoped(ctx, "t1", "wsOwner", TaskUpdate{Status: &completed}); !errors.Is(err, ErrPendingApprovals) {
		t.Fatalf("completion with pending approvals error = %v, want ErrPendingApprovals", err)
	}
	current, err := s.GetTaskScoped(ctx, "t1", "wsOwner")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if current.Status == "completed" {
		t.Fatal("task should not complete while pending approvals exist")
	}

	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET pending_approvals = 0 WHERE id = $1`, "t1"); err != nil {
		t.Fatalf("clear pending approvals: %v", err)
	}
	updated, err := s.UpdateTaskScoped(ctx, "t1", "wsOwner", TaskUpdate{Status: &completed})
	if err != nil {
		t.Fatalf("completion without pending approvals: %v", err)
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
}

// TestUpdateTaskScoped_NoFields 空 update 走 reread 分支，也必须受租户约束。
func TestUpdateTaskScoped_NoFields(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "标题")

	got, err := s.UpdateTaskScoped(ctx, "t1", "wsOwner", TaskUpdate{})
	if err != nil {
		t.Fatalf("empty update own workspace: %v", err)
	}
	if got.Title != "标题" {
		t.Errorf("title = %q", got.Title)
	}
	if _, err := s.UpdateTaskScoped(ctx, "t1", "wsAttacker", TaskUpdate{}); err == nil {
		t.Error("empty update must still enforce the tenant boundary")
	}
}

// TestDeleteTaskScoped_TenantIsolation 跨 workspace 删除既不能删任务，
// 也不能顺手删掉它的 session links（删除顺序调整后的关键回归点）。
func TestDeleteTaskScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "任务")
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t1", InstanceID: "i1", SessionID: "s1", Role: "primary",
	}, "wsOwner"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}

	if err := s.DeleteTaskScoped(ctx, "t1", "wsAttacker"); err == nil {
		t.Fatal("cross-workspace DeleteTaskScoped should fail")
	}
	// 任务还在。
	if _, err := s.GetTaskScoped(ctx, "t1", "wsOwner"); err != nil {
		t.Fatalf("task must survive cross-workspace delete: %v", err)
	}
	// links 也还在——删除顺序先 tasks 后 links，跨租户时不该动 links。
	links, err := s.ListSessionsForTaskScoped(ctx, "t1", "wsOwner")
	if err != nil {
		t.Fatalf("ListSessionsForTaskScoped: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1 (cross-tenant delete wiped them)", len(links))
	}

	// 本 workspace 删除会同时清掉 links。
	if err := s.DeleteTaskScoped(ctx, "t1", "wsOwner"); err != nil {
		t.Fatalf("DeleteTaskScoped own workspace: %v", err)
	}
	if _, err := s.GetTaskScoped(ctx, "t1", "wsOwner"); err == nil {
		t.Error("task should be gone")
	}
	links, _ = s.ListSessionsForTaskScoped(ctx, "t1", "wsOwner")
	if len(links) != 0 {
		t.Errorf("links after own delete = %d, want 0", len(links))
	}
}

// TestAttachSessionScoped_RejectsForeignTask 不能把 session 挂到别人的任务上。
func TestAttachSessionScoped_RejectsForeignTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "任务")

	err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t1", InstanceID: "evil", SessionID: "evil-session", Role: "primary",
	}, "wsAttacker")
	if err == nil {
		t.Fatal("attaching to a foreign task should fail")
	}
	links, _ := s.ListSessionsForTaskScoped(ctx, "t1", "wsOwner")
	if len(links) != 0 {
		t.Errorf("no link should have been written, got %d", len(links))
	}
}

// TestListSessionsForTaskScoped_TenantIsolation 跨 workspace 查 links 返回空，
// 不泄漏 instance/session ID。
func TestListSessionsForTaskScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "任务")
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t1", InstanceID: "i1", SessionID: "s1", Role: "primary",
	}, "wsOwner"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}

	own, err := s.ListSessionsForTaskScoped(ctx, "t1", "wsOwner")
	if err != nil {
		t.Fatalf("own list: %v", err)
	}
	if len(own) != 1 || own[0].SessionID != "s1" {
		t.Errorf("own links = %+v", own)
	}

	foreign, err := s.ListSessionsForTaskScoped(ctx, "t1", "wsAttacker")
	if err != nil {
		t.Fatalf("foreign list returned error: %v", err)
	}
	if len(foreign) != 0 {
		t.Errorf("cross-workspace links leaked: %+v", foreign)
	}
}

// TestAttachSessionScoped_UpdatesSessionCount 确认 session_count 仍被维护
// （加了租户校验后这条 UPDATE 不能被漏掉）。
func TestAttachSessionScoped_UpdatesSessionCount(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsOwner", "任务")

	for i, sid := range []string{"s1", "s2"} {
		if err := s.AttachSessionScoped(ctx, SessionLink{
			TaskID: "t1", InstanceID: "i1", SessionID: sid, Role: "primary",
		}, "wsOwner"); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	got, err := s.GetTaskScoped(ctx, "t1", "wsOwner")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.SessionCount != 2 {
		t.Errorf("sessionCount = %d, want 2", got.SessionCount)
	}
}

// TestListTasksCursorScoped_TenantIsolation 游标分页也必须按 workspace 过滤，
// 否则第一页就能翻出别人的任务。
func TestApplyApprovalProjection_TracksLinkedTasksAndVersions(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t-owner", "ws-owner", "owner task")
	mustCreate(t, s, "t-other", "ws-other", "other task")
	for _, link := range []SessionLink{
		{TaskID: "t-owner", InstanceID: "inst-1", SessionID: "session-1", Role: "primary"},
		{TaskID: "t-other", InstanceID: "inst-1", SessionID: "session-1", Role: "primary"},
	} {
		if err := s.AttachSessionScoped(ctx, link, map[string]string{"t-owner": "ws-owner", "t-other": "ws-other"}[link.TaskID]); err != nil {
			t.Fatalf("AttachSessionScoped %s: %v", link.TaskID, err)
		}
	}

	pending := ApprovalProjectionEvent{
		WorkspaceID: "ws-owner", InstanceID: "inst-1", SessionID: "session-1",
		RequestID: "request-1", Kind: ApprovalKindPermission, State: ApprovalStatePending, Version: 1,
	}
	if err := s.ApplyApprovalProjection(ctx, pending); err != nil {
		t.Fatalf("ApplyApprovalProjection pending: %v", err)
	}
	owner, err := s.GetTaskScoped(ctx, "t-owner", "ws-owner")
	if err != nil {
		t.Fatalf("GetTaskScoped owner: %v", err)
	}
	if owner.PendingApprovals != 1 {
		t.Fatalf("owner pending = %d, want 1", owner.PendingApprovals)
	}
	other, err := s.GetTaskScoped(ctx, "t-other", "ws-other")
	if err != nil {
		t.Fatalf("GetTaskScoped other: %v", err)
	}
	if other.PendingApprovals != 0 {
		t.Fatalf("other workspace pending = %d, want 0", other.PendingApprovals)
	}

	resolved := pending
	resolved.State = ApprovalStateApproved
	resolved.Version = 2
	if err := s.ApplyApprovalProjection(ctx, resolved); err != nil {
		t.Fatalf("ApplyApprovalProjection resolved: %v", err)
	}
	if err := s.ApplyApprovalProjection(ctx, pending); err != nil {
		t.Fatalf("ApplyApprovalProjection stale replay: %v", err)
	}
	owner, err = s.GetTaskScoped(ctx, "t-owner", "ws-owner")
	if err != nil {
		t.Fatalf("GetTaskScoped after resolved: %v", err)
	}
	latePending := pending
	latePending.Version = 3
	if err := s.ApplyApprovalProjection(ctx, latePending); err != nil {
		t.Fatalf("ApplyApprovalProjection terminal replay: %v", err)
	}
	owner, err = s.GetTaskScoped(ctx, "t-owner", "ws-owner")
	if err != nil {
		t.Fatalf("GetTaskScoped after terminal replay: %v", err)
	}
	if owner.PendingApprovals != 0 {
		t.Fatalf("pending after terminal replay = %d, want 0", owner.PendingApprovals)
	}
}

func TestCompleteTaskScoped_UsesApprovalProjection(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "ws-owner", "task")
	if err := s.AttachSessionScoped(ctx, SessionLink{TaskID: "t1", InstanceID: "inst-1", SessionID: "session-1", Role: "primary"}, "ws-owner"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}
	pending := ApprovalProjectionEvent{
		WorkspaceID: "ws-owner", InstanceID: "inst-1", SessionID: "session-1",
		RequestID: "request-1", Kind: ApprovalKindQuestion, State: ApprovalStatePending, Version: 1,
	}
	if err := s.ApplyApprovalProjection(ctx, pending); err != nil {
		t.Fatalf("ApplyApprovalProjection pending: %v", err)
	}
	if _, err := s.CompleteTaskScoped(ctx, "t1", "ws-owner", TaskUpdate{}); !errors.Is(err, ErrPendingApprovals) {
		t.Fatalf("CompleteTaskScoped while pending = %v, want ErrPendingApprovals", err)
	}

	pending.State = ApprovalStateAnswered
	pending.Version = 2
	if err := s.ApplyApprovalProjection(ctx, pending); err != nil {
		t.Fatalf("ApplyApprovalProjection answered: %v", err)
	}
	completed, err := s.CompleteTaskScoped(ctx, "t1", "ws-owner", TaskUpdate{})
	if err != nil {
		t.Fatalf("CompleteTaskScoped after resolution: %v", err)
	}
	if completed.Status != "completed" || completed.PendingApprovals != 0 {
		t.Fatalf("completed = %+v, want completed with no pending approvals", completed)
	}
}

func TestAttachSessionScoped_ProjectsPreviouslyObservedApprovals(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "ws-owner", "task")

	if err := s.ApplyApprovalProjection(ctx, ApprovalProjectionEvent{
		WorkspaceID: "ws-owner", InstanceID: "inst-1", SessionID: "session-1",
		RequestID: "request-1", Kind: ApprovalKindPermission, State: ApprovalStatePending, Version: 1,
	}); err != nil {
		t.Fatalf("observe pending approval before attachment: %v", err)
	}
	if err := s.AttachSessionScoped(ctx, SessionLink{TaskID: "t1", InstanceID: "inst-1", SessionID: "session-1", Role: "primary"}, "ws-owner"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}
	stored, err := s.GetTaskScoped(ctx, "t1", "ws-owner")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if stored.PendingApprovals != 1 {
		t.Fatalf("pending approvals after delayed task attachment = %d, want 1", stored.PendingApprovals)
	}
}

func TestApprovalProjectionAndCompletionSerialize(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t-concurrent", "ws-owner", "task")
	if err := s.AttachSessionScoped(ctx, SessionLink{TaskID: "t-concurrent", InstanceID: "inst-1", SessionID: "session-1", Role: "primary"}, "ws-owner"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- s.ApplyApprovalProjection(ctx, ApprovalProjectionEvent{
			WorkspaceID: "ws-owner", InstanceID: "inst-1", SessionID: "session-1",
			RequestID: "request-concurrent", Kind: ApprovalKindPermission, State: ApprovalStatePending, Version: 1,
		})
	}()
	go func() {
		<-start
		_, err := s.CompleteTaskScoped(ctx, "t-concurrent", "ws-owner", TaskUpdate{})
		results <- err
	}()
	close(start)
	first, second := <-results, <-results
	if first != nil && second != nil && !errors.Is(first, ErrPendingApprovals) && !errors.Is(second, ErrPendingApprovals) {
		t.Fatalf("concurrent operations failed unexpectedly: %v / %v", first, second)
	}
	stored, err := s.GetTaskScoped(ctx, "t-concurrent", "ws-owner")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if stored.Status == "completed" && stored.PendingApprovals != 0 {
		t.Fatalf("completed task has pending approvals after serialization: %+v", stored)
	}
}

func TestListTasksCursorScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t1", "wsA", "a1")
	mustCreate(t, s, "t2", "wsA", "a2")
	mustCreate(t, s, "t3", "wsB", "b1")

	tasks, _, err := s.ListTasksCursorScoped(ctx, "wsA", 10, 0, "")
	if err != nil {
		t.Fatalf("ListTasksCursorScoped: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("wsA count = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if task.WorkspaceID != "wsA" {
			t.Errorf("leaked task from %s", task.WorkspaceID)
		}
	}

	// 分页 + 租户过滤组合：limit=1 时 hasMore 为真，且第二页仍只在 wsA 内。
	page1, hasMore, err := s.ListTasksCursorScoped(ctx, "wsA", 1, 0, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 1 || !hasMore {
		t.Fatalf("page1 = %d tasks, hasMore = %v", len(page1), hasMore)
	}
	last := page1[0]
	page2, _, err := s.ListTasksCursorScoped(ctx, "wsA", 1, last.CreatedAt.Unix(), last.ID)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	for _, task := range page2 {
		if task.WorkspaceID != "wsA" {
			t.Errorf("page2 leaked task from %s", task.WorkspaceID)
		}
	}
}

// TestUpsertTask_InsertThenUpdate 锁定 tasksync 同步路径的语义：
// 首次写入创建行；同 id 重放走 ON CONFLICT 更新远程拥有的列（title/status/
// priority/updated_at），不碰本地状态（accepted_*、workspace_id）。
// 回归背景：tasksync 曾用纯 INSERT 重放同一批 ACC 任务，从第二个周期起
// 持续触发 PG duplicate key tasks_pkey（2026-09-08~09 日志 508 条）。
func TestUpsertTask_InsertThenUpdate(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	remote := &Task{ID: "acc-1", WorkspaceID: "default", Title: "v1", Status: "queue", Source: "acc"}
	if err := s.UpsertTask(ctx, remote); err != nil {
		t.Fatalf("first UpsertTask: %v", err)
	}
	got, err := s.GetTaskScoped(ctx, "acc-1", "default")
	if err != nil {
		t.Fatalf("GetTaskScoped after insert: %v", err)
	}
	if got.Title != "v1" || got.Status != "queue" || got.Source != "acc" {
		t.Fatalf("after insert, task = %+v", got)
	}

	remote.Title = "v2"
	remote.Status = "review"
	if err := s.UpsertTask(ctx, remote); err != nil {
		t.Fatalf("second UpsertTask (conflict path): %v", err)
	}
	got, err = s.GetTaskScoped(ctx, "acc-1", "default")
	if err != nil {
		t.Fatalf("GetTaskScoped after update: %v", err)
	}
	if got.Title != "v2" || got.Status != "review" {
		t.Errorf("conflict path should refresh remote columns, got title=%q status=%q", got.Title, got.Status)
	}
	if got.Source != "acc" || got.WorkspaceID != "default" {
		t.Errorf("source/workspace must be preserved, got %+v", got)
	}
	if got.PendingApprovals != 0 {
		t.Errorf("pending approvals must stay 0, got %d", got.PendingApprovals)
	}
}

// TestUpsertTask_EmptyDescriptionKeepsExisting 远端 description/workstream_id
// 为空时不得抹掉本地已有值（COALESCE(NULLIF(...)) 保护）。
func TestUpsertTask_EmptyDescriptionKeepsExisting(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	full := &Task{ID: "acc-2", Title: "t", Status: "queue", Description: "keep me", WorkstreamID: "ws-9", Source: "acc"}
	if err := s.UpsertTask(ctx, full); err != nil {
		t.Fatalf("first UpsertTask: %v", err)
	}
	thin := &Task{ID: "acc-2", Title: "t2", Status: "work", Source: "acc"}
	if err := s.UpsertTask(ctx, thin); err != nil {
		t.Fatalf("second UpsertTask: %v", err)
	}
	got, err := s.GetTaskScoped(ctx, "acc-2", "default")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.Description != "keep me" {
		t.Errorf("description = %q, want preserved %q", got.Description, "keep me")
	}
	if got.WorkstreamID != "ws-9" {
		t.Errorf("workstreamID = %q, want ws-9", got.WorkstreamID)
	}
}

// TestUpsertTask_RequiresID 空 id 直接报错，与 2026-09-09 垃圾 id 事故的
// 防线一致（解析层跳过空 id，存储层兜底拒绝）。
func TestUpsertTask_RequiresID(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	if err := s.UpsertTask(context.Background(), &Task{Title: "x"}); err == nil {
		t.Fatal("UpsertTask with empty id should fail")
	}
}

// TestUpsertTask_CrossWorkspaceConflict 锁定跨 workspace 同 ID 语义：
// ACC 任务 ID 全局唯一，tasks 表主键也是全局 (id)。同 ID 出现在另一
// workspace 属数据异常，UpsertTask 必须拒绝写入（而不是静默改写另一
// 租户任务的远端字段造成跨租户污染）。同时锁住 CreatedAt/UpdatedAt
// 不被无脑重写——上游携带的时间戳应当原样落盘。
func TestUpsertTask_CrossWorkspaceConflict(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	upstreamCreated := time.Unix(1_700_000_000, 0).UTC()
	upstreamUpdated := time.Unix(1_700_000_500, 0).UTC()

	ws1 := &Task{
		ID:          "task-cross",
		WorkspaceID: "workspace-alpha",
		Title:       "Task in alpha",
		Status:      "pending",
		CreatedAt:   upstreamCreated,
		UpdatedAt:   upstreamUpdated,
	}
	if err := s.UpsertTask(ctx, ws1); err != nil {
		t.Fatalf("upsert ws1 failed: %v", err)
	}
	// 入参对象不得被改写：调用者携带的 CreatedAt/UpdatedAt 在 UpsertTask
	// 返回后应保持原值（副作用仅落库，不污染调用者数据）。
	if !ws1.CreatedAt.Equal(upstreamCreated) || !ws1.UpdatedAt.Equal(upstreamUpdated) {
		t.Fatalf("UpsertTask mutated caller struct: CreatedAt=%s UpdatedAt=%s",
			ws1.CreatedAt.UTC(), ws1.UpdatedAt.UTC())
	}
	first, err := s.GetTaskScoped(ctx, "task-cross", "workspace-alpha")
	if err != nil {
		t.Fatalf("GetTaskScoped alpha: %v", err)
	}

	// 同 ID 另一 workspace 写入必须被拒绝。
	ws2 := &Task{
		ID:          "task-cross",
		WorkspaceID: "workspace-beta",
		Title:       "Task in beta",
		Status:      "pending",
		CreatedAt:   upstreamCreated.Add(time.Hour),
		UpdatedAt:   upstreamUpdated.Add(time.Hour),
	}
	if err := s.UpsertTask(ctx, ws2); err == nil {
		t.Fatal("cross-workspace same-id upsert should be rejected")
	}

	// 原 workspace 的行未被污染：title 保留，created_at 为首插时间。
	got, err := s.GetTaskScoped(ctx, "task-cross", "workspace-alpha")
	if err != nil {
		t.Fatalf("GetTaskScoped alpha: %v", err)
	}
	if got.Title != "Task in alpha" {
		t.Errorf("title = %q, want Task in alpha (row must not be polluted)", got.Title)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %s, want first-insert %s (rejected upsert must not touch the row)",
			got.CreatedAt.UTC(), first.CreatedAt.UTC())
	}

	// workspace-beta 看不到该任务。
	if _, err := s.GetTaskScoped(ctx, "task-cross", "workspace-beta"); err == nil {
		t.Fatal("task must not appear in workspace-beta")
	}

	// 同 workspace 重放仍走 ON CONFLICT 更新（tasks_pkey 防线），且
	// 上游携带的 CreatedAt 不被覆盖。
	replay := &Task{
		ID:          "task-cross",
		WorkspaceID: "workspace-alpha",
		Title:       "Task in alpha v2",
		Status:      "work",
		CreatedAt:   upstreamCreated.Add(2 * time.Hour),
		UpdatedAt:   upstreamUpdated.Add(2 * time.Hour),
	}
	if err := s.UpsertTask(ctx, replay); err != nil {
		t.Fatalf("same-workspace replay failed: %v", err)
	}
	got2, err := s.GetTaskScoped(ctx, "task-cross", "workspace-alpha")
	if err != nil {
		t.Fatalf("GetTaskScoped after replay: %v", err)
	}
	if got2.Title != "Task in alpha v2" || got2.Status != "work" {
		t.Errorf("replay did not refresh remote fields: %+v", got2)
	}
	if !got2.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %s, want first-insert %s (replay must not reset CreatedAt)",
			got2.CreatedAt.UTC(), first.CreatedAt.UTC())

	}
}

// ---- Pocket↔ACC canonical ID binding ----

// TestACCBinding_PersistAndRoundTrip 绑定经 SetACCBinding 写入后，所有读
// 路径（GetTaskScoped / ListTasksScoped / 游标分页）都必须带回全列。
func TestACCBinding_PersistAndRoundTrip(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-bind-1", "ws-a", "bound task")

	binding := Binding{
		TaskID:        "acc-task-9",
		RunID:         "run-3",
		DispatchID:    "disp-42",
		SourceRef:     "pocket://ws-a/acc-bind-1",
		CorrelationID: "corr-77",
	}
	if err := s.SetACCBinding(ctx, "ws-a", "acc-bind-1", binding); err != nil {
		t.Fatalf("SetACCBinding: %v", err)
	}

	got, err := s.GetTaskScoped(ctx, "acc-bind-1", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.ACCTaskID != binding.TaskID || got.ACCRunID != binding.RunID ||
		got.ACCDispatchID != binding.DispatchID || got.ACCSourceRef != binding.SourceRef ||
		got.ACCCorrelationID != binding.CorrelationID {
		t.Fatalf("binding round-trip mismatch: %+v", got)
	}
	if ab := ACCBinding(got); ab != binding {
		t.Fatalf("ACCBinding accessor = %+v, want %+v", ab, binding)
	}
	if !got.HasACCBinding() || !ACCBinding(got).Bound() {
		t.Fatalf("task must report bound: %+v", got)
	}

	// 列表读路径同样全列覆盖。
	list, err := s.ListTasksScoped(ctx, "ws-a")
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(list) != 1 || list[0].ACCDispatchID != binding.DispatchID {
		t.Fatalf("list read lost binding: %+v", list)
	}
	cursor, _, err := s.ListTasksCursorScoped(ctx, "ws-a", 10, 0, "")
	if err != nil {
		t.Fatalf("ListTasksCursorScoped: %v", err)
	}
	if len(cursor) != 1 || cursor[0].ACCCorrelationID != binding.CorrelationID {
		t.Fatalf("cursor read lost binding: %+v", cursor)
	}
}

// TestACCBinding_UpsertMustNotClobber 远端任务同步（UpsertTask）绝不允许
// 抹掉或改写本地权威绑定。
func TestACCBinding_UpsertMustNotClobber(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-bind-2", "ws-a", "sync target")

	if err := s.SetACCBinding(ctx, "ws-a", "acc-bind-2", Binding{
		TaskID: "acc-task-1", DispatchID: "disp-1", SourceRef: "ref-1", CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("SetACCBinding: %v", err)
	}

	if err := s.UpsertTask(ctx, &Task{ID: "acc-bind-2", Title: "remote replay", Status: "work", Source: "acc"}); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	got, err := s.GetTaskScoped(ctx, "acc-bind-2", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.ACCDispatchID != "disp-1" || got.ACCSourceRef != "ref-1" || got.ACCCorrelationID != "corr-1" || got.ACCTaskID != "acc-task-1" {
		t.Fatalf("UpsertTask clobbered binding: %+v", got)
	}
}

// TestACCBinding_ClearWithZeroBinding 空 Binding 即清空全部五列；
// 清空后仅剩 Source=="acc" 时 HasACCBinding 仍为 true（源即绑定）。
func TestACCBinding_ClearWithZeroBinding(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-bind-3", "ws-a", "clearable")
	if err := s.SetACCBinding(ctx, "ws-a", "acc-bind-3", Binding{DispatchID: "disp-x", SourceRef: "r", CorrelationID: "c"}); err != nil {
		t.Fatalf("SetACCBinding: %v", err)
	}

	if err := s.SetACCBinding(ctx, "ws-a", "acc-bind-3", Binding{}); err != nil {
		t.Fatalf("clear SetACCBinding: %v", err)
	}
	got, err := s.GetTaskScoped(ctx, "acc-bind-3", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if ACCBinding(got).Bound() {
		t.Fatalf("binding should be empty: %+v", got)
	}
	if got.HasACCBinding() {
		t.Fatalf("local source task should be unbound: %+v", got)
	}

	// Source=="acc" 的任务即使绑定列为空也算绑定。
	accTask := &Task{ID: "acc-bind-4", WorkspaceID: "ws-a", Title: "acc owned", Status: "open", Source: "acc"}
	if err := s.CreateTask(ctx, accTask); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got2, err := s.GetTaskScoped(ctx, "acc-bind-4", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if !got2.HasACCBinding() {
		t.Fatalf("source=acc must count as bound: %+v", got2)
	}
}

// TestACCBinding_CrossWorkspaceRejected 跨租户写绑定必须以 not found 拒绝。
func TestACCBinding_CrossWorkspaceRejected(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-bind-5", "ws-a", "owner only")

	if err := s.SetACCBinding(ctx, "ws-b", "acc-bind-5", Binding{DispatchID: "evil"}); err == nil {
		t.Fatal("cross-workspace SetACCBinding must fail")
	}
	got, err := s.GetTaskScoped(ctx, "acc-bind-5", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.HasACCBinding() && got.Source != "acc" && ACCBinding(got).Bound() {
		t.Fatalf("binding must not have been written: %+v", got)
	}
}

// TestFindTaskBySessionScoped 审批链路使用的 (workspace, instance, session)
// → task 受信任 join：最新 attach 胜出、跨租户不可见、无链接返回 (nil, nil)。
func TestFindTaskBySessionScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-sess-1", "ws-a", "first")
	mustCreate(t, s, "acc-sess-2", "ws-a", "latest")
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "acc-sess-1", InstanceID: "inst-1", SessionID: "sess-77", Role: "primary",
	}, "ws-a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "acc-sess-2", InstanceID: "inst-1", SessionID: "sess-77", Role: "primary",
	}, "ws-a"); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindTaskBySessionScoped(ctx, "ws-a", "inst-1", "sess-77")
	if err != nil {
		t.Fatalf("FindTaskBySessionScoped: %v", err)
	}
	if got == nil || got.ID != "acc-sess-2" {
		t.Fatalf("latest link must win, got %+v", got)
	}

	// 另一租户的 (instance, session) 不可见。
	cross, err := s.FindTaskBySessionScoped(ctx, "ws-b", "inst-1", "sess-77")
	if err != nil || cross != nil {
		t.Fatalf("cross-tenant lookup must be (nil,nil), got (%+v,%v)", cross, err)
	}
	// 无链接同样是 (nil, nil)。
	missing, err := s.FindTaskBySessionScoped(ctx, "ws-a", "inst-9", "sess-404")
	if err != nil || missing != nil {
		t.Fatalf("missing link must be (nil,nil), got (%+v,%v)", missing, err)
	}
}

// TestACCBinding_DefaultEmptyAndCreateInsert 老任务（迁移后新增列）与不带
// 绑定的 CreateTask 读回应为空串，不出现 NULL 解码错误。
func TestACCBinding_DefaultEmptyAndCreateInsert(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "acc-bind-6", "ws-a", "plain")

	got, err := s.GetTaskScoped(ctx, "acc-bind-6", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if ACCBinding(got) != (Binding{}) {
		t.Fatalf("plain task must have empty binding: %+v", got)
	}

	// CreateTask 直接携带绑定也应全列写入。
	withBinding := &Task{
		ID: "acc-bind-7", WorkspaceID: "ws-a", Title: "born bound", Status: "open",
		Source: "acc", ACCDispatchID: "disp-born", ACCSourceRef: "ref-born", ACCCorrelationID: "corr-born",
	}
	if err := s.CreateTask(ctx, withBinding); err != nil {
		t.Fatalf("CreateTask with binding: %v", err)
	}
	got2, err := s.GetTaskScoped(ctx, "acc-bind-7", "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got2.ACCDispatchID != "disp-born" || got2.ACCSourceRef != "ref-born" || got2.ACCCorrelationID != "corr-born" {
		t.Fatalf("create-time binding lost: %+v", got2)
	}
}
