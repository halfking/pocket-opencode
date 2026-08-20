package redclaw

// audit_pg_cursor_test.go — PG-backed regression for cursor precision.
//
// Mirrors audit_cursor_precision_test.go against the real database so the
// TIMESTAMPTZ (µs) round-trip path is covered too. Skips when
// POCKET_TEST_POSTGRES_DSN is unset.

import (
	"testing"
	"time"
)

func TestPGAuditStore_SubMilliCursorNoDoubleCount(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	base := time.Unix(1_700_000_000, 0)

	stamps := []time.Time{
		base.Add(100 * time.Millisecond),
		base.Add(123*time.Millisecond + 400*time.Microsecond),
		base.Add(123*time.Millisecond + 450*time.Microsecond), // same ms as prev
		base.Add(200 * time.Millisecond),
	}
	for _, tm := range stamps {
		mustRecordPG(t, s, "ws", "act", tm)
	}

	var collected []*AuditEntry
	cursor := ""
	for page := 0; page < 10; page++ {
		q := AuditQuery{TenantID: "ws", StartTime: base, Limit: 2}
		if cursor != "" {
			q.AfterCursor = cursor
		}
		p, err := s.QueryRange(q)
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

func TestPGAuditStore_SubMilliSameTimestampCursorDisambiguates(t *testing.T) {
	s, cleanup := newTestPGAuditStore(t)
	defer cleanup()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		mustRecordPG(t, s, "ws", "act", base.Add(time.Duration(i)*100*time.Nanosecond))
	}

	page1, err := s.QueryRange(AuditQuery{TenantID: "ws", StartTime: base, Limit: 2})
	if err != nil {
		t.Fatalf("QueryRange page1: %v", err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 unexpected: %+v", page1)
	}
	page2, err := s.QueryRange(AuditQuery{TenantID: "ws", StartTime: base, Limit: 10, AfterCursor: page1.NextCursor})
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
