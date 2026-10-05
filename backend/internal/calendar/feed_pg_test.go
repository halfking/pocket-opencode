package calendar

import (
	"context"
	"errors"
	"testing"
)

// TestPGFeedShowsTasksAndAutomations 是这个功能的核心断言：
// **重要的信息要出现在日历里**。日程事件只是其中一类来源 —— 真正让用户
// 每天打开日历的理由是任务截止与自动化运行时间也在同一张月视图上。
//
// 纯单元测试喂的是内存里的 []TaskDUE，验证不了 SQL（列名、workspace 过滤、
// enabled 条件）。所以这里建真实的 tasks / scheduled_tasks 行再读。
func TestPGFeedShowsTasksAndAutomations(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	pool := testPool(t, store)

	// tasks 表由 task 包拥有，这里只建本测试需要的最小列集；
	// 真实部署里它早已存在（id/workspace_id/title/status/due_at…）。
	mustExec(t, pool, `CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL DEFAULT 'default',
		title TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		priority TEXT NOT NULL DEFAULT 'normal',
		source TEXT NOT NULL DEFAULT 'local',
		created_at BIGINT NOT NULL DEFAULT 0,
		updated_at BIGINT NOT NULL DEFAULT 0,
		due_at BIGINT NOT NULL DEFAULT 0,
		remind_at BIGINT NOT NULL DEFAULT 0
	)`)
	mustExec(t, pool, `CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		next_run_at BIGINT NOT NULL DEFAULT 0,
		timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai'
	)`)

	// 窗口 [1000, 3000)
	mustExec(t, pool, `INSERT INTO tasks (id, workspace_id, title, status, due_at) VALUES
		('t-in',   'ws1', '窗口内截止',   'pending',    1500),
		('t-done', 'ws1', '已完成截止',   'completed', 1600),
		('t-early','ws1', '窗口外截止',   'pending',    9999),
		('t-none', 'ws1', '没有截止日',   'pending',    0),
		('t-other','ws2', '别的租户',     'pending',    1700)`)
	mustExec(t, pool, `INSERT INTO scheduled_tasks (id, workspace_id, name, enabled, next_run_at) VALUES
		('s-on',   'ws1', '每日摘要',   TRUE,  1800),
		('s-off',  'ws1', '已停用自动化', FALSE, 1900),
		('s-other','ws2', '别的租户自动化', TRUE, 2000)`)

	svc := &Service{store: store, sources: PGBridges{Pool: pool}}

	// 先建一个日程事件，确认三类来源同时在场。
	if _, err := svc.CreateEvent(ctx, "ws1", "u1", EventInput{
		Title: "季度评审", StartAt: 2500, EndAt: 2600, Timezone: "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	entries, err := svc.Feed(ctx, "ws1", "u1", 1000, 3000, true)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}

	byTitle := map[string]FeedEntry{}
	for _, e := range entries {
		byTitle[e.Title] = e
	}
	if len(entries) != 4 {
		t.Errorf("want 4 entries (1 event + 2 tasks + 1 automation), got %d: %+v", len(entries), entries)
	}

	// 三类来源都在。
	for _, want := range []string{"季度评审", "每日摘要"} {
		if _, ok := byTitle[want]; !ok {
			t.Errorf("feed missing %q — 重要信息没有进日历", want)
		}
	}
	if e, ok := byTitle["窗口内截止"]; !ok {
		t.Error("task deadline missing from feed")
	} else {
		if e.Source != SourceTask || e.RefID != "t-in" {
			t.Errorf("task entry must keep source/refId, got %+v", e)
		}
		if e.StartAt != e.EndAt {
			t.Errorf("deadline must be zero length, got %d..%d", e.StartAt, e.EndAt)
		}
		if e.Done {
			t.Error("pending task must not be marked done")
		}
	}
	if e, ok := byTitle["已完成截止"]; !ok {
		t.Error("completed task deadline missing from feed")
	} else if !e.Done {
		t.Error("completed task must be marked done")
	}

	// 排除项：窗口外、无截止日、别的租户、已停用的自动化。
	for _, absent := range []string{"窗口外截止", "没有截止日", "别的租户", "别的租户自动化", "已停用自动化"} {
		if _, ok := byTitle[absent]; ok {
			t.Errorf("feed must NOT contain %q", absent)
		}
	}

	// 来源前缀必须唯一，否则客户端索引里会互相覆盖。
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("duplicate feed id %q", e.ID)
		}
		seen[e.ID] = true
	}
}

// 某个来源挂掉时，日历不能整体失败 —— 少了「定时任务」这一类，远比
// 整页空白可用。**但错误必须被返回**：否则一条写错的 SQL 会让任务截止
// 整类静默消失，而日历看起来完全正常（这正是 timezone 那一列踩过的坑）。
// 降级 = 「数据还在 + 错误可见」，而不是「假装一切正常」。
func TestPGFeedToleratesMissingSourceTable(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	// 故意只装日程事件表，不建 tasks / scheduled_tasks。
	svc := &Service{store: store, sources: PGBridges{Pool: testPool(t, store)}}
	if _, err := svc.CreateEvent(ctx, "ws1", "u1", EventInput{
		Title: "只有日程", StartAt: 1500, EndAt: 1600, Timezone: "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	entries, err := svc.Feed(ctx, "ws1", "u1", 1000, 3000, true)
	if err == nil {
		t.Error("missing source tables must be reported, not silently swallowed")
	}
	if len(entries) != 1 || entries[0].Title != "只有日程" {
		t.Errorf("want the event to survive missing source tables, got %+v", entries)
	}
}

func TestPGFeedRangeValidation(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	pool := testPool(t, store)
	// 这条只验范围校验，先把两个来源表建出来，免得降级错误盖住范围错误。
	mustExec(t, pool, `CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL DEFAULT 'default', title TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', due_at BIGINT NOT NULL DEFAULT 0, remind_at BIGINT NOT NULL DEFAULT 0)`)
	mustExec(t, pool, `CREATE TABLE IF NOT EXISTS scheduled_tasks (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL DEFAULT 'default', name TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE, next_run_at BIGINT NOT NULL DEFAULT 0, timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai')`)
	svc := &Service{store: store, sources: PGBridges{Pool: pool}}

	if _, err := svc.Feed(ctx, "ws1", "u1", 3000, 1000, true); err == nil {
		t.Error("reversed range should be rejected")
	}
	// 超过 62 天的请求必须被拒 —— 否则一次请求会扫全工作区。
	tooWide := int64(63 * 86400)
	if _, err := svc.Feed(ctx, "ws1", "u1", 0, tooWide, true); err == nil {
		t.Error("over-wide range should be rejected")
	}
	// 恰好 62 天应当放行（月视图 42 格 + 补白正好在这个量级内）。
	entries, err := svc.Feed(ctx, "ws1", "u1", 0, maxFeedRangeSeconds, true)
	if err != nil {
		t.Errorf("max allowed range rejected: %v", err)
	}
	if entries == nil {
		t.Error("valid range must return a non-nil slice")
	}
}

func TestPGCreateEventDefaults(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	svc := &Service{store: store, sources: nil}

	got, err := svc.CreateEvent(ctx, "", "u1", EventInput{Title: "  会议  ", StartAt: 1500})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got.Title != "会议" {
		t.Errorf("title should be trimmed, got %q", got.Title)
	}
	if got.WorkspaceID != DefaultWorkspaceID {
		t.Errorf("empty workspace should default, got %q", got.WorkspaceID)
	}
	if got.Visibility != VisibilityPrivate {
		t.Errorf("visibility should default to private, got %q", got.Visibility)
	}
	if got.Timezone != DefaultTimezone {
		t.Errorf("timezone should default, got %q", got.Timezone)
	}
	if got.EndAt != 0 {
		t.Errorf("omitted endAt should stay 0 (a deadline), got %d", got.EndAt)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Errorf("timestamps should be set, got %d/%d", got.CreatedAt, got.UpdatedAt)
	}
}

func TestPGUpdateEventKeepsUntouchedFields(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	svc := &Service{store: store, sources: nil}

	created, err := svc.CreateEvent(ctx, "ws1", "u1", EventInput{
		Title: "原标题", Description: "原备注", Location: "原地点",
		StartAt: 1500, EndAt: 1600, Timezone: "Asia/Shanghai", RemindAt: 1400,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	// 只改标题与时间，不带 description/location/remindAt。
	updated, err := svc.UpdateEvent(ctx, created.ID, "ws1", EventInput{
		Title: "新标题", StartAt: 2500, EndAt: 2600,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if updated.Title != "新标题" {
		t.Errorf("title not updated: %q", updated.Title)
	}
	if updated.Description != "原备注" || updated.Location != "原地点" || updated.RemindAt != 1400 {
		t.Errorf("omitted fields must be preserved, got %+v", updated)
	}
	if updated.StartAt != 2500 || updated.EndAt != 2600 {
		t.Errorf("times not updated: %+v", updated)
	}
}

func TestPGUpdateEventRejectsInvalid(t *testing.T) {
	store, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	svc := &Service{store: store, sources: nil}

	created, err := svc.CreateEvent(ctx, "ws1", "u1", EventInput{Title: "x", StartAt: 1500})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if _, err := svc.UpdateEvent(ctx, created.ID, "ws1", EventInput{Title: " ", StartAt: 1500}); err == nil {
		t.Error("blank title should be rejected on update")
	}
	if _, err := svc.UpdateEvent(ctx, created.ID, "ws1", EventInput{Title: "x", StartAt: 2000, EndAt: 1000}); err == nil {
		t.Error("endAt before startAt should be rejected on update")
	}
	if _, err := svc.UpdateEvent(ctx, "missing-id", "ws1", EventInput{Title: "x", StartAt: 1500}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of missing id = %v, want ErrNotFound", err)
	}
}
