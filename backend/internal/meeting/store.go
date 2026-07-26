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
// Deprecated: Use CreateScoped for production code with proper ownership.
func (s *Store) Create(req CreateMeetingRequest) (*Meeting, error) {
	return s.CreateScoped(req, "", "")
}

// CreateScoped creates a new meeting record with ownership.
func (s *Store) CreateScoped(req CreateMeetingRequest, ownerID, workspaceID string) (*Meeting, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	m := &Meeting{
		ID:          fmt.Sprintf("mtg_%d", now.UnixNano()),
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
		Title:       req.Title,
		Status:      "recording",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.meetings[m.ID] = m
	return copyMeeting(m), nil
}

// Get retrieves a meeting by ID.
// Returns error if ID is empty or meeting not found.
// Deprecated: Use GetScoped for production code with ownership checks.
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
	return copyMeeting(m), nil
}

// GetScoped retrieves a meeting by ID with ownership verification.
func (s *Store) GetScoped(id, ownerID, workspaceID string) (*Meeting, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.meetings[id]
	if !ok || m.OwnerID != ownerID || m.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("meeting not found")
	}
	return copyMeeting(m), nil
}

// Update updates an existing meeting record.
// Returns error if meeting is nil, ID is empty, or meeting not found.
// Deprecated: Use UpdateScoped for production code with ownership checks.
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
	s.meetings[m.ID] = copyMeeting(m)
	return nil
}

// UpdateScoped updates an existing meeting record with ownership verification.
func (s *Store) UpdateScoped(m *Meeting, ownerID, workspaceID string) error {
	if m == nil {
		return fmt.Errorf("meeting cannot be nil")
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.meetings[m.ID]
	if !ok || existing.OwnerID != ownerID || existing.WorkspaceID != workspaceID {
		return fmt.Errorf("meeting not found")
	}
	m.UpdatedAt = time.Now()
	m.OwnerID = ownerID
	m.WorkspaceID = workspaceID
	s.meetings[m.ID] = copyMeeting(m)
	return nil
}

// List returns all meeting records.
// Deprecated: Use ListScoped for production code with ownership filtering.
func (s *Store) List() ([]*Meeting, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Meeting, 0, len(s.meetings))
	for _, m := range s.meetings {
		result = append(result, copyMeeting(m))
	}
	return result, nil
}

// ListScoped returns meeting records for the specified owner/workspace.
func (s *Store) ListScoped(ownerID, workspaceID string) ([]*Meeting, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Meeting, 0)
	for _, m := range s.meetings {
		if m.OwnerID == ownerID && m.WorkspaceID == workspaceID {
			result = append(result, copyMeeting(m))
		}
	}
	return result, nil
}

// Delete removes a meeting by ID.
// Returns error if ID is empty or meeting not found.
// Deprecated: Use DeleteScoped for production code with ownership checks.
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

// DeleteScoped removes a meeting by ID with ownership verification.
func (s *Store) DeleteScoped(id, ownerID, workspaceID string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.meetings[id]
	if !ok || m.OwnerID != ownerID || m.WorkspaceID != workspaceID {
		return fmt.Errorf("meeting not found")
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