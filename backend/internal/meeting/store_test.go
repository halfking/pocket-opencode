// internal/meeting/store_test.go
package meeting

import (
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
}