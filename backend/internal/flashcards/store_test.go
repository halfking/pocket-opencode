package flashcards

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestNewStore_RejectsNilPool verifies the construction guard so callers
// don't accidentally bring up a Store backed by a nil pool (which would
// panic on first query). Matches the spirit of scheduledtask's nil-store
// guard.
func TestNewStore_RejectsNilPool(t *testing.T) {
	if _, err := NewStore(context.Background(), nil); err == nil {
		t.Fatal("NewStore(nil pool) should return an error")
	}
}

// TestTagsToJSON verifies the canonical tag-array encoding used by the
// flashcard_notes.tags column (text default "[]").
func TestTagsToJSON(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"a"}, `["a"]`},
		{[]string{"a", "b", "c"}, `["a","b","c"]`},
	}
	for _, c := range cases {
		if got := TagsToJSON(c.in); got != c.want {
			t.Errorf("TagsToJSON(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRecordReview_RejectsInvalidRating verifies the rating bounds check
// without needing a DB.
func TestRecordReview_RejectsInvalidRating(t *testing.T) {
	s := &Store{pool: nil, now: func() time.Time { return time.Unix(0, 0) }}
	// pool is nil so rating validation runs first and short-circuits.
	_, _, err := s.RecordReview(context.Background(), "u", "c", 0, 100)
	if err == nil {
		t.Fatal("rating=0 should fail validation")
	}
	_, _, err = s.RecordReview(context.Background(), "u", "c", 5, 100)
	if err == nil {
		t.Fatal("rating=5 should fail validation")
	}
}

// TestElapsedDays exercises the day-diff helper used inside RecordReview.
func TestElapsedDays(t *testing.T) {
	const day = int64(86400)
	cases := []struct {
		prev, now int64
		want      int
	}{
		{0, 100, 0},           // no prior review → 0
		{100, 50, 0},          // now before prev → 0 (defensive)
		{100, 100, 0},         // same instant → 0
		{day, day * 5, 4},     // 4 days apart
		{day, day*5 + 100, 4}, // sub-day remainder truncated
	}
	for _, c := range cases {
		if got := elapsedDays(c.prev, c.now); got != c.want {
			t.Errorf("elapsedDays(%d, %d) = %d, want %d", c.prev, c.now, got, c.want)
		}
	}
}

// TestNilStore_PoolAndNow sanity-checks the helper accessors on a
// constructed-but-no-migrate Store.
func TestNilStore_PoolAndNow(t *testing.T) {
	s := &Store{pool: nil, now: func() time.Time { return time.Unix(42, 0) }}
	if s.Pool() != nil {
		t.Error("expected nil Pool()")
	}
	if s.Now() != 42 {
		t.Errorf("Now() = %d, want 42", s.Now())
	}
}

// TestStore_CRUD_RoundTrip runs against a real PostgreSQL if POCKET_TEST_PG_DSN
// is set. Skipped otherwise. The intent is exactly the same as the notes
// smoke tests: EnsureSchema → insert → read-back → field assertion.
func TestStore_CRUD_RoundTrip(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available (set POCKET_TEST_POSTGRES_DSN)")
	}
	defer cleanupTestData(t, pool)

	ctx := context.Background()
	s, err := NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	note := &Note{
		ID:     "test-note-fc-1",
		UserID: "user-fc-1",
		DeckID: "deck-default",
		Front:  "What is the capital of France?",
		Back:   "Paris",
		Tags:   `["geo","capitals"]`,
	}
	if err := s.CreateNote(ctx, note); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	got, err := s.GetNote(ctx, note.UserID, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got == nil {
		t.Fatal("expected note back, got nil")
	}
	if got.Front != note.Front || got.Back != note.Back || got.DeckID != note.DeckID {
		t.Errorf("field mismatch: got %+v", got)
	}
	if got.Tags != note.Tags {
		t.Errorf("tags mismatch: got %q want %q", got.Tags, note.Tags)
	}
	if got.Usn < 1 {
		t.Errorf("expected usn>=1 after create, got %d", got.Usn)
	}
}

// getTestPool 起一个**隔离 schema** 的真 PG 连接池；没配 DSN 时返回 nil
// （调用方 Skip）。
//
// ## 2026-10-05：这段原来是 `return nil`
//
// 上一版注释写着「v1 smoke test 先保守跳过，production CI 以后再接」——而
// 「以后」没有到来。后果不是「环境没配」而是**这条用例在任何环境下都不会
// 跑**：`TestStore_CRUD_RoundTrip` 的名字承诺「对真 PostgreSQL 走一遍
// EnsureSchema → insert → read-back」，实际是一次无条件 SKIP，而且它出现在
// 任何覆盖率/SKIP 统计里，读起来与「PG 测试在这里跑过」毫无区别。
//
// 同仓的 rss / server 包早就用「每测试一个 schema + 结束 DROP」的模式把这类
// 测试接上了（见 rss/starter_pg_test.go:newPGStore），本包照抄该模式：
// 只认测试专用 DSN（无生产库回退——email 包踩过并把 schema 留在生产库里的
// 事故），search_path 钉在 `fc_test_<hex>`，收尾 DROP CASCADE。
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("POCKET_TEST_PG_DSN") // 兼容别名，与 rss/server 同口径
	}
	if dsn == "" {
		return nil
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "fc_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
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
	// 用 t.Cleanup 而不是让调用方 defer：下面任一 t.Fatalf 走
	// runtime.Goexit() 时，栈上的 defer 不会执行，schema 就留在库里了
	// （email 包为此专门改过一次，注释见 store_workspace_test.go）。
	t.Cleanup(func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		rootPool.Close()
	})
	return pool
}

func cleanupTestData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if pool == nil {
		return
	}
	_, _ = pool.Exec(context.Background(), `DELETE FROM flashcard_notes WHERE id LIKE 'test-note-fc-%'`)
}
