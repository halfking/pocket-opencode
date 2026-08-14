package redclaw

import (
	"fmt"
	"testing"
	"time"
)

func mustRecord(s *AuditStore, tenant, action string, ts time.Time) *AuditEntry {
	entry := &AuditEntry{
		Action:    action,
		UserID:    "user-1",
		TenantID:  tenant,
		Timestamp: ts,
	}
	if err := s.Record(entry); err != nil {
		panic(err)
	}
	return entry
}

func TestQueryRangeRespectsTimeWindow(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(1_000_000)
	for i := 0; i < 10; i++ {
		mustRecord(s, "ws-a", "chat.send", base.Add(time.Duration(i)*time.Second))
	}

	page, err := s.QueryRange(AuditQuery{
		StartTime: base.Add(2 * time.Second),
		EndTime:   base.Add(5 * time.Second), // [2s, 5s) → 3 条
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("expected 3 entries in window, got %d", len(page.Entries))
	}
	for _, e := range page.Entries {
		if e.Timestamp.Before(base.Add(2*time.Second)) || !e.Timestamp.Before(base.Add(5*time.Second)) {
			t.Fatalf("entry outside window: %v", e.Timestamp)
		}
	}
	if page.NextCursor != "" {
		t.Fatal("window fully consumed, no cursor expected")
	}
}

func TestQueryRangeCursorPagination(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(2_000_000)
	var ids []string
	for i := 0; i < 25; i++ {
		ids = append(ids, mustRecord(s, "ws-a", "chat.send", base.Add(time.Duration(i)*time.Millisecond)).ID)
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

func TestQueryRangeSameTimestampCursorDisambiguates(t *testing.T) {
	s := NewAuditStore()
	ts := time.UnixMilli(3_000_000)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, mustRecord(s, "ws-a", fmt.Sprintf("act_%d", i), ts).ID)
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

func TestQueryRangeFiltersTenant(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(4_000_000)
	mustRecord(s, "ws-a", "chat.send", base)
	mustRecord(s, "ws-b", "chat.send", base.Add(time.Second))
	mustRecord(s, "ws-a", "file.read", base.Add(2*time.Second))

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

func TestQueryRangeLimitClamped(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(5_000_000)
	for i := 0; i < 20; i++ {
		mustRecord(s, "ws-a", "chat.send", base.Add(time.Duration(i)*time.Millisecond))
	}
	page, err := s.QueryRange(AuditQuery{StartTime: base, Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 20 || page.NextCursor != "" {
		t.Fatalf("over-limit query must clamp but still return all entries, got %d", len(page.Entries))
	}
	page, err = s.QueryRange(AuditQuery{StartTime: base, Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	// 零 limit 应用默认 500，不足一页时全量返回且无游标。
	if len(page.Entries) != 20 || page.NextCursor != "" {
		t.Fatalf("zero limit must apply default %d and return all entries, got %d", auditDefaultRangeLimit, len(page.Entries))
	}
}

func TestQueryRangeInvalidCursorIgnoredGracefully(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(6_000_000)
	mustRecord(s, "ws-a", "chat.send", base)
	page, err := s.QueryRange(AuditQuery{StartTime: base, AfterCursor: "not-a-cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("invalid cursor must degrade to timestamp-only query, got %d", len(page.Entries))
	}
}

func TestRecordPreservesCallerTimestamp(t *testing.T) {
	s := NewAuditStore()
	ts := time.UnixMilli(7_000_000)
	entry := mustRecord(s, "ws-a", "chat.send", ts)
	if !entry.Timestamp.Equal(ts) {
		t.Fatalf("caller timestamp must be preserved, got %v", entry.Timestamp)
	}
	if entry.ID == "" {
		t.Fatal("Record must always assign an id")
	}
}
