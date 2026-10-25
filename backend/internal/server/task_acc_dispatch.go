package server

// task_acc_dispatch.go — 从 ACC 的运行事件里解出「这条任务当前对应哪条派单」。
//
// ★ 2026-10-25（审计轮实测）：openpocket 的「停止」对经 ACC 委派的任务**根本没到
// ACC**。
//
// 根因有两层：
//  1. 任务的 acc_dispatch_id / acc_holder_id 两列**在生产里从来没被写过** ——
//     SetACCBinding 是唯一的写入口，而它**没有任何生产调用方**（只有测试）。
//     ⇒ handleCancelTask 走的是「未绑定 ⇒ 仅本地取消」分支：本地卡片变成
//     cancelled，ACC 那边的派单**照跑不误**。
//  2. 即使绑定了，ACC 的取消端点是 holder+**fencing token** 围栏的，而 token
//     每次租约获取都会换，只能现读现用（accruntime.GetCommand）。
//
// 数据其实一直都在：ACC 的运行事件里，`dispatch.*` 事件带 `dispatch_id`，
// `dispatch.leased` / `dispatch.started` 的 `actor_id` 就是**租约持有方**
// （companion runtime id）。所以这里从事件里把它们解出来。
//
// 注意「当前」：一次 run 可能有多次派单尝试（attempt 1/2…），必须取**最后**一条
// 带 dispatch_id 的事件，否则会对着上一条已死的派单发取消。

import (
	"context"
	"errors"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// Errors the dispatch resolution can produce. They are all "there is nothing
// to cancel at ACC" in disguise — the caller must not treat them as a
// product failure, but it also must not pretend the stop reached ACC.
var (
	errNoACCEventClient = errors.New("accruntime: ACC event client not configured")
	errNoACCRunID       = errors.New("accruntime: no ACC run id to resolve a dispatch from")
	errNoACCDispatch    = errors.New("accruntime: ACC run has no dispatch event yet")
)

// accDispatchRef 是一次派单的引用：命令 id + 租约持有方。
type accDispatchRef struct {
	// DispatchID is the ACC command id (dispatch_xxx).
	DispatchID string
	// HolderID is the runtime-control lease holder that may act on it.
	HolderID string
}

// resolveACCDispatch derives the current dispatch reference of an ACC run.
//
// It returns an error when the run has no dispatch event yet (the task was
// created but nothing was dispatched) — the caller then has nothing to
// cancel at ACC and must not pretend otherwise.
func (s *Server) resolveACCDispatch(ctx context.Context, runID string) (accDispatchRef, error) {
	if s.mcpClient == nil {
		return accDispatchRef{}, errNoACCEventClient
	}
	if strings.TrimSpace(runID) == "" {
		return accDispatchRef{}, errNoACCRunID
	}
	events, err := s.mcpClient.ListRunEvents(ctx, runID, 0)
	if err != nil {
		return accDispatchRef{}, err
	}
	ref, ok := accDispatchFromEvents(events)
	if !ok {
		return accDispatchRef{}, errNoACCDispatch
	}
	return ref, nil
}

// accDispatchFromEvents derives the dispatch reference from ACC's run events.
//
// 「当前」= **最后**一条带 dispatch_id 的事件：一次 run 可能有多次派单尝试
// （attempt 1/2…），对着上一条已死的派单发取消等于取消了一个不存在的东西。
//
// 持有方 = 最后一条**agent 角色**事件的 actor_id：ACC 的 dispatch.leased /
// dispatch.started 事件的 actor 就是租约持有方（companion runtime id），
// 而 dispatch.created 的 actor 是发起者（用户）—— 不能拿错。
func accDispatchFromEvents(events []mcp.AccRunEvent) (accDispatchRef, bool) {
	var ref accDispatchRef
	for _, ev := range events {
		if strings.TrimSpace(ev.DispatchID) != "" {
			ref.DispatchID = ev.DispatchID
		}
		if ev.ActorType == "agent" && strings.TrimSpace(ev.ActorID) != "" {
			ref.HolderID = ev.ActorID
		}
	}
	if ref.DispatchID == "" {
		return accDispatchRef{}, false
	}
	return ref, true
}

// accTerminalStatus reports whether an ACC command has already reached a
// state in which there is nothing left to stop.
//
// A stop request against a finished command would be answered by ACC with
// invalid_state_transition, which openpocket's fail-closed contract turns
// into 502 — i.e. the operator could not even close the card of work that
// is already over. So a terminal command means: skip the cancel call (there
// is no running side effect to stop) and finish the local cancel.
func accTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "failed", "cancelled", "expired", "superseded":
		return true
	}
	return false
}
