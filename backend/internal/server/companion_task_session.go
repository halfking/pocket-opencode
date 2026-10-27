package server

// companion_task_session.go —— 「ACC 派单跑出来的会话」在**任务域**的读面与续写面。
//
// ★ 2026-10-27 实测（不是推测，全部有活链路读数）：
//
//	· ACC 派单跑完后，**本地任务行从来没有关联过任何会话**
//	  （GET /api/tasks/{id}/sessions ⇒ {"sessions":[]}）。
//	· 但会话**确实存在**，而且三处都能读到：
//	    - agent-companion 的 run 历史里带着 session_id
//	      （GET /api/v1/runs/{dispatch_id} ⇒ {"session_id":"ses_…"}）
//	    - GET /api/v1/native/sessions/{id} ⇒ resumable=true
//	    - GET /api/v1/native/sessions/{id}/transcript ⇒ 正文读得到
//	  ⇒ **数据全在，缺的是接线**：任务 ⇄ 会话 的链接没人写。
//
//	· 而且 App 的**全局会话视图发不了消息**给这类会话：
//	  前端 stores/session.ts 的 sendPrompt 要求 instance_id，
//	  打的是实例作用域端点 /api/mobile/sessions/{id}/prompt?instance_id=…
//	  ⇒ companion 原生会话没有 instance ⇒ 消息根本发不出去。
//	⇒ 「继续」与「提示词的修改」必须走**任务域**的出口，也就是本文件。
//
// 本文件提供两件事：
//
//	① ensureAccNativeSessionLink：任务绑了 ACC 派单却还没有会话链接时，
//	   从 companion 的 run 历史解出那次派单用的原生会话并挂上去（幂等）。
//	② handleTaskSessionMessage：POST /api/tasks/{id}/sessions/{sid}/message
//	   —— 往原生会话里投一条**可改写**的提示词，返回 run 收据。
//
// ★ 为什么用 run 历史而不是 companion 的 /api/v1/session-links：
//
//	session-links 是**会话自己**的链接表，只在会话被 operate/bind 过之后
//	才有行；ACC 派单本身**不写**它（实测 dispatch 跑完，那一行不在）。
//	run 历史是**派单的产物**，每条都带 dispatch_id ⇒ session_id，且
//	ACC 派单的 run_id **就是** dispatch_id（accbridge 的
//	TaskRequestFromEvent 里 req.RunID = event.DispatchID）。
//	⇒ 权威且稳定的是 run 历史。
//
// ★ 安全：message 端点只接受**已经挂到这个任务上**的会话。
//	否则它就是一个"给任意 native session 发消息"的后门 ——
//	App 里任意一个任务号都能唤醒别人的会话。

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// taskSessionLinkView 是 GET /api/tasks/{id}/sessions 的一行。
//
// 在 task.SessionLink 之外补两个字段，让 App 能判断「这条链接能不能续」：
//
//	Kind   —— 原生智能体的 kind（opencode / mmcode / claude …）
//	Native —— true ⇒ 这是 companion 的**原生会话**（没有 instance）
type taskSessionLinkView struct {
	task.SessionLink
	Kind   string `json:"kind,omitempty"`
	Native bool   `json:"native"`
}

// accNativeSessionRef 是一次 ACC 派单用到的原生会话引用。
type accNativeSessionRef struct {
	SessionID string
	Kind      string
}

// resolveAccNativeSession 从 companion 的 run 历史里解出一次 ACC 派单用到的
// 原生会话。读不到 ⇒ (zero, false)，**不编造**。
func (s *Server) resolveAccNativeSession(ctx context.Context, dispatchID string) (accNativeSessionRef, bool) {
	if s.companion == nil {
		return accNativeSessionRef{}, false
	}
	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return accNativeSessionRef{}, false
	}
	run, err := s.companion.GetRun(dispatchID)
	if err != nil {
		log.Printf("[companion/task-session] read run %s failed: %v", dispatchID, err)
		return accNativeSessionRef{}, false
	}
	sid := strings.TrimSpace(run.SessionID)
	if sid == "" {
		return accNativeSessionRef{}, false
	}
	return accNativeSessionRef{SessionID: sid, Kind: strings.TrimSpace(run.Kind)}, true
}

// ensureAccNativeSessionLink 在任务绑了 ACC 派单、却还没有那条原生会话链接时，
// 把它挂上去。幂等：已经挂过 ⇒ 原样返回，不重复写。
func (s *Server) ensureAccNativeSessionLink(ctx context.Context, taskID, wsID string, links []task.SessionLink) []task.SessionLink {
	if s.companion == nil || s.taskStore == nil {
		return links
	}
	t, err := s.taskStore.GetTaskScoped(ctx, taskID, wsID)
	if err != nil || t == nil {
		return links
	}
	ref, ok := s.resolveAccNativeSession(ctx, t.ACCDispatchID)
	if !ok {
		return links
	}
	for _, l := range links {
		if l.SessionID == ref.SessionID {
			return links // 已经挂过了
		}
	}
	link := task.SessionLink{
		TaskID:     taskID,
		InstanceID: "", // 原生会话没有 instance —— 这正是它与实例会话的区别
		SessionID:  ref.SessionID,
		Role:       "primary",
	}
	if err := s.taskStore.AttachSessionScoped(ctx, link, wsID); err != nil {
		log.Printf("[companion/task-session] attach session %s to task %s failed: %v", ref.SessionID, taskID, err)
		return links
	}
	log.Printf("[companion/task-session] linked acc native session %s (kind=%s) to task %s", ref.SessionID, ref.Kind, taskID)
	return append(links, link)
}

// buildTaskSessionLinks 组装 GET /api/tasks/{id}/sessions 的响应行。
//
// 对**原生会话**额外读一次 companion 的会话元信息，拿 kind ——
// 这决定了「继续」时用哪个智能体去驱动它。
func (s *Server) buildTaskSessionLinks(ctx context.Context, taskID, wsID string) ([]taskSessionLinkView, error) {
	links, err := s.taskStore.ListSessionsForTaskScoped(ctx, taskID, wsID)
	if err != nil {
		return nil, err
	}
	links = s.ensureAccNativeSessionLink(ctx, taskID, wsID, links)
	view := make([]taskSessionLinkView, 0, len(links))
	kindCache := map[string]string{}
	for _, l := range links {
		v := taskSessionLinkView{SessionLink: l, Native: strings.TrimSpace(l.InstanceID) == ""}
		if v.Native && s.companion != nil {
			sid := l.SessionID
			kind, ok := kindCache[sid]
			if !ok {
				if sess, err := s.companion.GetSession("", sid); err == nil {
					kind = sess.Kind
				}
				kindCache[sid] = kind
			}
			v.Kind = kind
		}
		view = append(view, v)
	}
	return view, nil
}

// handleTaskSessionMessage 实现 POST /api/tasks/{id}/sessions/{sid}/message：
// 往这次派单用到的原生会话里投一条提示词（用户可改写），返回 run 收据。
//
// 这是「继续跟进」与「提示词的修改」的**唯一真实出口**。
// App 的全局会话视图做不到这件事 —— 它要求 instance_id，而原生会话没有。
func (s *Server) handleTaskSessionMessage(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_unavailable", "companion 未配置")
		return
	}
	if s.taskStore == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "task_store_unavailable", "任务存储未配置")
		return
	}
	var req struct {
		Prompt      string `json:"prompt"`
		OperationID string `json:"operation_id"`
	}
	// 请求体必须**有上界**（与 ACC 审批应答同款约定，§952 普查会判形态）：
	// 无界的请求体读点会让「读进内存」这件事变成一个攻击面。
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeStructuredJSONError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeStructuredJSONError(w, http.StatusBadRequest, "prompt_empty", "提示词不能为空")
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeStructuredJSONError(w, http.StatusBadRequest, "session_empty", "缺少 session id")
		return
	}

	ctx := r.Context()
	wsID := s.workspaceIDFromRequest(r)

	// ① 会话必须挂在这个任务上（否则这就是一个"给任意会话发消息"的后门）
	links, err := s.buildTaskSessionLinks(ctx, taskID, wsID)
	if err != nil {
		writeStructuredJSONError(w, http.StatusInternalServerError, "task_store_failed", err.Error())
		return
	}
	linked := false
	for _, l := range links {
		if l.SessionID == sessionID {
			linked = true
			break
		}
	}
	if !linked {
		writeStructuredJSONError(w, http.StatusNotFound, "session_not_linked", "该会话未关联到本任务")
		return
	}

	// ② 会话必须真的存在、且可驱动（observe-only 的会话要如实说）
	sess, err := s.companion.GetSession("", sessionID)
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "session_unreadable", "读不到会话: "+err.Error())
		return
	}
	if !sess.Resumable {
		writeStructuredJSONError(w, http.StatusConflict, "session_not_resumable",
			"该会话是只读观察会话（kind="+sess.Kind+"，agent="+sess.Agent+"}），不能投提示词")
		return
	}

	// ③ 投提示词
	receipt, err := s.companion.OperateSession(sess.Kind, sessionID, prompt, strings.TrimSpace(req.OperationID))
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "operate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runId":           receipt.RunID,
		"state":           receipt.State,
		"nativeSessionId": receipt.SessionID,
		"agentKind":       receipt.AgentKind,
		"sessionId":       sessionID,
		"prompt":          prompt,
	})
}
