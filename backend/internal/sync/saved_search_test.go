package sync

import (
	"testing"
)

func TestSavedSearchStore_Create(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSavedSearchStore(db)
	if err != nil {
		t.Fatalf("NewSavedSearchStore failed: %v", err)
	}

	search := &SavedSearch{
		ID:     "search-1",
		UserID: "user-1",
		Name:   "Q3 Budget",
		Query:  "budget q3",
		Filters: map[string]interface{}{
			"tags": []string{"budget", "finance"},
			"domain": "work",
		},
	}

	if err := store.Create(search); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetByID("search-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected non-nil SavedSearch")
	}
	if retrieved.Name != "Q3 Budget" || retrieved.Query != "budget q3" {
		t.Errorf("unexpected search: %+v", retrieved)
	}
}

func TestSavedSearchStore_ListByUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSavedSearchStore(db)
	if err != nil {
		t.Fatalf("NewSavedSearchStore failed: %v", err)
	}

	// Create multiple searches
	for i := 1; i <= 3; i++ {
		search := &SavedSearch{
			ID:     "search-" + string(rune(48+i)),
			UserID: "user-1",
			Name:   "Search-" + string(rune(48+i)),
			Query:  "query-" + string(rune(48+i)),
		}
		if err := store.Create(search); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// List by user
	searches, err := store.ListByUser("user-1")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}

	if len(searches) != 3 {
		t.Errorf("expected 3 searches, got %d", len(searches))
	}
}

func TestSavedSearchStore_Update(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSavedSearchStore(db)
	if err != nil {
		t.Fatalf("NewSavedSearchStore failed: %v", err)
	}

	search := &SavedSearch{
		ID:     "search-1",
		UserID: "user-1",
		Name:   "Original Name",
		Query:  "original query",
	}
	if err := store.Create(search); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update
	search.Name = "Updated Name"
	search.Query = "updated query"
	if err := store.Update(search); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetByID("search-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if retrieved.Name != "Updated Name" || retrieved.Query != "updated query" {
		t.Errorf("unexpected search after update: %+v", retrieved)
	}
}

func TestSavedSearchStore_Delete(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	store, err := NewSavedSearchStore(db)
	if err != nil {
		t.Fatalf("NewSavedSearchStore failed: %v", err)
	}

	search := &SavedSearch{
		ID:     "search-1",
		UserID: "user-1",
		Name:   "Test Search",
		Query:  "test query",
	}
	if err := store.Create(search); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Delete
	if err := store.Delete("search-1", "user-1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Try to retrieve (should fail)
	_, err = store.GetByID("search-1")
	if err == nil {
		t.Fatal("expected error when retrieving deleted search")
	}
}
