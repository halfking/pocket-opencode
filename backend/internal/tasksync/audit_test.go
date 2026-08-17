package tasksync

import (
	"sync"
	"testing"
)

// recordingWriter 把每次 Write 调用记录下来便于断言。
type recordingWriter struct {
	mu    sync.Mutex
	calls []recordedAudit
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

func TestTasksyncAudit_Recording(t *testing.T) {
	defer SetAuditWriter(nil)
	w := &recordingWriter{}
	SetAuditWriter(w)

	recordAudit("", "system:acc", "tasksync.sync", "acc_task:batch",
		AuditFields{Success: true, Detail: "parsed=5 saved=4"})
	recordAudit("", "system:acc", "tasksync.sync.error", "acc_task:abc",
		AuditFields{Success: false, Detail: "create_failed"})

	if len(w.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(w.calls))
	}
	if w.calls[0].TenantID != "system:acc" {
		t.Fatalf("system tenant must be passed through: %+v", w.calls[0])
	}
	if w.calls[0].Success != true || w.calls[1].Success != false {
		t.Fatalf("Success flag not propagated")
	}
}

func TestTasksyncAudit_NilWriterSafe(t *testing.T) {
	SetAuditWriter(nil)
	// 不应 panic。
	recordAudit("", "system:acc", "tasksync.sync", "acc_task:batch",
		AuditFields{Success: true, Detail: "noop"})
}

func TestTasksyncAudit_EmptyActionOrResourceSkipped(t *testing.T) {
	defer SetAuditWriter(nil)
	w := &recordingWriter{}
	SetAuditWriter(w)

	recordAudit("", "system:acc", "", "acc_task:batch",
		AuditFields{Success: true})
	recordAudit("", "system:acc", "tasksync.sync", "",
		AuditFields{Success: true})

	if len(w.calls) != 0 {
		t.Fatalf("empty action/resource must be skipped, got %+v", w.calls)
	}
}

// systemTenantID 必须与 server.AuditSystemTenant() 一致——否则跨包
// 检索「system 事件」会拿不到这些记录。该测试用 grep-check 兜底。
func TestTasksyncAudit_SystemTenantMatchesServer(t *testing.T) {
	if systemTenantID() != "system:acc" {
		t.Fatalf("tasksync.systemTenantID() = %q; want system:acc", systemTenantID())
	}
}
