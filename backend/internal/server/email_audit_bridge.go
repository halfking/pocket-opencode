package server

import (
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// emailAuditBridge 把 server.audit.WriteWithClaims 适配到 email 包的
// AuditWriter 接口，避免 email 包依赖 redclaw 与 server。
type emailAuditBridge struct {
	s *Server
}

// NewEmailAuditWriter 返回一个把 email.AuditFields 转写到当前 Server 持有
// 的 AuditStore 的 writer。
func NewEmailAuditWriter(s *Server) email.AuditWriter {
	return &emailAuditBridge{s: s}
}

func (b *emailAuditBridge) Write(userID, tenantID, action, resource string, fields email.AuditFields) {
	if b == nil || b.s == nil {
		return
	}
	claims := &authClaims{UserID: userID, WorkspaceID: tenantID}
	// Resource 形如 "email_account:<id>"，构造为 audit entry 的 Resource。
	// 若 caller 没填 resource（错误分支），保持为空由 helper 跳过。
	resource = strings.TrimSpace(resource)
	if action == "" || resource == "" {
		return
	}
	b.s.WriteWithClaims(claims, action, resource, AuditFields{
		Detail:  fields.Detail,
		Success: fields.Success,
	})
}
