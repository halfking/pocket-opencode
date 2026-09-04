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

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
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
