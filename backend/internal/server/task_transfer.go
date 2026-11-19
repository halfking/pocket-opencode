package server

// task_transfer.go —— POST /api/tasks/{id}/transfer：把 ACC 绑定任务的派单
// **转派**到另一个 runtime/agent（移效/移交）。
//
// 组合事务（pocketd 编排，ACC 每一步都是权威，任何一步失败即 fail-closed
// 不落地 —— 与 cancel 同一门禁哲学）：
//
//	① 解析旧派单：绑定列优先，运行事件兜底（同 cancel 路径的解析逻辑）
//	② 取消旧 dispatch：终态跳过（没有还在跑的副作用）；取消失败 ⇒ 502，
//	   **不**重派 —— 旧派单还活着时重派会双跑同一个任务
//	③ 同一 ACC 任务重派：不建新任务（转的是活任务，不是新需求）；
//	   correlation_id=transfer-from-<taskID> 标记谱系；
//	   IdempotencyKey 绑定 (task, 旧 dispatch) 对 —— 客户端重试安全
//	④ SetACCBinding 换绑新 run/dispatch（acc_task_id 不变）
//	⑤ activity 流记 transferred 事件（参与者可在活动流看到移交记录）
//
// 与 delegate 的区别：delegate 建新任务；transfer 复用同一 ACC 任务。
// 与 cancel 的区别：本地任务保持 active，只换执行者。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// transferTaskRequest 是 POST /api/tasks/{id}/transfer 的请求体。
// runtime_id / agent_id 缺省 ⇒ 用配置的派单目标
// （POCKET_ACC_DISPATCH_RUNTIME_ID / POCKET_ACC_DISPATCH_AGENT_ID）。
type transferTaskRequest struct {
	RuntimeID string `json:"runtime_id"`
	AgentID   string `json:"agent_id"`
	Reason    string `json:"reason"`
}

func (s *Server) handleTransferTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.taskStore == nil || s.accRuntime == nil {
		http.Error(w, "ACC runtime or task store not configured", http.StatusServiceUnavailable)
		return
	}
	var body transferTaskRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	ws := s.workspaceIDFromRequest(r)
	current, err := s.taskStore.GetTaskScoped(r.Context(), taskID, ws)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	accTaskID := current.ACCTaskID
	if strings.TrimSpace(accTaskID) == "" {
		http.Error(w, "task is not ACC-bound; nothing to transfer", http.StatusConflict)
		return
	}

	// ① 解析旧派单（与 cancel 路径同一套解析：绑定列 → 运行事件兜底）。
	dispatchID := current.ACCDispatchID
	holderID := current.ACCHolderID
	runIDCandidates := []string{current.ACCRunID}
	if rb, rerr := s.taskStore.GetTaskRunBinding(r.Context(), ws, taskID); rerr == nil && rb.RunID != "" {
		runIDCandidates = append(runIDCandidates, rb.RunID)
	}
	if dispatchID == "" {
		for _, runID := range runIDCandidates {
			if strings.TrimSpace(runID) == "" {
				continue
			}
			if ref, derr := s.resolveACCDispatch(r.Context(), runID); derr == nil {
				dispatchID, holderID = ref.DispatchID, ref.HolderID
				log.Printf("[tasks/transfer] task %s resolved old ACC dispatch %s (holder %s) from run events", taskID, dispatchID, holderID)
				break
			}
		}
	}
	if dispatchID != "" && holderID == "" {
		for _, runID := range runIDCandidates {
			if strings.TrimSpace(runID) == "" {
				continue
			}
			if ref, derr := s.resolveACCDispatch(r.Context(), runID); derr == nil && ref.HolderID != "" {
				holderID = ref.HolderID
				break
			}
		}
	}

	// ② 取消旧派单。解析不出 dispatch（从未派出去过）⇒ 跳过取消直接重派；
	//    命令已终态 ⇒ 跳过取消（没有还在跑的副作用，holder 也不再必要）；
	//    命令活跃却缺 holder ⇒ 与 cancel 同门禁 fail-closed（旧派单还活着
	//    时重派 = 双跑，宁可拒绝）。
	//
	// ★ 判定顺序：先读命令（要 fencing token 也得先读），再决定要不要
	//   holder —— 派单完成得快时（实测 probe 任务秒级跑完）租约可能还没
	//   在任务行留下 holder，但命令已经终态，此时不该被门禁拦住。
	alreadyStopped := false
	lastCmdStatus := ""
	if dispatchID == "" {
		log.Printf("[tasks/transfer] task %s has no live dispatch; transferring without cancel", taskID)
		alreadyStopped = true
	} else {
		var fencingToken uint64
		if cmd, cerr := s.accRuntime.GetCommand(r.Context(), dispatchID); cerr != nil {
			// 读命令失败不自己拍板：照常按「需要取消」走（token 带 0），
			// 由 ACC 裁决；取消失败照常 fail-closed。
			lastCmdStatus = "unreadable: " + cerr.Error()
			log.Printf("[tasks/transfer] read ACC command %s failed: %v", dispatchID, cerr)
		} else {
			fencingToken = cmd.FencingToken
			lastCmdStatus = cmd.Status
			if accTerminalStatus(cmd.Status) {
				alreadyStopped = true
				log.Printf("[tasks/transfer] old ACC dispatch %s is already %s; skip cancel", dispatchID, cmd.Status)
			}
		}
		if !alreadyStopped {
			if holderID == "" {
				s.writeTransferFailClosed(w, r, taskID, accTaskID, dispatchID,
					"ACC binding incomplete (missing holder_id); transfer withheld fail-closed", lastCmdStatus)
				return
			}
			if cerr := s.accRuntime.CancelCommand(r.Context(), dispatchID, holderID, fencingToken, body.Reason); cerr != nil {
				s.writeTransferFailClosed(w, r, taskID, accTaskID, dispatchID,
					"ACC cancel unavailable; transfer withheld fail-closed: "+cerr.Error(), lastCmdStatus)
				return
			}
			log.Printf("[tasks/transfer] old dispatch %s cancelled", dispatchID)
		}
	}

	// ③ 同一 ACC 任务重派到新目标。
	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		runtimeID = s.cfg.ACCDispatchRuntimeID
	}
	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		agentID = s.cfg.ACCDispatchAgentID
	}
	if runtimeID == "" || agentID == "" {
		http.Error(w, "transfer target not resolved: pass runtime_id/agent_id or set POCKET_ACC_DISPATCH_*", http.StatusBadRequest)
		return
	}
	prompt := current.Title
	if current.Description != "" {
		prompt = current.Title + "\n\n" + current.Description
	}
	correlationID := "transfer-from-" + taskID
	dispatch, err := s.accRuntime.Dispatch(r.Context(), accruntime.DispatchRequest{
		// 幂等键绑定 (task, 旧 dispatch) 对：同一轮移效重试安全；下一轮
		// 移效（旧 dispatch 变了）自然拿到新键。
		IdempotencyKey: "pocket-transfer-" + taskID + "-" + dispatchID,
		RuntimeID:      runtimeID,
		AgentID:        agentID,
		TaskID:         accTaskID,
		Operation:      "agent.task",
		SourceRef:      "pocketd",
		CorrelationID:  correlationID,
		Payload: map[string]any{
			"prompt":      prompt,
			"title":       current.Title,
			"description": current.Description,
			"task_id":     taskID,
		},
	})
	if err != nil {
		// 旧派单已取消、新派单失败 ⇒ 任务停在无活跃派单的 consistent 状态
		//（绑定仍指旧 dispatch，其命令已终态，后续 cancel 走本地路径）。
		// 照实报错，不回滚、不静默。
		log.Printf("[tasks/transfer] re-dispatch failed after cancel (task=%s old=%s runtime=%s agent=%s): %v",
			taskID, dispatchID, runtimeID, agentID, err)
		http.Error(w, "ACC re-dispatch failed (old dispatch already cancelled): "+err.Error(), http.StatusBadGateway)
		return
	}

	// ④ 换绑（acc_task_id 不变，run/dispatch/correlation 换新）。
	if berr := s.taskStore.SetACCBinding(r.Context(), ws, taskID, task.Binding{
		TaskID:        accTaskID,
		RunID:         dispatch.RunID,
		DispatchID:    dispatch.DispatchID,
		SourceRef:     "pocketd",
		CorrelationID: correlationID,
	}); berr != nil {
		log.Printf("[tasks/transfer] set ACC binding for %s failed (new dispatch=%s): %v", taskID, dispatch.DispatchID, berr)
		http.Error(w, "rebind ACC task failed: "+berr.Error(), http.StatusInternalServerError)
		return
	}

	// ⑤ activity 流记 transferred 事件（未知类型在 notify 映射里静默 ——
	//    移效由操作者发起，参与者在活动流看到记录即可）。
	actor := s.userIDFromRequest(r)
	note := fmt.Sprintf("transferred to %s/%s from dispatch %s", runtimeID, agentID, dispatchID)
	if _, aerr := s.appendWorkItemEvent(r.Context(), taskID, ws, task.EventTransferred, actor,
		task.EventPayload{TaskTitle: current.Title, Comment: note}, ""); aerr != nil {
		// 活动流是记录不是门禁：失败只降级日志，不影响移效本身。
		log.Printf("[tasks/transfer] record transfer event for %s failed: %v", taskID, aerr)
	}

	if updated, uerr := s.taskStore.GetTaskScoped(r.Context(), taskID, ws); uerr == nil {
		s.broadcastTaskEvent("task_updated", updated)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source":           "acc",
		"task_id":          taskID,
		"acc_task_id":      accTaskID,
		"run_id":           dispatch.RunID,
		"dispatch_id":      dispatch.DispatchID,
		"status":           dispatch.Status,
		"agent_id":         agentID,
		"runtime_id":       runtimeID,
		"correlation_id":   correlationID,
		"transferred_from": dispatchID,
		"old_cancelled":    !alreadyStopped,
	})
}

// writeTransferFailClosed 与 cancel 路径同形：ACC 门禁失败时结构化报错，
// 不做任何本地落地。cmdStatus 带出旧命令的实测状态（unreadable: … 表示
// 读命令本身失败）—— 运维不用再去 ACC 侧复查就能定位是哪一层拦的。
func (s *Server) writeTransferFailClosed(w http.ResponseWriter, r *http.Request, taskID, accTaskID, dispatchID, detail, cmdStatus string) {
	envelope := map[string]any{
		"error":              detail,
		"code":               "acc_transfer_unavailable",
		"retryable":          true,
		"task_id":            taskID,
		"acc_task_id":        accTaskID,
		"acc_dispatch_id":    dispatchID,
		"acc_command_status": cmdStatus,
	}
	envelope["request_id"] = s.requestIDFromContext(r)
	writeJSON(w, http.StatusBadGateway, envelope)
}
