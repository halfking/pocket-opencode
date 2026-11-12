package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/memora"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// memora 相关路由（2026-11-01）：「笔记与知识库打通 memora 服务」的后台通道。
//
// 形态与既有降级一致：POCKET_MEMORA_BASE_URL 未配置 ⇒ s.memora == nil ⇒
// 三条路由都回 503 并带可读原因（前端可据此隐藏入口），而不是 500 或静默空。
//
//   - GET  /api/memora/status          连通性 + 统计（设置页/笔记页入口探针）
//   - POST /api/memora/search          混合检索（BM25+向量 RRF），body {query, topK, tags?}
//   - POST /api/memora/notes/push      把一条笔记推入记忆库；body {noteId} 或 {title, content, tags?}
//   - POST /api/memora/notes/sync      批量推送（notebook 面，幂等，2026-11-13 A5）
//   - GET  /api/memora/notes/search    notebook 笔记检索转发（2026-11-13 A5）
//
// 归属：noteId 路径走 notesStore.GetByIDScoped（JWT 归属），永不信任 body 里的
// userId/workspaceId —— 与 /api/notes 同一条不变量。
//
// notebook 面（/api/v2/notebook/*）契约源：memora internal/notebook/handler.go
//（worktree 6dc951d7）。登记的契约差异：memora POST /notes **不消费**
// Idempotency-Key（handler.go 无该逻辑），任务包「幂等按 client 生成
// Idempotency-Key」落为「天然重试安全」实现——每条笔记打上
// sync:openpocket:note:<noteId> 定位 tag 与 sync:openpocket:hash:<sha16> 内容
// 版本 tag，重推先按 tag 找旧推送：哈希同 ⇒ skip；哈希异 ⇒ PUT 更新（If-Match
// CAS）；无旧推送 ⇒ POST 创建。进程重启、重复点击、并发重试都不产生重复笔记。

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

// ─── notebook 面（/api/v2/notebook/* 转发，2026-11-13 A5 thin integration）────

// notebook 幂等 tag 约定（见文件头「登记的契约差异」）。
const (
	memoraSyncSourceTag = "openpocket"
	// memoraSyncChangeReason 是源标注：memora 侧历史/审计可读。
	memoraSyncChangeReason = "sync:openpocket"
	// memoraSyncBatchLimit 单次 sync 的笔记数上限。每条 1-3 个上游请求
	//（tag 定位 + POST/PUT），上限同时挡住「全量误点」的放大系数。
	memoraSyncBatchLimit = 100
	// memoraSyncContentRunes 单条笔记正文的 rune 截断，与 /api/memora/notes/push
	// 的 BodyForLLM(20_000) 同一纪律。
	memoraSyncContentRunes = 20_000
)

// handleMemoraNoteItem 转发单条详情（GET /api/memora/notes/{id}）。
//
// 为什么多这一条：memora 的列表行**不含正文**（NoteListItem 无 plain_text），
// 前端「云端命中展开正文」需要详情；薄转发一条 GET，不做缓存。
// ServeMux 精确路径（/api/memora/notes/push、/sync、/search）比本子树更长，
// 优先命中，不会落到这里。
func (s *Server) handleMemoraNoteItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.memora == nil {
		writeError(w, http.StatusServiceUnavailable, memoraDisabledMsg)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/memora/notes/")
	id = strings.Trim(id, "/")
	if id == "" || id == "sync" || id == "search" || id == "push" {
		writeError(w, http.StatusNotFound, "note id is required")
		return
	}
	note, err := s.memora.GetNote(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            note.ID,
		"kb_id":         note.KBID,
		"title":         note.Title,
		"lifecycle":     note.Lifecycle,
		"revision":      note.Revision,
		"html":          note.HTML,
		"plain_text":    note.PlainText,
		"tags":          note.Tags,
		"change_reason": note.ChangeReason,
		"created_at":    note.CreatedAt,
		"updated_at":    note.UpdatedAt,
		"etag":          note.Etag,
		"removed":       note.Removed,
		"source":        "memora",
	})
}

// handleMemoraNoteSearch 转发 notebook 检索（GET /api/memora/notes/search）。
//
// query: q / tag / lifecycle / kb_id / page / limit（limit 上限 200，memora
// 侧校验，这里提前裁剪避免一次注定 400 的转发）。每条 item 附加
// source:"memora"，前端据此标注来源。
func (s *Server) handleMemoraNoteSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.memora == nil {
		writeError(w, http.StatusServiceUnavailable, memoraDisabledMsg)
		return
	}
	q := r.URL.Query()
	limit := 0
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > 200 {
		limit = 200
	}
	page := 0
	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "page must be a positive integer")
			return
		}
		page = parsed
	}
	res, err := s.memora.SearchNotes(r.Context(), memora.NotebookQuery{
		Q:         q.Get("q"),
		Tag:       q.Get("tag"),
		Lifecycle: q.Get("lifecycle"),
		KBID:      q.Get("kb_id"),
		Page:      page,
		Limit:     limit,
	})
	if err != nil {
		// 上游 4xx/不可达都按 502 透出（与 /api/memora/search 同一语义）。
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, map[string]any{
			"id":              it.ID,
			"kb_id":           it.KBID,
			"title":           it.Title,
			"lifecycle":       it.Lifecycle,
			"current_version": it.CurrentVersion,
			"tags":            it.Tags,
			"created_at":      it.CreatedAt,
			"updated_at":      it.UpdatedAt,
			"source":          "memora",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": res.Total})
}

// memoraSyncRequest 是 POST /api/memora/notes/sync 的请求体。noteIds 与 all
// 二选一：noteIds 精确推送（勾选若干条），all=true 推送当前 workspace 的
// 手记（domain 可再筛，limit ≤ memoraSyncBatchLimit）。
type memoraSyncRequest struct {
	NoteIDs []string `json:"noteIds"`
	All     bool     `json:"all"`
	Domain  string   `json:"domain"`
	Limit   int      `json:"limit"`
}

// memoraSyncResult 是单条笔记的同步结论。
type memoraSyncResult struct {
	NoteID   string `json:"noteId"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status"` // created | updated | skipped | failed
	MemoraID string `json:"memoraId,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleMemoraNoteSync 批量推送 openpocket 笔记到 memora notebook。
//
// 幂等（天然重试安全，见文件头）：每条笔记先按 tag=sync:openpocket:note:<id>
// 找旧推送 —— 内容哈希同 ⇒ skipped；异 ⇒ PUT（If-Match CAS）；无 ⇒ POST。
// 单条失败不阻断其余，逐条结果随响应返回。
func (s *Server) handleMemoraNoteSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.memora == nil {
		writeError(w, http.StatusServiceUnavailable, memoraDisabledMsg)
		return
	}
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	var req memoraSyncRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	refs, resolveErr := s.memoraSyncResolveNotes(r, uid, wsID, req)
	if resolveErr != nil {
		writeError(w, http.StatusBadRequest, resolveErr.Error())
		return
	}
	if len(refs) == 0 {
		writeError(w, http.StatusBadRequest, "no notes to sync (noteIds empty or all=true with no notes)")
		return
	}

	results := make([]memoraSyncResult, 0, len(refs))
	summary := map[string]int{"created": 0, "updated": 0, "skipped": 0, "failed": 0}
	for _, ref := range refs {
		status, memID, syncErr := s.memoraSyncOne(r.Context(), ref)
		res := memoraSyncResult{NoteID: ref.id, Status: status, MemoraID: memID}
		if ref.note != nil {
			res.Title = ref.note.Title
		}
		if syncErr != nil {
			res.Error = syncErr.Error()
		}
		summary[status]++
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":   len(results),
		"created": summary["created"],
		"updated": summary["updated"],
		"skipped": summary["skipped"],
		"failed":  summary["failed"],
		"results": results,
	})
}

// memoraSyncResolveNotes 按 noteIds / all 取出待同步笔记（scoped 归属）。
// 返回切片与请求一一对应：noteRef.note == nil 表示「请求了但服务端 store
// 查不到/不属于调用方」，后续以 failed 占位逐条出现在响应里，而不是整批 4xx。
func (s *Server) memoraSyncResolveNotes(r *http.Request, uid, wsID string, req memoraSyncRequest) ([]noteRef, error) {
	ctx := r.Context()
	if len(req.NoteIDs) > 0 {
		if len(req.NoteIDs) > memoraSyncBatchLimit {
			return nil, fmt.Errorf("too many noteIds (max %d per sync)", memoraSyncBatchLimit)
		}
		full := make([]noteRef, 0, len(req.NoteIDs))
		for _, id := range req.NoteIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			ref := noteRef{id: id}
			if n, err := s.notesStore.GetByIDScoped(ctx, id, uid, wsID); err == nil {
				ref.note = n
			}
			full = append(full, ref)
		}
		return full, nil
	}
	if !req.All {
		return nil, fmt.Errorf("noteIds or all is required")
	}
	limit := req.Limit
	if limit <= 0 || limit > memoraSyncBatchLimit {
		limit = memoraSyncBatchLimit
	}
	list, err := s.notesStore.ListScoped(ctx, uid, wsID, req.Domain)
	if err != nil {
		return nil, fmt.Errorf("list notes: %s", err.Error())
	}
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]noteRef, 0, len(list))
	for i := range list {
		out = append(out, noteRef{id: list[i].ID, note: &list[i]})
	}
	return out, nil
}

// noteRef 是 sync 队列里的一个条目：note == nil 表示「请求了但服务端 store
// 查不到」（failed 占位），id 恒有值。
type noteRef struct {
	id   string
	note *notes.Note
}

// memoraSyncOne 推送/更新/跳过单条。返回 (status, memoraID, err)。
func (s *Server) memoraSyncOne(ctx context.Context, n noteRef) (string, string, error) {
	if n.note == nil {
		return "failed", n.id, fmt.Errorf("note not found on server")
	}
	content := strings.TrimSpace(n.note.BodyForLLM(memoraSyncContentRunes))
	if content == "" {
		return "failed", n.id, fmt.Errorf("note has no content to push")
	}
	title := strings.TrimSpace(n.note.Title)
	if title == "" {
		// 与前端列表同口径：无标题笔记用正文首行兜底，memora 侧不留「无标题」。
		title = firstLine(content, 60)
	}
	hash := memoraSyncHash(title, content)
	noteTag := memoraSyncNoteTag(n.id)
	hashTag := memoraSyncHashTag(hash)
	payload := memora.PushNoteRequest{
		Title:        title,
		HTML:         notePlainToHTML(content),
		Tags:         append([]string{memoraSyncSourceTag, noteTag, hashTag}, parseNoteTags(n.note.Tags)...),
		ChangeReason: memoraSyncChangeReason,
	}
	if ts := n.note.UpdatedAt; ts > 0 {
		payload.SourceUpdatedAt = time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}

	// 1) tag 定位旧推送（精确匹配，最多回一条——noteTag 全局唯一）。
	found, err := s.memora.SearchNotes(ctx, memora.NotebookQuery{Tag: noteTag, Limit: 10})
	if err != nil {
		return "failed", n.id, fmt.Errorf("locate previous push: %s", err.Error())
	}
	if len(found.Items) > 0 {
		old := found.Items[0]
		if hasString(old.Tags, hashTag) {
			return "skipped", old.ID, nil
		}
		// 2) 内容有变 ⇒ 就地更新（CAS：If-Match = 列表行的 current_version）。
		upd, err := s.memora.UpdateNote(ctx, old.ID, old.CurrentVersion, payload)
		if err != nil {
			return "failed", old.ID, err
		}
		return "updated", upd.ID, nil
	}
	// 3) 无旧推送 ⇒ 创建。
	created, err := s.memora.PushNote(ctx, payload)
	if err != nil {
		return "failed", n.id, err
	}
	return "created", created.ID, nil
}

// memoraSyncHash 内容指纹：sha256(title \x00 content)。title 入 hash 的原因：
// memora 列表行有 title 无正文，标题改动也该触发更新。
func memoraSyncHash(title, content string) string {
	h := sha256.Sum256([]byte(title + "\x00" + content))
	return hex.EncodeToString(h[:])
}

func memoraSyncNoteTag(noteID string) string { return "sync:openpocket:note:" + noteID }

// memoraSyncHashTag 只取 16 hex（64 bit）——tag 是给 memora 侧 exact-match 过滤
// 与碰撞概率「够低」用的，不是防攻击指纹，不需要整串 64 字符塞进 tags。
func memoraSyncHashTag(hash string) string {
	if len(hash) > 16 {
		hash = hash[:16]
	}
	return "sync:openpocket:hash:" + hash
}

func hasString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// firstLine 取首行非空文本并按 rune 截断，用作无标题笔记的标题兜底。
func firstLine(s string, maxRunes int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > maxRunes {
			return string(runes[:maxRunes]) + "…"
		}
		return line
	}
	return ""
}

// notePlainToHTML 把纯文本笔记转成 memora 标签白名单内的受限 HTML：
// 段落（空行分隔）各包一层 <p>，文本 html.EscapeString——**不引入**白名单外
// 的任何标签（p/strong/em/ul/ol/li/h1-h6/a 之外一概没有），用户的笔记正文
// 原样呈现，不做 markdown 猜测（猜错的白名单标签比没有标签更糟）。
func notePlainToHTML(content string) string {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	var b strings.Builder
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(p))
		b.WriteString("</p>")
	}
	if b.Len() == 0 {
		// 全空白正文不该走到这里（调用方已拦截），兜底一个空段保持合法。
		return "<p></p>"
	}
	return b.String()
}
