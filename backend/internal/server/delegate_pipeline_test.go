package server

// delegate_pipeline_test.go — 需求「可以委托服务端进行」那条路径的执行体。
//
// execution_mode_test.go 只测了 shouldDelegatePipeline 这个**纯判定函数**
// （该不该委托）。真正发请求的 delegatePipeline —— URL 校验、HTTP 方法、
// 非 200 的处理、响应解码 —— 从来没有任何测试。
//
// 这正是本项目反复栽的那一类：判定测了，执行没测。判定全绿，但
// delegatePipeline 里改坏任何一个环节（漏了 scheme 校验、把错误页当 JSON
// 解、把解码失败报成别的原因）都不会有任何用例转红。
//
// 这条路径的特殊性在于它把**带邮箱权限的流水线**POST 到远端，所以
// 「发到哪里、发的是什么、失败时说的是不是真原因」都必须钉住。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func delegateServer(t *testing.T, url string) *Server {
	t.Helper()
	return &Server{cfg: config.Config{
		EmailExecutionMode:     "server",
		EmailServerPipelineURL: url,
	}}
}

// 正常路径：POST 到配置地址，并把远端返回的报告原样带回。
func TestDelegatePipeline_PostsAndDecodesRemoteReport(t *testing.T) {
	var gotMethod, gotBody, gotPath string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&email.PipelineReport{
			SpamDryRun: 2, SpamMoved: 1, RemindersSent: 3,
		})
	}))
	defer remote.Close()

	rep := delegateServer(t, remote.URL+"/pipeline/run").delegatePipeline(context.Background())

	if gotMethod != http.MethodPost {
		t.Errorf("远端收到的方法=%q, want POST", gotMethod)
	}
	if gotPath != "/pipeline/run" {
		t.Errorf("远端收到的路径=%q, want /pipeline/run —— 配置里的路径必须原样使用", gotPath)
	}
	// body 是 nil：远端若也是 pocketd，handleEmailPipelineRun 读到空 body 时
	// 会忽略解码错误、dryRunSpam 保持 nil。契约是「空 body 合法」。
	if gotBody != "" {
		t.Logf("远端收到 body=%q（非空，需确认契约仍兼容）", gotBody)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("不该有错误: %v", rep.Errors)
	}
	if rep.SpamDryRun != 2 || rep.SpamMoved != 1 || rep.RemindersSent != 3 {
		t.Errorf("远端报告未被如实带回: %+v", rep)
	}
}

// 非法 URL 必须在**发请求之前**被挡下。
//
// 这条最要紧：URL 来自部署配置，但它决定了带邮箱权限的流水线 POST 到哪里。
// 漏写 scheme（"llm.kxpms.cn/v1"）或写成 file:// 时，裸 NewRequest 要么报一句
// 难懂的 parse error，要么把请求发到不该去的地方。
func TestDelegatePipeline_RejectsUnusableURLBeforeSending(t *testing.T) {
	var reached bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	for _, bad := range []string{
		"pipeline.internal/run",   // 漏 scheme
		"file:///etc/passwd",      // 非 http(s)
		"ftp://pipeline.internal", // 非 http(s)
		"http://",                 // 无 host
		"://nonsense",             // 解析失败
	} {
		rep := delegateServer(t, bad).delegatePipeline(context.Background())
		if len(rep.Errors) == 0 {
			t.Errorf("URL %q 应被拒绝，却返回了无错报告", bad)
			continue
		}
		if !strings.Contains(rep.Errors[0], "POCKET_EMAIL_SERVER_PIPELINE_URL") {
			t.Errorf("URL %q 的错误信息应点名那个环境变量，实际=%q", bad, rep.Errors[0])
		}
	}
	if reached {
		t.Fatal("非法 URL 下仍然向远端发出了请求")
	}
}

// 非 200 必须报状态码，而不是把错误页当 JSON 解。
//
// 远端返回的多半是它自己的 HTML 错误页；解码它只会得到一条与真实原因无关的
// 「delegate decode」报错，把「编排服务挂了」说成「响应格式不对」。
func TestDelegatePipeline_NonOKReportsStatusNotDecodeError(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>502 Bad Gateway from upstream</body></html>"))
	}))
	defer remote.Close()

	rep := delegateServer(t, remote.URL).delegatePipeline(context.Background())
	if len(rep.Errors) != 1 {
		t.Fatalf("应恰好 1 条错误，实际 %d: %v", len(rep.Errors), rep.Errors)
	}
	msg := rep.Errors[0]
	if strings.Contains(msg, "decode") {
		t.Errorf("错误信息=%q，把远端错误页当成了 JSON 解码问题", msg)
	}
	if !strings.Contains(msg, "502") {
		t.Errorf("错误信息=%q，应包含远端返回的状态码", msg)
	}
}

// 远端返回 200 但不是合法 JSON：必须明确报 decode，且不能当成空报告成功。
func TestDelegatePipeline_InvalidJSONIsReportedNotSwallowed(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer remote.Close()

	rep := delegateServer(t, remote.URL).delegatePipeline(context.Background())
	if len(rep.Errors) == 0 {
		t.Fatal("200 + 非法 JSON 必须报错，不能当成一份空报告")
	}
	if !strings.Contains(rep.Errors[0], "delegate decode") {
		t.Errorf("错误信息=%q，应指明是解码失败", rep.Errors[0])
	}
}

// 连接不上（远端未监听）也必须报错，而不是返回一份空报告。
func TestDelegatePipeline_UnreachableRemoteIsReported(t *testing.T) {
	// 监听一个随机端口后立刻关闭，得到一个几乎必然拒绝连接的地址。
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := dead.URL
	dead.Close()

	rep := delegateServer(t, addr).delegatePipeline(context.Background())
	if len(rep.Errors) == 0 {
		t.Fatal("远端连不上必须报错，不能返回一份看起来成功的空报告")
	}
	if !strings.Contains(rep.Errors[0], "delegate") {
		t.Errorf("错误信息=%q，应标明是委托失败", rep.Errors[0])
	}
}
