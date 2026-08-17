package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// /api/tasks POST + source=acc 必须被服务端拒绝（fail-closed）。
// 这是 §5 「企业集成只读」的核心契约：mcp.Client 当前无写能力，
// 但前端 / curl 可能误传 source=acc 试图推任务；服务端必须拒绝 + 写 audit。
func TestTasksPost_AccSourceRejected(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()

	tok, _ := signer.SignWithWorkspace("op", "member", "ws-a")
	body := `{"title":"acc-test","source":"acc","status":"active"}`
	req := mobileRequest(http.MethodPost, "/api/tasks", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() == "" {
		t.Fatal("rejection body should explain")
	}

	// audit 必须有一条 task.post.rejected。
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "task.post.rejected"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 task.post.rejected, got %d", len(entries))
	}
	e := entries[0]
	if e.TenantID != "ws-a" {
		t.Fatalf("tenant must be ws-a, got %q", e.TenantID)
	}
	if e.Success {
		t.Fatalf("rejected event must be success=false")
	}
	if e.Detail == "" {
		t.Fatalf("rejected event must carry detail reason")
	}
}

// 即便 taskStore 为 nil（remote-only 部署），source=acc POST 也必须先
// 被拦截——不能借助 503 状态绕过只读断言。这要求把 source 校验提前到
// taskStore 检查之前（已通过在 server.go 中提前 body 解析实现）。
func TestTasksPost_AccSourceRejected_EvenWithoutTaskStore(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.taskStore = nil // remote-only mode

	tok, _ := signer.SignWithWorkspace("op", "member", "ws-a")
	body := `{"title":"x","source":"acc","status":"active"}`
	req := mobileRequest(http.MethodPost, "/api/tasks", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (not 503) so acc guard fires before store check; got %d body=%s",
			rr.Code, rr.Body.String())
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "task.post.rejected"})
	if len(entries) != 1 {
		t.Fatalf("audit must still fire when taskStore is nil, got %d", len(entries))
	}
}

// source 不为 acc 时不触发拦截——本地任务创建路径不受影响。
func TestTasksPost_LocalSourceNotBlocked(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	// 不实际注入 taskStore；即便没有 store，POST local 应走到 503（store
	// 未配置）而不是 403（acc 拦截）——证明 acc 拦截是 narrow scope。
	tok, _ := signer.SignWithWorkspace("op", "member", "ws-a")
	body := `{"title":"x","source":"local","status":"active"}`
	req := mobileRequest(http.MethodPost, "/api/tasks", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// 期待 503（taskStore 未配置）而不是 403（acc 拦截）。
	if rr.Code == http.StatusForbidden {
		t.Fatalf("local POST must not be flagged as acc: %s", rr.Body.String())
	}
	// audit 不应出现 task.post.rejected。
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "task.post.rejected"})
	if len(entries) != 0 {
		t.Fatalf("local POST must not trigger acc guard, got %+v", entries)
	}
}