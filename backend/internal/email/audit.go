package email

import (
	"log"
	"sync"
)

// AuditFields 是 email 包写入审计事件时使用的最小字段集合。值字段
// 必须在写入前经过调用方的 redact；本包只做「不引入敏感内容」约束
// （如绝不允许 Detail 含 access_token 字符串）。
type AuditFields struct {
	Detail  string
	Success bool
}

// AuditWriter 是 email 包到宿主审计存储的解耦接口。生产由
// server.audit.WriteWithClaims 适配，nil 时跳过。
type AuditWriter interface {
	Write(userID, tenantID, action, resource string, fields AuditFields)
}

var (
	auditMu sync.RWMutex
	auditW  AuditWriter
)

// SetAuditWriter 注入 audit writer；测试可通过 nil-safe 调用重置。
func SetAuditWriter(w AuditWriter) {
	auditMu.Lock()
	auditW = w
	auditMu.Unlock()
}

// recordAudit 调用注入的 writer；writer 为 nil 时仅 log，不 panic。
// resource/action 命名约定：
//
//	email.oauth.completed       email_account:<id>
//	email.oauth.completed.error email_account:<id>
//	email.oauth.refreshed       email_account:<id>
//	email.oauth.refreshed.error email_account:<id>
//	email.oauth.revoked         email_account:<id>
//	email.account.created       email_account:<id>
//	email.account.updated       email_account:<id>
//	email.account.deleted       email_account:<id>
func recordAudit(userID, tenantID, action, resource string, fields AuditFields) {
	if action == "" || resource == "" {
		// 没有索引意义的事件不写入；与 server.audit.Write 行为一致。
		return
	}
	auditMu.RLock()
	w := auditW
	auditMu.RUnlock()
	if w == nil {
		log.Printf("[email/audit] (no writer) %s user=%s tenant=%s resource=%s success=%v",
			action, userID, tenantID, resource, fields.Success)
		return
	}
	w.Write(userID, tenantID, action, resource, fields)
}
