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
	ListScoped(ownerID, workspaceID string) ([]*Meeting, error)
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
	m := &Meeting{
		ID:          fmt.Sprintf("mtg_%d_%d", now.UnixNano(), meetingIDSeq.Add(1)),
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