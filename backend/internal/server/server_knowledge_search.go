package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// KnowledgeSearchResult 是统一搜索结果的单条记录（docs/2026-11-14 §7.1）。
type KnowledgeSearchResult struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`       // "note" | "memory" | "email" | "meeting"
	Source     string            `json:"source"`     // "local" | "memora" | "email" | "meeting"
	Title      string            `json:"title"`
	Snippet    string            `json:"snippet"`
	Highlights []string          `json:"highlights,omitempty"`
	Notebook   *NotebookRef      `json:"notebook,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Domain     string            `json:"domain,omitempty"`
	CreatedAt  string            `json:"createdAt"`
	UpdatedAt  string            `json:"updatedAt,omitempty"`
	Score      float64           `json:"score"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// NotebookRef 是笔记本引用（轻量级）。
type NotebookRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// KnowledgeSearchResponse 是统一搜索 API 的响应（docs/2026-11-14 §7.1）。
type KnowledgeSearchResponse struct {
	Results    []KnowledgeSearchResult `json:"results"`
	Total      int                     `json:"total"`
	Limit      int                     `json:"limit"`
	Offset     int                     `json:"offset"`
	SearchTime int64                   `json:"searchTime"` // milliseconds
}

// handleKnowledgeSearch 实现统一知识搜索 API（GET /api/knowledge/search）。
//
// 查询参数（docs/2026-11-14 §7.1）：
//   - q: 搜索关键词（必填）
//   - source: 'all' | 'notes' | 'memora' | 'emails' | 'meetings'（默认 'all'）
//   - notebook: 笔记本 ID 过滤
//   - tags: 逗号分隔的标签列表
//   - domain: 'work' | 'study' | 'life' | 'idea'
//   - dateFrom: 起始日期（ISO 8601）
//   - dateTo: 结束日期（ISO 8601）
//   - limit: 返回数量（默认 50）
//   - offset: 分页偏移（默认 0）
//
// 实现策略：
//   1. 并发搜索多个源（notes + memora，emails/meetings 预留）
//   2. 结果合并、按相关性排序、去重
//   3. 应用过滤条件（notebook/tags/domain/dateRange）
//   4. 分页返回
func (s *Server) handleKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()

	// 解析查询参数
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing required parameter: q", http.StatusBadRequest)
		return
	}

	source := r.URL.Query().Get("source")
	if source == "" {
		source = "all"
	}

	notebookID := r.URL.Query().Get("notebook")
	tagsParam := r.URL.Query().Get("tags")
	domain := r.URL.Query().Get("domain")
	dateFrom := r.URL.Query().Get("dateFrom")
	dateTo := r.URL.Query().Get("dateTo")

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var tags []string
	if tagsParam != "" {
		tags = strings.Split(tagsParam, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
	}

	// 解析日期范围
	var dateFromTs, dateToTs int64
	if dateFrom != "" {
		if t, err := time.Parse(time.RFC3339, dateFrom); err == nil {
			dateFromTs = t.Unix()
		}
	}
	if dateTo != "" {
		if t, err := time.Parse(time.RFC3339, dateTo); err == nil {
			dateToTs = t.Unix()
		}
	}

	userID := r.Context().Value("user_id").(string)
	workspaceID := s.workspaceIDFromRequest(r)

	// 并发搜索多个源
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allResults []KnowledgeSearchResult

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// 1. 搜索本地笔记
	if (source == "all" || source == "notes") && s.notesStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := s.searchLocalNotes(ctx, userID, workspaceID, query, domain, notebookID, tags, limit*2)
			mu.Lock()
			allResults = append(allResults, results...)
			mu.Unlock()
		}()
	}

	// 2. 搜索 Memora 记忆库
	if (source == "all" || source == "memora") && s.memora != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := s.searchMemora(ctx, query, tags, limit*2)
			mu.Lock()
			allResults = append(allResults, results...)
			mu.Unlock()
		}()
	}

	// 3. 搜索邮件（预留）
	if (source == "all" || source == "emails") && s.emailStore != nil {
		// TODO: 实现邮件搜索（Phase 2）
		// wg.Add(1)
		// go func() {
		//     defer wg.Done()
		//     results := s.searchEmails(ctx, userID, workspaceID, query, limit*2)
		//     mu.Lock()
		//     allResults = append(allResults, results...)
		//     mu.Unlock()
		// }()
	}

	// 4. 搜索会议（预留）
	if (source == "all" || source == "meetings") && s.meetingStore != nil {
		// TODO: 实现会议搜索（Phase 2）
		// wg.Add(1)
		// go func() {
		//     defer wg.Done()
		//     results := s.searchMeetings(ctx, userID, workspaceID, query, limit*2)
		//     mu.Lock()
		//     allResults = append(allResults, results...)
		//     mu.Unlock()
		// }()
	}

	wg.Wait()

	// 应用过滤条件
	filtered := s.filterKnowledgeResults(allResults, domain, tags, dateFromTs, dateToTs)

	// 去重（基于 ID + Source）
	deduped := s.deduplicateResults(filtered)

	// 按相关性排序
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	// 分页
	total := len(deduped)
	if offset >= total {
		deduped = []KnowledgeSearchResult{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		deduped = deduped[offset:end]
	}

	searchTime := time.Since(startTime).Milliseconds()

	writeJSON(w, http.StatusOK, KnowledgeSearchResponse{
		Results:    deduped,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		SearchTime: searchTime,
	})
}

// searchLocalNotes 搜索本地笔记（使用 notesStore）。
func (s *Server) searchLocalNotes(ctx context.Context, userID, workspaceID, query, domain, notebookID string, tags []string, limit int) []KnowledgeSearchResult {
	var notesList []notes.Note
	var err error

	// 如果指定了 notebookID，按笔记本查询
	if notebookID != "" {
		notesList, err = s.notesStore.ListByNotebook(ctx, userID, workspaceID, notebookID)
	} else if domain != "" {
		notesList, err = s.notesStore.ListScoped(ctx, userID, workspaceID, domain)
	} else {
		notesList, err = s.notesStore.ListScoped(ctx, userID, workspaceID, "")
	}

	if err != nil {
		log.Printf("[knowledge-search] local notes search failed: %v", err)
		return nil
	}

	// 简单的全文匹配（生产环境应使用 FTS5）
	var results []KnowledgeSearchResult
	queryLower := strings.ToLower(query)

	for _, note := range notesList {
		// 计算相关性得分
		score := s.calculateNoteRelevance(note, queryLower)
		if score <= 0 {
			continue
		}

		// 解析标签（Tags 是 JSON 字符串）
		var noteTags []string
		if note.Tags != "" {
			_ = json.Unmarshal([]byte(note.Tags), &noteTags)
		}

		// 标签过滤
		if len(tags) > 0 && !s.matchTags(noteTags, tags) {
			continue
		}

		// 构造结果
		result := KnowledgeSearchResult{
			ID:        note.ID,
			Type:      "note",
			Source:    "local",
			Title:     note.Title,
			Snippet:   note.Snippet,
			Tags:      noteTags,
			Domain:    note.Domain,
			CreatedAt: time.Unix(note.CreatedAt, 0).Format(time.RFC3339),
			Score:     score,
		}

		if note.UpdatedAt > 0 {
			result.UpdatedAt = time.Unix(note.UpdatedAt, 0).Format(time.RFC3339)
		}

		// 添加笔记本信息
		if note.NotebookID != "" {
			result.Notebook = &NotebookRef{
				ID: note.NotebookID,
				// Name 需要从 notebookStore 查询（优化：批量查询或缓存）
			}
		}

		// 生成高亮片段
		result.Highlights = s.generateHighlights(note.Snippet, queryLower)

		results = append(results, result)

		if len(results) >= limit {
			break
		}
	}

	return results
}

// searchMemora 搜索 Memora 记忆库。
func (s *Server) searchMemora(ctx context.Context, query string, tags []string, limit int) []KnowledgeSearchResult {
	resp, err := s.memora.SearchHybrid(ctx, query, limit, tags)
	if err != nil {
		log.Printf("[knowledge-search] memora search failed: %v", err)
		return nil
	}

	if !resp.OK {
		log.Printf("[knowledge-search] memora search returned ok=false")
		return nil
	}

	var results []KnowledgeSearchResult
	for _, item := range resp.Results {
		result := KnowledgeSearchResult{
			ID:      item.ID,
			Type:    "memory",
			Source:  "memora",
			Title:   s.extractTitle(item.Memory),
			Snippet: item.Memory,
			Score:   item.FinalScore,
		}

		// Memora 结果没有 tags，可以从 metadata 提取
		// TODO: 增强 memora client 返回更多元数据

		results = append(results, result)
	}

	return results
}

// calculateNoteRelevance 计算笔记的相关性得分（简单实现）。
func (s *Server) calculateNoteRelevance(note notes.Note, queryLower string) float64 {
	var score float64

	titleLower := strings.ToLower(note.Title)
	contentLower := strings.ToLower(note.Snippet)

	// 标题匹配权重更高
	if strings.Contains(titleLower, queryLower) {
		score += 1.0
	}

	// 内容匹配
	if strings.Contains(contentLower, queryLower) {
		score += 0.5
	}

	// 标签匹配（Tags 是 JSON 字符串）
	if note.Tags != "" {
		var noteTags []string
		if err := json.Unmarshal([]byte(note.Tags), &noteTags); err == nil {
			for _, tag := range noteTags {
				if strings.Contains(strings.ToLower(tag), queryLower) {
					score += 0.3
				}
			}
		}
	}

	return score
}

// matchTags 检查笔记标签是否匹配查询标签（AND 语义）。
func (s *Server) matchTags(noteTags, queryTags []string) bool {
	if len(queryTags) == 0 {
		return true
	}

	noteTagSet := make(map[string]bool)
	for _, tag := range noteTags {
		noteTagSet[strings.ToLower(tag)] = true
	}

	for _, queryTag := range queryTags {
		if !noteTagSet[strings.ToLower(queryTag)] {
			return false
		}
	}

	return true
}

// filterKnowledgeResults 应用过滤条件。
func (s *Server) filterKnowledgeResults(results []KnowledgeSearchResult, domain string, tags []string, dateFromTs, dateToTs int64) []KnowledgeSearchResult {
	var filtered []KnowledgeSearchResult

	for _, result := range results {
		// Domain 过滤
		if domain != "" && result.Domain != domain {
			continue
		}

		// Tags 过滤
		if len(tags) > 0 && !s.matchTags(result.Tags, tags) {
			continue
		}

		// 日期范围过滤
		if dateFromTs > 0 || dateToTs > 0 {
			createdAt, err := time.Parse(time.RFC3339, result.CreatedAt)
			if err != nil {
				continue
			}
			ts := createdAt.Unix()

			if dateFromTs > 0 && ts < dateFromTs {
				continue
			}
			if dateToTs > 0 && ts > dateToTs {
				continue
			}
		}

		filtered = append(filtered, result)
	}

	return filtered
}

// deduplicateResults 去重（基于 ID + Source）。
func (s *Server) deduplicateResults(results []KnowledgeSearchResult) []KnowledgeSearchResult {
	seen := make(map[string]bool)
	var deduped []KnowledgeSearchResult

	for _, result := range results {
		key := result.Source + ":" + result.ID
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, result)
		}
	}

	return deduped
}

// generateHighlights 生成高亮片段。
func (s *Server) generateHighlights(snippet, query string) []string {
	if snippet == "" || query == "" {
		return nil
	}

	snippetLower := strings.ToLower(snippet)
	index := strings.Index(snippetLower, query)
	if index == -1 {
		return nil
	}

	// 提取匹配位置前后的上下文
	start := index
	if start > 30 {
		start = index - 30
	} else {
		start = 0
	}

	end := index + len(query) + 50
	if end > len(snippet) {
		end = len(snippet)
	}

	fragment := snippet[start:end]
	highlighted := strings.Replace(fragment, snippet[index:index+len(query)], "<em>"+snippet[index:index+len(query)]+"</em>", 1)

	return []string{highlighted}
}

// extractTitle 从 memory 内容中提取标题（取第一行或前 50 个字符）。
func (s *Server) extractTitle(memory string) string {
	lines := strings.Split(memory, "\n")
	if len(lines) > 0 && lines[0] != "" {
		title := lines[0]
		if len(title) > 50 {
			return title[:50] + "..."
		}
		return title
	}

	if len(memory) > 50 {
		return memory[:50] + "..."
	}
	return memory
}

// handleKnowledgeSearchSaved 处理保存的搜索（预留）。
func (s *Server) handleKnowledgeSearchSaved(w http.ResponseWriter, r *http.Request) {
	// TODO: 实现保存的搜索功能（Phase 3）
	switch r.Method {
	case http.MethodGet:
		// 获取保存的搜索列表
		writeJSON(w, http.StatusOK, map[string]any{
			"searches": []any{},
		})
	case http.MethodPost:
		// 保存搜索
		var req struct {
			Name    string          `json:"name"`
			Query   string          `json:"query"`
			Filters json.RawMessage `json:"filters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":   "saved-search-1",
			"name": req.Name,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
