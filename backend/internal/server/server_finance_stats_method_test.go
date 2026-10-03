// internal/server/server_finance_stats_method_test.go
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

// BUG-AD 回归：/api/finance/stats 之前没有方法白名单。
// handleFinanceOps 在进 method switch 之前就把 "stats" 分流给 handleFinanceStats，
// 而 handleFinanceStats 自己不看 r.Method，于是
// DELETE /api/finance/stats 会回 200 + 统计结果 —— 同一个前缀下的
// parse（只收 POST）和 /{id}（只收 GET/DELETE）都有白名单，只有 stats 没有。
//
// 判据必须能在有缺陷一侧失败：回退掉 handleFinanceStats 里的白名单，
// 下面 Delete/Post/Put 三个子测试会立刻失败（405 → 200）。
func TestFinanceStats_RejectsNonGET(t *testing.T) {
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

	// 阳性对照：GET 必须是 200 且带统计字段（否则下面的 405 断言没有意义）
	ok := serveWorkspaceJSON(t, h, http.MethodGet, "/api/finance/stats", token, "")
	if ok.Code != http.StatusOK {
		t.Fatalf("baseline GET /api/finance/stats status=%d (expected 200), body=%s", ok.Code, ok.Body.String())
	}
	if body := ok.Body.String(); len(body) == 0 {
		t.Fatal("baseline GET /api/finance/stats 返回空 body")
	}
	t.Logf("baseline GET -> 200, body=%s", ok.Body.String())

	for _, tc := range []struct {
		name   string
		method string
	}{
		{"Delete", http.MethodDelete},
		{"Post", http.MethodPost},
		{"Put", http.MethodPut},
		{"Patch", http.MethodPatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, tc.method, "/api/finance/stats", token, "")
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s /api/finance/stats status=%d (expected 405), body=%s",
					tc.method, rr.Code, rr.Body.String())
			}
			// 405 也不能把统计内容漏出去
			body := rr.Body.String()
			for _, leak := range []string{"total_income", "by_category", "balance"} {
				if strings.Contains(body, leak) {
					t.Fatalf("%s 405 body 仍泄露统计内容 %q: %s", tc.method, leak, body)
				}
			}
		})
	}
}
