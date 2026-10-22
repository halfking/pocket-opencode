package notes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStore_Upsert_Insert tests inserting a new note
func TestStore_Upsert_Insert(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)
	defer cleanupTestData(t, pool)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	note := &Note{
		ID:             "test-note-1",
		UserID:         "user-1",
		WorkspaceID:    "workspace-1",
		Title:          "Test Note",
		Snippet:        "This is a test note",
		ContentType:    "text",
		Domain:         "work",
		Tags:           `["Go","Testing"]`,
		CreatedByVoice: false,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	err = store.Upsert(context.Background(), note)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Verify the note was inserted
	fetched, err := store.GetByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected note to be found")
	}
	if fetched.Title != note.Title {
		t.Errorf("expected title %s, got %s", note.Title, fetched.Title)
	}
}

// TestStore_Upsert_Update tests updating an existing note
func TestStore_Upsert_Update(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)
	defer cleanupTestData(t, pool)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	note := &Note{
		ID:          "test-note-2",
		UserID:      "user-1",
		Title:       "Original Title",
		Snippet:     "Original snippet",
		ContentType: "text",
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}

	err = store.Upsert(context.Background(), note)
	if err != nil {
		t.Fatalf("first Upsert failed: %v", err)
	}

	// Update the note
	note.Title = "Updated Title"
	note.Snippet = "Updated snippet"
	note.UpdatedAt = time.Now().Unix()

	err = store.Upsert(context.Background(), note)
	if err != nil {
		t.Fatalf("second Upsert failed: %v", err)
	}

	// Verify the update
	fetched, err := store.GetByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %s", fetched.Title)
	}
}

// TestStore_Upsert_InvalidDomain tests that invalid domains are handled
func TestStore_Upsert_InvalidDomain(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)
	defer cleanupTestData(t, pool)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	note := &Note{
		ID:          "test-note-3",
		UserID:      "user-1",
		Snippet:     "Test",
		ContentType: "text",
		Domain:      "invalid-domain", // Should be converted to NULL
		CreatedAt:   time.Now().Unix(),
	}

	err = store.Upsert(context.Background(), note)
	if err != nil {
		t.Fatalf("Upsert with invalid domain failed: %v", err)
	}

	fetched, err := store.GetByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched.Domain != "" {
		t.Errorf("expected empty domain for invalid input, got %s", fetched.Domain)
	}
}

// TestStore_List tests listing notes by user
func TestStore_List(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)
	defer cleanupTestData(t, pool)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Insert multiple notes
	for i := 0; i < 3; i++ {
		note := &Note{
			ID:          "test-note-list-" + string(rune('a'+i)),
			UserID:      "user-list",
			Snippet:     "Test note",
			ContentType: "text",
			Domain:      "work",
			CreatedAt:   time.Now().Unix(),
		}
		if err := store.Upsert(context.Background(), note); err != nil {
			t.Fatalf("Upsert %d failed: %v", i, err)
		}
	}

	notes, err := store.List(context.Background(), "user-list", "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(notes) != 3 {
		t.Errorf("expected 3 notes, got %d", len(notes))
	}
}

// TestStore_GetByID_NotFound tests GetByID returns nil for non-existent note
func TestStore_GetByID_NotFound(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	note, err := store.GetByID(context.Background(), "non-existent-id")
	if err != nil {
		t.Fatalf("GetByID should not error on not found: %v", err)
	}
	if note != nil {
		t.Errorf("expected nil note for non-existent ID, got %+v", note)
	}
}

// TestStore_Delete tests soft delete functionality
func TestStore_Delete(t *testing.T) {
	// ⚠ 这条跳过**必须写在测试函数体内**：skipaudit 的 census 与本包台账
	// 只数「测试函数体内」的 t.Skip( ⇒ 跳过点若只藏在 helper 里，读数是 0
	// ⇒ 本包台账的 6 条会被判「过期」⇒ 门当场转红（§960）。
	if testPGDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
	}
	pool := getTestPool(t)
	defer cleanupTestData(t, pool)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	note := &Note{
		ID:          "test-note-delete",
		UserID:      "user-1",
		Snippet:     "To be deleted",
		ContentType: "text",
		CreatedAt:   time.Now().Unix(),
	}

	err = store.Upsert(context.Background(), note)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	err = store.Delete(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify the note is soft-deleted (GetByID should return nil)
	fetched, err := store.GetByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("GetByID after delete failed: %v", err)
	}
	if fetched != nil {
		t.Errorf("expected nil after soft delete, got %+v", fetched)
	}
}

// Helper functions

// getTestPool 建一个**隔离 schema** 的真 PG 连接池，用完即删。
//
// ⚠⚠ §940 的 D2：这一段原来是
//
//	func getTestPool(t *testing.T) *pgxpool.Pool { return nil }
//
// 注释写着「For now, we'll return nil to skip tests that need a real DB」
// ——⇒ ⇒ 本包里**每一个** PG 用例都**无条件 SKIP** ⇒ ⇒ 不是「环境没配」，
// 而是「这条用例在任何环境下都不会跑」⇒ ⇒ ⇒ §940 的 D1（时间戳往返偏一个
// 时区差）就是这么活下来的：门禁里根本没有能看见它的用例。
//
// ⇒ ⇒ ⇒ 照抄 flashcards / calendar 的房规（每测试一个 schema + 结束 DROP
// CASCADE）：只认测试专用 DSN（**无生产库回退** —— email 包踩过并把 schema
// 留在生产库里的事故），search_path 钉在 `notes_test_<hex>`，收尾 DROP。
//
// ⚠ 没有 DSN 时 **Skip**（而不是返回 nil）：返回 nil 会让调用方的
// `if pool == nil { t.Skip }` 变成一段**永远走不到**的分支 ⇒ ⇒ 读代码的人
// 会以为「这些用例在跑」⇒ ⇒ ⇒ 判据的可信度不能靠注释维持。
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return newNotesTestPool(t, nil)
}

// testPGDSN 返回**测试专用** Postgres 的 DSN；没配就是空串。
//
// ⚠ 为什么抽成函数（而不是只在 helper 里判一次）：skipaudit 的 census 与
// 本包台账**只数「测试函数体内」的 t.Skip(** ⇒⇒⇒ 跳过点若只藏在 helper 里，
// 读数是 **0** ⇒⇒⇒ 而运行时**它确实会跳过** ⇒⇒⇒ 台账会被判「条目过期」
// ⇒⇒⇒ 门当场转红（§960 实测：台账 6 条 / 可见跳过点 0 个）。
// ⇒⇒⇒ 所以每个 PG 用例必须在**自己体内**也写一条同义的跳过。
//
// ⚠ 这里**只认测试专用 DSN**（无生产库回退 —— email 包踩过并把 schema
// 留在生产库里的事故）。
func testPGDSN() string {
	if dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return os.Getenv("POCKET_TEST_PG_DSN") // 兼容别名，与 rss/server 同口径
}

// newNotesTestPool 是 getTestPool 的可调参版本。
//
// ⚠ runtimeParams 用来改**会话级**参数（例如 TimeZone）⇒ ⇒ §940 需要它
// 来证明「往返精度**与会话时区无关**」⇒ ⇒ ⇒ 这个参数必须真的生效，
// 否则那条判据就是「恰好在本机时区对」。
func newNotesTestPool(t *testing.T, runtimeParams map[string]string) *pgxpool.Pool {
	t.Helper()
	dsn := testPGDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notes PG integration test")
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
	schema := "notes_test_" + hex.EncodeToString(b)
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
	for k, v := range runtimeParams {
		cfg.ConnConfig.RuntimeParams[k] = v
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	// 用 t.Cleanup 而不是让调用方 defer：下面任一 t.Fatalf 走
	// runtime.Goexit() 时，栈上的 defer 不会执行，schema 就留在库里了。
	t.Cleanup(func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		rootPool.Close()
	})
	return pool
}

func cleanupTestData(t *testing.T, pool *pgxpool.Pool) {
	// 行是隔离 schema 里的，schema 收尾会被整个 DROP ⇒ 这里只是让
	// 同一个用例内的多次写入互不干扰。
	_, err := pool.Exec(context.Background(), `DELETE FROM notes WHERE id LIKE 'test-note-%'`)
	if err != nil {
		t.Logf("cleanup warning: %v", err)
	}
}
