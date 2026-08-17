package server

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/tasksync"
)

// tasksync bridge 端到端：注入 → recordAudit → server.auditStore。
func TestTasksyncAuditBridge_EndToEnd(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	bridge := NewTasksyncAuditWriter(srv)
	tasksync.SetAuditWriter(bridge)
	defer tasksync.SetAuditWriter(nil)

	// 直接通过 bridge.Write 模拟 recordAudit 调用——bridge 是 tasksync
	// AuditWriter 接口的实现，命中真实路径。
	bridge.Write("", "", "tasksync.sync", "acc_task:batch",
		tasksync.AuditFields{Success: true, Detail: "parsed=3 saved=3"})
	bridge.Write("", "", "tasksync.sync.error", "acc_task:abc",
		tasksync.AuditFields{Success: false, Detail: "create_failed"})

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "system:acc"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 system-tenant entries, got %d", len(entries))
	}
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
		if e.TenantID != "system:acc" {
			t.Fatalf("system tenant must be enforced: %+v", e)
		}
	}
	if !actions["tasksync.sync"] || !actions["tasksync.sync.error"] {
		t.Fatalf("missing actions: %+v", actions)
	}
}

// bridge 必须忽略 caller 传入的非 system tenant——避免 tasksync 事件
// 误标到某个普通 workspace。
func TestTasksyncAuditBridge_IgnoresCallerTenant(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	bridge := NewTasksyncAuditWriter(srv)
	tasksync.SetAuditWriter(bridge)
	defer tasksync.SetAuditWriter(nil)

	bridge.Write("", "ws-a", "tasksync.sync", "acc_task:batch",
		tasksync.AuditFields{Success: true, Detail: "noop"})

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	if len(entries) != 0 {
		t.Fatalf("tasksync events must not leak into ws-a: %+v", entries)
	}
	entries, _ = srv.auditStore.Query(redclaw.AuditQuery{TenantID: "system:acc"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 system-tenant entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Detail, "noop") {
		t.Fatalf("detail must be passed through: %q", entries[0].Detail)
	}
}

// 空 action / resource 必须跳过。
func TestTasksyncAuditBridge_SkipsEmpty(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	bridge := NewTasksyncAuditWriter(srv)
	tasksync.SetAuditWriter(bridge)
	defer tasksync.SetAuditWriter(nil)

	bridge.Write("", "", "", "acc_task:batch",
		tasksync.AuditFields{Success: true})
	bridge.Write("", "", "tasksync.sync", "",
		tasksync.AuditFields{Success: true})

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 0 {
		t.Fatalf("empty action/resource must be skipped, got %+v", entries)
	}
}
