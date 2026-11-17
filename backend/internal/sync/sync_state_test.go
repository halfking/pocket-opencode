package sync

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB creates an in-memory SQLite database for testing.
func newTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return db
}

func TestSyncStateStore_Upsert(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSyncStateStore(db)
	if err != nil {
		t.Fatalf("NewSyncStateStore failed: %v", err)
	}

	state := &SyncState{
		NoteID:      "note-1",
		MemoraID:    "mem-1",
		ContentHash: "abc123",
		SyncStatus:  "synced",
		Version:     1,
	}

	if err := store.Upsert(state); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetByNoteID("note-1")
	if err != nil {
		t.Fatalf("GetByNoteID failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected non-nil SyncState")
	}
	if retrieved.NoteID != "note-1" || retrieved.SyncStatus != "synced" {
		t.Errorf("unexpected sync state: %+v", retrieved)
	}
}

func TestSyncStateStore_GetPending(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSyncStateStore(db)
	if err != nil {
		t.Fatalf("NewSyncStateStore failed: %v", err)
	}

	// Insert multiple sync states
	for i := 1; i <= 3; i++ {
		status := "synced"
		if i%2 == 0 {
			status = "pending"
		}
		state := &SyncState{
			NoteID:      "note-" + string(rune(48+i)),
			ContentHash: "hash-" + string(rune(48+i)),
			SyncStatus:  status,
			Version:     1,
		}
		if err := store.Upsert(state); err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}
	}

	// Get pending
	pending, err := store.GetPending()
	if err != nil {
		t.Fatalf("GetPending failed: %v", err)
	}

	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}
}

func TestSyncStateStore_MarkSynced(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSyncStateStore(db)
	if err != nil {
		t.Fatalf("NewSyncStateStore failed: %v", err)
	}

	state := &SyncState{
		NoteID:     "note-1",
		SyncStatus: "pending",
		Version:    1,
	}
	if err := store.Upsert(state); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	if err := store.MarkSynced("note-1", "mem-1"); err != nil {
		t.Fatalf("MarkSynced failed: %v", err)
	}

	retrieved, err := store.GetByNoteID("note-1")
	if err != nil {
		t.Fatalf("GetByNoteID failed: %v", err)
	}

	if retrieved.SyncStatus != "synced" || retrieved.MemoraID != "mem-1" {
		t.Errorf("unexpected state after MarkSynced: %+v", retrieved)
	}
}

func TestSyncStateStore_MarkError(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSyncStateStore(db)
	if err != nil {
		t.Fatalf("NewSyncStateStore failed: %v", err)
	}

	state := &SyncState{
		NoteID:     "note-1",
		SyncStatus: "pending",
		Version:    1,
	}
	if err := store.Upsert(state); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	errMsg := "network timeout"
	if err := store.MarkError("note-1", errMsg); err != nil {
		t.Fatalf("MarkError failed: %v", err)
	}

	retrieved, err := store.GetByNoteID("note-1")
	if err != nil {
		t.Fatalf("GetByNoteID failed: %v", err)
	}

	if retrieved.SyncStatus != "error" || retrieved.ErrorMessage != errMsg {
		t.Errorf("unexpected state after MarkError: %+v", retrieved)
	}
}
