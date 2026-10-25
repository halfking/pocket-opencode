package server

import (
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/tasksync"
)

// tasksyncAuditBridge 把 server.audit.WriteWithClaims 适配到
// tasksync.AuditWriter，避免 tasksync 包依赖 redclaw。
type tasksyncAuditBridge struct {
	s *Server
}

// NewTasksyncAuditWriter 返回一个把 tasksync.AuditFields 转写到当前 Server
// 持有的 AuditStore 的 writer。writer 内部强制使用 system tenant
// （"system:acc"），与 tasksync.systemTenantID 一致；该常量在两边必须
// 保持同步。
func NewTasksyncAuditWriter(s *Server) tasksync.AuditWriter {
	return &tasksyncAuditBridge{s: s}
}

func (b *tasksyncAuditBridge) Write(userID, tenantID, action, resource string, fields tasksync.AuditFields) {
	if b == nil || b.s == nil {
		return
	}
	action = strings.TrimSpace(action)
	resource = strings.TrimSpace(resource)
	if action == "" || resource == "" {
		return
	}
	// tasksync 事件必须落到 system tenant；即使 caller 传了非空
	// tenantID，也强制覆盖为 AuditSystemTenant()。
	claims := &authClaims{
		UserID:      userID,
		WorkspaceID: AuditSystemTenant(),
	}
	_ = tenantID // 上文已固定为 system tenant
	b.s.WriteWithClaims(claims, action, resource, AuditFields{
		Detail:  fields.Detail,
		Success: fields.Success,
	})
}

// NotifyTaskUpdated 让 tasksync 的 ACC 派单状态回填能把变化推给客户端。
//
// ★ 为什么必须有它：回填是直接写 store 的，没有广播 ⇒ App 的任务卡片会
// 一直显示「进行中」，哪怕那一单早就跑完了。卡片是用户正在盯着的那一块，
// 它必须是被推的，不是等用户下拉刷新才更新。
func (s *Server) NotifyTaskUpdated(t *task.Task) {
	if s == nil || t == nil {
		return
	}
	s.broadcastTaskEvent("task_updated", t)
}
