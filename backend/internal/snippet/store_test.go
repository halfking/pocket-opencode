// internal/snippet/store_test.go
package snippet

import (
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()

	// Create
	req := CreateSnippetRequest{
		Title:    "Hello World",
		Language: "go",
		Code:     `fmt.Println("hello")`,
		Tags:     []string{"example"},
	}

	snip, err := s.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if snip.Title != "Hello World" {
		t.Errorf("expected title Hello World, got %s", snip.Title)
	}
	if snip.ID == "" {
		t.Error("expected non-empty ID")
	}

	// Get
	got, err := s.Get(snip.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Code != `fmt.Println("hello")` {
		t.Errorf("expected code match, got %s", got.Code)
	}

	// List
	snippets, err := s.List(ListSnippetsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snippets) != 1 {
		t.Errorf("expected 1 snippet, got %d", len(snippets))
	}

	// Delete
	err = s.Delete(snip.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = s.Get(snip.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestStore_Search(t *testing.T) {
	s := NewStore()
	s.Create(CreateSnippetRequest{Title: "Sort Array", Language: "go", Code: "sort.Ints()"})
	s.Create(CreateSnippetRequest{Title: "Fetch API", Language: "ts", Code: "fetch(url)"})
	s.Create(CreateSnippetRequest{Title: "Map Filter", Language: "ts", Code: "arr.map().filter()"})

	// Search by language
	results, _ := s.List(ListSnippetsRequest{Language: "ts", Limit: 10})
	if len(results) != 2 {
		t.Errorf("expected 2 ts snippets, got %d", len(results))
	}

	// Search by keyword
	results, _ = s.List(ListSnippetsRequest{Search: "sort", Limit: 10})
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'sort', got %d", len(results))
	}
}