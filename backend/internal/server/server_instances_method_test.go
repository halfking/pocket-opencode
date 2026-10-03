// internal/server/server_instances_method_test.go
package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// BUG-AE 回归：/api/instances 是只读资源（没有创建/删除 handler，
// 前端 InstanceListView 也只有刷新与选择），但 handleInstances 完全不看
// r.Method，POST/DELETE/PUT 都会回 200 + 完整实例列表 —— 危害和 BUG-AD 同形：
// 调用方看到 200 会以为写成功了，实际什么都没发生。
//
// 判据能在有缺陷一侧失败：删掉那段方法白名单，下面 3 个子测试立刻红（405 → 200）。
func TestInstances_RejectsNonGET(t *testing.T) {
	signer, err := auth.NewSigner("test-secret-012345678901234567890123", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.SignWithWorkspace("u1", "member", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	srv := newServer(
		config.Config{OpenCodeTimeoutMS: "5000"},
		adapter.NewStaticNPSAdapter(),
		adapter.NewOpenCodeHTTPAdapter(5000),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		signer, nil, nil, nil, nil, "", false, nil, nil,
	)
	h := srv.Handler()

	// 阳性对照：GET 必须是 200 且确实返回了实例（否则下面的 405 断言没有意义）
	ok := serveWorkspaceJSON(t, h, http.MethodGet, "/api/instances", token, "")
	if ok.Code != http.StatusOK {
		t.Fatalf("baseline GET /api/instances status=%d (expected 200), body=%s", ok.Code, ok.Body.String())
	}
	if !strings.Contains(ok.Body.String(), `"instances"`) {
		t.Fatalf("baseline GET /api/instances body 缺 instances 字段: %s", ok.Body.String())
	}
	t.Logf("baseline GET -> 200, body=%s", ok.Body.String())

	for _, tc := range []struct {
		name   string
		method string
	}{
		{"Post", http.MethodPost},
		{"Delete", http.MethodDelete},
		{"Put", http.MethodPut},
		{"Patch", http.MethodPatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, tc.method, "/api/instances", token, "")
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s /api/instances status=%d (expected 405), body=%s",
					tc.method, rr.Code, rr.Body.String())
			}
			// 405 也不能把实例列表漏出去
			if body := rr.Body.String(); strings.Contains(body, `"instances"`) || strings.Contains(body, "demo-main") {
				t.Fatalf("%s 405 body 仍泄露实例列表: %s", tc.method, body)
			}
		})
	}
}
