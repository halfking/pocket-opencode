package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newAnthropicStub 起一个 /v1/messages 桩：断言请求形态（x-api-key、
// anthropic-version、顶层 system），返回固定 content 块。
func newAnthropicStub(t *testing.T, check func(r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		check(r, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_1", "model": "claude-x", "role": "assistant",
			"content": [{"type": "text", "text": "你好，译文"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 11, "output_tokens": 7}
		}`)
	}))
}

func TestChatViaMessagesTranslatesRequestAndResponse(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion string
	srv := newAnthropicStub(t, func(r *http.Request, body map[string]any) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if _, hasAuth := r.Header["Authorization"]; !hasAuth {
			t.Errorf("Authorization header missing")
		}
		if body["system"] != "你是翻译助手" {
			t.Errorf("system not hoisted: %v", body["system"])
		}
		msgs, _ := body["messages"].([]any)
		if len(msgs) != 1 {
			t.Fatalf("want 1 message (system hoisted out), got %d", len(msgs))
		}
		m0 := msgs[0].(map[string]any)
		if m0["role"] != "user" {
			t.Errorf("message role = %v, want user", m0["role"])
		}
		if mt, ok := body["max_tokens"].(float64); !ok || mt <= 0 {
			t.Errorf("max_tokens must be positive, got %v", body["max_tokens"])
		}
	})
	defer srv.Close()

	c := NewClient(srv.URL, "sk-test")
	c.Format = "anthropic-messages"
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model: "claude-x",
		Messages: []ChatMessage{
			{Role: "system", Content: "你是翻译助手"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %s, want /v1/messages", gotPath)
	}
	if gotAPIKey != "sk-test" || gotVersion != anthropicVersion {
		t.Errorf("auth headers = %q / %q", gotAPIKey, gotVersion)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "你好，译文" {
		t.Errorf("content not mapped: %+v", resp.Choices)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Errorf("usage not mapped: %+v", resp.Usage)
	}
}

func TestChatFallsBackToAnthropicOnChatCompletions404(t *testing.T) {
	// 网关形态复刻 llm.kxpms.cn：/v1/chat/completions 不可用（404），
	// /v1/messages 正常。
	var anthropicHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		anthropicHits++
		fmt.Fprint(w, `{"id":"m2","model":"claude-x","content":[{"type":"text","text":"回退成功"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "sk-test")
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "claude-x",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat with fallback: %v", err)
	}
	if anthropicHits != 1 {
		t.Fatalf("anthropic endpoint hits = %d, want 1", anthropicHits)
	}
	if resp.Choices[0].Message.Content != "回退成功" {
		t.Errorf("fallback content = %q", resp.Choices[0].Message.Content)
	}
	// 进程级形态记忆：第二个 client 实例（模拟下一次请求新建）应直连 anthropic，
	// 不再打 chat/completions。
	c2 := NewClient(srv.URL, "sk-test")
	if _, err := c2.Chat(context.Background(), ChatRequest{Model: "claude-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("second client Chat: %v", err)
	}
	if anthropicHits != 2 {
		t.Errorf("after sticky discovery, hits = %d, want 2 (no retry on chat/completions)", anthropicHits)
	}
}

func TestChatDoesNotFallbackOnAuthError(t *testing.T) {
	var anthropicHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"invalid_key"}}`, http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		anthropicHits++
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "bad-key")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("want error for 401")
	}
	if anthropicHits != 0 {
		t.Errorf("anthropic fallback fired on auth error (%d hits), want 0", anthropicHits)
	}
}

func TestStreamViaMessagesParsesNamedEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":9}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"，世界"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "sk-test")
	c.Format = "anthropic-messages"
	var parts []string
	final, err := c.Stream(context.Background(), ChatRequest{
		Model: "claude-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(d StreamDelta) bool {
		if d.Content != "" {
			parts = append(parts, d.Content)
		}
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := strings.Join(parts, ""); got != "你好，世界" {
		t.Errorf("stream content = %q", got)
	}
	if !final.Done || final.FinishReason != "stop" {
		t.Errorf("final = %+v, want done+stop", final)
	}
	if final.PromptTokens != 9 || final.CompletionTokens != 4 || final.TotalTokens != 13 {
		t.Errorf("stream usage = %+v", final)
	}
}

func TestShouldFallbackToAnthropic(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"llm-gateway chat 404: not found", true},
		{"llm-gateway stream 405: method not allowed", true},
		{"llm-gateway chat: context deadline exceeded", true},
		{"llm-gateway stream: connection reset", true},
		{"llm-gateway chat 401: invalid key", false},
		{"llm-gateway chat 429: rate limited", false},
		{"llm-gateway chat 503: no_candidate", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.msg != "" {
			err = fmt.Errorf("%s", tc.msg)
		}
		if got := shouldFallbackToAnthropic(err); got != tc.want {
			t.Errorf("shouldFallbackToAnthropic(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
