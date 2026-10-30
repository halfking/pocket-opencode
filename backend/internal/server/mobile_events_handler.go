package server

// mobile_events_handler.go — P1 §3 快照追赶端点 + task.health 的任务→会话映射适配。
//
// 冻结契约：docs/2026-08-27-p1-contracts-frozen.md §2/§3。路由注册在 server.go
// （/api/mobile/events/snapshot，requireAuth）；handler 走 requireMobileWorkspace
// fail-closed（与 mobile_approval_handler.go 同款鉴权纪律）。
//
// broadcaster 由 main.go 在 server.New 之后经 SetMobileEventsBroadcaster 注入
// （pocketd 单实例进程；Server struct 不扩字段，见契约 §1 的 server.go 仅追加
// 路由行约束）。未注入（测试/降级）时端点返回 503。

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// mobileEventsBroadcaster 保存 main.go 注入的会话事件广播器（快照数据源）。
var mobileEventsBroadcaster atomic.Pointer[opencode.SessionEventBroadcaster]

// SetMobileEventsBroadcaster 注入会话事件广播器，启用 /api/mobile/events/snapshot。
func (s *Server) SetMobileEventsBroadcaster(b *opencode.SessionEventBroadcaster) {
	mobileEventsBroadcaster.Store(b)
}

// handleMobileEventsSnapshot GET /api/mobile/events/snapshot
//
// 返回内存态 EventsSnapshot（各活跃 session 的 health/phase/round_index/
// last_event_at/latest_round + generated_at），供前端重连后一次性对齐，
// 不落库、不做事件回放（§3）。
func (s *Server) handleMobileEventsSnapshot(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := s.requireMobileWorkspace(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	b := mobileEventsBroadcaster.Load()
	if b == nil {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable,
			"mobile events broadcaster is not configured")
		return
	}
	writeJSON(w, http.StatusOK, b.Snapshot(r.Context(), workspaceID))
}

// taskStoreSessionIndex 把 *task.Store 适配为 opencode.TaskSessionIndex：
// 复用 /api/tasks/:id/sessions 的 ListTasks + ListSessionsForTask 链路。
// 无会话任务产出 InstanceID/SessionID 为空的 link（广播器对其发 idle）。
type taskStoreSessionIndex struct {
	store *task.Store
}

// NewTaskSessionIndex 构造 task.Store 适配器；store 为 nil 时返回 nil（降级）。
func NewTaskSessionIndex(store *task.Store) opencode.TaskSessionIndex {
	if store == nil {
		return nil
	}
	return taskStoreSessionIndex{store: store}
}

// SessionLinks 枚举全部任务及其会话链接（跨 workspace；每行带任务归属
// workspace，广播器按它定向，不越租户）。
//
// ⚠⚠ **一条查询**取完（2026-10-28 §976）：旧实现是「ListTasks + 每个任务再查
// 一遍链接」的 **N+1**，而它**每 5 秒**被调一次（广播器的 taskHealthTick）
// 外加每次快照请求 ⇒ 实测 44 次任务列表请求带来 **528 次**链接查询
// ⇒ 查询数随任务数线性增长。现在一次 LEFT JOIN 取完（见
// task.Store.ListAllSessionLinks），与任务数无关。
//
// ⚠ 语义与旧实现**逐条等价**（判据见 server_session_links_batch_test.go）：
//
//	· 有链接的任务 ⇒ 每个链接一行；
//	· 没有链接的任务 ⇒ 一行，InstanceID/SessionID 为空（广播器对它发 idle）。
func (i taskStoreSessionIndex) SessionLinks(ctx context.Context) ([]opencode.TaskSessionLink, error) {
	rows, err := i.store.ListAllSessionLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]opencode.TaskSessionLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, opencode.TaskSessionLink{
			TaskID:      r.TaskID,
			WorkspaceID: r.WorkspaceID,
			InstanceID:  r.InstanceID,
			SessionID:   r.SessionID,
		})
	}
	return out, nil
}
