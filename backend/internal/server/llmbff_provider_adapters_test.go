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

// TestDynamicGatewayStreamFinalCandidateGetsFullRemainingBudget 最终候选
// 不叠加尝试级超时：首位候选挂死耗掉一个尝试窗后，最终候选慢于尝试窗但
// 在整链剩余预算内出首 token，应成功而非被 20s（测试中 100ms）尝试窗误杀
// （§21.6 #2 / §22.4：kimi-k3 长 prompt 首 token >20s，旧逻辑整链终态失败）。
func TestDynamicGatewayStreamFinalCandidateGetsFullRemainingBudget(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 2*time.Second
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			<-r.Context().Done() // 首位候选挂死：耗掉一个尝试窗后触发回退
			return
		}
		if req.Model != "model-b" {
			t.Errorf("retry attempt model = %q, want model-b", req.Model)
		}
		time.Sleep(500 * time.Millisecond) // 慢于尝试窗(100ms)、快于剩余预算
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"slow-but-alive\"}}]}\n\n"+
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
	start := time.Now()
	usage, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(d llmbff.Delta) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v (elapsed %s)", err, time.Since(start).Round(time.Millisecond))
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
	if got[1].Content != "slow-but-alive" {
		t.Errorf("second delta content = %q, want slow-but-alive", got[1].Content)
	}
	if last := got[len(got)-1]; !last.Done || last.FinishReason != "stop" {
		t.Errorf("final delta = %+v, want done + finish_reason=stop", last)
	}
}

// TestDynamicGatewayStreamFinalCandidateStillBoundedByChainBudget 最终候选
// 免尝试超时后仍受整链预算兜底：唯一候选无限挂死时，错误必须在整链预算
// 处浮出（晚于尝试窗、远早于无限期），不得因放宽而悬挂。
func TestDynamicGatewayStreamFinalCandidateStillBoundedByChainBudget(t *testing.T) {
	oldAttempt, oldBudget := autoFallbackAttemptTimeout, autoFallbackTotalBudget
	autoFallbackAttemptTimeout, autoFallbackTotalBudget = 100*time.Millisecond, 600*time.Millisecond
	defer func() { autoFallbackAttemptTimeout, autoFallbackTotalBudget = oldAttempt, oldBudget }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 必须先消费 body：net/http 服务端在 body 读到 EOF 后才启动
		// background read 感知客户端断连；不读 body 则客户端超时断开后
		// r.Context() 永不触发，handler 与下面的 srv.Close() 全部挂死。
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		<-r.Context().Done() // 唯一候选无限挂死
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"only-model"},
		}
	})

	start := time.Now()
	_, err := p.Stream(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "auto",
	}, func(llmbff.Delta) bool { return true })
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Stream returned nil error, want chain-budget deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
	if elapsed < 600*time.Millisecond {
		t.Fatalf("Stream returned in %s, want >= chain budget 600ms (attempt window must not short-circuit final candidate)",
			elapsed.Round(time.Millisecond))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Stream returned in %s, want ~chain budget 600ms (must stay bounded)", elapsed.Round(time.Millisecond))
	}
}

// TestIsModelUnavailableErrorGatewayShapes 网关侧错误形状改版回归：
// 2026-09-05 下午实测显式 kimi-k3（无 provider）chat 503 直接透传、不回退——
// 网关对无 provider 模型的 code 已从 "no_candidate" 改为 "model_not_found"、
// kind 用复数 "no_candidates"，旧匹配两形态都不认识。三个新形状必须视为
// 「该 model 无货」触发 preferred 回退。
func TestIsModelUnavailableErrorGatewayShapes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"503 model_not_found + kind no_candidates (2026-09-05 observed)", errors.New(
			`llm-gateway chat 503: {"error":{"code":"model_not_found","gateway_debug":{"attempts":null,"kind":"","retryable":false,"stage":"execution","tried":0},"kind":"no_candidates","message":"No available provider for model 'kimi-k3'. All 0 candidates failed.","request_id":"x","type":"server_error"}}`,
		), true},
		{"code model_not_found without kind", errors.New(
			`llm-gateway chat 503: {"error":{"code":"model_not_found","message":"nope"}}`,
		), true},
		{"kind no_candidates with other code", errors.New(
			`llm-gateway chat 503: {"error":{"code":"whatever","kind":"no_candidates"}}`,
		), true},
		{"unrelated error keeps old behavior", errors.New(
			`llm-gateway chat 503: {"error":{"code":"rate_limit_exceeded","kind":"rate_limit_error"}}`,
		), false},
	}
	for _, tc := range cases {
		if got := isModelUnavailableError(tc.err); got != tc.want {
			t.Errorf("%s: isModelUnavailableError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDynamicGatewayChatFallsBackOnModelNotFound 显式 model 无 provider
// （网关新版 503 model_not_found/no_candidates 形状）时 Chat 路径同样回退
// 到 preferred 下一候选，而不是把错误直接透传（2026-09-05 16:20 实测缺口）。
func TestDynamicGatewayChatFallsBackOnModelNotFound(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			if req.Model != "model-a" {
				t.Errorf("first attempt model = %q, want model-a", req.Model)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"code":"model_not_found","kind":"no_candidates",`+
				`"message":"No available provider for model 'model-a'. All 0 candidates failed.","request_id":"t","type":"server_error"}}`)
			return
		}
		if req.Model != "model-b" {
			t.Errorf("fallback attempt model = %q, want model-b", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"pong"}}],`+
			`"model":"model-b","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"model-a", "model-b"},
		}
	})

	resp, err := p.Chat(context.Background(), llmbff.ChatRequest{
		WorkspaceID: "ws",
		Model:       "model-a",
		Messages:    []llmbff.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Content != "pong" {
		t.Fatalf("content = %q, want pong", resp.Content)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2", n)
	}
}

// TestDynamicGatewayStreamFallsBackOnEmptyStream 首位候选 200 干净关闭但零
// delta（只回 [DONE]，§26.4「glm 空回复」的流式透传形态）：按候选失败处理，
// 换下一候选重试并给出 Retry 进度帧，而不是把「200+零帧」透传给前端
// （2026-09-05 真机纪要空流复现）。
func TestDynamicGatewayStreamFallsBackOnEmptyStream(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			if req.Model != "model-a" {
				t.Errorf("first attempt model = %q, want model-a", req.Model)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if req.Model != "model-b" {
			t.Errorf("fallback attempt model = %q, want model-b", req.Model)
		}
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
	if first := got[0]; first.Retry != "model-b" || first.Content != "" || first.Done {
		t.Errorf("first delta = %+v, want retry=model-b, no content, not done", first)
	}
	if got[1].Content != "hello" {
		t.Errorf("second delta content = %q, want hello", got[1].Content)
	}
}

// TestDynamicGatewayStreamFinalCandidateEmptyStreamIsError 单一最终候选空流：
// 无候选可换，必须以 errEmptyStreamAttempt 结构化错误收尾（handler 据此落
// 错误终态帧），而不是 err==nil 静默零帧成功。
func TestDynamicGatewayStreamFinalCandidateEmptyStreamIsError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewDynamicLLMGatewayBFFProvider(func(string) GatewayConfig {
		return GatewayConfig{
			BaseURL:         srv.URL,
			APIKey:          "k",
			PreferredModels: []string{"only-model"},
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
	if !errors.Is(err, errEmptyStreamAttempt) {
		t.Fatalf("err = %v, want errEmptyStreamAttempt", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1 (final candidate, no fallback)", n)
	}
	if len(got) != 0 {
		t.Fatalf("fn got %d deltas, want 0: %+v", len(got), got)
	}
	if usage == nil {
		t.Fatal("usage = nil, want non-nil zero usage")
	}
}
