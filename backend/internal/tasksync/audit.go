package tasksync

import (
	"log"
	"sync"
)

// AuditFields 是 tasksync 包写审计事件时使用的最小字段集合。
type AuditFields struct {
	Success bool
	Detail  string
}

// AuditWriter 是 tasksync 包到宿主审计存储的解耦接口。
type AuditWriter interface {
	Write(userID, tenantID, action, resource string, fields AuditFields)
}

var (
	auditMu sync.RWMutex
	auditW  AuditWriter
)

// SetAuditWriter 注入 audit writer；为 nil 时 skip。
func SetAuditWriter(w AuditWriter) {
	auditMu.Lock()
	auditW = w
	auditMu.Unlock()
}

// recordAudit 由 scheduler 触发；writer 为 nil 时仅 log，不 panic。
func recordAudit(userID, tenantID, action, resource string, fields AuditFields) {
	if action == "" || resource == "" {
		return
	}
	auditMu.RLock()
	w := auditW
	auditMu.RUnlock()
	if w == nil {
		log.Printf("[tasksync/audit] (no writer) %s user=%s tenant=%s resource=%s success=%v",
			action, userID, tenantID, resource, fields.Success)
		return
	}
	w.Write(userID, tenantID, action, resource, fields)
}
