package redclaw

// audit_cursor_precision_test.go — regression coverage for the cursor
// precision bug (P1 audit export verification).
//
// The export cursor encodes (timestamp, id). encodeAuditCursor historically
// used UnixMilli(), dropping sub-millisecond precision. With time.Now()-style
// nanosecond timestamps, two entries sharing a millisecond would be
// double-counted across pages: the next page's filter `(timestamp, id) >
// (cursorTs, cursorID)` compared the stored µs/ns timestamp against a cursor
// truncated to whole milliseconds, so the cursor entry's entire ms bucket was
// re-included. The cursor must encode full precision so the boundary entry is
// excluded exactly.

import (
	"testing"
	"time"
)

func TestQueryRangeSubMilliCursorNoDoubleCount(t *testing.T) {
	store := NewAuditStore()
	base := time.Unix(1_700_000_000, 0)

	// Two entries share the same millisecond (1123.4ms and 1123.45ms); the
	// rest are in distinct milliseconds.
	stamps := []time.Time{
		base.Add(100 * time.Millisecond),                      // 1100.000ms
		base.Add(123*time.Millisecond + 400*time.Microsecond), // 1123.400ms
		base.Add(123*time.Millisecond + 450*time.Microsecond), // 1123.450ms (same ms)
		base.Add(200 * time.Millisecond),                      // 1200.000ms
	}
	for _, tm := range stamps {
		if err := store.Record(&AuditEntry{
			Action:    "act",
			UserID:    "u",
			TenantID:  "ws",
			Timestamp: tm,
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	var collected []*AuditEntry
	cursor := ""
	for page := 0; page < 10; page++ {
		q := AuditQuery{TenantID: "ws", StartTime: base, Limit: 2}
		if cursor != "" {
			q.AfterCursor = cursor
		}
		p, err := store.QueryRange(q)
		if err != nil {
			t.Fatalf("QueryRange: %v", err)
		}
		collected = append(collected, p.Entries...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	if len(collected) != len(stamps) {
		t.Fatalf("expected %d unique entries across pages, got %d (double-count bug)", len(stamps), len(collected))
	}
	seen := make(map[string]bool, len(collected))
	for _, e := range collected {
		if seen[e.ID] {
			t.Fatalf("duplicate entry id across pages (double-count bug): %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestQueryRangeSubMilliSameTimestampCursorDisambiguates(t *testing.T) {
	store := NewAuditStore()
	// All five share one millisecond but differ in sub-millisecond precision,
	// exercising the same-ms disambiguation under realistic timestamps.
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		if err := store.Record(&AuditEntry{
			Action:    "act",
			UserID:    "u",
			TenantID:  "ws",
			Resource:  "",
			Timestamp: base.Add(time.Duration(i) * 100 * time.Nanosecond),
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	page1, err := store.QueryRange(AuditQuery{TenantID: "ws", StartTime: base, Limit: 2})
	if err != nil {
		t.Fatalf("QueryRange page1: %v", err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 unexpected: %+v", page1)
	}
	page2, err := store.QueryRange(AuditQuery{TenantID: "ws", StartTime: base, Limit: 10, AfterCursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("QueryRange page2: %v", err)
	}
	if len(page2.Entries) != 3 {
		t.Fatalf("same-ms cursor must resume exactly after the last id, got %d", len(page2.Entries))
	}
	seen := map[string]bool{}
	for _, e := range append(page1.Entries, page2.Entries...) {
		if seen[e.ID] {
			t.Fatalf("duplicate id across pages: %s", e.ID)
		}
		seen[e.ID] = true
	}
}
