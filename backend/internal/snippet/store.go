// internal/snippet/store.go
package snippet

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Store 代码片段存储（内存实现，后续可迁移到数据库）
type Store struct {
	mu       sync.RWMutex
	snippets map[string]*Snippet
}

func NewStore() *Store {
	return &Store{
		snippets: make(map[string]*Snippet),
	}
}

func (s *Store) Create(req CreateSnippetRequest) (*Snippet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	snip := &Snippet{
		ID:          generateID(),
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
	return snip, nil
}

func (s *Store) Get(id string) (*Snippet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snip, ok := s.snippets[id]
	if !ok {
		return nil, fmt.Errorf("snippet not found: %s", id)
	}
	return snip, nil
}

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
	if req.Offset > 0 && req.Offset < len(result) {
		result = result[req.Offset:]
	}

	// 应用 limit
	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.snippets[id]; !ok {
		return fmt.Errorf("snippet not found: %s", id)
	}
	delete(s.snippets, id)
	return nil
}

var idCounter atomic.Int64

func generateID() string {
	return fmt.Sprintf("snip_%d", idCounter.Add(1))
}