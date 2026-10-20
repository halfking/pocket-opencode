package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
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
			// ⚠ §937（D2）：只改 title ⇒ **定向更新**，不重写整行（不碰 transcript）。
			//
			//	修复前：UpdateScoped 无条件重写 transcript 列 ⇒ 一场长会议每同步
			//	一次就把**整份转写**重写一遍 ⇒ 写放大 ∝ 时长²；且「读-改-写整行」
			//	会把并发写入的新转写覆盖回旧值。
			//
			//	⚠ 这里**仍然要读整行**：响应体就是这一整条会议（含 transcript）⇒
			//	「详情/列表要不要带全文」是**响应形状 + 仓外移动端兼容**问题
			//	（§936 的 D4 / 简报第 160 条 ①）⇒ 不在技术层自决。
			//	⇒ 本轮只把**写**那一半从 O(全文) 降到 O(1)。
			title := req.Title
			updatedAt, err := s.meetingStore.UpdateMetaScoped(id, uid, workspaceID, &title, nil)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update meeting: "+err.Error())
				return
			}
			existing.Title = title
			existing.UpdatedAt = updatedAt
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

	// ★ §938：落点（租户 + 归属）**在最开始就定好**。
	//
	//	修复前这条链把 workspace / owner 整个丢了 ⇒ 任务落进遗留单租户桶
	//	"default" 且没有归属 ⇒ 用户在自己的 workspace 里**看不见**这些待办，
	//	点进去也**点不开**（CanReadWorkItem 对创建者本人为 false）。
	//
	//	⚠ 这里的回落与 meetingStore 那一半**必须同口径**：
	//	  同一个 workspace 归一 ⇒ 会议缓存与会议待办落在同一个租户里。
	uidForStore := uid
	if uidForStore == "" {
		uidForStore = "local"
	}
	workspaceIDForStore := workspaceID
	if workspaceIDForStore == "" {
		// Legacy single-tenant callsites that never carried a workspace.
		workspaceIDForStore = "default"
	}

	// ★ §939：笔记的落点与**待办同一个租户**（同一份归一口径）。
	//
	//	修复前这条链建笔记时**根本不传 workspace** ⇒ notes 行落在
	//	workspace_id='' ⇒ 所有作用域读路径都查不到它 ⇒
	//	「会议 → 笔记」这条链在服务端是**断的**。
	noteTarget := newMeetingNoteTarget(uidForStore, workspaceIDForStore, meetingID)

	if noteID == "" && refined != "" {
		if id, err := s.ingestMeetingNote(ctx, noteTarget, refined, meta); err == nil {
			noteID = id
		} else {
			log.Printf("[meeting] note ingest failed %s: %v", meetingID, err)
		}
	}

	todos := extractTodos(result)
	tasksCreated := s.ingestMeetingTasks(ctx, meetingTodoTarget{
		WorkspaceID: workspaceIDForStore,
		OwnerID:     uidForStore,
		MeetingID:   meetingID,
	}, todos)

	if noteID != "" {
		result["note_id"] = noteID
	}
	result["tasks_created"] = tasksCreated

	// 更新 PG meeting 缓存（严格限定在调用方的 workspace 作用域内）
	if s.meetingStore != nil {
		// ⚠⚠ §937（D2）：**定向更新** title/status —— 不读、也不写 transcript。
		//
		//	修复前：GetScoped（读**整行**，含整份转写）+ UpdateScoped（写**整行**，
		//	含整份转写）⇒ 一场长会议每精校一块就把**整份转写**重写一遍
		//	⇒ ⇒ 写放大 ∝ 时长²；而且「读-改-写整行」在两步之间若有人并发
		//	写入了新转写，会被这一轮**覆盖回旧值**（lost update）。
		//
		//	⇒ 现在只写两列 ⇒ 成本与会议长度**无关**，且不存在覆盖窗口。
		var titlePtr *string
		if meta.Title != "" {
			titlePtr = &meta.Title
		}
		status := "refined"
		if refineDegraded(result) {
			status = "refine-failed"
		}
		if _, err := s.meetingStore.UpdateMetaScoped(
			meetingID, uidForStore, workspaceIDForStore, titlePtr, &status); err != nil {
			if errors.Is(err, meeting.ErrMeetingNotFound) {
				// No stub creation: fabricating a row here would land it in the wrong
				// workspace and mask a genuine cross-workspace/missing-meeting error.
				log.Printf("[meeting] refine target %s not found in workspace %s (owner=%s): %v",
					meetingID, workspaceIDForStore, uidForStore, err)
			} else {
				log.Printf("[meeting] update after refine %s: %v", meetingID, err)
			}
		}
	}

	s.wsHub.Broadcast("meeting.refined", map[string]any{
		"meetingId": meetingID, "noteId": noteID, "tasksCreated": tasksCreated,
	})
	return result
}

// ⚠ §939：`ingestMeetingNote`（会议笔记的落库）已经搬到
// server_meeting_note_ingest.go —— 因为它与随手记那条建笔记路径是
// **同一件事的两条路径**（租户/归属/id/广播目标），口径必须抽成一份共用，
// 而不是抄两遍（抄两遍 ⇒ 同一缺陷必然出现在两处）。
//
// ⚠§938：`ingestMeetingTasks`（会议待办的落库）也搬到了
// server_meeting_todo_ingest.go —— 理由相同（它有两条落库路径）。
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
