package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

// realGatewayAnthropicParallelToolSSE 是 Anthropic 形态的**并发工具调用**帧。
//
// 2026-10-06 真网关复测修的是 OpenAI 形态下 index 丢失导致的并发串台；
// 但同一类缺陷在 anthropic-messages 形态下同样存在，而 `anthropic.go`
// 里的 `Index: payload.Index` 正是本轮**新加**的一行 —— 没有它，
// 并发的两个 tool_use 块会都落到 index 0。
//
// 注意 content_block 的下标语义：text 块会占用下标，所以
// 「第二个 tool_use 的 index」不一定是 1（前面有 text 就是 2）。
// 这道门刻意把第 0 块留成 text，检验分组用的是 content_block 真实下标
// 而不是「第几个 tool」。
var realGatewayAnthropicParallelToolSSE = strings.Join([]string{
	`event: message_start
data: {"type":"message_start","message":{"id":"msg_probe","model":"deepseek-v4-flash","usage":{"input_tokens":10}}}`,
	`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
	`event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_a","name":"search_notes","input":{}}}`,
	`event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\": \"北极星项目续期合同\"}"}}`,
	`event: content_block_stop
data: {"type":"content_block_stop","index":1}`,
	`event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_b","name":"search_notes","input":{}}}`,
	`event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\": \"评审会客户会议室\"}"}}`,
	`event: content_block_stop
data: {"type":"content_block_stop","index":2}`,
	`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
	`event: message_stop
data: {"type":"message_stop"}`,
	"",
}, "\n\n")

// TestAdapterPreservesIndexOnAnthropicPath 是上面那道 OpenAI 门在
// anthropic-messages 形态下的对偶：同样用真实帧驱动真实客户端全链。
func TestAdapterPreservesIndexOnAnthropicPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("请求路径=%q，期望 /v1/messages", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(realGatewayAnthropicParallelToolSSE))
	}))
	defer srv.Close()

	c := llmgateway.NewClient(srv.URL, "test-key")
	c.Client = srv.Client()
	c.Format = "anthropic-messages"
	p := &llmGatewayBFFProvider{client: c}

	idx := map[int]string{}
	argFrag := map[int]string{}
	sawTerminal := false
	_, err := p.Stream(context.Background(), llmbff.ChatRequest{
		Messages: []llmbff.Message{{Role: "user", Content: "查一下我的笔记"}},
		Tools: []llmbff.Tool{{
			Type: "function",
			Function: llmbff.ToolFunction{
				Name:       "search_notes",
				Parameters: map[string]interface{}{"type": "object"},
			},
		}},
	}, func(d llmbff.Delta) bool {
		// 终态帧：消费方的回调契约是「增量帧 + 一帧终态」，usage 靠它。
		// 不发这一帧 ⇒ anthropic 形态下 token 用量恒为 0（2026-10-06 修）。
		if d.FinishReason != "" || (d.Usage != nil && d.Usage.TotalTokens > 0) {
			sawTerminal = true
		}
		for _, tc := range d.ToolCalls {
			if tc.Function.Name != "" {
				idx[tc.Index] = tc.Function.Name
			}
			argFrag[tc.Index] += tc.Function.Arguments
		}
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !sawTerminal {
		t.Error("没有收到终态帧：anthropic 形态下 usage 会恒为 0，计费与预算统计全部失真")
	}

	// 两个 tool_use 块必须落在**不同**下标（第 0 块是 text，被跳过）。
	if len(idx) != 2 {
		t.Fatalf("收到的 index 集合=%v，期望两个（1 与 2）：%v", keysOf(idx), idx)
	}
	for k, got := range idx {
		if got != "search_notes" {
			t.Errorf("index %d 的工具名=%q，期望 search_notes", k, got)
		}
	}
	if argFrag[1] != `{"query": "北极星项目续期合同"}` {
		t.Errorf("index 1 的参数=%q", argFrag[1])
	}
	if argFrag[2] != `{"query": "评审会客户会议室"}` {
		t.Errorf("index 2 的参数=%q", argFrag[2])
	}
}

// TestAnthropicToolCallIDSurvives 钉住 tool_call_id：Anthropic 用
// `toolu_xxx` 而不是 OpenAI 的 `call_xxx`，而它是 tool 消息与 assistant
// 消息配对的唯一依据（下游要把它回填进 toolCall_id）。
func TestAnthropicToolCallIDSurvives(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(realGatewayAnthropicParallelToolSSE))
	}))
	defer srv.Close()

	c := llmgateway.NewClient(srv.URL, "test-key")
	c.Client = srv.Client()
	c.Format = "anthropic-messages"
	p := &llmGatewayBFFProvider{client: c}

	ids := map[string]bool{}
	_, err := p.Stream(context.Background(), llmbff.ChatRequest{
		Messages: []llmbff.Message{{Role: "user", Content: "查一下我的笔记"}},
	}, func(d llmbff.Delta) bool {
		for _, tc := range d.ToolCalls {
			if tc.ID != "" {
				ids[tc.ID] = true
			}
		}
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !ids["toolu_a"] || !ids["toolu_b"] {
		t.Errorf("tool_use id 丢失：收到 %v，期望同时含 toolu_a 与 toolu_b", keysAny(ids))
	}
}

func keysAny(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
