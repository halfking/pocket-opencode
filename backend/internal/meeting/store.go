// internal/meeting/store.go
package meeting

import (
	"fmt"
	"sync"
	"time"
)

// Store 会议记录存储（内存实现）
type Store struct {
	mu       sync.RWMutex
	meetings map[string]*Meeting
}

func NewStore() *Store {
	return &Store{
		meetings: make(map[string]*Meeting),
	}
}

func (s *Store) Create(req CreateMeetingRequest) (*Meeting, error) {
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
	return m, nil
}

func (s *Store) Get(id string) (*Meeting, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.meetings[id]
	if !ok {
		return nil, fmt.Errorf("meeting not found: %s", id)
	}
	return m, nil
}

func (s *Store) Update(m *Meeting) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.meetings[m.ID]; !ok {
		return fmt.Errorf("meeting not found: %s", m.ID)
	}
	m.UpdatedAt = time.Now()
	s.meetings[m.ID] = m
	return nil
}

func (s *Store) List() ([]*Meeting, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Meeting
	for _, m := range s.meetings {
		result = append(result, m)
	}
	return result, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.meetings[id]; !ok {
		return fmt.Errorf("meeting not found: %s", id)
	}
	delete(s.meetings, id)
	return nil
}