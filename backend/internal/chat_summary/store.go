// internal/chat_summary/store.go
package chat_summary

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var idCounter uint64

// Store 聊天摘要存储（内存实现）
type Store struct {
	mu        sync.RWMutex
	summaries map[string]*ChatSummary
}

// NewStore creates a new in-memory chat summary store
func NewStore() *Store {
	return &Store{
		summaries: make(map[string]*ChatSummary),
	}
}

// Create creates a new chat summary in the store
// Deprecated: Use CreateScoped for production code with proper ownership.
func (s *Store) Create(summary *ChatSummary) error {
	if summary == nil {
		return fmt.Errorf("cannot create nil summary")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summary.ID = fmt.Sprintf("cs_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
	summary.CreatedAt = time.Now()
	s.summaries[summary.ID] = summary
	return nil
}

// CreateScoped creates a new chat summary with ownership
func (s *Store) CreateScoped(summary *ChatSummary, ownerID, workspaceID string) error {
	if summary == nil {
		return fmt.Errorf("cannot create nil summary")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summary.ID = fmt.Sprintf("cs_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
	summary.OwnerID = ownerID
	summary.WorkspaceID = workspaceID
	summary.CreatedAt = time.Now()
	s.summaries[summary.ID] = summary
	return nil
}

// Get retrieves a chat summary by ID
// Deprecated: Use GetScoped for production code with ownership checks.
func (s *Store) Get(id string) (*ChatSummary, error) {
	if id == "" {
		return nil, fmt.Errorf("cannot get summary with empty id")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	cs, ok := s.summaries[id]
	if !ok {
		return nil, fmt.Errorf("chat summary not found: %s", id)
	}
	return cs, nil
}

// GetScoped retrieves a chat summary by ID with ownership verification
func (s *Store) GetScoped(id, ownerID, workspaceID string) (*ChatSummary, error) {
	if id == "" {
		return nil, fmt.Errorf("cannot get summary with empty id")
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	cs, ok := s.summaries[id]
	if !ok || cs.OwnerID != ownerID || cs.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("chat summary not found")
	}
	return cs, nil
}

// List retrieves chat summaries, optionally filtered by channelID
// Deprecated: Use ListScoped for production code with ownership filtering.
func (s *Store) List(channelID string, limit int) ([]*ChatSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	var result []*ChatSummary
	for _, cs := range s.summaries {
		if channelID != "" && cs.ChannelID != channelID {
			continue
		}
		result = append(result, cs)
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// ListScoped retrieves chat summaries with ownership filtering
func (s *Store) ListScoped(channelID, ownerID, workspaceID string, limit int) ([]*ChatSummary, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	var result []*ChatSummary
	for _, cs := range s.summaries {
		if cs.OwnerID != ownerID || cs.WorkspaceID != workspaceID {
			continue
		}
		if channelID != "" && cs.ChannelID != channelID {
			continue
		}
		result = append(result, cs)
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Delete removes a chat summary by ID
// Deprecated: Use DeleteScoped for production code with ownership checks.
func (s *Store) Delete(id string) error {
	if id == "" {
		return fmt.Errorf("cannot delete summary with empty id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.summaries[id]; !ok {
		return fmt.Errorf("chat summary not found: %s", id)
	}
	delete(s.summaries, id)
	return nil
}

// DeleteScoped removes a chat summary by ID with ownership verification
func (s *Store) DeleteScoped(id, ownerID, workspaceID string) error {
	if id == "" {
		return fmt.Errorf("cannot delete summary with empty id")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cs, ok := s.summaries[id]
	if !ok || cs.OwnerID != ownerID || cs.WorkspaceID != workspaceID {
		return fmt.Errorf("chat summary not found")
	}
	delete(s.summaries, id)
	return nil
}