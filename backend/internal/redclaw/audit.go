package redclaw

import (
	"fmt"
	"sync"
	"time"
)

// AuditEntry 审计日志条目
type AuditEntry struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`                // chat.send / file.read / session.create
	UserID     string    `json:"user_id"`
	TenantID   string    `json:"tenant_id"`
	Resource   string    `json:"resource,omitempty"`    // 操作的资源
	Detail     string    `json:"detail,omitempty"`      // 详细信息
	DurationMs int64     `json:"duration_ms,omitempty"` // 耗时（毫秒）
	Success    bool      `json:"success"`               // 是否成功
	Timestamp  time.Time `json:"timestamp"`
	IP         string    `json:"ip,omitempty"`
}

// AuditQuery 审计查询
type AuditQuery struct {
	TenantID string `json:"tenant_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	Action   string `json:"action,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// AuditStore 审计日志存储
type AuditStore struct {
	mu      sync.RWMutex
	entries []*AuditEntry
	maxSize int
}

// NewAuditStore creates a new audit log store with default capacity.
func NewAuditStore() *AuditStore {
	return &AuditStore{
		entries: make([]*AuditEntry, 0, 1000),
		maxSize: 10000,
	}
}

// Record records an audit entry to the store.
func (s *AuditStore) Record(entry *AuditEntry) error {
	if entry == nil {
		return fmt.Errorf("audit entry cannot be nil")
	}
	
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.ID = fmt.Sprintf("aud_%d_%d", time.Now().UnixNano(), len(s.entries))
	entry.Timestamp = time.Now()
	s.entries = append(s.entries, entry)

	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize/2:]
	}

	return nil
}

// Query retrieves audit entries matching the given query filters.
func (s *AuditStore) Query(query AuditQuery) ([]*AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	var result []*AuditEntry
	for _, e := range s.entries {
		if query.TenantID != "" && e.TenantID != query.TenantID {
			continue
		}
		if query.UserID != "" && e.UserID != query.UserID {
			continue
		}
		if query.Action != "" && e.Action != query.Action {
			continue
		}
		result = append(result, e)
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Flush returns all stored audit entries and clears the store.
func (s *AuditStore) Flush() []*AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.entries
	s.entries = make([]*AuditEntry, 0, 1000)
	return entries
}