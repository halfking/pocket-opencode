// internal/snippet/store_test.go
package snippet

import (
	"sync"
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

func TestStore_CreateValidation(t *testing.T) {
	s := NewStore()

	tests := []struct {
		name    string
		req     CreateSnippetRequest
		wantErr bool
	}{
		{
			name: "valid snippet",
			req: CreateSnippetRequest{
				Title:    "Test",
				Language: "go",
				Code:     "package main",
			},
			wantErr: false,
		},
		{
			name: "empty title",
			req: CreateSnippetRequest{
				Title:    "",
				Language: "go",
				Code:     "package main",
			},
			wantErr: true,
		},
		{
			name: "whitespace title",
			req: CreateSnippetRequest{
				Title:    "   ",
				Language: "go",
				Code:     "package main",
			},
			wantErr: true,
		},
		{
			name: "empty language",
			req: CreateSnippetRequest{
				Title:    "Test",
				Language: "",
				Code:     "package main",
			},
			wantErr: true,
		},
		{
			name: "empty code",
			req: CreateSnippetRequest{
				Title:    "Test",
				Language: "go",
				Code:     "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Create(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Create() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStore_GetValidation(t *testing.T) {
	s := NewStore()

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "empty id",
			id:      "",
			wantErr: true,
		},
		{
			name:    "whitespace id",
			id:      "   ",
			wantErr: true,
		},
		{
			name:    "non-existent id",
			id:      "snip_999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Get(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStore_DeleteValidation(t *testing.T) {
	s := NewStore()

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "empty id",
			id:      "",
			wantErr: true,
		},
		{
			name:    "whitespace id",
			id:      "   ",
			wantErr: true,
		},
		{
			name:    "non-existent id",
			id:      "snip_999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Delete(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("Delete() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStore_ListTagFilter(t *testing.T) {
	s := NewStore()
	
	s.Create(CreateSnippetRequest{
		Title:    "Test 1",
		Language: "go",
		Code:     "code1",
		Tags:     []string{"web", "api"},
	})
	s.Create(CreateSnippetRequest{
		Title:    "Test 2",
		Language: "go",
		Code:     "code2",
		Tags:     []string{"cli"},
	})
	s.Create(CreateSnippetRequest{
		Title:    "Test 3",
		Language: "go",
		Code:     "code3",
		Tags:     []string{"web"},
	})

	results, err := s.List(ListSnippetsRequest{Tag: "web", Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 snippets with 'web' tag, got %d", len(results))
	}

	results, err = s.List(ListSnippetsRequest{Tag: "cli", Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 snippet with 'cli' tag, got %d", len(results))
	}

	results, err = s.List(ListSnippetsRequest{Tag: "nonexistent", Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 snippets with 'nonexistent' tag, got %d", len(results))
	}
}

func TestStore_ListPagination(t *testing.T) {
	s := NewStore()

	// Create 5 snippets
	for i := 0; i < 5; i++ {
		s.Create(CreateSnippetRequest{
			Title:    "Test",
			Language: "go",
			Code:     "code",
		})
	}

	// Test limit
	results, _ := s.List(ListSnippetsRequest{Limit: 2})
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit=2, got %d", len(results))
	}

	// Test offset
	results, _ = s.List(ListSnippetsRequest{Offset: 3, Limit: 10})
	if len(results) != 2 {
		t.Errorf("expected 2 results with offset=3, got %d", len(results))
	}

	// Test offset >= length
	results, _ = s.List(ListSnippetsRequest{Offset: 10, Limit: 10})
	if len(results) != 0 {
		t.Errorf("expected 0 results with offset >= length, got %d", len(results))
	}

	// Test default limit
	results, _ = s.List(ListSnippetsRequest{})
	if len(results) != 5 {
		t.Errorf("expected 5 results with default limit, got %d", len(results))
	}
}

func TestStore_CopyMutation(t *testing.T) {
	s := NewStore()

	req := CreateSnippetRequest{
		Title:    "Original",
		Language: "go",
		Code:     "original code",
		Tags:     []string{"tag1"},
	}

	snip, _ := s.Create(req)
	originalID := snip.ID

	// Mutate the returned snippet
	snip.Title = "Modified"
	snip.Code = "modified code"
	snip.Tags = append(snip.Tags, "tag2")

	// Get the snippet again and verify it wasn't mutated
	retrieved, _ := s.Get(originalID)
	if retrieved.Title != "Original" {
		t.Errorf("expected title 'Original', got '%s' - external mutation affected store", retrieved.Title)
	}
	if retrieved.Code != "original code" {
		t.Errorf("expected code 'original code', got '%s' - external mutation affected store", retrieved.Code)
	}
	if len(retrieved.Tags) != 1 || retrieved.Tags[0] != "tag1" {
		t.Errorf("expected tags ['tag1'], got %v - external mutation affected store", retrieved.Tags)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	
	// Create initial snippet
	snip, _ := s.Create(CreateSnippetRequest{
		Title:    "Concurrent Test",
		Language: "go",
		Code:     "test",
	})

	// Concurrent reads and writes
	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrent creates
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(n int) {
			defer wg.Done()
			s.Create(CreateSnippetRequest{
				Title:    "Concurrent",
				Language: "go",
				Code:     "code",
			})
		}(i)
	}

	// Concurrent reads
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			s.Get(snip.ID)
		}()
	}

	// Concurrent lists
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			s.List(ListSnippetsRequest{Limit: 10})
		}()
	}

	wg.Wait()

	// Verify data integrity
	results, _ := s.List(ListSnippetsRequest{Limit: 100})
	if len(results) != numGoroutines+1 {
		t.Errorf("expected %d snippets after concurrent operations, got %d", numGoroutines+1, len(results))
	}
}