// internal/meeting/store.go
package meeting

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Store 会议记录存储（内存实现）
type Store struct {
	mu       sync.RWMutex
	meetings map[string]*Meeting
}

// NewStore creates a new in-memory meeting store
func NewStore() *Store {
	return &Store{
		meetings: make(map[string]*Meeting),
	}
}

// Create creates a new meeting record with the given request.
// Returns error if title is empty.
func (s *Store) Create(req CreateMeetingRequest) (*Meeting, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	m := &Meeting{
		ID:        fmt.Sprintf("mtg_%d", now.UnixNano()),
		Title:     req.Title,
		Status:    "recording",
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.meetings[m.ID] = m
	// Return a copy to prevent external modification without lock
	return copyMeeting(m), nil
}

// Get retrieves a meeting by ID.
// Returns error if ID is empty or meeting not found.
func (s *Store) Get(id string) (*Meeting, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("meeting ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.meetings[id]
	if !ok {
		return nil, fmt.Errorf("meeting not found: %s", id)
	}
	// Return a copy to prevent external modification without lock
	return copyMeeting(m), nil
}

// Update updates an existing meeting record.
// Returns error if meeting is nil, ID is empty, or meeting not found.
func (s *Store) Update(m *Meeting) error {
	if m == nil {
		return fmt.Errorf("meeting cannot be nil")
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.meetings[m.ID]; !ok {
		return fmt.Errorf("meeting not found: %s", m.ID)
	}
	m.UpdatedAt = time.Now()
	// Store a copy to prevent external modification without lock
	s.meetings[m.ID] = copyMeeting(m)
	return nil
}

// List returns all meeting records.
// The returned slice is a snapshot and safe for concurrent use.
func (s *Store) List() ([]*Meeting, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Meeting, 0, len(s.meetings))
	for _, m := range s.meetings {
		// Return copies to prevent external modification without lock
		result = append(result, copyMeeting(m))
	}
	return result, nil
}

// Delete removes a meeting by ID.
// Returns error if ID is empty or meeting not found.
func (s *Store) Delete(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.meetings[id]; !ok {
		return fmt.Errorf("meeting not found: %s", id)
	}
	delete(s.meetings, id)
	return nil
}

// copyMeeting creates a deep copy of a meeting to prevent data races
func copyMeeting(m *Meeting) *Meeting {
	if m == nil {
		return nil
	}
	result := *m
	// Deep copy slices
	if m.KeyDecisions != nil {
		result.KeyDecisions = make([]string, len(m.KeyDecisions))
		copy(result.KeyDecisions, m.KeyDecisions)
	}
	if m.ActionItems != nil {
		result.ActionItems = make([]ActionItem, len(m.ActionItems))
		copy(result.ActionItems, m.ActionItems)
	}
	if m.Tags != nil {
		result.Tags = make([]string, len(m.Tags))
		copy(result.Tags, m.Tags)
	}
	return &result
}