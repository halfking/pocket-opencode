package redclaw

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
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

	// 增量导出参数（P1 审计导出）。
	// StartTime/EndTime 构成闭开区间 [start, end)；零值表示不设界。
	StartTime time.Time `json:"-"`
	EndTime   time.Time `json:"-"`
	// AfterCursor 为上一页 QueryRange 返回的 NextCursor；空串表示从
	// StartTime 开始。游标编码了 (timestamp, id)，同时间戳内按 id 定位。
	AfterCursor string `json:"-"`
}

// AuditPage 增量查询结果页。
type AuditPage struct {
	Entries []*AuditEntry `json:"entries"`
	// NextCursor 非空表示可能还有下一页；将其作为 AfterCursor 传入下一
	// 次查询。为空表示已到 EndTime 或末尾。
	NextCursor string `json:"next_cursor"`
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
	// 调用方显式提供的时间戳优先（backfill / 测试）；否则取当前时间。
	// entries 按追加顺序即时间顺序，QueryRange 的二分依赖该不变量。
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
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

// ---------------------------------------------------------------------------
// P1 审计增量导出
// ---------------------------------------------------------------------------

const (
	auditDefaultRangeLimit = 500
	auditMaxRangeLimit     = 1000
)

// encodeAuditCursor 编码 (timestamp, id) 为不透明游标。
func encodeAuditCursor(e *AuditEntry) string {
	return strconv.FormatInt(e.Timestamp.UnixMilli(), 10) + ":" + e.ID
}

// decodeAuditCursor 解析游标；非法游标返回零值。
func decodeAuditCursor(cursor string) (time.Time, string) {
	idx := strings.Index(cursor, ":")
	if idx <= 0 {
		return time.Time{}, ""
	}
	ms, err := strconv.ParseInt(cursor[:idx], 10, 64)
	if err != nil {
		return time.Time{}, ""
	}
	return time.UnixMilli(ms), cursor[idx+1:]
}

// afterCursor 判断 e 是否严格位于游标之后。
func afterCursor(e *AuditEntry, ts time.Time, id string) bool {
	if e.Timestamp.After(ts) {
		return true
	}
	return e.Timestamp.Equal(ts) && e.ID > id
}

// QueryRange 增量分页查询（审计导出用）。
//
// 复杂度：O(log n) 二分定位 StartTime + O(limit) 扫描，避免全量扫描。
// 游标同时编码时间戳与 id：同毫秒多条记录也能精确续传；即使底层 entries
// 因 maxSize 截断丢失旧记录，游标仍按时间戳正确对齐。
func (s *AuditStore) QueryRange(query AuditQuery) (*AuditPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = auditDefaultRangeLimit
	}
	if limit > auditMaxRangeLimit {
		limit = auditMaxRangeLimit
	}

	// 二分：第一个 Timestamp >= StartTime 的位置（entries 按时间升序）。
	start := query.StartTime
	cursorTs, cursorID := decodeAuditCursor(query.AfterCursor)
	if !cursorTs.IsZero() && cursorTs.After(start) {
		// 游标比 StartTime 更靠后时以游标为准（增量语义）。
		start = cursorTs
	}
	lo := sort.Search(len(s.entries), func(i int) bool {
		return !s.entries[i].Timestamp.Before(start)
	})

	page := &AuditPage{Entries: make([]*AuditEntry, 0, limit)}
	for i := lo; i < len(s.entries); i++ {
		e := s.entries[i]
		if !query.EndTime.IsZero() && !e.Timestamp.Before(query.EndTime) {
			return page, nil // 到达 end（闭开区间），必无更多
		}
		if cursorID != "" && !afterCursor(e, cursorTs, cursorID) {
			continue // 同时间段内游标之前的记录（含已导出的本批）
		}
		if query.TenantID != "" && e.TenantID != query.TenantID {
			continue
		}
		if query.UserID != "" && e.UserID != query.UserID {
			continue
		}
		if query.Action != "" && e.Action != query.Action {
			continue
		}
		page.Entries = append(page.Entries, e)
		if len(page.Entries) == limit {
			page.NextCursor = encodeAuditCursor(e)
			return page, nil
		}
	}
	return page, nil
}