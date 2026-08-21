package redclaw

import (
	"sort"
	"testing"
	"time"
)

func recordPGAuditWithID(t *testing.T, store *PGAuditStore, id, tenant, action string, ts time.Time) {
	t.Helper()
	if err := store.Record(&AuditEntry{
		ID:        id,
		Action:    action,
		UserID:    "user-1",
		TenantID:  tenant,
		Timestamp: ts,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPGAuditStore_QueryRangeExactLimitHasNoCursor(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	ts := time.UnixMilli(8_000_000)
	for _, id := range []string{"aud_exact_00", "aud_exact_01"} {
		recordPGAuditWithID(t, store, id, "ws-a", "wanted", ts)
	}
	recordPGAuditWithID(t, store, "aud_other_tenant", "ws-b", "wanted", ts)
	recordPGAuditWithID(t, store, "aud_other_action", "ws-a", "ignored", ts)

	page, err := store.QueryRange(AuditQuery{
		TenantID:  "ws-a",
		Action:    "wanted",
		StartTime: ts,
		Limit:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextCursor != "" {
		t.Fatalf("exact terminal page=%+v, want two entries without a cursor", page)
	}
}

func TestPGAuditStore_QueryRangeLookaheadUsesReturnedEntryCursor(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	ts := time.UnixMilli(8_100_000)
	want := []string{"aud_more_00", "aud_more_01", "aud_more_02"}
	for _, id := range want {
		recordPGAuditWithID(t, store, id, "ws-a", "wanted", ts)
	}

	first, err := store.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: ts, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.NextCursor == "" {
		t.Fatalf("first page=%+v, want two entries and a cursor", first)
	}
	second, err := store.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: ts, Limit: 2, AfterCursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.NextCursor != "" {
		t.Fatalf("second page=%+v, want final one-entry page without a cursor", second)
	}

	got := append(append([]*AuditEntry{}, first.Entries...), second.Entries...)
	gotIDs := make([]string, 0, len(got))
	seen := make(map[string]struct{}, len(got))
	for _, entry := range got {
		if _, ok := seen[entry.ID]; ok {
			t.Fatalf("duplicate entry %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		gotIDs = append(gotIDs, entry.ID)
	}
	sort.Strings(gotIDs)
	if len(gotIDs) != len(want) {
		t.Fatalf("entries=%v, want %v", gotIDs, want)
	}
	for i, id := range want {
		if gotIDs[i] != id {
			t.Fatalf("entries=%v, want %v", gotIDs, want)
		}
	}
}

func TestPGAuditStore_QueryRangeExactRemainderHasNoCursor(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	base := time.UnixMilli(8_200_000)
	for i, id := range []string{"aud_remainder_00", "aud_remainder_01", "aud_remainder_02"} {
		recordPGAuditWithID(t, store, id, "ws-a", "wanted", base.Add(time.Duration(i)*time.Second))
	}

	first, err := store.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: base, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page missing cursor")
	}
	second, err := store.QueryRange(AuditQuery{TenantID: "ws-a", StartTime: base, Limit: 2, AfterCursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 2 || second.NextCursor != "" {
		t.Fatalf("exact remainder page=%+v, want two entries without a cursor", second)
	}
}
