package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// missingEndpointGateway 对两种转写形态都回 404，并附带一段**上游风格的 JSON 响应体**。
// 那段 body 是本测试的关键：它模拟真实网关「没开这个端点」时的返回，
// 正是它会被 describeProbe 拼进「探测失败(...)」甩到设置页上的那种内容。
type missingEndpointGateway struct {
	t *testing.T
}

func (g missingEndpointGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/audio/transcriptions", "/v1/chat/completions":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"no_candidate","message":"no upstream available","choices":[]}}`))
	default:
		g.t.Errorf("unexpected probe path %s", r.URL.Path)
		http.NotFound(w, r)
	}
}

// TestProbeClassifiesBothTransportsMissing 网关两种传输形态都不存在时，
// 必须判 endpoint_missing 而不是 failed。
//
// 这条守的是 2026-10-01 审计发现的真实缺陷：ProbeEndpointMissing 曾是死常量
// （声明了、也给它配了中文文案，但生产代码从不赋值），于是「网关没开转写端点」
// 全部落到 ProbeFailed。server_stt_settings.go 的 describeProbe 对 ProbeFailed
// 拼的是 "探测失败(" + Detail + ")"，而 Detail 里带着上游原始响应体，
// 于是设置页直接把这坨 JSON 甩给用户。
func TestProbeClassifiesBothTransportsMissing(t *testing.T) {
	srv := httptest.NewServer(missingEndpointGateway{t: t})
	defer srv.Close()

	c := ProbeModel(context.Background(), srv.Client(), srv.URL+"/v1", "key", "no-asr-here")
	if c.Status != ProbeEndpointMissing {
		t.Fatalf("status=%s want %s（detail=%s）", c.Status, ProbeEndpointMissing, c.Detail)
	}
	if c.Usable() {
		t.Error("没有转写端点的网关不能被判为可用")
	}
	// Detail 会进设置页文案，绝不能是上游原始响应体。
	if strings.Contains(c.Detail, "{") || strings.Contains(c.Detail, "choices") || strings.Contains(c.Detail, "no_candidate") {
		t.Errorf("Detail 泄漏了上游原始响应体：%q", c.Detail)
	}
}

// TestProbeNoProviderIsNotEndpointMissing 反向护栏：503 no_candidate
// 表示「网关列了模型但没有可用上游」，与「端点不存在」是两回事。
//
// 它守的是上一条修复的边界——判定条件必须是 404/405，
// 不能顺手把 no_provider 也并进去，否则设置页会把「换个模型」的建议
// 换成「网关没开转写端点」，把用户引向错误的修复方向。
func TestProbeNoProviderIsNotEndpointMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"no_candidate","message":"no upstream available"}}`))
	}))
	defer srv.Close()

	c := ProbeModel(context.Background(), srv.Client(), srv.URL+"/v1", "key", "listed-but-dead")
	if c.Status != ProbeNoProvider {
		t.Fatalf("status=%s want %s（detail=%s）", c.Status, ProbeNoProvider, c.Detail)
	}
	if c.Usable() {
		t.Error("no_candidate 不能被判为可用")
	}
}
