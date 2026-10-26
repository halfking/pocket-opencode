package server

// ACC 派单的**审批面**：把 agent-companion 的待批队列接到 App。
//
// ★ 为什么需要这一层（2026-10-26 实测，不是推测）：
//
//	· agent-companion 的 ACP 权限门把工具审批**转发给 ACC**：
//	  POST /api/v2/runtime/commands/{dispatch_id}/permission。
//	· 但 acc-go **从未实现这条路由**（对权威 ACC 实测 GET/POST 都 404）。
//	  agent-companion 自己的 ACC_BRIDGE_SPEC.md 却写着这条（companion→ACC：
//	  /commands/:id/start|complete|permission|cancel-ack）—— 契约在，实现没有。
//	· 于是「委托给 ACC」的任务只要碰到需要人工批准的工具，就必然
//	  permission_denied；而 agent-companion 侧原来连拒绝原因都不打日志，
//	  现场只剩一个没有解释的 permission_denied。
//
// 本轮 agent-companion 已改：ACC 端点「未接线」（404/501）时，请求落到
// daemon 自己的干预队列（GET /api/v1/interventions），等人应答；没人应答
// 仍然 fail-closed。
//
// 于是这条路由补的是最后一跳：**App 看得到待批项、并且能应答它**。
// 数据来源是 agent-companion（不是 ACC）—— ACC 在这一跳上根本没有端点。
//
// 鉴权沿用 pocketd 惯例：requireAuth（用户态）；daemon 侧的鉴权由
// CompanionClient 用 AC_API_SECRET 完成，pocketd 不代做。

import (
	"encoding/json"
	"net/http"
	"strings"
)

// accApprovalItem is one pending approval as the App sees it.
//
// The daemon speaks camelCase; this surface keeps camelCase so the
// frontend's types match the wire without a translation layer that could
// quietly drop a field.
type accApprovalItem struct {
	ID         string                       `json:"id"`
	RunID      string                       `json:"run_id"`
	SessionID  string                       `json:"session_id"`
	AgentKind  string                       `json:"agent_kind"`
	ToolCallID string                       `json:"tool_call_id"`
	Title      string                       `json:"title"`
	Kind       string                       `json:"kind"`
	Status     string                       `json:"status"`
	CreatedAt  string                       `json:"created_at"`
	Reason     string                       `json:"reason,omitempty"`
	DecidedBy  string                       `json:"decided_by,omitempty"`
	Options    []accApprovalOption         `json:"options"`
}

type accApprovalOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// handleACCApprovals lists the pending approvals of ACC-dispatched runs.
//
// GET /api/acc/approvals[?status=pending]
//
// ★ 503（而不是 200 + 空列表）when companion is not configured: an empty
// list and "the approval surface is not wired" are different facts, and
// rendering them the same way is how a broken integration looks healthy.
func (s *Server) handleACCApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStructuredJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_not_configured",
			"agent-companion client not configured; set POCKET_COMPANION_URL")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	items, err := s.companion.ListInterventions(status)
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "companion_unavailable",
			"read agent-companion interventions failed: "+err.Error())
		return
	}
	out := make([]accApprovalItem, 0, len(items))
	for _, it := range items {
		row := accApprovalItem{
			ID:         it.ID,
			RunID:      it.RunID,
			SessionID:  it.SessionID,
			AgentKind:  it.AgentKind,
			ToolCallID: it.ToolCallID,
			Title:      it.Title,
			Kind:       it.Kind,
			Status:     it.Status,
			CreatedAt:  it.CreatedAt,
			Reason:     it.Reason,
			DecidedBy:  it.DecidedBy,
			Options:    make([]accApprovalOption, 0, len(it.Options)),
		}
		for _, o := range it.Options {
			row.Options = append(row.Options, accApprovalOption{ID: o.ID, Title: o.Title, Kind: o.Kind})
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals": out,
		"count":     len(out),
		"status":    status,
	})
}

// handleACCApprovalOps answers one pending approval.
//
// POST /api/acc/approvals/{id}/decision
// body: {"option_id": "once", "reason": "...", "decided_by": "admin"}
//
// The option id must be one the agent offered — the daemon refuses
// anything else, because relaying a fabricated option to the agent is
// worse than refusing.
func (s *Server) handleACCApprovalOps(w http.ResponseWriter, r *http.Request) {
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_not_configured",
			"agent-companion client not configured; set POCKET_COMPANION_URL")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/acc/approvals/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "decision" || parts[0] == "" {
		writeStructuredJSONError(w, http.StatusNotFound, "not_found", "unknown approval route")
		return
	}
	if r.Method != http.MethodPost {
		writeStructuredJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var body struct {
		OptionID  string `json:"option_id"`
		Reason    string `json:"reason"`
		DecidedBy string `json:"decided_by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeStructuredJSONError(w, http.StatusBadRequest, "invalid_request", "decode body failed: "+err.Error())
		return
	}
	if strings.TrimSpace(body.OptionID) == "" {
		writeStructuredJSONError(w, http.StatusBadRequest, "invalid_request", "option_id is required")
		return
	}
	if body.DecidedBy == "" {
		body.DecidedBy = "pocketd"
	}
	item, err := s.companion.DecideIntervention(parts[0], body.OptionID, body.Reason, body.DecidedBy)
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "companion_unavailable",
			"answer agent-companion intervention failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": accApprovalItem{
			ID:         item.ID,
			RunID:      item.RunID,
			SessionID:  item.SessionID,
			AgentKind:  item.AgentKind,
			ToolCallID: item.ToolCallID,
			Title:      item.Title,
			Kind:       item.Kind,
			Status:     item.Status,
			CreatedAt:  item.CreatedAt,
			Reason:     item.Reason,
			DecidedBy:  item.DecidedBy,
		},
	})
}

// writeStructuredJSONError writes the same envelope shape the rest of
// the mobile surface uses, so the frontend's error handling needs no
// special case for this route.
func writeStructuredJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":     message,
		"code":      code,
		"retryable": status >= 500,
	})
}
