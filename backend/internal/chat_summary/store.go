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

func NewStore() *Store {
	return &Store{
		summaries: make(map[string]*ChatSummary),
	}
}

func (s *Store) Create(summary *ChatSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	summary.ID = fmt.Sprintf("cs_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
	summary.CreatedAt = time.Now()
	s.summaries[summary.ID] = summary
	return nil
}

func (s *Store) Get(id string) (*ChatSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cs, ok := s.summaries[id]
	if !ok {
		return nil, fmt.Errorf("chat summary not found: %s", id)
	}
	return cs, nil
}

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

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.summaries[id]; !ok {
		return fmt.Errorf("chat summary not found: %s", id)
	}
	delete(s.summaries, id)
	return nil
}