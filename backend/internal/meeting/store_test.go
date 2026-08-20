// internal/meeting/store_test.go
package meeting

import (
	"strings"
	"sync"
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()

	// Create
	m, err := s.Create(CreateMeetingRequest{Title: "Sprint Planning"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if m.Title != "Sprint Planning" {
		t.Errorf("expected title Sprint Planning, got %s", m.Title)
	}
	if m.Status != "recording" {
		t.Errorf("expected status recording, got %s", m.Status)
	}

	// Get
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch")
	}

	// Update
	got.Transcript = "Hello world"
	got.Status = "done"
	err = s.Update(got)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, _ := s.Get(m.ID)
	if updated.Transcript != "Hello world" {
		t.Errorf("expected transcript updated")
	}

	// List
	meetings, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(meetings) != 1 {
		t.Errorf("expected 1 meeting, got %d", len(meetings))
	}

	// Delete
	err = s.Delete(m.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = s.Get(m.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestStore_Create_EmptyTitle(t *testing.T) {
	s := NewStore()
	
	_, err := s.Create(CreateMeetingRequest{Title: ""})
	if err == nil {
		t.Error("expected error for empty title")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
	
	_, err = s.Create(CreateMeetingRequest{Title: "   "})
	if err == nil {
		t.Error("expected error for whitespace-only title")
	}
}

func TestStore_Get_InvalidID(t *testing.T) {
	s := NewStore()
	
	_, err := s.Get("")
	if err == nil {
		t.Error("expected error for empty ID")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
	
	_, err = s.Get("   ")
	if err == nil {
		t.Error("expected error for whitespace-only ID")
	}
	
	_, err = s.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
	}
}

func TestStore_Update_NilMeeting(t *testing.T) {
	s := NewStore()
	
	err := s.Update(nil)
	if err == nil {
		t.Error("expected error for nil meeting")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("expected 'nil' in error message, got: %v", err)
	}
}

func TestStore_Update_EmptyID(t *testing.T) {
	s := NewStore()
	
	err := s.Update(&Meeting{ID: ""})
	if err == nil {
		t.Error("expected error for empty ID")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	s := NewStore()
	
	err := s.Update(&Meeting{ID: "nonexistent", Title: "Test"})
	if err == nil {
		t.Error("expected error for nonexistent meeting")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
	}
}

func TestStore_Delete_InvalidID(t *testing.T) {
	s := NewStore()
	
	err := s.Delete("")
	if err == nil {
		t.Error("expected error for empty ID")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
	
	err = s.Delete("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	
	// Create initial meeting
	m, err := s.Create(CreateMeetingRequest{Title: "Concurrent Test"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	
	var wg sync.WaitGroup
	errChan := make(chan error, 100)
	
	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Get(m.ID)
			if err != nil {
				errChan <- err
			}
		}()
	}
	
	// Concurrent updates
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Get a fresh copy
			meeting, err := s.Get(m.ID)
			if err != nil {
				errChan <- err
				return
			}
			meeting.Status = "done"
			err = s.Update(meeting)
			if err != nil {
				errChan <- err
			}
		}(i)
	}
	
	wg.Wait()
	close(errChan)
	
	for err := range errChan {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestStore_DataIsolation(t *testing.T) {
	s := NewStore()
	
	// Create meeting
	m, err := s.Create(CreateMeetingRequest{Title: "Isolation Test"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	
	// Get meeting and modify it
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	got.Transcript = "Modified externally"
	got.KeyDecisions = []string{"External decision"}
	
	// Get again - should not reflect external modifications
	got2, err := s.Get(m.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got2.Transcript != "" {
		t.Errorf("external modification leaked into store: transcript = %s", got2.Transcript)
	}
	if len(got2.KeyDecisions) > 0 {
		t.Errorf("external modification leaked into store: decisions = %v", got2.KeyDecisions)
	}
}