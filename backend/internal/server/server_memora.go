package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/memora"
)

// memora 相关路由（2026-11-01）：「笔记与知识库打通 memora 服务」的后台通道。
//
// 形态与既有降级一致：POCKET_MEMORA_BASE_URL 未配置 ⇒ s.memora == nil ⇒
// 三条路由都回 503 并带可读原因（前端可据此隐藏入口），而不是 500 或静默空。
//
//   - GET  /api/memora/status          连通性 + 统计（设置页/笔记页入口探针）
//   - POST /api/memora/search          混合检索（BM25+向量 RRF），body {query, topK, tags?}
//   - POST /api/memora/notes/push      把一条笔记推入记忆库；body {noteId} 或 {title, content, tags?}
//
// 归属：noteId 路径走 notesStore.GetByIDScoped（JWT 归属），永不信任 body 里的
// userId/workspaceId —— 与 /api/notes 同一条不变量。

const memoraDisabledMsg = "memora 未配置（POCKET_MEMORA_BASE_URL）"

func (s *Server) handleMemoraStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.memora == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"configured": false, "reachable": false, "error": memoraDisabledMsg,
		})
		return
	}
	ctx := r.Context()
	out := map[string]any{"configured": true}
	if err := s.memora.Health(ctx); err != nil {
		out["reachable"] = false
		out["error"] = err.Error()
		writeJSON(w, http.StatusBadGateway, out)
		return
	}
	out["reachable"] = true
	if stats, err := s.memora.Stats(ctx); err == nil {
		out["stats"] = stats
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoraSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.memora == nil {
		writeError(w, http.StatusServiceUnavailable, memoraDisabledMsg)
		return
	}
	var req struct {
		Query string   `json:"query"`
		TopK  int      `json:"topK"`
		Tags  []string `json:"tags"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	res, err := s.memora.SearchHybrid(r.Context(), req.Query, req.TopK, req.Tags)
	if err != nil {
		// 上游 4xx/不可达都按 502 透出（与 kxmemory 客户端错误语义一致）。
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleMemoraNotePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.memora == nil {
		writeError(w, http.StatusServiceUnavailable, memoraDisabledMsg)
		return
	}
	var req struct {
		NoteID  string   `json:"noteId"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	source := "inline"
	if req.NoteID != "" {
		if s.notesStore == nil {
			writeError(w, http.StatusServiceUnavailable, "notes store not configured")
			return
		}
		n, err := s.notesStore.GetByIDScoped(r.Context(), req.NoteID, uid, wsID)
		if err != nil {
			writeError(w, http.StatusNotFound, "note not found: "+err.Error())
			return
		}
		req.Title = n.Title
		body := n.BodyForLLM(20_000)
		if strings.TrimSpace(body) == "" {
			writeError(w, http.StatusUnprocessableEntity, "note has no content to push")
			return
		}
		req.Content = body
		if n.Tags != "" {
			req.Tags = append(req.Tags, parseNoteTags(n.Tags)...)
		}
		source = "pocket-note"
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "noteId or content is required")
		return
	}
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	tags = append(tags, source)
	add, err := s.memora.Add(r.Context(), memora.AddRequest{
		Content: content,
		Tags:    tags,
		Metadata: map[string]any{
			"title":       req.Title,
			"userId":      uid,
			"workspaceId": wsID,
		},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"memoraId": add.ID, "source": source,
	})
}

// parseNoteTags 把 notes.Note.Tags（JSON array 字符串）安全解成 []string；
// 非法 JSON 按无标签处理（与列表渲染的宽松口径一致）。
func parseNoteTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
