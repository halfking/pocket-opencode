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
	Action     string    `json:"action"` // chat.send / file.read / session.create
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
	TenantID      string `json:"tenant_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	Action        string `json:"action,omitempty"`
	ExcludeAction string `json:"-"`
	Limit         int    `json:"limit,omitempty"`

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
	seq     uint64
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

	stored := *entry
	if stored.ID == "" {
		s.seq++
		stored.ID = fmt.Sprintf("aud_%d_%d", time.Now().UnixNano(), s.seq)
	}
	// 调用方显式提供的时间戳优先（backfill / 测试）；否则取当前时间。
	if stored.Timestamp.IsZero() {
		stored.Timestamp = time.Now()
	}
	entry.ID = stored.ID
	entry.Timestamp = stored.Timestamp

	// Keep the same tuple order used by PGAuditStore.QueryRange. This also
	// keeps backfilled entries compatible with the in-memory fallback.
	idx := sort.Search(len(s.entries), func(i int) bool {
		return auditEntryLess(&stored, s.entries[i])
	})
	s.entries = append(s.entries, nil)
	copy(s.entries[idx+1:], s.entries[idx:])
	s.entries[idx] = &stored

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
		if query.ExcludeAction != "" && e.Action == query.ExcludeAction {
			continue
		}
		result = append(result, cloneAuditEntry(e))
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Timestamp.Equal(result[j].Timestamp) {
			return result[i].ID > result[j].ID
		}
		return result[i].Timestamp.After(result[j].Timestamp)
	})

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Flush returns all stored audit entries and clears the store.
func (s *AuditStore) Flush() []*AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := make([]*AuditEntry, len(s.entries))
	for i, entry := range s.entries {
		entries[i] = cloneAuditEntry(entry)
	}
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
//
// 必须使用全精度（UnixNano）：time.Now() 产生的纳秒级时间戳经 PG
// TIMESTAMPTZ 落库为微秒精度。若仅用 UnixMilli 编码，游标会丢失亚毫秒精度，
// 导致下一页的 (timestamp, id) > (cursorTs, cursorID) 把游标所在毫秒桶整体
// 重新包含，造成跨页重复计数。UnixNano 在内存（ns）与 PG（µs 往返）下都能与
// 落库值精确对齐，边界条目被严格排除。
func encodeAuditCursor(e *AuditEntry) string {
	return "v2:" + strconv.FormatInt(e.Timestamp.UnixNano(), 10) + ":" + e.ID
}

// decodeAuditCursor accepts the current v2 UnixNano:id format and the
// historical bare UnixMilli:id format used before sub-millisecond cursors.
func decodeAuditCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	if strings.TrimSpace(cursor) != cursor {
		return time.Time{}, "", fmt.Errorf("invalid audit cursor")
	}
	value := cursor
	unit := "legacy"
	if strings.HasPrefix(cursor, "v2:") {
		value = strings.TrimPrefix(cursor, "v2:")
		unit = "nano"
	}
	idx := strings.IndexByte(value, ':')
	if idx <= 0 || idx == len(value)-1 || strings.Contains(value[idx+1:], ":") {
		return time.Time{}, "", fmt.Errorf("invalid audit cursor")
	}
	n, err := strconv.ParseInt(value[:idx], 10, 64)
	if err != nil || n < 0 {
		return time.Time{}, "", fmt.Errorf("invalid audit cursor")
	}
	id := value[idx+1:]
	if strings.TrimSpace(id) != id || strings.ContainsAny(id, "\r\n") {
		return time.Time{}, "", fmt.Errorf("invalid audit cursor")
	}
	if unit == "nano" || (unit == "legacy" && n >= 1_000_000_000_000_000) {
		return time.Unix(0, n), id, nil
	}
	return time.UnixMilli(n), id, nil
}

// ValidateAuditCursor validates an optional cursor at an API boundary.
func ValidateAuditCursor(cursor string) error {
	_, _, err := decodeAuditCursor(cursor)
	return err
}

func auditEntryLess(a, b *AuditEntry) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.ID < b.ID
	}
	return a.Timestamp.Before(b.Timestamp)
}

func cloneAuditEntry(entry *AuditEntry) *AuditEntry {
	if entry == nil {
		return nil
	}
	copy := *entry
	return &copy
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
	cursorTs, cursorID, err := decodeAuditCursor(query.AfterCursor)
	if err != nil {
		return nil, err
	}
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
		if query.ExcludeAction != "" && e.Action == query.ExcludeAction {
			continue
		}
		page.Entries = append(page.Entries, cloneAuditEntry(e))
		if len(page.Entries) == limit+1 {
			page.Entries = page.Entries[:limit]
			page.NextCursor = encodeAuditCursor(page.Entries[limit-1])
			return page, nil
		}
	}
	return page, nil
}
