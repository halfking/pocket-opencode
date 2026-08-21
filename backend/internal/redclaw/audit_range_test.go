package redclaw

import (
	"context"
	"errors"
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
		EndTime:   base.Add(5 * time.Second),
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

func TestQueryRangeFiltersTenantAndExclusion(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(4_000_000)
	mustRecord(s, "ws-a", "chat.send", base)
	mustRecord(s, "ws-b", "chat.send", base.Add(time.Second))
	mustRecord(s, "ws-a", "file.read", base.Add(2*time.Second))
	mustRecord(s, "ws-a", "audit.export", base.Add(3*time.Second))

	page, err := s.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: base, ExcludeAction: "audit.export"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("tenant/exclusion filter expected 2, got %d", len(page.Entries))
	}
	page, err = s.QueryRange(AuditQuery{TenantID: "ws-a", Action: "file.read", StartTime: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("action filter expected 1, got %d", len(page.Entries))
	}
}

func TestQueryRangeExactLimitHasNoCursor(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(4_500_000)
	for i := 0; i < 2; i++ {
		mustRecord(s, "ws-a", "chat.send", base.Add(time.Duration(i)*time.Second))
	}
	page, err := s.QueryRange(AuditQuery{StartTime: base, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextCursor != "" {
		t.Fatalf("exact terminal page=%+v, want two entries without a cursor", page)
	}
}

func TestQueryRangeOutOfOrderTimestamps(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(4_750_000)
	mustRecord(s, "ws-a", "chat.send", base.Add(2*time.Second))
	mustRecord(s, "ws-a", "chat.send", base)
	mustRecord(s, "ws-a", "chat.send", base.Add(time.Second))

	page, err := s.QueryRange(AuditQuery{StartTime: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("entries=%d, want 3", len(page.Entries))
	}
	for i := 1; i < len(page.Entries); i++ {
		if page.Entries[i].Timestamp.Before(page.Entries[i-1].Timestamp) {
			t.Fatalf("out-of-order page: %+v", page.Entries)
		}
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
	if len(page.Entries) != 20 || page.NextCursor != "" {
		t.Fatalf("zero limit must apply default %d and return all entries, got %d", auditDefaultRangeLimit, len(page.Entries))
	}
}

func TestQueryRangeContextCanceled(t *testing.T) {
	s := NewAuditStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.QueryRangeContext(ctx, AuditQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v, want context.Canceled", err)
	}
}

func TestQueryRangeLegacyMillisecondCursor(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(6_000_000)
	first := mustRecord(s, "ws-a", "chat.send", base)
	second := mustRecord(s, "ws-a", "chat.send", base.Add(time.Second))
	page, err := s.QueryRange(AuditQuery{StartTime: base, AfterCursor: fmt.Sprintf("%d:%s", first.Timestamp.UnixMilli(), first.ID)})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != second.ID {
		t.Fatalf("legacy millisecond cursor page=%+v err=%v", page, err)
	}
}

func TestQueryRangeInvalidCursorReturnsError(t *testing.T) {
	s := NewAuditStore()
	base := time.UnixMilli(6_000_000)
	mustRecord(s, "ws-a", "chat.send", base)
	_, err := s.QueryRange(AuditQuery{StartTime: base, AfterCursor: "not-a-cursor"})
	if !errors.Is(err, ErrInvalidAuditCursor) {
		t.Fatalf("invalid cursor error=%v, want ErrInvalidAuditCursor", err)
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
