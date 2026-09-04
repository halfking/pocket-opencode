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
