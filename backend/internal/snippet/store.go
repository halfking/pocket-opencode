// internal/snippet/store.go
package snippet

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// legacyOwnerID / legacyWorkspaceID are the identity defaults used by the
// deprecated non-scoped API. They match the handler fallback used when a
// request carries no authenticated claims, so single-tenant/local callers keep
// working while the scoped API stays strict about ownership.
const (
	legacyOwnerID     = "local"
	legacyWorkspaceID = "default"
)

// Store 代码片段存储（内存实现，后续可迁移到数据库）
// Store is an in-memory code snippet storage with thread-safe operations.
type Store struct {
	mu       sync.RWMutex
	snippets map[string]*Snippet
}

// NewStore creates a new snippet store instance.
func NewStore() *Store {
	return &Store{
		snippets: make(map[string]*Snippet),
	}
}

// Create creates a new snippet and returns a copy.
// Returns error if required fields (Title, Language, Code) are empty.
// Deprecated: Use CreateScoped for production code with proper ownership.
func (s *Store) Create(req CreateSnippetRequest) (*Snippet, error) {
	return s.CreateScoped(req, legacyOwnerID, legacyWorkspaceID)
}

// CreateScoped creates a new snippet with ownership and returns a copy.
// ownerID and workspaceID are required for tenant isolation.
func (s *Store) CreateScoped(req CreateSnippetRequest, ownerID, workspaceID string) (*Snippet, error) {
	// Validate required fields
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("snippet title cannot be empty")
	}
	if strings.TrimSpace(req.Language) == "" {
		return nil, fmt.Errorf("snippet language cannot be empty")
	}
	if strings.TrimSpace(req.Code) == "" {
		return nil, fmt.Errorf("snippet code cannot be empty")
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	snip := &Snippet{
		ID:          generateID(),
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
		Title:       req.Title,
		Language:    req.Language,
		Code:        req.Code,
		Description: req.Description,
		Tags:        req.Tags,
		ProjectID:   req.ProjectID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.snippets[snip.ID] = snip
	
	// Return a copy to prevent external mutation
	return copySnippet(snip), nil
}

// Get retrieves a snippet by ID and returns a copy.
// Returns error if snippet not found or ID is empty.
// Deprecated: Use GetScoped for production code with ownership checks.
func (s *Store) Get(id string) (*Snippet, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("snippet ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snip, ok := s.snippets[id]
	if !ok {
		return nil, fmt.Errorf("snippet not found: %s", id)
	}
	
	// Return a copy to prevent external mutation
	return copySnippet(snip), nil
}

// GetScoped retrieves a snippet by ID with ownership verification.
// Returns error if not found or ownership doesn't match.
func (s *Store) GetScoped(id, ownerID, workspaceID string) (*Snippet, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("snippet ID cannot be empty")
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snip, ok := s.snippets[id]
	if !ok || snip.OwnerID != ownerID || snip.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("snippet not found")
	}

	return copySnippet(snip), nil
}

// List retrieves snippets matching the filter criteria and returns copies.
// Supports filtering by Language, ProjectID, Tag, and Search (title).
// Applies pagination via Offset and Limit (default 50).
// Deprecated: Use ListScoped for production code with ownership filtering.
func (s *Store) List(req ListSnippetsRequest) ([]*Snippet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	var result []*Snippet
	for _, snip := range s.snippets {
		if req.Language != "" && snip.Language != req.Language {
			continue
		}
		if req.ProjectID != "" && snip.ProjectID != req.ProjectID {
			continue
		}
		if req.Search != "" && !strings.Contains(strings.ToLower(snip.Title), strings.ToLower(req.Search)) {
			continue
		}
		if req.Tag != "" {
			hasTag := false
			for _, t := range snip.Tags {
				if t == req.Tag {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		result = append(result, snip)
	}

	// 应用 offset
	if req.Offset > 0 {
		if req.Offset >= len(result) {
			result = []*Snippet{}
		} else {
			result = result[req.Offset:]
		}
	}

	// 应用 limit
	if len(result) > limit {
		result = result[:limit]
	}

	// Return copies to prevent external mutation
	copies := make([]*Snippet, len(result))
	for i, snip := range result {
		copies[i] = copySnippet(snip)
	}

	return copies, nil
}

// ListScoped retrieves snippets with ownership filtering.
func (s *Store) ListScoped(req ListSnippetsRequest, ownerID, workspaceID string) ([]*Snippet, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	var result []*Snippet
	for _, snip := range s.snippets {
		// Ownership filter
		if snip.OwnerID != ownerID || snip.WorkspaceID != workspaceID {
			continue
		}
		// Other filters
		if req.Language != "" && snip.Language != req.Language {
			continue
		}
		if req.ProjectID != "" && snip.ProjectID != req.ProjectID {
			continue
		}
		if req.Search != "" && !strings.Contains(strings.ToLower(snip.Title), strings.ToLower(req.Search)) {
			continue
		}
		if req.Tag != "" {
			hasTag := false
			for _, t := range snip.Tags {
				if t == req.Tag {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		result = append(result, snip)
	}

	if req.Offset > 0 {
		if req.Offset >= len(result) {
			result = []*Snippet{}
		} else {
			result = result[req.Offset:]
		}
	}

	if len(result) > limit {
		result = result[:limit]
	}

	copies := make([]*Snippet, len(result))
	for i, snip := range result {
		copies[i] = copySnippet(snip)
	}

	return copies, nil
}

// Delete removes a snippet by ID.
// Returns error if snippet not found or ID is empty.
// Deprecated: Use DeleteScoped for production code with ownership checks.
func (s *Store) Delete(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("snippet ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.snippets[id]; !ok {
		return fmt.Errorf("snippet not found: %s", id)
	}
	delete(s.snippets, id)
	return nil
}

// DeleteScoped removes a snippet by ID with ownership verification.
func (s *Store) DeleteScoped(id, ownerID, workspaceID string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("snippet ID cannot be empty")
	}
	if strings.TrimSpace(ownerID) == "" {
		return fmt.Errorf("owner_id is required")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snip, ok := s.snippets[id]
	if !ok || snip.OwnerID != ownerID || snip.WorkspaceID != workspaceID {
		return fmt.Errorf("snippet not found")
	}
	delete(s.snippets, id)
	return nil
}

var idCounter atomic.Int64

// generateID creates a unique sequential ID for snippets.
func generateID() string {
	return fmt.Sprintf("snip_%d", idCounter.Add(1))
}

// copySnippet creates a deep copy of a snippet to prevent external mutation.
func copySnippet(snip *Snippet) *Snippet {
	if snip == nil {
		return nil
	}
	
	// Copy tags slice
	tagsCopy := make([]string, len(snip.Tags))
	copy(tagsCopy, snip.Tags)
	
	return &Snippet{
		ID:          snip.ID,
		OwnerID:     snip.OwnerID,
		WorkspaceID: snip.WorkspaceID,
		Title:       snip.Title,
		Language:    snip.Language,
		Code:        snip.Code,
		Description: snip.Description,
		Tags:        tagsCopy,
		ProjectID:   snip.ProjectID,
		Source:      snip.Source,
		SourceID:    snip.SourceID,
		CreatedAt:   snip.CreatedAt,
		UpdatedAt:   snip.UpdatedAt,
	}
}