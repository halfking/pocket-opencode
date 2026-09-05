package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

func TestIsNoCandidateError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error without json", errors.New("connection refused"), false},
		{"empty 503 with no body", errors.New("llm-gateway stream 503: "), false},
		{"503 no_candidate body", errors.New(
			`llm-gateway stream 503: {"error":{"code":"no_candidate","kind":"no_candidate","message":"No available provider for model 'claude-sonnet-4.5'","request_id":"abc","type":"server_error"}}`,
		), true},
		{"503 different error code", errors.New(
			`llm-gateway stream 503: {"error":{"code":"rate_limit","kind":"rate_limit","message":"slow down"}}`,
		), false},
		{"401 unauthorized body", errors.New(
			`llm-gateway stream 401: {"error":{"code":"unauthorized","message":"bad key"}}`,
		), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoCandidateError(tc.err); got != tc.want {
				t.Errorf("isNoCandidateError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPickFallbackModel(t *testing.T) {
	cases := []struct {
		name      string
		original  string
		preferred []string
		catalog   []string
		want      string
	}{
		{"empty preferred", "auto", nil, []string{"a", "b"}, ""},
		{"first matches original, second is fallback", "auto",
			[]string{"auto-skip", "claude-sonnet-5"},
			[]string{"claude-sonnet-5", "glm-5.2"}, "claude-sonnet-5"},
		{"original matches preferred[0], skip it", "minimax-m3",
			[]string{"minimax-m3", "claude-sonnet-5", "glm-5.2"},
			[]string{"claude-sonnet-5", "glm-5.2"}, "claude-sonnet-5"},
		{"preferred not in catalog filtered out", "auto",
			[]string{"not-in-catalog-1", "claude-sonnet-5"},
			[]string{"claude-sonnet-5"}, "claude-sonnet-5"},
		{"all preferred not in catalog", "auto",
			[]string{"x", "y"},
			[]string{"a", "b"}, ""},
		{"empty catalog allows all preferred", "auto",
			[]string{"claude-sonnet-5", "glm-5.2"}, nil, "claude-sonnet-5"},
		{"skip empty entries", "auto",
			[]string{"", "claude-sonnet-5"},
			[]string{"claude-sonnet-5"}, "claude-sonnet-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickFallbackModel(tc.original, tc.preferred, tc.catalog)
			if got != tc.want {
				t.Errorf("pickFallbackModel(%q, %v, %v) = %q, want %q",
					tc.original, tc.preferred, tc.catalog, got, tc.want)
			}
		})
	}
}

// fakeNoCandidateErr 用来在其它需要 error 的场景里复用现成的 error 字符串。
func fakeNoCandidateErr() error {
	return fmt.Errorf("llm-gateway stream 503: {\"error\":{\"code\":\"no_candidate\",\"kind\":\"no_candidate\",\"message\":\"x\"}}")
}

// TestDynamicGatewayStreamEmitsRetryProgressFrame 复现 auto 回退链：fake 上游
// 第一次返回 503 no_candidate，第二次（已切到 model-b）成功流式返回。断言
// fn 在候选切换间隙收到一帧 Retry 进度（无 content、非终态），随后正文与
// 终态帧正常到达、整条流成功结束。
func TestDynamicGatewayStreamEmitsRetryProgressFrame(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"code":"no_candidate","kind":"no_candidate",`+
				`"message":"No available provider for model 'model-a'","request_id":"t","type":"server_error"}}`)
			return
		}
		if req.Model != "model-b" {
			t.Errorf("retry attempt model = %q, want model-b", req.Model)
		}
		// 第二次尝试：OpenAI SSE 形状的两帧正文 + [DONE]。
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b"},
		}
	})

	var got []llmbff.Delta
	usage, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if usage == nil || usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, want total_tokens=3", usage)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2", n)
	}
	if len(got) < 3 {
		t.Fatalf("fn got %d deltas, want >= 3: %+v", len(got), got)
	}
	// 第一帧必须是 Retry 进度帧（切换到 model-b），其后才是正文与终态。
	if first := got[0]; first.Retry != "model-b" || first.Content != "" || first.Done {
		t.Errorf("first delta = %+v, want retry=model-b, no content, not done", first)
	}
	if got[1].Content != "hello" {
		t.Errorf("second delta content = %q, want hello", got[1].Content)
	}
	if last := got[len(got)-1]; !last.Done || last.FinishReason != "stop" {
		t.Errorf("final delta = %+v, want done + finish_reason=stop", last)
	}
}

// TestDynamicGatewayStreamFallsBackOnAttemptDeadline 首位候选在流式路径上
// 挂死（不返回响应头，直到客户端超时断开）：尝试级 20s 超时产生
// context.DeadlineExceeded，旧逻辑不属于 isModelUnavailableError、整链直接
// 报错，auto 永远到不了可用候选（2026-09-05 实测：glm-5.2 无 provider 时
// chat 600ms 快速失败可回退、stream 挂满 20s 后直接终态）。修复后：超时
// 且未写出正文 → 发 Retry 帧换下一候选，第二候选成功则整流成功。
func TestDynamicGatewayStreamFallsBackOnAttemptDeadline(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 10*time.Second
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			<-r.Context().Done() // 模拟上游挂死：直到客户端超时断开都不响应
			return
		}
		if req.Model != "model-b" {
			t.Errorf("retry attempt model = %q, want model-b", req.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"after-deadline\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b"},
		}
	})

	var got []llmbff.Delta
	usage, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("usage = %+v, want total_tokens=2", usage)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2", n)
	}
	if first := got[0]; first.Retry != "model-b" || first.Content != "" || first.Done {
		t.Errorf("first delta = %+v, want retry=model-b, no content, not done", first)
	}
	if last := got[len(got)-1]; !last.Done || last.FinishReason != "stop" {
		t.Errorf("final delta = %+v, want done + finish_reason=stop", last)
	}
}

// TestDynamicGatewayStreamNoFallbackAfterContent 首位候选已写出正文后才
// 挂死超时：换候选重试会在同一气泡里重复作答，必须原样上抛错误、不再回退。
func TestDynamicGatewayStreamNoFallbackAfterContent(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 10*time.Second
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done() // 正文后挂死直到客户端超时
			return
		}
		t.Error("second attempt must not happen after content was streamed")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b"},
		}
	})

	var got []llmbff.Delta
	_, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err == nil {
		t.Fatal("Stream returned nil error, want deadline error after partial content")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1 (no fallback after content)", n)
	}
	for _, d := range got {
		if d.Retry != "" {
			t.Errorf("unexpected retry frame after content: %+v", d)
		}
	}
}

// TestDynamicGatewayStreamNoFallbackAfterDoneFrame 首位候选已发出 finish/usage
// 终态帧后才挂死（未发 [DONE]）：SSE 解析仍会阻塞到尝试级超时，但客户端已
// 收到完整作答，换候选重试会在同一气泡里重复作答，必须原样上抛错误、不再回退。
func TestDynamicGatewayStreamNoFallbackAfterDoneFrame(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 10*time.Second
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
					"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done() // 终态帧后挂死直到客户端超时
			return
		}
		t.Error("second attempt must not happen after a done frame was streamed")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b"},
		}
	})

	var got []llmbff.Delta
	_, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err == nil {
		t.Fatal("Stream returned nil error, want deadline error after done frame")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1 (no fallback after done frame)", n)
	}
	for _, d := range got {
		if d.Retry != "" {
			t.Errorf("unexpected retry frame after done frame: %+v", d)
		}
	}
}

// TestDynamicGatewayStreamChainVisitsEachCandidateOnce 前两个候选都挂死超时、
// 第三个成功：链必须按 preferred 顺序走到第三个候选，且不得重访已试过的
// 候选（旧逻辑只排除 current，会 glm-5.2 → minimax-m3 → glm-5.2 成环，
// 2026-09-05 实测两轮 fallback 日志均指向已试过的 glm-5.2）。
func TestDynamicGatewayStreamChainVisitsEachCandidateOnce(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 10*time.Second
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	var mu atomic.Int32
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := mu.Add(1)
		seen[req.Model]++
		if req.Model != "model-a" && req.Model != "model-b" {
			// 第三候选：成功返回
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{\"content\":\"third\"}}]}\n\n"+
					"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
					"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		_ = n
		<-r.Context().Done() // model-a / model-b 挂死直到客户端超时
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b", "model-c"},
		}
	})

	var got []llmbff.Delta
	usage, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("usage = %+v, want total_tokens=2", usage)
	}
	if seen["model-a"] != 1 || seen["model-b"] != 1 {
		t.Fatalf("candidate visits = %v, want model-a×1 model-b×1 (no revisit)", seen)
	}
	if seen["model-c"] != 1 {
		t.Fatalf("model-c visits = %d, want 1", seen["model-c"])
	}
	if len(got) < 4 {
		t.Fatalf("fn got %d deltas, want >= 4: %+v", len(got), got)
	}
	if got[0].Retry != "model-b" {
		t.Errorf("first delta retry = %q, want model-b", got[0].Retry)
	}
	if got[1].Retry != "model-c" {
		t.Errorf("second delta retry = %q, want model-c", got[1].Retry)
	}
	if got[2].Content != "third" {
		t.Errorf("content delta = %q, want third", got[2].Content)
	}
	if last := got[len(got)-1]; !last.Done || last.FinishReason != "stop" {
		t.Errorf("final delta = %+v, want done + finish_reason=stop", last)
	}
}

// TestIsModelUnavailableError invalid_model（HTTP 400，模型 id 不存在/未上架）
// 与 no_candidate 一样应视为「该 model 无货」并触发 preferred 回退
// （2026-09-05 E2E 实测：preferred 首位设为不存在模型时网关回 400 invalid_model）。
func TestIsModelUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error without json", errors.New("connection refused"), false},
		{"400 invalid_model body", errors.New(
			`llm-gateway stream 400: {"error":{"code":"invalid_model","message":"Model 'no-such-model-e2e' not found","request_id":"t"}}`,
		), true},
		{"503 no_candidate body", fakeNoCandidateErr(), true},
		{"503 rate_limit body", errors.New(
			`llm-gateway stream 503: {"error":{"code":"rate_limit","message":"slow down"}}`,
		), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isModelUnavailableError(tc.err); got != tc.want {
				t.Errorf("isModelUnavailableError = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDynamicGatewayStreamFallsBackOnInvalidModel 同
// TestDynamicGatewayStreamEmitsRetryProgressFrame，但首试错误是 400
// invalid_model（非 503 no_candidate）——回退链必须同样触发 Retry 帧。
func TestDynamicGatewayStreamFallsBackOnInvalidModel(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":"invalid_model",`+
				`"message":"Model 'no-such-model-e2e' not found","request_id":"t"}}`)
			return
		}
		if req.Model != "model-b" {
			t.Errorf("retry attempt model = %q, want model-b", req.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"no-such-model-e2e", "model-b"},
		}
	})

	var got []llmbff.Delta
	usage, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("usage = %+v, want total_tokens=2", usage)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2", n)
	}
	if first := got[0]; first.Retry != "model-b" || first.Content != "" || first.Done {
		t.Errorf("first delta = %+v, want retry=model-b, no content, not done", first)
	}
	if last := got[len(got)-1]; !last.Done || last.FinishReason != "stop" {
		t.Errorf("final delta = %+v, want done + finish_reason=stop", last)
	}
}
