package notes

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStore_Upsert_Insert tests inserting a new note
func TestStore_Upsert_Insert(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}
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
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}
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
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}
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
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}
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
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}

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
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("no test database available")
	}
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

func getTestPool(t *testing.T) *pgxpool.Pool {
	// Try to connect to test database
	// This uses the same connection logic as the main app but points to a test DB
	// For now, we'll return nil to skip tests that need a real DB
	// In production, you'd set up a test database connection here
	return nil
}

func cleanupTestData(t *testing.T, pool *pgxpool.Pool) {
	// Clean up test data
	_, err := pool.Exec(context.Background(), `DELETE FROM notes WHERE id LIKE 'test-note-%'`)
	if err != nil {
		t.Logf("cleanup warning: %v", err)
	}
}
