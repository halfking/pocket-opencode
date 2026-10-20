// internal/meeting/store.go
package meeting

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// legacyOwnerID / legacyWorkspaceID are the identity defaults used by the
// deprecated non-scoped API, matching the handler fallback for requests
// without authenticated claims.
const (
	legacyOwnerID     = "local"
	legacyWorkspaceID = "default"
)

// MeetingStore 是会议存储的抽象，内存版（*Store）与 PG 版（*PGStore）共用。
//
// 这里显式列出服务端与 learning resolver 真正调用到的方法，而不是直接拿
// *Store 当依赖：server.Server 里的字段类型、SetMeetingStore 的入参、
// learning/sources.Resolver 的字段三处都要跟着变，提前定死接口可以让
// 编译器在注入点就把"少实现了某个方法"拦下来。
type MeetingStore interface {
	CreateScoped(req CreateMeetingRequest, ownerID, workspaceID string) (*Meeting, error)
	GetScoped(id, ownerID, workspaceID string) (*Meeting, error)
	// ListScoped 返回**一页**会议 + **过滤后的总条数**（§936）。
	//
	// ⚠ total **不是** len(page)：有上限时「本页有几行」是**谎话**（§936 的 D1
	// —— 修复前 total = len(被 LIMIT 截断后的列表) ⇒ 用户有 600 场会议时
	// total 恒为 500 ⇒ 分页器显示「共 500 场」，第 501 场之后永远取不到）。
	//
	// ⚠ 两个实现必须**逐字一致**（同一份数据、同一组 opts ⇒ 同样的 id 序列
	// 与同样的 total）⇒ 口径见 list.go，判据见 list_test.go。
	ListScoped(ownerID, workspaceID string, opts ListOptions) ([]*Meeting, int, error)
	UpdateScoped(m *Meeting, ownerID, workspaceID string) error
	DeleteScoped(id, ownerID, workspaceID string) error
	// DeletedIDsSince 见 pg_store.go 同名方法：查询失败时返回 nil 而非 error。
	DeletedIDsSince(ownerID, workspaceID string, since time.Time) []string
}

// Store 会议记录存储（内存实现）
//
// ⚠️ 进程一重启即全部丢失。生产路径由 cmd/pocketd 注入 meeting.PGStore
// 覆盖它（见 Server.SetMeetingStore）；没有 PG 的测试环境才用这份。
type Store struct {
	mu       sync.RWMutex
	meetings map[string]*Meeting
	// tombstones 记录已删除会议的墓碑（删除时间 + 归属），供增量同步
	// 下发 deletedIds；内存实现重启即清空，客户端以全量对账兜底。
	tombstones map[string]meetingTombstone
}

type meetingTombstone struct {
	ownerID     string
	workspaceID string
	at          time.Time
}

// meetingIDSeq 保证同一进程内 ID 唯一。
//
// 为什么必须要它：ID 原先只有 `time.Now().UnixNano()`，而
// **`time.Now()` 在很多环境（本仓库的 Windows 机器实测 1000 次调用
// 只产生 1 个不同值）根本没有纳秒精度**。同一时钟刻度内连续创建的两条
// 会议会拿到完全相同的 ID，而 `s.meetings[m.ID] = m` 让后者直接覆盖前者 ——
// **创建成功、返回 201、库里却查不到**（实测 200 次创建只剩 6 条）。
//
// 加上单调递增的序号后，`nano_seq` 这一对在进程内必然唯一；跨进程则由
// 纳秒部分区分。格式与 `finance.Store` / `chat_summary.Store` 保持一致。
var meetingIDSeq atomic.Uint64

// NewStore creates a new in-memory meeting store
func NewStore() *Store {
	return &Store{
		meetings:   make(map[string]*Meeting),
		tombstones: make(map[string]meetingTombstone),
	}
}

// Create creates a new meeting record with the given request.
// Returns error if title is empty.
// Deprecated: Use CreateScoped for production code with proper ownership.
func (s *Store) Create(req CreateMeetingRequest) (*Meeting, error) {
	return s.CreateScoped(req, legacyOwnerID, legacyWorkspaceID)
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
	// ★ 2026-10-07：与 PGStore 对齐——客户端带的 id 现在被尊重，
	// 且**重复 POST 变成幂等更新**（旧实现无条件新生成 ⇒ 重试即重复行）。

	// 客户端给了 ID 就用它（§105）：客户端拿着同一个 ID 调 refine，
	// 服务端另发号会让那条链必然 404。
	//
	// ⚠ 但**只在没人占着它的时候**用：`s.meetings` 是按 ID 索引的单表，
	//   盲写会把别的 owner/workspace 的同名行直接顶掉。PG 版靠唯一约束
	//   + 重试换号挡住了这件事（pg_store.go 的 isUniqueViolation 分支），
	//   内存版没有那层护栏 ⇒ **两个 store 会不同形**，而门禁跑在内存版上。
	//   这里显式对齐 PG 语义：异作用域撞号 ⇒ 退回服务端发号，绝不覆盖。
	if req.StartedAt > 0 {
		now = time.UnixMilli(req.StartedAt)
	}
	status := req.Status
	if status == "" {
		status = "recording"
	}
	id := strings.TrimSpace(req.ID)
	if id != "" {
		if existing, ok := s.meetings[id]; ok &&
			(existing.OwnerID != ownerID || existing.WorkspaceID != workspaceID) {
			id = ""
		}
	}
	if id == "" {
		id = fmt.Sprintf("mtg_%d_%d", now.UnixNano(), meetingIDSeq.Add(1))
	}
	m := &Meeting{
		ID:           id,
		OwnerID:      ownerID,
		WorkspaceID:  workspaceID,
		Title:        req.Title,
		Duration:     int(req.DurationMs / 1000),
		Transcript:   req.RefinedTranscript,
		Summary:      req.Summary,
		Participants: req.Participants,
		Location:     req.Location,
		NoteID:       req.NoteID,
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if old, ok := s.meetings[m.ID]; ok {
		// 同一 id 再 POST（同作用域）：保留服务端补出来的空值列，别用零值覆盖已有内容。
		if m.Transcript == "" {
			m.Transcript = old.Transcript
		}
		if m.Summary == "" {
			m.Summary = old.Summary
		}
		if len(m.Participants) == 0 {
			m.Participants = old.Participants
		}
		if m.Location == "" {
			m.Location = old.Location
		}
		if m.NoteID == "" {
			m.NoteID = old.NoteID
		}
		if !now.After(old.CreatedAt) {
			m.CreatedAt = old.CreatedAt
		}
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

// ListScoped returns one page of meeting records for the specified
// owner/workspace, plus the total number of rows that matched the filter
// (before paging).
//
// ⚠ §936 之前，本方法**既无上限、也不排序**（map 遍历 ⇒ 随机顺序），
// 而 PG 版有 `LIMIT 500` + `ORDER BY created_at DESC` ⇒ 同一个端点在两种
// store 下形状不同 ⇒ dev 环境测不出生产的问题。现在两个实现共用
// list.go 的口径（排序 / 过滤 / 分页 / 计数各只有一份）。
func (s *Store) ListScoped(ownerID, workspaceID string, opts ListOptions) ([]*Meeting, int, error) {
	if ownerID == "" {
		return nil, 0, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, 0, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	scoped := make([]*Meeting, 0)
	for _, m := range s.meetings {
		if m.OwnerID == ownerID && m.WorkspaceID == workspaceID {
			scoped = append(scoped, copyMeeting(m))
		}
	}
	// 与 PG 版一致返回非 nil 空切片：handler 直接把它写进 JSON 的 meetings，
	// nil 切片编码成 null 会让前端拿到"字段存在但不是数组"的形状。
	page, total := ApplyListOptions(scoped, opts)
	return page, total, nil
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
	s.tombstones[id] = meetingTombstone{ownerID: ownerID, workspaceID: workspaceID, at: time.Now()}
	return nil
}

// DeletedIDsSince returns ids of meetings deleted after since for the
// requested scope (增量同步墓碑清单)。
func (s *Store) DeletedIDsSince(ownerID, workspaceID string, since time.Time) []string {
	if ownerID == "" || workspaceID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0)
	for id, t := range s.tombstones {
		if t.ownerID == ownerID && t.workspaceID == workspaceID && (since.IsZero() || t.at.After(since)) {
			out = append(out, id)
		}
	}
	return out
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
