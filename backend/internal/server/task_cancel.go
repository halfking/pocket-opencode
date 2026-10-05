package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// handleCancelTask 实现 POST /api/tasks/{id}/cancel（三层契约：ACC 是
// task/run/cancel 的唯一权威）。
//
//   - ACC 绑定 task（acc_dispatch_id 非空）：ACC Runtime Control cancel 是
//     权威动作，必须先成功；任何 ACC 失败（含 gate 不可用 404/501、5xx、
//     网络失败）→ fail-closed：本地不取消，返回 502 acc_cancel_unavailable。
//   - 绑定 task 但 accRuntime 未装配：装配错误 → 503，绝不静默本地取消。
//   - 未绑定 task：仅本地取消（status=cancelled），不触 ACC。
//
// 请求体 { "reason": "..." }（可选）。
func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.taskStore == nil {
		http.Error(w, "task store not configured", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	workspaceID := s.workspaceIDFromRequest(r)
	current, err := s.taskStore.GetTaskScoped(r.Context(), taskID, workspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if current.ACCDispatchID != "" {
		if s.accRuntime == nil {
			s.writeStructuredError(w, r, http.StatusServiceUnavailable,
				"acc_runtime_not_configured",
				"task is ACC-bound but ACC runtime client is not configured")
			return
		}
		// ACC 的 cancel 端点按 holder（runtime lease 持有方）围栏：绑定缺
		// holder_id 时无法构造合法请求，与 ACC 失败同样 fail-closed。
		if current.ACCHolderID == "" {
			s.Write(r, "mobile.task.acc_cancel_unavailable",
				"task:"+taskID+"/dispatch:"+current.ACCDispatchID,
				AuditFields{Detail: "class=binding_incomplete reason=" + body.Reason, Success: false})
			envelope := map[string]any{
				"error":           "ACC binding incomplete (missing holder_id); local cancel withheld fail-closed",
				"code":            "acc_cancel_unavailable",
				"retryable":       false,
				"gate_class":      "binding_incomplete",
				"task_id":         taskID,
				"acc_task_id":     current.ACCTaskID,
				"acc_dispatch_id": current.ACCDispatchID,
			}
			envelope["request_id"] = s.requestIDFromContext(r)
			writeJSON(w, http.StatusBadGateway, envelope)
			return
		}
		if err := s.accRuntime.CancelCommand(r.Context(), current.ACCDispatchID, current.ACCHolderID, body.Reason); err != nil {
			s.Write(r, "mobile.task.acc_cancel_unavailable",
				"task:"+taskID+"/dispatch:"+current.ACCDispatchID,
				AuditFields{Detail: "reason=" + body.Reason, Success: false})
			envelope := map[string]any{
				"error":           "ACC cancel unavailable; local cancel withheld fail-closed",
				"code":            "acc_cancel_unavailable",
				"retryable":       true,
				"task_id":         taskID,
				"acc_task_id":     current.ACCTaskID,
				"acc_dispatch_id": current.ACCDispatchID,
			}
			envelope["request_id"] = s.requestIDFromContext(r)
			writeJSON(w, http.StatusBadGateway, envelope)
			return
		}
	}

	cancelled := "cancelled"
	updated, err := s.taskStore.UpdateTaskScoped(r.Context(), taskID, workspaceID, task.TaskUpdate{Status: &cancelled})
	if err != nil {
		if errors.Is(err, task.ErrPendingApprovals) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if current.Status != updated.Status {
		s.auditTaskStatusChange(r, updated, current.Status)
	}
	s.broadcastTaskEvent("task_updated", updated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}
