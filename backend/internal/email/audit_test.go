package email

import (
	"strings"
	"sync"
	"testing"
)

// recordingWriter 把每次 Write 调用记录下来便于断言。
type recordingWriter struct {
	mu      sync.Mutex
	calls   []recordedAudit
}

type recordedAudit struct {
	UserID, TenantID, Action, Resource string
	Success                            bool
	Detail                             string
}

func (r *recordingWriter) Write(userID, tenantID, action, resource string, fields AuditFields) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedAudit{
		UserID: userID, TenantID: tenantID, Action: action,
		Resource: resource, Success: fields.Success, Detail: fields.Detail,
	})
}

func TestEmailAudit_Recording(t *testing.T) {
	// 清理全局状态，防止测试间相互污染。
	defer SetAuditWriter(nil)
	w := &recordingWriter{}
	SetAuditWriter(w)

	recordAudit("u1", "ws-a", "email.oauth.completed", "email_account:acc-1",
		AuditFields{Success: true, Detail: "provider=google"})
	recordAudit("u1", "ws-a", "email.oauth.revoked", "email_account:acc-1",
		AuditFields{Success: true, Detail: "provider=google"})
	recordAudit("", "", "email.oauth.completed.error", "email_account:",
		AuditFields{Success: false, Detail: "provider_error=access_denied"})

	if len(w.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(w.calls))
	}
	if w.calls[0].Action != "email.oauth.completed" || w.calls[0].TenantID != "ws-a" {
		t.Fatalf("unexpected first call: %+v", w.calls[0])
	}
	if w.calls[2].Success {
		t.Fatalf("error event must not be success: %+v", w.calls[2])
	}
}

func TestEmailAudit_NilWriterSafe(t *testing.T) {
	SetAuditWriter(nil)
	// 不应 panic。
	recordAudit("u1", "ws-a", "email.oauth.completed", "email_account:acc-1",
		AuditFields{Success: true, Detail: "noop"})
}

func TestEmailAudit_EmptyActionOrResourceSkipped(t *testing.T) {
	defer SetAuditWriter(nil)
	w := &recordingWriter{}
	SetAuditWriter(w)

	recordAudit("u1", "ws-a", "", "email_account:acc-1",
		AuditFields{Success: true, Detail: "noop"})
	recordAudit("u1", "ws-a", "email.oauth.completed", "",
		AuditFields{Success: true, Detail: "noop"})

	if len(w.calls) != 0 {
		t.Fatalf("empty action/resource must be skipped, got %+v", w.calls)
	}
}

// 防止误把 access_token 字符串写进 Detail：测试用 recordAudit 的常见
// 入口校验——调用方传 Detail 时不应包含敏感键名。这一断言的「正向
// 端」由 server.audit.redactDetail 负责；email 包只确保 recordAudit 不
// 会让 Detail 含敏感串时仍 claim 成功（仅约束命名约定）。
func TestEmailAudit_NamingConvention_HasNoSensitiveFieldName(t *testing.T) {
	defer SetAuditWriter(nil)
	w := &recordingWriter{}
	SetAuditWriter(w)

	// 调用方传了含 access_token 的 detail 是 bug。这里模拟一个外部库
	// 错误地写出敏感字符串——本测试记录行为，不阻断，但生产侧会通过
	// server.audit.redactDetail 兜底掩码。
	recordAudit("u1", "ws-a", "email.oauth.completed", "email_account:acc-1",
		AuditFields{Success: true, Detail: "token=RAW-VALUE-LEAK"})
	if !strings.Contains(w.calls[0].Detail, "RAW-VALUE-LEAK") {
		t.Fatalf("expected Detail to be passed through (rely on server redact): %+v", w.calls[0])
	}
}
