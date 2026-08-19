package redclaw

// audit_pg_test.go — integration tests for PG-backed audit store.
//
// Mirrors lobster/store_test.go: isolated schema per test, skipped when
// POCKET_TEST_POSTGRES_DSN is unset so `go test ./...` stays green in CI
// environments without PG.

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

func testDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func newTestPGAuditStore(t *testing.T) (*PGAuditStore, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping audit PG integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// Isolated schema so concurrent test runs don't collide.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "audit_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// Pin search_path via pgxpool config so we don't mutate the DSN string
	// (which would break keyword/value DSNs and URLs without a '?').
	// Mirrors the pattern in identity/store_test.go and lobster/store_test.go.
	scopedCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	scopedCfg.ConnConfig.RuntimeParams["search_path"] = schema
	scopedPool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	cleanup := func() {
		scopedPool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
	store, err := NewPGAuditStore(scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("NewPGAuditStore: %v", err)
	}
	return store, cleanup
}

func mustRecordPG(t *testing.T, s *PGAuditStore, tenant, action string, ts time.Time) *AuditEntry {
	t.Helper()
	entry := &AuditEntry{
		Action:    action,
		UserID:    "user-1",
		TenantID:  tenant,
		Timestamp: ts,
	}
	if err := s.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}
	return entry
}

func TestPGAuditStore_RecordAndQuery(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()

	entry := &AuditEntry{
		Action:     "chat.send",
		UserID:     "user-123",
		TenantID:   "enterprise-a",
		Resource:   "session/sess_abc",
		Detail:     "Sent message to AI",
		DurationMs: 1500,
		Success:    true,
		IP:         "10.0.0.1",
	}

	if err := s.Record(entry); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	entries, err := s.Query(AuditQuery{TenantID: "enterprise-a"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Action != "chat.send" || got.UserID != "user-123" {
		t.Errorf("unexpected action/user: %s/%s", got.Action, got.UserID)
	}
	if !got.Success || got.DurationMs != 1500 || got.IP != "10.0.0.1" {
		t.Errorf("roundtrip lost fields: success=%v duration=%d ip=%q", got.Success, got.DurationMs, got.IP)
	}
}

func TestPGAuditStore_QueryRangeTimeWindow(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	base := time.UnixMilli(1_000_000)

	mustRecordPG(t, s, "ws-a", "chat.send", base.Add(-time.Hour))
	mustRecordPG(t, s, "ws-a", "chat.send", base.Add(-30*time.Minute))
	mustRecordPG(t, s, "ws-a", "chat.send", base.Add(30*time.Minute))
	mustRecordPG(t, s, "ws-a", "chat.send", base.Add(time.Hour))

	// QueryRange 的 EndTime 是闭开区间 [start, end) 的端点；
	// 我们要拿到 30min 处的条目，所以上界取到 +31min。
	page, err := s.QueryRange(AuditQuery{
		StartTime: base.Add(-30 * time.Minute),
		EndTime:   base.Add(31 * time.Minute),
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("expected 2 entries in window, got %d", len(page.Entries))
	}
	for _, e := range page.Entries {
		if e.Timestamp.Before(base.Add(-30*time.Minute)) || !e.Timestamp.Before(base.Add(31*time.Minute)) {
			t.Fatalf("entry outside window: %v", e.Timestamp)
		}
	}
	if page.NextCursor != "" {
		t.Fatal("window fully consumed, no cursor expected")
	}
}

func TestPGAuditStore_QueryRangeCursorPagination(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	base := time.UnixMilli(2_000_000)
	var ids []string
	for i := 0; i < 25; i++ {
		ids = append(ids, mustRecordPG(t, s, "ws-a", "chat.send", base.Add(time.Duration(i)*time.Millisecond)).ID)
	}

	var collected []*AuditEntry
	cursor := ""
	pages := 0
	for {
		query := AuditQuery{StartTime: base, Limit: 10}
		if cursor != "" {
			query.AfterCursor = cursor
		}
		page, err := s.QueryRange(query)
		if err != nil {
			t.Fatal(err)
		}
		collected = append(collected, page.Entries...)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(collected) != 25 {
		t.Fatalf("expected all 25 entries across pages, got %d", len(collected))
	}
	// 顺序与去重：分页结果必须按时间升序且无重复 id。
	seen := map[string]bool{}
	for i, e := range collected {
		if seen[e.ID] {
			t.Fatalf("duplicate entry across pages: %s", e.ID)
		}
		seen[e.ID] = true
		if i > 0 && collected[i-1].Timestamp.After(e.Timestamp) {
			t.Fatal("pages must be time-ordered")
		}
	}
	if collected[0].ID != ids[0] || collected[24].ID != ids[24] {
		t.Fatal("pagination lost entries at boundaries")
	}
	if pages != 3 {
		t.Fatalf("expected 3 pages (10+10+5), got %d", pages)
	}
}

func TestPGAuditStore_QueryRangeSameTimestampCursorDisambiguates(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	ts := time.UnixMilli(3_000_000)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, mustRecordPG(t, s, "ws-a", fmt.Sprintf("act_%d", i), ts).ID)
	}
	// 取前两条作为第一页。
	page1, err := s.QueryRange(AuditQuery{StartTime: ts, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 unexpected: %+v", page1)
	}
	page2, err := s.QueryRange(AuditQuery{StartTime: ts, Limit: 10, AfterCursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Entries) != 3 {
		t.Fatalf("same-ms cursor must resume exactly after the last id, got %d", len(page2.Entries))
	}
	if page2.Entries[0].ID == ids[0] || page2.Entries[0].ID == ids[1] {
		t.Fatal("cursor did not skip exported same-ms entries")
	}
}

func TestPGAuditStore_QueryRangeFiltersTenant(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	base := time.UnixMilli(4_000_000)
	mustRecordPG(t, s, "ws-a", "chat.send", base)
	mustRecordPG(t, s, "ws-b", "chat.send", base.Add(time.Second))
	mustRecordPG(t, s, "ws-a", "file.read", base.Add(2*time.Second))

	page, err := s.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("tenant filter expected 2, got %d", len(page.Entries))
	}
	page, err = s.QueryRange(AuditQuery{TenantID: "ws-a", Action: "file.read", StartTime: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("action filter expected 1, got %d", len(page.Entries))
	}
}

func TestPGAuditStore_RecordIdempotentOnConflict(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	ts := time.UnixMilli(5_000_000)
	entry := mustRecordPG(t, s, "ws-a", "chat.send", ts)

	// 同 id 重复写入必须被 ON CONFLICT 吞掉而非报错。
	if err := s.Record(entry); err != nil {
		t.Fatalf("duplicate Record must not error: %v", err)
	}
	entries, err := s.Query(AuditQuery{TenantID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected dedup to keep 1 row, got %d", len(entries))
	}
}

func TestPGAuditStore_RecordPreservesCallerTimestamp(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	ts := time.UnixMilli(7_000_000)
	entry := mustRecordPG(t, s, "ws-a", "chat.send", ts)
	if !entry.Timestamp.Equal(ts) {
		t.Fatalf("caller timestamp must be preserved, got %v", entry.Timestamp)
	}
	if entry.ID == "" {
		t.Fatal("Record must always assign an id")
	}
	// 回读时间戳也应一致（毫秒精度经 TIMESTAMPTZ 往返无损）。
	page, err := s.QueryRange(AuditQuery{StartTime: ts.Add(-time.Second), EndTime: ts.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || !page.Entries[0].Timestamp.Equal(ts) {
		t.Fatalf("roundtrip timestamp mismatch: %+v", page.Entries)
	}
}

func TestPGAuditStore_WithPoolNil(t *testing.T) {
	// nil pool 的防御在 PGAuditStoreWithPool：返回 (nil, nil) 让调用方回退内存版。
	store, err := PGAuditStoreWithPool(nil)
	if err != nil {
		t.Fatalf("PGAuditStoreWithPool(nil) must not error, got %v", err)
	}
	if store != nil {
		t.Fatalf("PGAuditStoreWithPool(nil) must return nil store, got %T", store)
	}
}

func TestPGAuditStore_FlushUnsupported(t *testing.T) {
	// PG 版持久化，Flush 语义不存在：返回空且不 panic。
	s := &PGAuditStore{}
	if got := s.Flush(); got != nil {
		t.Fatalf("PG Flush must return nil, got %d entries", len(got))
	}
}
