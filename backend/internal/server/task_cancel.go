package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// handleCancelTask 实现 POST /api/tasks/{id}/cancel（三层契约：ACC 是
// task/run/cancel 的唯一权威）。
//
//   - ACC 绑定 task（能解出 acc_dispatch_id）：ACC Runtime Control cancel 是
//     权威动作，必须先成功；任何 ACC 失败（含 gate 不可用 404/501、5xx、
//     网络失败）→ fail-closed：本地不取消，返回 502 acc_cancel_unavailable。
//   - 绑定 task 但 accRuntime 未装配：装配错误 → 503，绝不静默本地取消。
//   - 既没有绑定、也解不出派单：仅本地取消（status=cancelled），不触 ACC。
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

	// Which ACC command does this task correspond to?
	//
	// ★ 2026-10-25（审计轮实测）：任务的 acc_dispatch_id / acc_holder_id 两列
	// **在生产里从来没被写过**（SetACCBinding 是唯一写入口，而它没有生产调用方）
	// ⇒ 之前这条路径永远走「未绑定 ⇒ 仅本地取消」：本地卡片变成 cancelled，
	// ACC 那边的派单**照跑不误**。用户按「停止」，agent 继续跑。
	//
	// 数据其实一直都在：run 绑定里有 ACC 的 run id，ACC 的运行事件里
	// `dispatch.*` 事件带 dispatch_id、`dispatch.leased/started` 的 actor_id
	// 就是**租约持有方**。所以解不出来时才退回去按 run 查事件。
	dispatchID := current.ACCDispatchID
	holderID := current.ACCHolderID
	// 可能的 run id 来源：绑定列 + 运行绑定表。
	runIDCandidates := []string{current.ACCRunID}
	if rb, rerr := s.taskStore.GetTaskRunBinding(r.Context(), workspaceID, taskID); rerr == nil && rb.RunID != "" {
		runIDCandidates = append(runIDCandidates, rb.RunID)
	}
	if dispatchID == "" {
		for _, runID := range runIDCandidates {
			if strings.TrimSpace(runID) == "" {
				continue
			}
			ref, derr := s.resolveACCDispatch(r.Context(), runID)
			if derr == nil {
				dispatchID, holderID = ref.DispatchID, ref.HolderID
				log.Printf("[tasks/cancel] task %s resolved ACC dispatch %s (holder %s) from run events", taskID, dispatchID, holderID)
				break
			}
			log.Printf("[tasks/cancel] task %s: no ACC dispatch resolvable from run %s: %v", taskID, runID, derr)
		}
	}
	// ★ 派单 id 已知、但租约持有方还空着时，去运行事件里解**持有方**。
	//
	// 2026-10-25 实测：委派链刚跑通时，任务行里写的是 dispatch_id，
	// 而 holder_id 要等命令真正被租出去才有值（租约是异步发的）。
	// 原来的写法在这里直接 502「binding incomplete」⇒ 用户按「停止」，
	// 系统答「绑定不全」。而持有方就在同一批运行事件里（dispatch.leased /
	// dispatch.started 的 actor_id），和 dispatch_id 是同一次解出来的。
	if dispatchID != "" && holderID == "" {
		for _, runID := range runIDCandidates {
			if strings.TrimSpace(runID) == "" {
				continue
			}
			ref, derr := s.resolveACCDispatch(r.Context(), runID)
			if derr == nil && ref.HolderID != "" {
				holderID = ref.HolderID
				log.Printf("[tasks/cancel] task %s resolved ACC holder %s from run events", taskID, holderID)
				break
			}
		}
	}

	if dispatchID != "" {
		if s.accRuntime == nil {
			s.writeStructuredError(w, r, http.StatusServiceUnavailable,
				"acc_runtime_not_configured",
				"task is ACC-bound but ACC runtime client is not configured")
			return
		}
		// ACC 的 cancel 端点按 holder（runtime lease 持有方）围栏：绑定缺
		// holder_id 时无法构造合法请求，与 ACC 失败同样 fail-closed。
		if holderID == "" {
			s.Write(r, "mobile.task.acc_cancel_unavailable",
				"task:"+taskID+"/dispatch:"+dispatchID,
				AuditFields{Detail: "class=binding_incomplete reason=" + body.Reason, Success: false})
			envelope := map[string]any{
				"error":           "ACC binding incomplete (missing holder_id); local cancel withheld fail-closed",
				"code":            "acc_cancel_unavailable",
				"retryable":       false,
				"gate_class":      "binding_incomplete",
				"task_id":         taskID,
				"acc_task_id":     current.ACCTaskID,
				"acc_dispatch_id": dispatchID,
			}
			envelope["request_id"] = s.requestIDFromContext(r)
			writeJSON(w, http.StatusBadGateway, envelope)
			return
		}
		// ★ ACC 的围栏不只看 holder_id，还看 **fencing_token**，且它必须是
		// **当前**那把（每次租约获取都会换）。pocketd 手里有 holder id
		// （它是绑定了这个任务的租约持有方），但 token 不在任务行里 ⇒
		// 只能现读现用。
		//
		// 实测（2026-10-25，真实 ACC + 真实 daemon）：
		//   不带 token 取消 ⇒ ACC 答 409 {"code":"lease_fenced",
		//   "message":"orchestration: stale fencing token"} ⇒ 那条派单后来
		//   等槽位超时落 `failed`（也就是 agent_busy 那条路）——操作员说
		//   「停」，系统答的是「忙，稍后重试」。
		//   先 GET 命令拿到当前 token 再取消 ⇒ ACC 答 200，daemon 侧那条
		//   run 落 `cancelled`，且**从未运行**。
		//
		// 取 token 失败不自己拍板：照常发起取消（带 0），由 ACC 裁决；
		// ACC 拒绝就按既有契约 fail-closed（不本地取消）。ACC 才是权威。
		var fencingToken uint64
		alreadyStopped := false
		if cmd, err := s.accRuntime.GetCommand(r.Context(), dispatchID); err != nil {
			log.Printf("[tasks/cancel] read ACC command %s for fencing token failed: %v", dispatchID, err)
		} else {
			fencingToken = cmd.FencingToken
			if accTerminalStatus(cmd.Status) {
				// 命令已经终态 ⇒ **没有还在跑的副作用**可停 ⇒ 不必再发取消
				//（再发只会撞 invalid_state_transition，然后被当成「ACC 不可用」
				//⇒ 502，而任务其实早就结束了）。本地照常落 cancelled。
				alreadyStopped = true
				log.Printf("[tasks/cancel] ACC command %s is already %s; no cancel call needed", dispatchID, cmd.Status)
			}
		}
		if !alreadyStopped {
			if err := s.accRuntime.CancelCommand(r.Context(), dispatchID, holderID, fencingToken, body.Reason); err != nil {
				s.Write(r, "mobile.task.acc_cancel_unavailable",
					"task:"+taskID+"/dispatch:"+dispatchID,
					AuditFields{Detail: "reason=" + body.Reason, Success: false})
				envelope := map[string]any{
					"error":           "ACC cancel unavailable; local cancel withheld fail-closed",
					"code":            "acc_cancel_unavailable",
					"retryable":       true,
					"task_id":         taskID,
					"acc_task_id":     current.ACCTaskID,
					"acc_dispatch_id": dispatchID,
				}
				envelope["request_id"] = s.requestIDFromContext(r)
				writeJSON(w, http.StatusBadGateway, envelope)
				return
			}
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
	writeJSON(w, http.StatusOK, updated)
}
