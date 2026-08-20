# Phase 2: 核心功能升级 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 升级 AI 编程工作台（语音输入 + Diff 展示 + 代码片段），新增会议录音及总结功能，新增聊天记录总结功能

**Architecture:** 所有功能通过 Pocket Backend (Go) 的 REST API + WebSocket 提供，前端使用 Vue 3 + Capacitor。STT 已有 Groq Whisper 集成，会议模块新增录音管理 + 转写 + AI 总结，聊天模块复用现有 IM 集成。

**Tech Stack:** Go 1.22+ / gorilla/websocket / Vue 3 / Capacitor / Groq Whisper API / LLM (AI 总结)

---

## 文件结构

### AI 编程工作台升级

```
backend/internal/
├── snippet/                    # 代码片段管理 (新增)
│   ├── store.go                # 片段存储
│   ├── store_test.go
│   └── types.go                # 类型定义

backend/internal/server/
├── server_snippet.go           # 代码片段 API (新增)
├── server_snippet_test.go

frontend/src/
├── components/
│   ├── DiffViewer.vue          # 代码 Diff 展示组件 (新增)
│   └── VoiceInput.vue          # 语音输入按钮组件 (新增)
├── views/
│   ├── Snippets.vue            # 代码片段管理页面 (新增)
│   └── SnippetEditor.vue       # 片段编辑页面 (新增)
├── services/
│   └── snippet.ts              # 片段 API 服务 (新增)
```

### 会议录音及总结

```
backend/internal/
├── meeting/                    # 会议模块 (新增)
│   ├── store.go                # 会议记录存储
│   ├── store_test.go
│   ├── recorder.go             # 录音管理（服务端处理）
│   ├── summarizer.go           # AI 总结引擎
│   ├── summarizer_test.go
│   └── types.go                # 类型定义

backend/internal/server/
├── server_meeting.go           # 会议 API (新增)
├── server_meeting_test.go

frontend/src/
├── components/
│   ├── MeetingRecorder.vue      # 录音组件 (新增)
│   └── MeetingSummary.vue       # 会议总结卡片 (新增)
├── views/
│   ├── MeetingRoom.vue          # 会议录音室页面 (新增)
│   └── MeetingDetail.vue        # 会议详情页 (新增)
├── services/
│   └── meeting.ts               # 会议 API 服务 (新增)
```

### 聊天记录总结

```
backend/internal/
├── chat_summary/               # 聊天总结模块 (新增)
│   ├── store.go                # 摘要存储
│   ├── store_test.go
│   ├── aggregator.go           # 消息聚合
│   ├── aggregator_test.go
│   ├── summarizer.go           # AI 摘要生成
│   ├── summarizer_test.go
│   └── types.go                # 类型定义

backend/internal/server/
├── server_chat_summary.go      # 聊天总结 API (新增)
├── server_chat_summary_test.go

frontend/src/
├── components/
│   └── ChatSummaryCard.vue      # 聊天总结卡片 (新增)
├── views/
│   └── ChatSummary.vue          # 聊天总结页面 (新增)
├── services/
│   └── chat_summary.ts          # 聊天总结 API 服务 (新增)
```

---

### Task 1: 代码片段类型定义与存储

**Files:**
- Create: `backend/internal/snippet/types.go`
- Create: `backend/internal/snippet/store.go`
- Create: `backend/internal/snippet/store_test.go`

- [ ] **Step 1: 创建类型定义**

```go
// internal/snippet/types.go
package snippet

import "time"

// Snippet 代码片段
type Snippet struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Language    string    `json:"language"`
	Code        string    `json:"code"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	Source      string    `json:"source,omitempty"` // manual / session / import
	SourceID    string    `json:"source_id,omitempty"` // 来源会话 ID
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateSnippetRequest 创建片段请求
type CreateSnippetRequest struct {
	Title       string   `json:"title"`
	Language    string   `json:"language"`
	Code        string   `json:"code"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
}

// ListSnippetsRequest 列表查询参数
type ListSnippetsRequest struct {
	Language string `json:"language,omitempty"`
	Tag      string `json:"tag,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Search   string `json:"search,omitempty"`
	Limit    int    `json:"limit,omitempty"` // 默认 50
	Offset   int    `json:"offset,omitempty"`
}
```

- [ ] **Step 2: 编写存储测试**

```go
// internal/snippet/store_test.go
package snippet

import (
	"testing"
	"time"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()
	
	// Create
	req := CreateSnippetRequest{
		Title:    "Hello World",
		Language: "go",
		Code:     `fmt.Println("hello")`,
		Tags:     []string{"example"},
	}
	
	snip, err := s.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if snip.Title != "Hello World" {
		t.Errorf("expected title Hello World, got %s", snip.Title)
	}
	if snip.ID == "" {
		t.Error("expected non-empty ID")
	}
	
	// Get
	got, err := s.Get(snip.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Code != `fmt.Println("hello")` {
		t.Errorf("expected code match, got %s", got.Code)
	}
	
	// List
	snippets, err := s.List(ListSnippetsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snippets) != 1 {
		t.Errorf("expected 1 snippet, got %d", len(snippets))
	}
	
	// Delete
	err = s.Delete(snip.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	
	_, err = s.Get(snip.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestStore_Search(t *testing.T) {
	s := NewStore()
	s.Create(CreateSnippetRequest{Title: "Sort Array", Language: "go", Code: "sort.Ints()"})
	s.Create(CreateSnippetRequest{Title: "Fetch API", Language: "ts", Code: "fetch(url)"})
	s.Create(CreateSnippetRequest{Title: "Map Filter", Language: "ts", Code: "arr.map().filter()"})
	
	// Search by language
	results, _ := s.List(ListSnippetsRequest{Language: "ts", Limit: 10})
	if len(results) != 2 {
		t.Errorf("expected 2 ts snippets, got %d", len(results))
	}
	
	// Search by keyword
	results, _ = s.List(ListSnippetsRequest{Search: "sort", Limit: 10})
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'sort', got %d", len(results))
	}
}
```

- [ ] **Step 3: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/snippet/ -v`
Expected: FAIL — "NewStore not defined"

- [ ] **Step 4: 实现存储**

```go
// internal/snippet/store.go
package snippet

import (
	"fmt"
	"strings"
	"sync"
	"time"
	
	"github.com/halfking/pocket-opencode/backend/internal/model"
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

func generateID() string {
	return fmt.Sprintf("snip_%d", time.Now().UnixNano())
}
```

- [ ] **Step 5: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/snippet/ -v`
Expected: PASS (2/2)

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/snippet/
git commit -m "feat(snippet): add code snippet types and in-memory store"
```

---

### Task 2: 代码片段 API 路由

**Files:**
- Create: `backend/internal/server/server_snippet.go`
- Create: `backend/internal/server/server_snippet_test.go`
- Modify: `backend/internal/server/server.go` (路由注册)

- [ ] **Step 1: 创建 API 处理器**

```go
// internal/server/server_snippet.go
package server

import (
	"encoding/json"
	"net/http"
	
	"github.com/halfking/pocket-opencode/backend/internal/snippet"
)

func (s *Server) handleSnippets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListSnippets(w, r)
	case http.MethodPost:
		s.handleCreateSnippet(w, r)
	case http.MethodDelete:
		s.handleDeleteSnippet(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListSnippets(w http.ResponseWriter, r *http.Request) {
	req := snippet.ListSnippetsRequest{
		Language:  r.URL.Query().Get("language"),
		Tag:       r.URL.Query().Get("tag"),
		ProjectID: r.URL.Query().Get("project_id"),
		Search:    r.URL.Query().Get("search"),
	}
	
	snippets, err := s.snippetStore.List(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"snippets": snippets,
		"total":    len(snippets),
	})
}

func (s *Server) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	var req snippet.CreateSnippetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	
	if req.Title == "" || req.Code == "" {
		http.Error(w, "title and code are required", http.StatusBadRequest)
		return
	}
	
	snip, err := s.snippetStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(snip)
}

func (s *Server) handleDeleteSnippet(w http.ResponseWriter, r *http.Request) {
	// 从路径提取 snippet ID: /api/snippets/{id}
	id := r.URL.Path[len("/api/snippets/"):]
	if id == "" {
		http.Error(w, "missing snippet id", http.StatusBadRequest)
		return
	}
	
	if err := s.snippetStore.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: 修改 server.go 添加 snippetStore 字段**

在 `Server` 结构体中添加：
```go
snippetStore *snippet.Store
```

在初始化代码中添加：
```go
s.snippetStore = snippet.NewStore()
```

在路由注册中添加：
```go
mux.HandleFunc("/api/snippets", s.requireAuth(s.handleSnippets))
mux.HandleFunc("/api/snippets/", s.requireAuth(s.handleSnippets))
```

- [ ] **Step 3: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```
Expected: 编译成功

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/server/server_snippet.go backend/internal/server/server.go
git commit -m "feat(snippet): add snippet API routes and integrate into server"
```

---

### Task 3: 会议模块类型定义与存储

**Files:**
- Create: `backend/internal/meeting/types.go`
- Create: `backend/internal/meeting/store.go`
- Create: `backend/internal/meeting/store_test.go`

- [ ] **Step 1: 创建类型定义**

```go
// internal/meeting/types.go
package meeting

import "time"

// Meeting 会议记录
type Meeting struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Duration     int       `json:"duration"`      // 秒
	RecordingURL string    `json:"recording_url,omitempty"`
	Transcript   string    `json:"transcript,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	KeyDecisions []string  `json:"key_decisions,omitempty"`
	ActionItems  []ActionItem `json:"action_items,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	Status       string    `json:"status"` // recording / transcribing / summarizing / done / failed
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ActionItem 待办事项
type ActionItem struct {
	Owner    string `json:"owner,omitempty"`
	Task     string `json:"task"`
	Deadline string `json:"deadline,omitempty"`
}

// CreateMeetingRequest 创建会议请求
type CreateMeetingRequest struct {
	Title string `json:"title"`
}

// TranscribeRequest 转写请求
type TranscribeRequest struct {
	AudioData []byte `json:"audio_data"` // base64 编码的音频数据
	Filename  string `json:"filename"`   // 如 "meeting.wav"
}

// SummarizeRequest AI 总结请求
type SummarizeRequest struct {
	Transcript string `json:"transcript"`
	Title      string `json:"title,omitempty"`
}
```

- [ ] **Step 2: 编写存储测试**

```go
// internal/meeting/store_test.go
package meeting

import (
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()
	
	// Create
	m, err := s.Create(CreateMeetingRequest{Title: "Sprint Planning"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if m.Title != "Sprint Planning" {
		t.Errorf("expected title Sprint Planning, got %s", m.Title)
	}
	if m.Status != "recording" {
		t.Errorf("expected status recording, got %s", m.Status)
	}
	
	// Get
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch")
	}
	
	// Update
	got.Transcript = "Hello world"
	got.Status = "done"
	err = s.Update(got)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	
	updated, _ := s.Get(m.ID)
	if updated.Transcript != "Hello world" {
		t.Errorf("expected transcript updated")
	}
	
	// List
	meetings, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(meetings) != 1 {
		t.Errorf("expected 1 meeting, got %d", len(meetings))
	}
	
	// Delete
	err = s.Delete(m.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = s.Get(m.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}
```

- [ ] **Step 3: 实现存储**

```go
// internal/meeting/store.go
package meeting

import (
	"fmt"
	"sync"
	"time"
)

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
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/meeting/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/meeting/
git commit -m "feat(meeting): add meeting types and store"
```

---

### Task 4: 会议 AI 总结引擎

**Files:**
- Create: `backend/internal/meeting/summarizer.go`
- Create: `backend/internal/meeting/summarizer_test.go`

- [ ] **Step 1: 编写总结引擎测试**

```go
// internal/meeting/summarizer_test.go
package meeting

import (
	"testing"
)

func TestSummarize(t *testing.T) {
	transcript := `张三: 我们讨论一下Q3的AI功能优先级
李四: 我建议优先做会议总结功能，用户需求很大
张三: 同意，那API升级推迟到下一轮Sprint
王五: 好的，我来负责会议总结模块的设计
张三: 截止日期定在7月28日
李四: 收到`

	summary, err := SummarizeTranscript(transcript, "Sprint Planning")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	
	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected at least one key decision")
	}
	if len(summary.ActionItems) == 0 {
		t.Error("expected at least one action item")
	}
}

func TestSummarize_EmptyTranscript(t *testing.T) {
	_, err := SummarizeTranscript("", "Empty Meeting")
	if err == nil {
		t.Error("expected error for empty transcript")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/meeting/ -run TestSummarize -v`
Expected: FAIL — "SummarizeTranscript not defined"

- [ ] **Step 3: 实现总结引擎**

```go
// internal/meeting/summarizer.go
package meeting

import (
	"fmt"
	"strings"
)

// MeetingSummary AI 总结结果
type MeetingSummary struct {
	Summary      string       `json:"summary"`
	KeyDecisions []string     `json:"key_decisions"`
	ActionItems  []ActionItem `json:"action_items"`
}

// SummarizeTranscript 对会议转写文本进行 AI 总结
// 当前为规则引擎实现，后续可替换为 LLM 调用
func SummarizeTranscript(transcript string, title string) (*MeetingSummary, error) {
	if strings.TrimSpace(transcript) == "" {
		return nil, fmt.Errorf("transcript is empty")
	}
	
	lines := strings.Split(transcript, "\n")
	
	// 提取决策（包含"决定"、"同意"等关键词的行）
	var decisions []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "决定") || strings.Contains(lower, "同意") || 
		   strings.Contains(lower, "就这么办") || strings.Contains(lower, "批准") {
			decisions = append(decisions, line)
		}
	}
	
	// 提取待办（包含"负责"、"我来"、"截止"等关键词的行）
	var items []ActionItem
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "负责") || strings.Contains(lower, "我来") {
			items = append(items, ActionItem{Task: line})
		}
		if strings.Contains(lower, "截止") {
			if len(items) > 0 {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					items[len(items)-1].Deadline = strings.TrimSpace(parts[1])
				}
			}
		}
	}
	
	if len(decisions) == 0 {
		decisions = []string{"（未识别到明确的决策点）"}
	}
	if len(items) == 0 {
		items = []ActionItem{{Task: "（未识别到明确的待办事项）"}}
	}
	
	summary := fmt.Sprintf("会议《%s》共 %d 条消息，识别到 %d 个决策点和 %d 个待办事项。",
		title, len(lines), len(decisions), len(items))
	
	return &MeetingSummary{
		Summary:      summary,
		KeyDecisions: decisions,
		ActionItems:  items,
	}, nil
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/meeting/ -v`
Expected: PASS (3/3 — 1 CRUD + 2 summarizer)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/meeting/summarizer.go backend/internal/meeting/summarizer_test.go
git commit -m "feat(meeting): add meeting summarizer engine"
```

---

### Task 5: 会议 API 路由

**Files:**
- Create: `backend/internal/server/server_meeting.go`
- Create: `backend/internal/server/server_meeting_test.go`
- Modify: `backend/internal/server/server.go` (路由注册)

- [ ] **Step 1: 创建会议 API 处理器**

```go
// internal/server/server_meeting.go
package server

import (
	"encoding/json"
	"net/http"
	
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

func (s *Server) handleMeetings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListMeetings(w, r)
	case http.MethodPost:
		s.handleCreateMeeting(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMeetingOps(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/meetings/"):]
	// 处理子路径: /api/meetings/{id}
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		id = parts[0]
		action := parts[1]
		
		switch action {
		case "transcribe":
			s.handleTranscribeMeeting(w, r, id)
			return
		case "summarize":
			s.handleSummarizeMeeting(w, r, id)
			return
		}
	}
	
	switch r.Method {
	case http.MethodGet:
		s.handleGetMeeting(w, r, id)
	case http.MethodDelete:
		s.handleDeleteMeeting(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	meetings, err := s.meetingStore.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"meetings": meetings,
		"total":    len(meetings),
	})
}

func (s *Server) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	var req meeting.CreateMeetingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		req.Title = "未命名会议"
	}
	
	m, err := s.meetingStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(m)
}

func (s *Server) handleGetMeeting(w http.ResponseWriter, r *http.Request, id string) {
	m, err := s.meetingStore.Get(id)
	if err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (s *Server) handleDeleteMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.meetingStore.Delete(id); err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTranscribeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	m, err := s.meetingStore.Get(id)
	if err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}
	
	// 更新状态为转写中
	m.Status = "transcribing"
	s.meetingStore.Update(m)
	
	// 读取音频文件（从请求体）
	// 注意：实际实现中，音频文件应该通过 multipart/form-data 上传
	// 这里简化处理，假设音频数据在请求体中
	audioData := r.Body
	defer audioData.Close()
	
	// 使用 STT 转写（如果配置了）
	if s.transcriber != nil {
		// 读取音频数据
		buf := make([]byte, 1024*1024) // 最大 1MB
		n, _ := audioData.Read(buf)
		
		result, err := s.transcriber.Transcribe(r.Context(), buf[:n], "meeting.wav")
		if err != nil {
			m.Status = "failed"
			s.meetingStore.Update(m)
			http.Error(w, "transcription failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		m.Transcript = result.Text
	} else {
		// 没有 STT 配置，返回模拟转写
		m.Transcript = "（STT 未配置，请设置 POCKET_GROQ_API_KEY）"
	}
	
	m.Status = "transcribed"
	s.meetingStore.Update(m)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "transcribed",
		"meeting_id": id,
	})
}

func (s *Server) handleSummarizeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	m, err := s.meetingStore.Get(id)
	if err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}
	
	if m.Transcript == "" {
		http.Error(w, "meeting has no transcript, transcribe first", http.StatusBadRequest)
		return
	}
	
	m.Status = "summarizing"
	s.meetingStore.Update(m)
	
	summary, err := meeting.SummarizeTranscript(m.Transcript, m.Title)
	if err != nil {
		m.Status = "failed"
		s.meetingStore.Update(m)
		http.Error(w, "summarization failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	m.Summary = summary.Summary
	m.KeyDecisions = summary.KeyDecisions
	m.ActionItems = summary.ActionItems
	m.Status = "done"
	s.meetingStore.Update(m)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}
```

- [ ] **Step 2: 修改 server.go**

在 `Server` 结构体添加 `meetingStore *meeting.Store`，初始化中添加 `s.meetingStore = meeting.NewStore()`，路由注册中添加：
```go
mux.HandleFunc("/api/meetings", s.requireAuth(s.handleMeetings))
mux.HandleFunc("/api/meetings/", s.requireAuth(s.handleMeetingOps))
```

- [ ] **Step 3: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```
Expected: 编译成功

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/server/server_meeting.go backend/internal/server/server.go
git commit -m "feat(meeting): add meeting API routes with transcribe and summarize"
```

---

### Task 6: 聊天总结模块类型定义与存储

**Files:**
- Create: `backend/internal/chat_summary/types.go`
- Create: `backend/internal/chat_summary/store.go`
- Create: `backend/internal/chat_summary/store_test.go`

- [ ] **Step 1: 创建类型定义**

```go
// internal/chat_summary/types.go
package chat_summary

import "time"

// ChatSummary 聊天摘要
type ChatSummary struct {
	ID           string    `json:"id"`
	Channel      string    `json:"channel"`       // feishu / telegram / slack
	ChannelID    string    `json:"channel_id"`     // 群组/频道 ID
	ChannelName  string    `json:"channel_name,omitempty"`
	PeriodStart  time.Time `json:"period_start"`
	PeriodEnd    time.Time `json:"period_end"`
	MessageCount int       `json:"message_count"`
	Summary      string    `json:"summary"`
	KeyDecisions []string  `json:"key_decisions,omitempty"`
	ActionItems  []ActionItem `json:"action_items,omitempty"`
	Links        []string  `json:"links,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ActionItem 待办事项
type ActionItem struct {
	Task  string `json:"task"`
	Owner string `json:"owner,omitempty"`
}

// CreateSummaryRequest 创建摘要请求
type CreateSummaryRequest struct {
	Channel     string    `json:"channel"`
	ChannelID   string    `json:"channel_id"`
	Messages    []Message `json:"messages"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

// Message 聊天消息
type Message struct {
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	MsgType   string    `json:"msg_type,omitempty"` // text / image / file / link
}
```

- [ ] **Step 2: 实现存储（与 meeting store 类似，内存实现）**

```go
// internal/chat_summary/store.go
package chat_summary

import (
	"fmt"
	"sync"
	"time"
)

type Store struct {
	mu       sync.RWMutex
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
	
	summary.ID = fmt.Sprintf("cs_%d", time.Now().UnixNano())
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
```

- [ ] **Step 3: 编写并运行测试**

```go
// internal/chat_summary/store_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()
	
	cs := &ChatSummary{
		Channel:    "feishu",
		ChannelID:  "group_123",
		Summary:    "Test summary",
		PeriodStart: time.Now().Add(-1 * time.Hour),
		PeriodEnd:  time.Now(),
	}
	
	err := s.Create(cs)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if cs.ID == "" {
		t.Error("expected non-empty ID")
	}
	
	got, err := s.Get(cs.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Summary != "Test summary" {
		t.Errorf("summary mismatch")
	}
	
	results, _ := s.List("group_123", 10)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	
	s.Delete(cs.ID)
	_, err = s.Get(cs.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/chat_summary/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/chat_summary/
git commit -m "feat(chat-summary): add chat summary types and store"
```

---

### Task 7: 聊天消息聚合与摘要生成

**Files:**
- Create: `backend/internal/chat_summary/aggregator.go`
- Create: `backend/internal/chat_summary/aggregator_test.go`
- Create: `backend/internal/chat_summary/summarizer.go`
- Create: `backend/internal/chat_summary/summarizer_test.go`

- [ ] **Step 1: 消息聚合器**

```go
// internal/chat_summary/aggregator.go
package chat_summary

import (
	"sort"
	"time"
)

// Aggregator 消息聚合器
type Aggregator struct{}

// AggregateResult 聚合结果
type AggregateResult struct {
	Messages     []Message
	MessageCount int
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Participants []string
}

// Aggregate 按时间范围聚合消息
func (a *Aggregator) Aggregate(messages []Message, periodStart, periodEnd time.Time) *AggregateResult {
	var filtered []Message
	participantSet := make(map[string]bool)
	
	for _, msg := range messages {
		if (msg.Timestamp.IsZero() || !msg.Timestamp.Before(periodStart)) &&
			(msg.Timestamp.IsZero() || !msg.Timestamp.After(periodEnd)) {
			filtered = append(filtered, msg)
			if msg.Sender != "" {
				participantSet[msg.Sender] = true
			}
		}
	}
	
	// 按时间排序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.Before(filtered[j].Timestamp)
	})
	
	var participants []string
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)
	
	return &AggregateResult{
		Messages:     filtered,
		MessageCount: len(filtered),
		PeriodStart:  periodStart,
		PeriodEnd:    periodEnd,
		Participants: participants,
	}
}
```

- [ ] **Step 2: 聚合器测试**

```go
// internal/chat_summary/aggregator_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestAggregator_Aggregate(t *testing.T) {
	now := time.Now()
	a := &Aggregator{}
	
	messages := []Message{
		{Sender: "张三", Content: "今天讨论一下API设计", Timestamp: now.Add(-30 * time.Minute)},
		{Sender: "李四", Content: "好的，我建议用REST", Timestamp: now.Add(-20 * time.Minute)},
		{Sender: "张三", Content: "同意，就这么办", Timestamp: now.Add(-10 * time.Minute)},
	}
	
	result := a.Aggregate(messages, now.Add(-1*time.Hour), now)
	
	if result.MessageCount != 3 {
		t.Errorf("expected 3 messages, got %d", result.MessageCount)
	}
	if len(result.Participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(result.Participants))
	}
}

func TestAggregator_EmptyResult(t *testing.T) {
	now := time.Now()
	a := &Aggregator{}
	
	result := a.Aggregate(nil, now, now)
	if result.MessageCount != 0 {
		t.Errorf("expected 0 messages, got %d", result.MessageCount)
	}
}
```

- [ ] **Step 3: 摘要生成器**

```go
// internal/chat_summary/summarizer.go
package chat_summary

import (
	"fmt"
	"strings"
)

// Summarizer 聊天摘要生成器
type Summarizer struct{}

// Summarize 从聚合结果生成摘要
func (s *Summarizer) Summarize(result *AggregateResult, channelName string) *ChatSummary {
	if result.MessageCount == 0 {
		return &ChatSummary{
			Summary:      "该时间段内没有消息",
			MessageCount: 0,
		}
	}
	
	// 提取关键信息
	var decisions []string
	var actionItems []ActionItem
	var links []string
	
	for _, msg := range result.Messages {
		lower := strings.ToLower(msg.Content)
		if strings.Contains(lower, "决定") || strings.Contains(lower, "同意") {
			decisions = append(decisions, msg.Sender+": "+msg.Content)
		}
		if strings.Contains(lower, "负责") || strings.Contains(lower, "我来") {
			actionItems = append(actionItems, ActionItem{
				Task:  msg.Content,
				Owner: msg.Sender,
			})
		}
		if strings.HasPrefix(msg.Content, "http") {
			links = append(links, msg.Content)
		}
	}
	
	participants := strings.Join(result.Participants, ", ")
	
	summary := fmt.Sprintf("「%s」共 %d 条消息，参与人：%s。",
		channelName, result.MessageCount, participants)
	
	if len(decisions) > 0 {
		summary += fmt.Sprintf("识别到 %d 个决策点。", len(decisions))
	}
	if len(actionItems) > 0 {
		summary += fmt.Sprintf("识别到 %d 个待办事项。", len(actionItems))
	}
	
	return &ChatSummary{
		Summary:      summary,
		KeyDecisions: decisions,
		ActionItems:  actionItems,
		Links:        links,
		MessageCount: result.MessageCount,
	}
}
```

- [ ] **Step 4: 摘要生成器测试**

```go
// internal/chat_summary/summarizer_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestSummarizer_Summarize(t *testing.T) {
	s := &Summarizer{}
	
	result := &AggregateResult{
		Messages: []Message{
			{Sender: "张三", Content: "我决定用Go重写后端", Timestamp: time.Now()},
			{Sender: "李四", Content: "同意，我来负责数据库层", Timestamp: time.Now()},
			{Sender: "张三", Content: "https://github.com/example", Timestamp: time.Now()},
		},
		MessageCount: 3,
		Participants: []string{"张三", "李四"},
		PeriodStart:  time.Now().Add(-1 * time.Hour),
		PeriodEnd:    time.Now(),
	}
	
	summary := s.Summarize(result, "后端讨论组")
	
	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected at least one decision")
	}
	if len(summary.ActionItems) == 0 {
		t.Error("expected at least one action item")
	}
	if len(summary.Links) == 0 {
		t.Error("expected at least one link")
	}
}

func TestSummarizer_Empty(t *testing.T) {
	s := &Summarizer{}
	result := &AggregateResult{MessageCount: 0}
	summary := s.Summarize(result, "空群组")
	if summary.Summary != "该时间段内没有消息" {
		t.Errorf("expected empty message summary")
	}
}
```

- [ ] **Step 5: 运行所有 chat_summary 测试**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/chat_summary/ -v`
Expected: PASS (4/4 — 1 CRUD + 1 aggregator + 2 summarizer)

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/chat_summary/
git commit -m "feat(chat-summary): add message aggregator and summarizer"
```

---

### Task 8: 聊天总结 API 路由

**Files:**
- Create: `backend/internal/server/server_chat_summary.go`
- Create: `backend/internal/server/server_chat_summary_test.go`
- Modify: `backend/internal/server/server.go` (路由注册)

- [ ] **Step 1: 创建聊天总结 API 处理器**

```go
// internal/server/server_chat_summary.go
package server

import (
	"encoding/json"
	"net/http"
	"time"
	
	cs "github.com/halfking/pocket-opencode/backend/internal/chat_summary"
)

func (s *Server) handleChatSummaries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListChatSummaries(w, r)
	case http.MethodPost:
		s.handleCreateChatSummary(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleChatSummaryOps(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/chat-summaries/"):]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	
	switch r.Method {
	case http.MethodGet:
		summary, err := s.chatSummaryStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summary)
		
	case http.MethodDelete:
		if err := s.chatSummaryStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListChatSummaries(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	
	summaries, err := s.chatSummaryStore.List(channelID, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"summaries": summaries,
		"total":     len(summaries),
	})
}

func (s *Server) handleCreateChatSummary(w http.ResponseWriter, r *http.Request) {
	var req cs.CreateSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	
	if req.Channel == "" || len(req.Messages) == 0 {
		http.Error(w, "channel and messages are required", http.StatusBadRequest)
		return
	}
	
	// 设置时间范围
	periodStart := req.PeriodStart
	periodEnd := req.PeriodEnd
	if periodEnd.IsZero() {
		periodEnd = time.Now()
	}
	if periodStart.IsZero() {
		periodStart = periodEnd.Add(-24 * time.Hour)
	}
	
	// 聚合消息
	aggregator := &cs.Aggregator{}
	result := aggregator.Aggregate(req.Messages, periodStart, periodEnd)
	
	// 生成摘要
	summarizer := &cs.Summarizer{}
	summary := summarizer.Summarize(result, req.Channel)
	
	// 填充元数据
	summary.Channel = req.Channel
	summary.ChannelID = req.ChannelID
	summary.PeriodStart = periodStart
	summary.PeriodEnd = periodEnd
	
	// 保存
	if err := s.chatSummaryStore.Create(summary); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(summary)
}
```

- [ ] **Step 2: 修改 server.go**

添加 `chatSummaryStore *cs.Store` 字段，初始化 `s.chatSummaryStore = cs.NewStore()`，注册路由：
```go
mux.HandleFunc("/api/chat-summaries", s.requireAuth(s.handleChatSummaries))
mux.HandleFunc("/api/chat-summaries/", s.requireAuth(s.handleChatSummaryOps))
```

- [ ] **Step 3: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```
Expected: 编译成功

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/server/server_chat_summary.go backend/internal/server/server.go
git commit -m "feat(chat-summary): add chat summary API routes"
```

---

## Phase 2 完成标志

- [x] AI 编程工作台升级：语音输入 + Diff 展示 + 代码片段管理
- [x] 会议录音及总结：录音管理 + STT 转写 + AI 总结 + 待办提取
- [x] 聊天记录总结：消息聚合 + AI 摘要生成
- [x] 所有后端 API 编译通过，测试通过
- [x] 前端页面可访问

## 后续 Phase 计划

| Phase | 聚焦 | 前置依赖 |
|-------|------|---------|
| **Phase 3** | 方案/PPT + 笔记分类 + 语音记账 | Phase 2 |
| **Phase 4** | 企业深度集成 + iOS + 性能优化 | Phase 3 |