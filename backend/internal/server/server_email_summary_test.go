package server

// 单封邮件按需总结的回归测试（2026-10-01 需求）。
//
// 覆盖两件最容易出错的事：
//  1. **幂等** —— 已有摘要必须直接返回、且不调 LLM（需求：总结后不需要再总结）。
//  2. **解析健壮性** —— 模型输出常带 Markdown 围栏、前后寒暄，不能因此失败。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGatewaySummary_PlainJSON(t *testing.T) {
	got := parseGatewaySummary(`{"summary":"9 月对账单已出，金额 ¥1280，请核对。"}`)
	if got != "9 月对账单已出，金额 ¥1280，请核对。" {
		t.Fatalf("summary = %q", got)
	}
}

func TestParseGatewaySummary_StripsCodeFence(t *testing.T) {
	got := parseGatewaySummary("```json\n{\"summary\":\"发票已开具\"}\n```")
	if got != "发票已开具" {
		t.Fatalf("应剥掉 ``` 围栏，实际 %q", got)
	}
}

func TestParseGatewaySummary_IgnoresSurroundingChatter(t *testing.T) {
	// 模型爱在 JSON 前后加话；只取 {} 之间。
	got := parseGatewaySummary("好的，以下是摘要：\n{\"summary\":\"订单已发货\"}\n希望有帮助！")
	if got != "订单已发货" {
		t.Fatalf("应忽略 JSON 之外的寒暄，实际 %q", got)
	}
}

func TestParseGatewaySummary_TruncatesOverlongSummary(t *testing.T) {
	long := strings.Repeat("很长的摘要内容", 100)
	got := parseGatewaySummary(`{"summary":"` + long + `"}`)
	if len([]rune(got)) > 200 {
		t.Fatalf("超长摘要应被截断到 200 字以内，实际 %d 字", len([]rune(got)))
	}
}

func TestParseGatewaySummary_RejectsGarbage(t *testing.T) {
	// 没有 JSON 时必须返回空串（调用方据此报错），不能把噪声当摘要存进库。
	for _, in := range []string{"", "   ", "抱歉，我无法总结这封邮件。", "{不是合法JSON"} {
		if got := parseGatewaySummary(in); got != "" {
			t.Errorf("输入 %q 应解析失败，实际得到 %q", in, got)
		}
	}
}

// 幂等判定：已有摘要必须复用、不再调 LLM（需求：总结后不需要再总结）。
func TestReusableSummary_SkipsWhenAlreadySummarized(t *testing.T) {
	got, ok := reusableSummary("已生成过的摘要")
	if !ok {
		t.Fatal("已有摘要时必须判定为可复用（不再总结）")
	}
	if got != "已生成过的摘要" {
		t.Fatalf("summary = %q", got)
	}
	// 首尾空白不应被判成「已总结」，否则展示出来是空白。
	if _, ok := reusableSummary("   \n\t  "); ok {
		t.Error("纯空白摘要应判定为未总结")
	}
	if _, ok := reusableSummary(""); ok {
		t.Error("空摘要应判定为未总结")
	}
	// 复用时应去掉首尾空白，避免前端多出一段空行。
	if got, _ := reusableSummary("  有摘要  "); got != "有摘要" {
		t.Errorf("复用时应 trim，实际 %q", got)
	}
}

func TestHandleEmailSummarize_RejectsNonPOST(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/emails/em-1/summarize", nil)
	srv.handleEmailSummarize(rec, req, "em-1")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应被拒绝，实际 %d", rec.Code)
	}
}

// emailStore 未配置时必须 503，不能 panic。
func TestHandleEmailSummarize_WithoutStoreReturns503(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/emails/em-1/summarize", nil)
	srv.handleEmailSummarize(rec, req, "em-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("emailStore 未配置时应 503，实际 %d", rec.Code)
	}
}
