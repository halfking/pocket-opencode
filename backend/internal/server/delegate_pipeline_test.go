package server

// delegate_pipeline_test.go — 需求 6「委托服务端执行」的服务端那一条腿。
//
// ## 为什么必须测这里
//
// `execution_mode_test.go` 只测了**判定**（shouldDelegatePipeline），
// 而 `delegatePipeline` —— 真正把带邮箱权限的整条流水线 POST 到远端的那个
// 函数 —— **零覆盖**。判定绿了不等于委托能跑：URL 校验、非 200、解码失败、
// 请求到底发没发出去，全都没有任何用例守着。
//
// 这类代码的失败模式特别难看：配置写错时它**静默地把请求发到别处**，
// 或者返回一个与真实原因无关的 decode 报错，而日志里只有一行
// "delegate: ..."。所以每条分支都要能自证。
//
// ## 负控
//
// 1) 去掉 scheme 校验（只留 `target.Host == ""`）→ RejectsNonHTTPSchemes 转红。
// 2) 去掉 non-200 分支（直接 Decode）→ Non200DoesNotDecodeErrorPage 转红。
// 两条都实测过，见 handoff §7db。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func srvWithURL(raw string) *Server {
	return &Server{cfg: config.Config{EmailServerPipelineURL: raw}}
}

// 委托成功时，远端返回的 PipelineReport 必须被如实解码回来 ——
// 定时任务只 log `len(rep.Errors)`，字段丢了就只剩一个空报告。
func TestDelegatePipeline_DecodesRemoteReport(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBodyLen int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBodyLen = int64(len(b))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(email.PipelineReport{
			AccountsSynced: 3, NewEmails: 7, RemindersSent: 2, FeishuPushed: 1, FeishuFailed: 0,
		})
	}))
	defer ts.Close()

	rep := srvWithURL(ts.URL + "/api/pipeline/run").delegatePipeline(t.Context())

	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/pipeline/run" {
		t.Fatalf("remote saw %s %s, want POST /api/pipeline/run", gotMethod, gotPath)
	}
	if rep.AccountsSynced != 3 || rep.NewEmails != 7 || rep.RemindersSent != 2 || rep.FeishuPushed != 1 {
		t.Fatalf("remote report not decoded: %+v", rep)
	}
	// 契约现状：委托请求**不带任何凭证**、也不带 body（远端用自己的配置跑）。
	// 这不是「应该这样」，而是**当前实现的事实**。写死成断言是为了让它成为
	// tripwire：谁给它加了鉴权，这个用例会红，迫使他同时更新 §7db 与部署文档，
	// 而不是悄悄改掉「谁能触发带邮箱权限的流水线」这个事实。
	if gotAuth != "" {
		t.Errorf("delegation now carries Authorization=%q —— 契约变了，请同步更新 §7db 与部署文档", gotAuth)
	}
	if gotBodyLen != 0 {
		t.Errorf("delegation now sends a %d-byte body —— 契约变了，请同步更新 §7db", gotBodyLen)
	}
}

// 非 http(s) / 无 host 的地址必须在**发请求之前**被挡掉。
// 判据是「远端一次都没被敲」，不是「返回了错误」——
// 后者在「先发出去再报错」的实现下也会成立。
func TestDelegatePipeline_RejectsNonHTTPSchemes(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cases := []struct{ name, raw string }{
		{"file scheme", "file:///etc/passwd"},
		{"ftp scheme", "ftp://example.com/run"},
		{"no scheme", "llm.kxpms.cn/v1/pipeline"},
		{"scheme only", "https://"},
		{"empty", ""},
		{"whitespace", "   "},
	}
	for _, c := range cases {
		rep := srvWithURL(c.raw).delegatePipeline(t.Context())
		if len(rep.Errors) == 0 {
			t.Errorf("%s (%q): must be rejected with an error, got a clean report %+v", c.name, c.raw, rep)
			continue
		}
		if !strings.Contains(rep.Errors[0], "POCKET_EMAIL_SERVER_PIPELINE_URL") {
			t.Errorf("%s (%q): error should name the env var to fix, got %q", c.name, c.raw, rep.Errors[0])
		}
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("rejected URLs still reached the remote %d time(s) —— 校验发生在发请求之后，等于没校验", n)
	}
}

// 远端 5xx 的错误页是 HTML/文本，不是 PipelineReport。
// 直接 Decode 会得到一条与真实原因无关的 "delegate decode" 报错，
// 排障的人会去查 JSON 格式，而真正的问题是远端 502。
func TestDelegatePipeline_Non200ReportsStatusNotDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>502 Bad Gateway from nginx</body></html>"))
	}))
	defer ts.Close()

	rep := srvWithURL(ts.URL).delegatePipeline(t.Context())
	if len(rep.Errors) != 1 {
		t.Fatalf("want exactly 1 error, got %v", rep.Errors)
	}
	if !strings.Contains(rep.Errors[0], "502") {
		t.Errorf("error must carry the remote status, got %q", rep.Errors[0])
	}
	if strings.Contains(rep.Errors[0], "delegate decode") {
		t.Errorf("non-200 must not fall through to JSON decode, got %q", rep.Errors[0])
	}
}

// 200 但响应体不是合法 JSON：这是**远端契约不兼容**，必须与「远端挂了」
// 区分开，否则两类问题在日志里长得一样。
func TestDelegatePipeline_MalformedJSONIsLabelledDecode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer ts.Close()

	rep := srvWithURL(ts.URL).delegatePipeline(t.Context())
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "delegate decode") {
		t.Fatalf("want a labelled decode error, got %v", rep.Errors)
	}
}

// 远端连不上：必须报出连接错误，而不是静默返回空报告。
// 空报告会走进定时任务的 `len(rep.Errors) > 0` 判断之外，看起来像「跑完了、没发现任何东西」。
func TestDelegatePipeline_UnreachableRemoteSurfacesError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := ts.URL // 先关掉再连，确定性优于碰运气
	ts.Close()

	rep := srvWithURL(dead).delegatePipeline(t.Context())
	if len(rep.Errors) == 0 {
		t.Fatalf("unreachable remote must surface an error, got clean report %+v", rep)
	}
	if !strings.Contains(rep.Errors[0], "delegate:") {
		t.Errorf("error should be prefixed with `delegate:`, got %q", rep.Errors[0])
	}
}
