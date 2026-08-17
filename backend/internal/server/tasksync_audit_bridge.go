package server

import (
	"strings"

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
