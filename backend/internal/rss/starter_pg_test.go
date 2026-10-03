package rss

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newPGStore 起一个隔离 schema 的真实 PG 测试库。
// 每个测试一个独立 schema，结束后整体 DROP —— 不用共享表也就不会互相污染。
func newPGStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("POCKET_TEST_PG_DSN")
	}
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping rss integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "rss_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	s, err := NewStore(ctx, pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewStore: %v", err)
	}
	drop := func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
	return s, drop
}

func testScope() Scope { return Scope{UserID: "u1", WorkspaceID: "ws1"} }

// TestImportStarterSourcesIsIdempotent 锁住"一键导入"最关键的性质：
// 第二次导入不能产生重复订阅，也不能把用户改过的标题冲掉。
func TestImportStarterSourcesIsIdempotent(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()

	first, err := s.ImportStarterSources(ctx, sc, StarterImportOptions{Enabled: true})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Created != first.Total || first.Skipped != 0 {
		t.Fatalf("first import: created=%d skipped=%d total=%d", first.Created, first.Skipped, first.Total)
	}
	if first.Created < 20 {
		t.Errorf("catalog too small: %d feeds", first.Created)
	}
	// 分类必须真的落库，否则日报分不了组。
	cats := map[string]int{}
	for _, src := range first.Sources {
		cats[src.Category]++
	}
	for _, cat := range StarterCategories() {
		if cats[cat] == 0 {
			t.Errorf("no source imported with category %q", cat)
		}
	}

	// 用户把其中一条改成自己的标题，再导入一次。
	edited := first.Sources[0]
	if _, err := s.UpdateSource(ctx, edited.ID, UpdateSourceRequest{Title: strPtr("我自己的标题")}, sc); err != nil {
		t.Fatalf("update source: %v", err)
	}

	second, err := s.ImportStarterSources(ctx, sc, StarterImportOptions{Enabled: true})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Created != 0 {
		t.Errorf("second import created %d rows, want 0", second.Created)
	}
	if second.Skipped != first.Created {
		t.Errorf("second import skipped %d, want %d", second.Skipped, first.Created)
	}
	sources, err := s.ListSources(ctx, sc)
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != first.Created {
		t.Errorf("source count = %d, want %d (no duplicates)", len(sources), first.Created)
	}
	after, err := s.GetSource(ctx, edited.ID, sc)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if after.Title != "我自己的标题" {
		t.Errorf("re-import overwrote user edit: title = %q", after.Title)
	}
}

func TestImportStarterSourcesRespectsCategoryFilterAndCap(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()

	res, err := s.ImportStarterSources(ctx, sc, StarterImportOptions{
		Categories:     []string{CategoryFinance},
		MaxPerCategory: 2,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("created %d, want 2 (cap)", res.Created)
	}
	sources, err := s.ListSources(ctx, sc)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, src := range sources {
		if src.Category != CategoryFinance {
			t.Errorf("category filter leaked: %q is %q", src.URL, src.Category)
		}
	}
}

func TestImportStarterSourcesRejectsInvalidScope(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	if _, err := s.ImportStarterSources(context.Background(), Scope{}, StarterImportOptions{}); err != ErrInvalidScope {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

func TestListActiveScopes(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()

	if got, err := s.ListActiveScopes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty db: got %v err %v", got, err)
	}
	if _, err := s.ImportStarterSources(ctx, Scope{UserID: "u1", WorkspaceID: "ws1"}, StarterImportOptions{}); err != nil {
		t.Fatalf("import ws1: %v", err)
	}
	if _, err := s.ImportStarterSources(ctx, Scope{UserID: "u1", WorkspaceID: "ws2"}, StarterImportOptions{}); err != nil {
		t.Fatalf("import ws2: %v", err)
	}
	got, err := s.ListActiveScopes(ctx)
	if err != nil {
		t.Fatalf("ListActiveScopes: %v", err)
	}
	if len(got) != 2 || got[0].WorkspaceID != "ws1" || got[1].WorkspaceID != "ws2" {
		t.Errorf("scopes = %+v, want ws1 then ws2", got)
	}
}

// seedItemsAt 直接写行，绕开 fetcher：这里要测的是时间窗聚合，不是解析。
func seedItemsAt(t *testing.T, s *Store, sc Scope, category string, at time.Time, n int) {
	t.Helper()
	ctx := context.Background()
	src, err := s.CreateSource(ctx, CreateSourceRequest{
		URL: "https://seed.example/" + category + "/" + fmt.Sprint(at.Unix()), Title: category + " 源", Category: category, Enabled: true, FetchInterval: time.Hour,
	}, sc)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	for i := 0; i < n; i++ {
		it := Item{
			SourceID: src.ID, UserID: sc.UserID, WorkspaceID: sc.WorkspaceID,
			Hash:    fmt.Sprintf("%s-%d-%d", category, at.Unix(), i),
			URL:     fmt.Sprintf("https://seed.example/%s/%d", category, i),
			Title:   fmt.Sprintf("%s 条目 %d", category, i),
			Summary: "  摘要内容  ", PublishedAt: &at, Status: ItemUnread,
		}
		if _, err := s.UpsertItemScoped(ctx, it, sc); err != nil {
			t.Fatalf("upsert item: %v", err)
		}
	}
}

// TestBuildDigestOnlyCountsThatDay 是日报正确性的核心：跨天不能串。
func TestBuildDigestOnlyCountsThatDay(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	seedItemsAt(t, s, sc, CategoryIT, day.Add(9*time.Hour), 3)
	seedItemsAt(t, s, sc, CategoryFinance, day.Add(20*time.Hour), 2)
	seedItemsAt(t, s, sc, CategoryNews, day.Add(-time.Hour), 4)   // 前一天 23:00
	seedItemsAt(t, s, sc, CategoryNews, day.Add(24*time.Hour), 5) // 次日 00:00 整点

	d, err := s.BuildDigest(ctx, sc, day, DigestOptions{IncludeSummary: true})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	if d.Date != "2026-10-03" {
		t.Errorf("date = %q", d.Date)
	}
	if d.ItemCount != 5 {
		t.Errorf("item count = %d, want 5 (前一日与次日 00:00 都不该算进来)", d.ItemCount)
	}
	if len(d.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(d.Sections))
	}
	if d.Sections[0].Category != CategoryIT || d.Sections[1].Category != CategoryFinance {
		t.Errorf("section order = %q, %q", d.Sections[0].Category, d.Sections[1].Category)
	}
	if d.Sections[0].Items[0].Summary != "摘要内容" {
		t.Errorf("summary not normalized: %q", d.Sections[0].Items[0].Summary)
	}
	if d.SourceCount != 2 {
		t.Errorf("source count = %d, want 2", d.SourceCount)
	}
}

func TestBuildDigestRespectsMaxPerSection(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	seedItemsAt(t, s, sc, CategoryIT, day.Add(10*time.Hour), 25)

	d, err := s.BuildDigest(ctx, sc, day, DigestOptions{MaxPerSection: 4})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	if d.ItemCount != 25 {
		t.Errorf("item count should be the true total, got %d", d.ItemCount)
	}
	if len(d.Sections[0].Items) != 4 {
		t.Errorf("section items = %d, want 4", len(d.Sections[0].Items))
	}
}

func TestSaveDigestIsIdempotentPerUserAndDate(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	seedItemsAt(t, s, sc, CategoryIT, day.Add(10*time.Hour), 2)

	build := func() *Digest {
		d, err := s.BuildDigest(ctx, sc, day, DigestOptions{})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		saved, err := s.SaveDigest(ctx, sc, d)
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		return saved
	}
	first := build()
	second := build()
	// 同一天重复生成走 ON CONFLICT DO UPDATE：行 id 必须保持不变（还是那一行），
	// 而不是每次插一条新行。
	if first.ID != second.ID {
		t.Errorf("row id changed across re-save: %q -> %q, want the same row", first.ID, second.ID)
	}
	var rows int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM rss_digests WHERE user_id=$1 AND workspace_id=$2`, sc.UserID, sc.WorkspaceID).Scan(&rows); err != nil {
		t.Fatalf("count digests: %v", err)
	}
	if rows != 1 {
		t.Errorf("digest rows = %d, want 1 (re-save must upsert, not append)", rows)
	}
	got, err := s.GetDigest(ctx, sc, "2026-10-03")
	if err != nil {
		t.Fatalf("GetDigest: %v", err)
	}
	if got.ItemCount != 2 || len(got.Sections) != 1 {
		t.Errorf("stored digest = %+v", got)
	}

	// 另一个作用域的同一天日报不能覆盖这一条。
	other := Scope{UserID: "u1", WorkspaceID: "ws2"}
	if _, err := s.SaveDigest(ctx, other, &Digest{Date: "2026-10-03", Headline: "别人的", ItemCount: 1}); err != nil {
		t.Fatalf("save other scope: %v", err)
	}
	again, err := s.GetDigest(ctx, sc, "2026-10-03")
	if err != nil {
		t.Fatalf("GetDigest after other scope save: %v", err)
	}
	if again.Headline == "别人的" {
		t.Error("digest from another workspace overwrote this one")
	}
	if _, err := s.GetDigest(ctx, other, "2026-10-04"); err != ErrNotFound {
		t.Errorf("GetDigest for absent date: err=%v, want ErrNotFound", err)
	}
}

func TestListDigestsNewestFirst(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx := context.Background()
	sc := testScope()
	for _, d := range []string{"2026-10-01", "2026-10-02", "2026-10-03"} {
		if _, err := s.SaveDigest(ctx, sc, &Digest{Date: d, Headline: "h" + d, ItemCount: 1}); err != nil {
			t.Fatalf("save %s: %v", d, err)
		}
	}
	list, err := s.ListDigests(ctx, sc, 30)
	if err != nil {
		t.Fatalf("ListDigests: %v", err)
	}
	if len(list) != 3 || list[0].Date != "2026-10-03" || list[2].Date != "2026-10-01" {
		t.Errorf("list = %+v, want newest first", list)
	}
}

func strPtr(s string) *string { return &s }
