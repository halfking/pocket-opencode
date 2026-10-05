package calendar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPGStore 建一个隔离 schema 的 store，用完即删。
//
// 只读 POCKET_TEST_POSTGRES_DSN，不回退 POCKET_POSTGRES_DSN：回退会让本地
// `go test ./...` 零配置地打到生产库（2026-10-02 已在生产库留下
// meeting_test_* 残留 schema，见 server/audit_pg_test.go 的同款注释）。
func newTestPGStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping calendar PG integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "calendar_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	store, err := NewStore(pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("NewStore: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
	return store, cleanup
}

// testPool exposes the scoped pool so a test can create the foreign tables the
// feed reads from (tasks / scheduled_tasks are owned by other packages).
func testPool(t *testing.T, s *Store) *pgxpool.Pool {
	t.Helper()
	if s == nil || s.pool == nil {
		t.Fatal("store has no pool")
	}
	return s.pool
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("exec failed: %v\nsql: %s", err, sql)
	}
}

func TestPGCreateAndGet(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	e := &Event{
		ID: NewID(), WorkspaceID: "ws1", OwnerUserID: "u1", Title: "季度评审",
		Description: "材料已发", Location: "会议室 A",
		StartAt: 1000, EndAt: 2000, AllDay: false, Timezone: "Asia/Shanghai",
		Visibility: VisibilityPrivate, CreatedAt: 10, UpdatedAt: 10,
	}
	if err := s.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, e.ID, "ws1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != e.Title || got.StartAt != e.StartAt || got.EndAt != e.EndAt {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if got.AllDay != false || got.Timezone != "Asia/Shanghai" {
		t.Errorf("bool/timezone lost: %+v", got)
	}
}

// 租户隔离是这个包的硬不变量：跨租户读必须读成「不存在」，而不是别人的行。
func TestPGWorkspaceIsolation(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	e := &Event{ID: NewID(), WorkspaceID: "ws1", OwnerUserID: "u1", Title: "私密日程",
		StartAt: 1000, EndAt: 2000, Timezone: "Asia/Shanghai", Visibility: VisibilityPrivate,
		CreatedAt: 10, UpdatedAt: 10}
	if err := s.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Get(ctx, e.ID, "ws2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get = %v, want ErrNotFound", err)
	}
	if err := s.Update(ctx, &Event{ID: e.ID, WorkspaceID: "ws2", Title: "x", StartAt: 1,
		Visibility: VisibilityPrivate}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Update = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, e.ID, "ws2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Delete = %v, want ErrNotFound", err)
	}
	// 原租户的行必须还在 —— 上面三次操作都不该碰到它。
	if _, err := s.Get(ctx, e.ID, "ws1"); err != nil {
		t.Errorf("owner workspace row disappeared: %v", err)
	}
}

// ListRange 的区间语义是本文件最容易被写错的地方：跨天事件与零长到期点。
func TestPGListRangeSemantics(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	mk := func(title string, start, end int64, vis string) {
		t.Helper()
		e := &Event{ID: NewID(), WorkspaceID: "ws1", OwnerUserID: "u1", Title: title,
			StartAt: start, EndAt: end, Timezone: "Asia/Shanghai", Visibility: vis,
			CreatedAt: 10, UpdatedAt: 10}
		if err := s.Create(ctx, e); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}

	mk("早于窗口的跨天事件", 100, 5000, VisibilityPrivate) // 跨 [1000,3000)
	mk("窗口内单日", 1500, 1600, VisibilityPrivate)
	mk("零长到期点", 2000, 2000, VisibilityPrivate)
	mk("窗口外", 9000, 9100, VisibilityPrivate)
	mk("右开端点恰好等于 from 的事件", 1000, 1001, VisibilityPrivate)
	mk("左开端点恰好等于 to 的事件", 2999, 3000, VisibilityPrivate)

	got, err := s.ListRange(ctx, "ws1", "u1", 1000, 3000, false)
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	titles := map[string]bool{}
	for _, e := range got {
		titles[e.Title] = true
	}
	for _, want := range []string{"早于窗口的跨天事件", "窗口内单日", "零长到期点", "右开端点恰好等于 from 的事件", "左开端点恰好等于 to 的事件"} {
		if !titles[want] {
			t.Errorf("missing %q in range results", want)
		}
	}
	if titles["窗口外"] {
		t.Error("event fully outside the range must not be returned")
	}
}

func TestPGListRangeVisibility(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	mk := func(title, owner, vis string) {
		t.Helper()
		e := &Event{ID: NewID(), WorkspaceID: "ws1", OwnerUserID: owner, Title: title,
			StartAt: 1500, EndAt: 1600, Timezone: "Asia/Shanghai", Visibility: vis,
			CreatedAt: 10, UpdatedAt: 10}
		if err := s.Create(ctx, e); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}
	mk("我的私密", "u1", VisibilityPrivate)
	mk("我的共享", "u1", VisibilityShared)
	mk("别人的私密", "u2", VisibilityPrivate)
	mk("别人的共享", "u2", VisibilityShared)

	privateOnly, err := s.ListRange(ctx, "ws1", "u1", 1000, 3000, false)
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	got := map[string]bool{}
	for _, e := range privateOnly {
		got[e.Title] = true
	}
	if !got["我的私密"] || !got["我的共享"] {
		t.Error("owner must see both own private and own shared events")
	}
	if !got["别人的共享"] {
		t.Error("shared events from other users must be visible in the workspace")
	}
	if got["别人的私密"] {
		t.Error("another user's PRIVATE event must never leak")
	}
}

func TestPGUpdateAndDelete(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	e := &Event{ID: NewID(), WorkspaceID: "ws1", OwnerUserID: "u1", Title: "旧标题",
		StartAt: 1000, EndAt: 2000, Timezone: "Asia/Shanghai", Visibility: VisibilityPrivate,
		CreatedAt: 10, UpdatedAt: 10}
	if err := s.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	e.Title = "新标题"
	e.StartAt = 3000
	e.EndAt = 4000
	e.UpdatedAt = 99
	if err := s.Update(ctx, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(ctx, e.ID, "ws1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "新标题" || got.StartAt != 3000 || got.EndAt != 4000 || got.UpdatedAt != 99 {
		t.Errorf("update not persisted: %+v", got)
	}
	// 创建时间不应被 update 改写。
	if got.CreatedAt != 10 {
		t.Errorf("CreatedAt must be immutable, got %d", got.CreatedAt)
	}

	if err := s.Delete(ctx, e.ID, "ws1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, e.ID, "ws1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete Get = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, e.ID, "ws1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestPGCreateRejectsBadInput(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()

	cases := []struct {
		name string
		e    Event
	}{
		{"缺 id", Event{ID: "", Title: "x", StartAt: 1, Visibility: VisibilityPrivate}},
		{"缺标题", Event{ID: NewID(), Title: "  ", StartAt: 1, Visibility: VisibilityPrivate}},
		{"缺开始时间", Event{ID: NewID(), Title: "x", StartAt: 0, Visibility: VisibilityPrivate}},
		{"非法可见性", Event{ID: NewID(), Title: "x", StartAt: 1, Visibility: "public"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.e
			if err := s.Create(ctx, &e); err == nil {
				t.Error("expected Create to reject bad input")
			}
		})
	}
}

func TestPGListRangeEmptyWindow(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	ctx := context.Background()
	// to <= from 必须是空结果而不是报错，更不能变成全表扫描。
	got, err := s.ListRange(ctx, "ws1", "u1", 1000, 1000, false)
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestPGMigrateIsIdempotent(t *testing.T) {
	s, done := newTestPGStore(t)
	defer done()
	// NewStore 已跑过一次 migrate；再跑一次必须无副作用（服务重启会走这条路径）。
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
