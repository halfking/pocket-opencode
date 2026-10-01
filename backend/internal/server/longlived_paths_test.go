package server

// longlived_paths_test.go — 长耗时端点的写 deadline 白名单。
//
// 背景：`http.Server` 配了 `WriteTimeout: 30s`。对普通请求这是必要的
// Slowloris 防护，但对会跑很久的端点，它会在 30s 处**掐断写**——连接已废，
// handler 之后 writeJSON 写不进去，客户端收到的是
// 「基础连接已经关闭 / UND_ERR_SOCKET」，**一个字节都没有**。
//
// 2026-10-01 在 `POST /api/emails/invoices/extract` 上实测到这个故障：
// 它命中发票但缺开票日期时，会只为这一封拉一次 IMAP 原文补日期
// （handleEmailInvoiceExtract:235），单封就超过 30s。客户端报连接被关闭，
// 而服务端**已经把发票行建好了**（summary 显示 count=1 / pending=1）——
// 操作成功、界面报错，用户会以为没提取而反复点击。
// 根因就是漏了这条白名单。
//
// 这个白名单是「加了才算」的：漏一条不会编译失败、不会启动失败，只在真实
// 慢调用时才暴露。所以这里用测试把它钉住 —— 新增长耗时端点时忘了加，
// CI 就会红。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// deadlineRecorder 记录中间件是否真的清了本连接的写 deadline。
//
// http.NewResponseController 通过接口断言探测能力，所以只要实现
// SetWriteDeadline(time.Time{}) error 就会被它认出来 —— 这样测的是
// **中间件的真实机制**，而不只是「名单里有个字符串」。
type deadlineRecorder struct {
	http.ResponseWriter
	calls []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.calls = append(d.calls, t)
	return nil
}

func TestLongLivedPathMiddleware_ClearsWriteDeadline(t *testing.T) {
	cases := []struct {
		path       string
		wantCleard bool
	}{
		{"/api/emails/invoices/extract", true},  // 本轮修的（客户端空响应）
		{"/api/emails/invoices/harvest", true},  // 既有
		{"/api/email/pipeline/run", true},       // 既有
		{"/api/emails/invoices/summary", false}, // 短请求，不该放宽
		{"/api/emails/sync", false},             // 短请求，不该放宽
	}
	for _, c := range cases {
		rec := &deadlineRecorder{ResponseWriter: httptest.NewRecorder()}
		var innerCalled bool
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			innerCalled = true
			w.WriteHeader(http.StatusOK)
		})
		longLivedPathMiddleware(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, c.path, nil))

		if !innerCalled {
			t.Fatalf("%s: 下游 handler 没被调用，中间件把请求吞了", c.path)
		}
		cleared := len(rec.calls) > 0 && rec.calls[0].IsZero()
		if cleared != c.wantCleard {
			t.Errorf("%s: 写 deadline 是否被清除 = %v, want %v（调用记录 %v）",
				c.path, cleared, c.wantCleard, rec.calls)
		}
	}
}

// mustBeLongLived 是「已知会跑很久、必须清写 deadline」的端点前缀。
//
// 维护约定：**任何**会做 IMAP 拉取、全量扫描或批量下载的端点，都要在这里
// 出现一次。加端点时顺手加一行，比出事后再从日志里反推快得多。
var mustBeLongLived = []string{
	// 手动触发一整轮流水线：同步 5 个账户 + 清垃圾 + 提醒 + 采集 + 推送，
	// 实测 1m30s。
	"/api/email/pipeline/run",
	// 批量下载发票文件，自带 5 分钟预算。
	"/api/emails/invoices/harvest",
	// 单封邮件的发票提取：命中但缺开票日期时会拉一次 IMAP 原文，
	// 实测单封就能超过 30s（见文件头的事故记录）。
	"/api/emails/invoices/extract",
	// SSE / 长连接类端点。
	"/api/llm/stream",
	"/api/llm-gateway/nodes/",
	"/api/mobile/sessions/",
	"/api/llmbff/stream",
}

func TestLongLivedPaths_CoversKnownSlowEndpoints(t *testing.T) {
	for _, want := range mustBeLongLived {
		found := false
		for _, got := range longLivedPaths {
			// 中间件用的是 strings.HasPrefix，所以登记前缀即可覆盖其子路径。
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q 不在 longLivedPaths 里 —— 它会跑很久，"+
				"30s 写超时会掐断连接，客户端收到空响应而服务端其实成功了。"+
				"把它加进 longLivedPaths，并在这里补一行", want)
		}
	}
}

// 负控性质的反向检查：白名单里不该出现「短请求路径」，
// 否则等于给普通端点开了无限写时间，白白放弃 Slowloris 防护。
func TestLongLivedPaths_NoShortPaths(t *testing.T) {
	shortish := []string{
		"/api/emails/sync",
		"/api/emails/invoices/summary",
		"/api/emails/accounts",
		"/api/healthz",
	}
	for _, p := range shortish {
		for _, got := range longLivedPaths {
			if strings.HasPrefix(p, got) {
				t.Errorf("%q 被 %q 前缀命中 —— 这些是短请求端点，"+
					"不该放宽写 deadline", p, got)
			}
		}
	}
}
