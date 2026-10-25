package server

// server_task_delegate_acc.go —— 「把任务交给 ACC 去做」的**完整**链路。
//
// ★ 2026-10-25（审计轮实测）：移动端的「委托给 ACC」按钮**从来没有通过过**。
//
// 端点契约与调用形态对不上：
//
//	后端 POST /api/tasks/delegate 要求 {"title","run_id",...}，
//	    run_id 缺失 ⇒ 400 "run_id is required for canonical task binding"；
//	前端 frontend/src/api/accTasks.ts 只发 {"title","description","kind"}，
//	    **没有 run_id** ⇒ 每一次点击都是 400。
//
// 而 run 本来就不该由 App 提供：run 是**派发**的产物（ACC runtime control
// 在 dispatch 时才生成 run_id + dispatch_id）。App 能提供的只有「做什么」。
//
// 所以这里补上后端自己完成的那一段：
//
//	① 解析派单目标：哪台 agent-companion（runtime_id）× 哪个槽位（agent_id）
//	② ACC 建任务：MCP acc_create_task → ACC 任务 id
//	③ 派发：ACC runtime control POST /api/v2/runtime/dispatch
//	    —— 必须带 source_ref + correlation_id，否则智能体的权限门
//	       fail-closed（"missing correlation triple"），任务跑不起来
//	④ 本地落一行任务（source=acc，落在**调用者**的 workspace）
//	⑤ 写绑定：SetACCBinding 把 acc_task_id / acc_run_id / acc_dispatch_id
//	    写进任务行
//	    ★ 这一步补上了「SetACCBinding 是唯一写入口、却没有生产调用方」的洞：
//	      没有绑定，/api/tasks/{id}/cancel 只能靠运行事件反推派单。
//	⑥ 返回本地任务 id + ACC 侧的三个 id，App 拿得到才能显示与停止。

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// delegateAccRequest 是 POST /api/tasks/delegate 的请求体。
//
// RunID 为空 ⇒ 走「后端建任务并派发」；非空 ⇒ 走原来的「绑定一个已存在的
// ACC 任务」语义（ResolveTaskRun）。
type delegateAccRequest struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	RunID       string `json:"run_id"`
	TaskID      string `json:"task_id"`
	OperationID string `json:"operation_id"`
	// AgentID / RuntimeID 可选：指定派给哪个智能体；不指定则由
	// ResolveDispatchTarget 选一个当前可派的槽位。
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id"`
}

// accTaskIDFromCreate 解析 acc_create_task 的返回，取 ACC 任务 id。
//
// ACC 返回的是 work item：{"id":"<uuid>","tenant_id":...,"phase":"queue",...}。
// 老的 canonical contract 形状用 task_id，所以两个都认。
func accTaskIDFromCreate(raw string) string {
	var doc struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Data   struct {
			ID     string `json:"id"`
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}
	for _, v := range []string{doc.ID, doc.TaskID, doc.Data.ID, doc.Data.TaskID} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// delegateCreateAndDispatch 实现「App 只想说做什么，剩下的 ACC 侧自己办」。
//
// ctx 由调用方（handleDelegateTask）按预算派生 —— 这里所有下游调用都用它，
// 不要用 r.Context()：ACC 的 runtime-control 面实测会抖到 22~67s
// （2026-10-25），没有预算的话这条请求会被网关先砍掉。
func (s *Server) delegateCreateAndDispatch(ctx context.Context, w http.ResponseWriter, r *http.Request, req delegateAccRequest) {
	if s.accRuntime == nil {
		http.Error(w, "ACC runtime client not configured", http.StatusServiceUnavailable)
		return
	}
	if s.taskStore == nil {
		http.Error(w, "task store not configured", http.StatusServiceUnavailable)
		return
	}

	// ① 派单目标
	//
	// 优先用配置（少一次往返，且不受 ACC runtime-control 面的抖动影响）；
	// 没配才去 ACC 的 runtime 清单里现查。
	runtimeID, agentID := s.cfg.ACCDispatchRuntimeID, s.cfg.ACCDispatchAgentID
	if v := strings.TrimSpace(req.AgentID); v != "" {
		agentID = v
	}
	if v := strings.TrimSpace(req.RuntimeID); v != "" {
		runtimeID = v
	}
	if runtimeID == "" || agentID == "" {
		rt, ag, err := s.accRuntime.ResolveDispatchTarget(ctx, agentID, runtimeID)
		if err != nil {
			log.Printf("[tasks/delegate] resolve dispatch target failed: %v", err)
			// 「ACC 读不到」与「ACC 上没有可派智能体」是两件事，必须分开报：
			// 前者是通道故障（网关 502 / 超时），后者是库存为空。
			// 混为一谈会把一次网络抖动说成「没有智能体可用」。
			if errors.Is(err, accruntime.ErrNoDispatchableAgent) {
				http.Error(w, "no dispatchable ACC agent: "+err.Error(), http.StatusBadGateway)
				return
			}
			http.Error(w, "ACC runtime control unavailable: "+err.Error(), http.StatusBadGateway)
			return
		}
		runtimeID, agentID = rt, ag
	}

	// 本地任务 id 先定下来：ACC 侧要用它当 correlation_id，
	// 这样「ACC 那条派单」和「Pocket 这条任务」在两边是同一条线。
	localID := "task-" + generateUUID()
	correlationID := "pocket-" + localID

	// ② ACC 建任务
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = "agent_task/opencode"
	}
	createArgs := map[string]interface{}{"kind": kind, "title": req.Title}
	if req.Description != "" {
		createArgs["description"] = req.Description
	}
	raw, err := s.mcpClient.CreateTask(ctx, createArgs)
	if err != nil {
		log.Printf("[tasks/delegate] acc_create_task failed: %v", err)
		http.Error(w, "ACC create task failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	accTaskID := accTaskIDFromCreate(raw)
	if accTaskID == "" {
		log.Printf("[tasks/delegate] acc_create_task returned no task id: %.400s", raw)
		http.Error(w, "ACC create task returned no task id", http.StatusBadGateway)
		return
	}

	// ③ 派发
	prompt := req.Title
	if req.Description != "" {
		prompt = req.Title + "\n\n" + req.Description
	}
	dispatch, err := s.accRuntime.Dispatch(ctx, accruntime.DispatchRequest{
		IdempotencyKey: "pocket-dispatch-" + localID,
		RuntimeID:      runtimeID,
		AgentID:        agentID,
		TaskID:         accTaskID,
		Operation:      "agent.task",
		SourceRef:      "pocketd",
		CorrelationID:  correlationID,
		Payload: map[string]any{
			"prompt":      prompt,
			"title":       req.Title,
			"description": req.Description,
			"task_id":     localID,
		},
	})
	if err != nil {
		log.Printf("[tasks/delegate] dispatch failed (acc_task=%s runtime=%s agent=%s): %v",
			accTaskID, runtimeID, agentID, err)
		http.Error(w, "ACC dispatch failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// ④ 本地任务行（落在调用者的 workspace，不是系统 workspace）
	ws := s.workspaceIDFromRequest(r)
	t := &task.Task{
		ID:          localID,
		Title:       req.Title,
		Description: req.Description,
		Status:      "active",
		Priority:    "normal",
		WorkspaceID: ws,
		Source:      "acc",
		Type:        task.TypeOther,
		Visibility:  task.VisibilityPrivate,
		OwnerID:     s.userIDFromRequest(r),
		Tags:        []string{},
	}
	if err := s.taskStore.CreateTask(ctx, t); err != nil {
		log.Printf("[tasks/delegate] local task create failed (acc_task=%s dispatch=%s): %v",
			accTaskID, dispatch.DispatchID, err)
		http.Error(w, "local task create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.taskStore.SetParticipants(ctx, localID, ws, []task.Participant{
		{UserID: t.OwnerID, Role: task.RoleOwner},
	}); err != nil {
		log.Printf("[tasks/delegate] seed owner participant for %s failed: %v", localID, err)
	}

	// ⑤ 绑定：这一条是「停止」能真的到达 ACC 的前提。
	//
	// HolderID 此刻多半还是空的（命令刚创建，租约还没发出去）；
	// /api/tasks/{id}/cancel 在 holder 缺失时会去运行事件里解（见
	// task_cancel.go 与 task_acc_dispatch.go），所以这里不猜。
	if err := s.taskStore.SetACCBinding(ctx, ws, localID, task.Binding{
		TaskID:        accTaskID,
		RunID:         dispatch.RunID,
		DispatchID:    dispatch.DispatchID,
		SourceRef:     "pocketd",
		CorrelationID: correlationID,
	}); err != nil {
		log.Printf("[tasks/delegate] set ACC binding for %s failed (acc_task=%s dispatch=%s): %v",
			localID, accTaskID, dispatch.DispatchID, err)
		http.Error(w, "bind ACC task failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.broadcastTaskEvent("task_created", t)

	// ⑥ 响应
	writeJSON(w, http.StatusOK, map[string]any{
		"source":         "acc",
		"task_id":        localID,
		"acc_task_id":    accTaskID,
		"run_id":         dispatch.RunID,
		"dispatch_id":    dispatch.DispatchID,
		"status":         dispatch.Status,
		"agent_id":       agentID,
		"runtime_id":     runtimeID,
		"correlation_id": correlationID,
		"raw":            raw,
	})
}
