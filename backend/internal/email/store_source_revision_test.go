package email

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestListEmailsScopedVersionsPortableMetadataOnWrite(t *testing.T) {
	store, _ := newWorkspaceTestStore(t)
	ctx := context.Background()
	seedAccount(t, store, "mail-a", "alice", "ws-a")
	seedReasonEmail(t, store, "historical", "mail-a", "ws-a", "high", "")

	// Recreate the legacy schema and its historical cache timestamp.
	if _, err := store.pool.Exec(ctx, `
		DROP TRIGGER emails_stamp_source_revision ON emails;
		ALTER TABLE emails DROP COLUMN source_updated_at;
		UPDATE emails SET created_at=1750000010, updated_at=NULL WHERE id='historical';
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}

	read := func(filter ListFilter) Email {
		t.Helper()
		items, err := store.ListEmailsScoped(ctx, filter, "alice", "ws-a")
		if err != nil || len(items) != 1 {
			t.Fatalf("list returned %d rows, err=%v", len(items), err)
		}
		return items[0]
	}
	original := read(ListFilter{})
	if original.Date != 1750000000 || original.Snippet == "" || original.SourceUpdatedAt != 0 || original.UpdatedAt != 0 {
		t.Fatalf("historical mail changed or gained a synthetic revision: %+v", original)
	}
	wire, err := json.Marshal(original)
	if err != nil || bytes.Contains(wire, []byte(`"sourceUpdatedAt"`)) || bytes.Contains(wire, []byte(`"updatedAt"`)) {
		t.Fatalf("historical list response exposed a version: %s, err=%v", wire, err)
	}

	cutoff := time.Now().Add(-time.Minute).UnixMilli()
	if err := store.SetSummaryScoped(ctx, original.ID, "alice", "ws-a", "First summary"); err != nil {
		t.Fatal(err)
	}
	filter := ListFilter{Since: cutoff}
	first := read(filter)
	if first.AISummary != "First summary" || first.SourceUpdatedAt <= cutoff || first.UpdatedAt != first.SourceUpdatedAt {
		t.Fatalf("missing durable summary revision: %+v", first)
	}
	count, err := store.CountEmailsScoped(ctx, filter, "alice", "ws-a")
	if err != nil || count != 1 {
		t.Fatalf("count after metadata edit = %d, err=%v", count, err)
	}
	for _, scope := range [][2]string{{"bob", "ws-a"}, {"alice", "ws-b"}} {
		items, err := store.ListEmailsScoped(ctx, filter, scope[0], scope[1])
		if err != nil || len(items) != 0 {
			t.Fatalf("other scope %v returned %d rows, err=%v", scope, len(items), err)
		}
	}

	if err := store.MarkRead(ctx, original.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSummaryScoped(ctx, original.ID, "alice", "ws-a", first.AISummary); err != nil {
		t.Fatal(err)
	}
	if got := read(ListFilter{}); !got.IsRead || got.SourceUpdatedAt != first.SourceUpdatedAt {
		t.Fatalf("read status or repeated write changed the source revision: %+v", got)
	}
	if err := store.SetSummaryScoped(ctx, original.ID, "alice", "ws-a", "Second summary"); err != nil {
		t.Fatal(err)
	}
	if got := read(ListFilter{Since: first.SourceUpdatedAt}); got.SourceUpdatedAt <= first.SourceUpdatedAt || got.AISummary != "Second summary" {
		t.Fatalf("updated summary did not advance revision: %+v", got)
	}
}
