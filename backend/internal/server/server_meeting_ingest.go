package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// handleMeetings dispatches GET (list) / POST (create) on /api/meetings.
func (s *Server) handleMeetings(w http.ResponseWriter, r *http.Request) {
	if s.meetingStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleListMeetings(w, r)
	case http.MethodPost:
		s.handleCreateMeeting(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleListMeetings returns the workspace-scoped list with a total counter so
// the mobile UI can render pagination without a second round-trip.
//
// ⚠ §936：**total 现在是「作用域内的真实总条数」**，不是本页行数。
//
//	修复前 total = len(list)，而 list 已被 `LIMIT 500` 截断 ⇒
//	用户有 600 场会议时 total 恒为 500 ⇒ 移动端分页器显示「共 500 场」，
//	第 501 场之后**永远取不到** —— 而且当时**根本没有** limit/offset 参数，
//	⇒ ⇒ 本函数上面那句注释承诺的「一个来回渲染分页」在实现上**不可能成立**。
//	⇒ ⇒ 现在 total 来自一条独立的 COUNT（与 since 同一过滤条件），
//	并新增可选的 limit / offset 让「分页」这句话真的能兑现。
//
// 增量同步：可选 since（Unix 秒或毫秒）只返回 UpdatedAt 更晚的行，并附带
// deletedIds 墓碑与 serverTimeMs，供客户端做无刷新差异合并。
// ⚠ since 现在**下推进 SQL**（先过滤后 LIMIT）⇒ 一场「创建于 500 名之外
// 但刚被更新」的会议不再永远同步不到（§936 的 D2）。
func (s *Server) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	query := r.URL.Query()
	opts := meeting.ListOptions{
		Since:  meeting.ParseListSince(query.Get("since")),
		Limit:  meeting.ParseListInt(query.Get("limit"), meeting.ListDefaultLimit),
		Offset: meeting.ParseListInt(query.Get("offset"), 0),
	}
	list, total, err := s.meetingStore.ListScoped(uid, workspaceID, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list meetings: "+err.Error())
		return
	}
	if list == nil {
		list = []*meeting.Meeting{}
	}
	resp := map[string]any{
		"meetings": list,
		// ⚠ total 是**作用域内的总条数**（过滤后、分页前），不是 len(list)。
		"total":        total,
		"serverTimeMs": time.Now().UnixMilli(),
	}
	if !opts.Since.IsZero() {
		resp["deletedIds"] = s.meetingStore.DeletedIDsSince(uid, workspaceID, opts.Since)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCreateMeeting decodes the request body and stores a new meeting in the
// caller's workspace scope.
func (s *Server) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)
	var req meeting.CreateMeetingRequest
	// ★ 2026-10-07 开启 DisallowUnknownFields。
	//
	// 为什么现在才加：之前这个 handler 用的是 `json.NewDecoder(r.Body).Decode(&req)`，
	// 而 req 当时**只有 Title 一个字段**。前端 syncMeetingMetadata 发的是 10 个字段
	// （id/location/participants/startedAt/durationMs/summary/refinedTranscript/noteId/status），
	// Go 的 Decode 默认**静默忽略**未知字段 ⇒ 9/10 的同步数据被丢弃，
	// 客户端重试一次就多一行会议（id 也被丢，服务端无条件新生成）。
	//
	// 本仓已有这个更严的写法（server_calendar.go:41、scheduled_task_handler.go:244、
	// mobile_endpoint_scope.go:206），此处补齐是为了让**下一次契约再错配时立刻 400**，
	// 而不是继续静默吞数据。CreateMeetingRequest 现已覆盖客户端全部 10 个字段。
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "未命名会议"
	}

	// ★ §105 幂等：同一个客户端 ID 再同步一次，**更新同一行**而不是插第二行。
	//
	// 为什么会来第二次：一次「停止录音」里会串两条 POST
	// （`useSessionLiveRecord.stop()`：fire-and-forget 的 syncMeetingMetadata，
	// 然后 await finalizeRecording → ingestMeetingArtifacts → syncMeeting）。
	// 本端点此前是纯 INSERT ⇒ 一次录音在云端列表里留两行同名会议。
	//
	// ⚠ 作用域检查是**同作用域**的（GetScoped 带 owner+workspace）而不是
	//   「ID 存在就更新」：那是跨用户覆盖。异作用域的同号由 store 的唯一冲突
	//   重试退回服务端发号，绝不写别人的行。
	//
	// ⚠ 这不是「覆盖写」，是**同步同一份事实**：两次 POST 的 title 同源
	//   （都是 meeting.title），所以这里只改 title，不碰任何另一路才写的字段。
	if id := strings.TrimSpace(req.ID); id != "" {
		if existing, err := s.meetingStore.GetScoped(id, uid, workspaceID); err == nil {
			existing.Title = req.Title
			if err := s.meetingStore.UpdateScoped(existing, uid, workspaceID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update meeting: "+err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, existing)
			s.wsHub.Broadcast("meeting.synced", map[string]string{"meetingId": existing.ID, "userId": uid})
			return
		}
	}

	m, err := s.meetingStore.CreateScoped(req, uid, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create meeting: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
	s.wsHub.Broadcast("meeting.synced", map[string]string{"meetingId": m.ID, "userId": uid})
}

// finalizeMeetingRefine 精翻完成后：笔记入库 + 待办任务 + 更新 meeting 缓存
// workspaceID must be the caller's authenticated workspace (from
// workspaceIDFromRequest); passing "" degrades to the legacy "default"
// workspace for the non-S0 single-tenant callsites only.
func (s *Server) finalizeMeetingRefine(
	ctx context.Context,
	uid, workspaceID, meetingID string,
	result map[string]any,
	meta meetingMetaIn,
) map[string]any {
	noteID := strVal(result["note_id"])
	if noteID == "" {
		noteID = strVal(result["noteId"])
	}
	refined := strVal(result["refined_transcript"])
	if refined == "" {
		refined = strVal(result["refinedTranscript"])
	}

	if noteID == "" && refined != "" {
		if id, err := s.ingestMeetingNote(ctx, uid, meetingID, refined, meta); err == nil {
			noteID = id
		} else {
			log.Printf("[meeting] note ingest failed %s: %v", meetingID, err)
		}
	}

	todos := extractTodos(result)
	tasksCreated := s.ingestMeetingTasks(ctx, uid, meetingID, todos)

	if noteID != "" {
		result["note_id"] = noteID
	}
	result["tasks_created"] = tasksCreated

	// 更新 PG meeting 缓存（严格限定在调用方的 workspace 作用域内）
	if s.meetingStore != nil {
		uidForStore := uid
		if uidForStore == "" {
			uidForStore = "local"
		}
		workspaceIDForStore := workspaceID
		if workspaceIDForStore == "" {
			// Legacy single-tenant callsites that never carried a workspace.
			workspaceIDForStore = "default"
		}
		if existing, err := s.meetingStore.GetScoped(meetingID, uidForStore, workspaceIDForStore); err == nil {
			if meta.Title != "" {
				existing.Title = meta.Title
			}
			// ⚠ 2026-10-07：降级或被拦下时**不能**标 refined。
			//
			//   前端列表徽章把 refined 渲染成「已精翻」。而这里原先是无条件赋值，
			//   于是 refine_fallback（§17 解析失败）与 refine_rejected（§35 名单污染
			//   被 guardRefineResult 拦下）两种降级都会留下「已精翻」。
			//   用户点进去看到的是刚说过的原话。
			//
			//   注意：这条不能只改前端 —— meetingStore 里缓存的 status
			//   会覆盖前端本地的判断，列表读的是它。
			existing.Status = "refined"
			if refineDegraded(result) {
				existing.Status = "refine-failed"
			}
			if err := s.meetingStore.UpdateScoped(existing, uidForStore, workspaceIDForStore); err != nil {
				log.Printf("[meeting] update after refine %s: %v", meetingID, err)
			}
		} else {
			// No stub creation: fabricating a row here would land it in the wrong
			// workspace and mask a genuine cross-workspace/missing-meeting error.
			log.Printf("[meeting] refine target %s not found in workspace %s (owner=%s): %v",
				meetingID, workspaceIDForStore, uidForStore, err)
		}
	}

	s.wsHub.Broadcast("meeting.refined", map[string]any{
		"meetingId": meetingID, "noteId": noteID, "tasksCreated": tasksCreated,
	})
	return result
}

func (s *Server) ingestMeetingNote(
	ctx context.Context, uid, meetingID, content string, meta meetingMetaIn,
) (string, error) {
	if s.notesStore == nil {
		return "", fmt.Errorf("notes store not configured")
	}
	title := meta.Title
	if title == "" {
		title = "会议纪要"
	}
	now := time.Now().Unix()
	n := notes.Note{
		ID:             randomID("note"),
		UserID:         uid,
		Title:          title,
		Snippet:        truncateStr(content, 500),
		ContentType:    "voice",
		Domain:         "work",
		CreatedByVoice: true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.notesStore.Upsert(ctx, &n); err != nil {
		return "", err
	}
	s.wsHub.Broadcast("note.created", &n)
	go s.classifyNoteAsync(n)
	log.Printf("[meeting] created note %s from meeting %s", n.ID, meetingID)
	return n.ID, nil
}

func (s *Server) ingestMeetingTasks(ctx context.Context, uid, meetingID string, todos []todoItem) int {
	if s.taskStore == nil || len(todos) == 0 {
		return 0
	}
	created := 0
	for _, td := range todos {
		if strings.TrimSpace(td.Text) == "" {
			continue
		}
		t := &task.Task{
			ID:          randomID("task"),
			Title:       td.Text,
			Description: fmt.Sprintf("来自会议 %s", meetingID),
			Status:      "pending",
			Priority:    mapTodoPriority(td.Priority),
			Source:      "meeting",
		}
		if err := s.taskStore.CreateTask(ctx, t); err != nil {
			log.Printf("[meeting] create task failed: %v", err)
			continue
		}
		created++
		s.wsHub.Broadcast("task.created", t)
	}
	if created > 0 {
		log.Printf("[meeting] created %d tasks from meeting %s (user=%s)", created, meetingID, uid)
	}
	return created
}

type todoItem struct {
	Text     string
	Priority string
	Due      string
}

func extractTodos(result map[string]any) []todoItem {
	var out []todoItem
	for _, key := range []string{"todos", "action_items"} {
		raw, ok := result[key]
		if !ok {
			continue
		}
		arr, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, item := range arr {
			switch v := item.(type) {
			case string:
				if v != "" {
					out = append(out, todoItem{Text: v})
				}
			case map[string]any:
				text := strVal(v["text"])
				if text == "" {
					continue
				}
				out = append(out, todoItem{
					Text:     text,
					Priority: strVal(v["priority"]),
					Due:      strVal(v["due"]),
				})
			}
		}
	}
	// structured_minutes.action_items
	if sm, ok := result["structured_minutes"].(map[string]any); ok {
		if arr, ok := sm["action_items"].([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					text := strVal(m["text"])
					if text != "" {
						out = append(out, todoItem{Text: text, Priority: strVal(m["priority"])})
					}
				}
			}
		}
	}
	return out
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func mapTodoPriority(p string) string {
	switch strings.ToLower(p) {
	case "urgent", "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

// kxmemory refine 响应转 map 并 ingest
func (s *Server) finalizeKxRefine(
	ctx context.Context, uid, workspaceID, meetingID string,
	resp *kxmemory.MeetingRefineResponse, meta meetingMetaIn,
) map[string]any {
	result := map[string]any{
		"refined_transcript": resp.RefinedTranscript,
		"translations":       resp.Translations,
		"structured_minutes": resp.StructuredMinutes,
		"todos":              resp.Todos,
	}
	if resp.NoteID != "" {
		result["note_id"] = resp.NoteID
	}
	return s.finalizeMeetingRefine(ctx, uid, workspaceID, meetingID, result, meta)
}

// refineDegraded 判断这次精校是不是「看起来成功、其实没有」。
//
// 两种降级都会带 refine_fallback=true，但**原因不同**，对外状态只需要知道
// 「没精成」——具体原因由 refine_rejected 单独透传给前端说清楚（§36.1）。
func refineDegraded(result map[string]any) bool {
	fb, _ := result["refine_fallback"].(bool)
	rj, _ := result["refine_rejected"].(bool)
	return fb || rj
}
