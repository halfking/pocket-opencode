package rss

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPortableItemStateIsIndependentIdempotentAndScoped(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(0)
	src, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/feed", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err = s.UpsertItemScoped(ctx, Item{ID: "article", SourceID: src.ID, Hash: "article", Title: "Author title", Content: "PRIVATE BODY", UpdatedAt: &at}, sc); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListPortableChanges(ctx, sc, 0, 100)
	action := PortableItemState{ExtID: "pocketd-item:article", Status: ItemStarred, ClientUpdatedAt: "2026-01-02T00:00:00.123456789Z", OpID: "original-status-op", Epoch: 0}
	ack, err := s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action})
	if err != nil || len(ack) != 1 || ack[0].Outcome != "applied" {
		t.Fatalf("apply %+v %v", ack, err)
	}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, before.NextCursor, 100)
	if err != nil || len(page.Changes) != 1 {
		t.Fatalf("journal %+v %v", page, err)
	}
	change := page.Changes[0]
	if change.Kind != "item-state" || change.OpID != action.OpID || change.ClientUpdatedAt.Format(time.RFC3339Nano) != action.ClientUpdatedAt || change.Payload["status"] != "starred" {
		t.Fatalf("original lost: %+v", change)
	}
	wire, _ := json.Marshal(change)
	var decoded map[string]any
	if err = json.Unmarshal(wire, &decoded); err != nil || decoded["client_updated_at"] != action.ClientUpdatedAt {
		t.Fatalf("timestamp spelling changed: %s %v", wire, err)
	}
	item, _ := s.GetItem(ctx, "article", sc)
	if item.Status != ItemStarred || item.Content != "PRIVATE BODY" || item.Title != "Author title" || !item.UpdatedAt.Equal(at) {
		t.Fatalf("feed content changed: %+v", item)
	}
	action.Status = ItemRead
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action}); !errors.Is(err, ErrPortableStateConflict) {
		t.Fatalf("reused operation changed body: %v", err)
	}
	action.OpID = "other-owner"
	other, _ := s.ApplyPortableItemStates(ctx, Scope{UserID: "other", WorkspaceID: sc.WorkspaceID}, []PortableItemState{action})
	if len(other) != 1 || other[0].Outcome != "unavailable" {
		t.Fatal(other)
	}
	if err = s.DeleteSource(ctx, src.ID, sc); err != nil {
		t.Fatal(err)
	}
	action.OpID = "after-deletion"
	action.ClientUpdatedAt = "2026-01-03T00:00:00Z"
	ack, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action})
	if err != nil || ack[0].Outcome != "unavailable" {
		t.Fatalf("deleted target revived: %+v %v", ack, err)
	}
}

func TestPortableStatusWireKeepsTitleAndTimestampSpelling(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(0)
	src, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/wire", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertItemScoped(ctx, Item{ID: "wire", SourceID: src.ID, Hash: "wire"}, sc); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListPortableChanges(ctx, sc, 0, 100)
	action := PortableItemState{ExtID: "pocketd-item:wire", Title: "Portal status label", Status: ItemRead, ClientUpdatedAt: "2026-01-01T08:00:00.000000+08:00", OpID: "wire-version", Epoch: 0}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, before.NextCursor, 100)
	if err != nil || len(page.Changes) != 1 {
		t.Fatalf("page %+v %v", page, err)
	}
	wire, _ := json.Marshal(page.Changes[0])
	var decoded map[string]any
	if err = json.Unmarshal(wire, &decoded); err != nil || decoded["client_updated_at"] != action.ClientUpdatedAt || decoded["title"] != action.Title {
		t.Fatalf("wire version changed: %s %v", wire, err)
	}
}

func TestPortableStatusReplayAcceptsEquivalentInstantsAndRetainsFirstReceipt(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(0)
	src, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/two-devices", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertItemScoped(ctx, Item{ID: "two-devices", SourceID: src.ID, Hash: "two-devices"}, sc); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListPortableChanges(ctx, sc, 0, 100)
	original := PortableItemState{ExtID: "pocketd-item:two-devices", Title: "Original status title", Status: ItemStarred, ClientUpdatedAt: "2026-01-01T00:00:00.123456789Z", OpID: "same-version-two-devices", Epoch: 0}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{original}); err != nil {
		t.Fatal(err)
	}
	secondDevice := original
	secondDevice.ClientUpdatedAt = "2026-01-01T08:00:00.123456789+08:00"
	acks, err := s.ApplyPortableItemStates(ctx, sc, []PortableItemState{secondDevice})
	if err != nil || len(acks) != 1 || acks[0].Outcome != "duplicate" {
		t.Fatalf("same instant rejected on second device: %+v %v", acks, err)
	}
	negativeZone := original
	negativeZone.ClientUpdatedAt = "2025-12-31T19:00:00.123456789-05:00"
	if acks, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{negativeZone}); err != nil || acks[0].Outcome != "duplicate" {
		t.Fatalf("negative zone equivalent rejected: %+v %v", acks, err)
	}
	mutations := []PortableItemState{}
	changed := secondDevice
	changed.ClientUpdatedAt = "2026-01-01T08:00:00.123456790+08:00"
	mutations = append(mutations, changed)
	changed = secondDevice
	changed.Status = ItemRead
	mutations = append(mutations, changed)
	changed = secondDevice
	changed.Title = "Changed"
	mutations = append(mutations, changed)
	changed = secondDevice
	changed.ExtID = "pocketd-item:different"
	mutations = append(mutations, changed)
	changed = secondDevice
	changed.Epoch = 1
	mutations = append(mutations, changed)
	changed = secondDevice
	changed.Deleted = true
	changed.Status = ""
	mutations = append(mutations, changed)
	for _, mutation := range mutations {
		if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{mutation}); !errors.Is(err, ErrPortableStateConflict) {
			t.Fatalf("changed version accepted: %+v %v", mutation, err)
		}
	}
	unsupported := secondDevice
	unsupported.ClientUpdatedAt = "2026-01-01T08:00:00.1234567891+08:00"
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{unsupported}); err == nil {
		t.Fatal("unsupported precision was truncated into the original version")
	}
	for index, offset := range []string{"+24:00", "+00:60"} {
		invalid := original
		invalid.OpID = original.OpID + offset
		invalid.ClientUpdatedAt = "2026-01-01T08:00:00.123456789" + offset
		if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{invalid}); err == nil {
			t.Fatalf("invalid offset accepted (%d): %s", index, offset)
		}
	}
	for _, table := range []string{"rss_portable_state_ops", "rss_portable_item_states"} {
		var encoded []byte
		var saved PortableItemState
		if err = s.pool.QueryRow(ctx, `SELECT operation FROM `+table+` WHERE user_id=$1 AND workspace_id=$2`, sc.UserID, sc.WorkspaceID).Scan(&encoded); err != nil || json.Unmarshal(encoded, &saved) != nil || saved != original {
			t.Fatalf("first original rewritten in %s: %+v %v", table, saved, err)
		}
	}
	page, err := s.ListPortableChanges(ctx, sc, before.NextCursor, 100)
	if err != nil || len(page.Changes) != 1 || page.Changes[0].OriginalTime == nil || *page.Changes[0].OriginalTime != original.ClientUpdatedAt {
		t.Fatalf("first journal changed: %+v %v", page, err)
	}
}

func TestPortableItemStateBatchRollbackEpochAndLegacyGuard(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc, epoch := context.Background(), testScope(), int64(0)
	src, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/batch", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if _, err = s.UpsertItemScoped(ctx, Item{ID: id, SourceID: src.ID, Hash: id}, sc); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/legacy"}, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertItemScoped(ctx, Item{ID: "legacy", SourceID: legacy.ID, Hash: "legacy"}, sc); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkItems(ctx, []string{"legacy", "one"}, ItemRead, sc); !errors.Is(err, ErrPortableStateRequired) {
		t.Fatalf("legacy bypass: %v", err)
	}
	old, _ := s.GetItem(ctx, "legacy", sc)
	if old.Status != ItemUnread {
		t.Fatal("partial legacy batch persisted")
	}
	a := PortableItemState{ExtID: "pocketd-item:one", Status: ItemStarred, ClientUpdatedAt: "2026-01-01T00:00:00.123456Z", OpID: "z", Epoch: 0}
	b := a
	b.ExtID = "pocketd-item:two"
	b.OpID = "other"
	if _, err = s.pool.Exec(ctx, `ALTER TABLE rss_portable_changes ADD CONSTRAINT test_state_journal_failure CHECK(kind<>'item-state' OR ext_id<>'pocketd-item:two')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{a, b}); err == nil {
		t.Fatal("journal failure did not abort batch")
	}
	one, _ := s.GetItem(ctx, "one", sc)
	if one.Status != ItemUnread {
		t.Fatal("source state committed without journal")
	}
	selected, err := s.ListItems(ctx, sc, ListItemsOptions{IDs: []string{"one", "two", "legacy"}, SourceID: src.ID, Limit: 1, Offset: 1})
	if err != nil || len(selected) != 1 || selected[0].SourceID != src.ID {
		t.Fatalf("item/source filter before paging: %+v %v", selected, err)
	}
	isolated, err := s.ListItems(ctx, Scope{UserID: "other", WorkspaceID: sc.WorkspaceID}, ListItemsOptions{IDs: []string{"one"}})
	if err != nil || len(isolated) != 0 {
		t.Fatalf("item ID filter owner leak: %+v %v", isolated, err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM rss_portable_state_ops`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt committed on rollback: %d %v", count, err)
	}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{a}); err != nil {
		t.Fatal(err)
	}
	lower := a
	lower.OpID = "a"
	lower.Status = ItemRead
	ack, err := s.ApplyPortableItemStates(ctx, sc, []PortableItemState{lower})
	if err != nil || ack[0].Outcome != "kept_existing" {
		t.Fatalf("tie order: %+v %v", ack, err)
	}
	newEpoch := lower
	newEpoch.Epoch = 1
	newEpoch.OpID = "new-epoch"
	newEpoch.ClientUpdatedAt = "2025-01-01T00:00:00Z"
	ack, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{newEpoch})
	if err != nil || ack[0].Outcome != "applied" {
		t.Fatalf("epoch order: %+v %v", ack, err)
	}
	late := a
	late.OpID = "old-epoch"
	late.ClientUpdatedAt = "2026-01-03T00:00:00Z"
	ack, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{late})
	if err != nil || ack[0].Outcome != "kept_existing" {
		t.Fatalf("stale epoch accepted: %+v %v", ack, err)
	}
	if err = s.migratePortable(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestPortableSourceDeleteUsesDeletionEpochForStatusTombstone(t *testing.T) {
	s, cleanup := newPGStore(t)
	defer cleanup()
	ctx, sc := context.Background(), testScope()
	epoch := int64(0)
	src, err := s.CreateSource(ctx, CreateSourceRequest{URL: "https://example.test/delete-status", PortalSync: true, PortalEpoch: &epoch}, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertItemScoped(ctx, Item{ID: "status-delete", SourceID: src.ID, Hash: "status-delete"}, sc); err != nil {
		t.Fatal(err)
	}
	action := PortableItemState{ExtID: "pocketd-item:status-delete", Status: ItemStarred, ClientUpdatedAt: "2026-01-01T00:00:00Z", OpID: "original-star", Epoch: 0}
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListPortableChanges(ctx, sc, 0, 100)
	fresh := int64(1)
	if err = s.DeleteSourcePortable(ctx, src.ID, sc, &fresh); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPortableChanges(ctx, sc, before.NextCursor, 100)
	if err != nil || len(page.Changes) != 3 {
		t.Fatalf("cascade: %+v %v", page, err)
	}
	for _, change := range page.Changes {
		if !change.Deleted || change.Epoch == nil || *change.Epoch != 1 {
			t.Fatalf("deletion intent epoch lost: %+v", change)
		}
	}
	// The status receipt remains immutable, while its old operation cannot
	// recreate a source or article after the new deletion intent.
	if _, err = s.ApplyPortableItemStates(ctx, sc, []PortableItemState{action}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetItem(ctx, "status-delete", sc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old replay revived article: %v", err)
	}
}
