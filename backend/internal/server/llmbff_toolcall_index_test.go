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

// realGatewayParallelToolSSE 是 2026-10-06 在 llmgo.kxpms.cn 上**实测抓到的原始分片**
// （model=deepseek-v4-flash），逐字照抄，只删掉了与工具调用无关的 reasoning_content 帧。
//
// 关键形态（也是旧实现栽的地方）：
//   - 一轮里**并发两个**工具调用，各带独立 id：call_5zy4…（index 0）/ call_gxg…（index 1）
//   - id 是 `call_<hex>`，**没有数字下标** ⇒ 从 id 猜下标恒得 0
//   - 工具名只出现在每个调用的**首帧**，后续帧 name 为空、arguments 逐字增量
var realGatewayParallelToolSSE = strings.Join([]string{
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_5zy4c6vk9eds203sq9miyn97","type":"function","function":{"name":"search_notes","arguments":""}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"","type":"","function":{"name":"","arguments":"{\"query\": \""}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"","type":"","function":{"name":"","arguments":"北极星项目续期合同"}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"","type":"","function":{"name":"","arguments":"\"}"}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_gxgypmgx0s5ie8asaf32oqzm","type":"function","function":{"name":"search_notes","arguments":""}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"","type":"","function":{"name":"","arguments":"{\"query\": \"评审会客户会议室\"}"}}]},"finish_reason":null,"index":0}],"model":"deepseek-v4-flash","object":"chat.completion.chunk"}`,
	`data: {"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"tool_calls"}],"model":"deepseek-v4-flash","object":"chat.completion.chunk","usage":{"prompt_tokens":363,"completion_tokens":80,"total_tokens":443}}`,
	`data: [DONE]`,
	"",
}, "\n\n")

// TestAdapterPreservesWireToolCallIndex 钉住「adapter 必须把 wire 上的 index 传下去」。
//
// ⚠ 这道门补的是一个**真实的覆盖盲区**，不是走过场：
// 把 llmbff_provider_adapters.go 里的 `Index: tc.Index` 改成 `Index: 0`，
// meetingagent 包的全部单测**仍然全绿** —— 因为 agent 包直接构造
// llmbff.Delta，根本不经过 adapter。传输层那一跳当时零覆盖，
// 于是「Index 在这一跳被丢掉」这种缺陷可以毫无阻力地合进来。
//
// 现在用真实抓包帧驱动真实的 llmgateway.Client → adapter → llmbff.Delta 全链，
// 断言两个并发调用的 Index 分别是 0 和 1。
func TestAdapterPreservesWireToolCallIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("请求路径=%q，期望 /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(realGatewayParallelToolSSE))
	}))
	defer srv.Close()

	c := llmgateway.NewClient(srv.URL, "test-key")
	c.Client = srv.Client()
	p := &llmGatewayBFFProvider{client: c}

	idx := map[int]string{} // index → 工具名
	argFrag := map[int]string{}

	// ⚠ 这里必须按「增量分片」语义累加，不能直接覆盖：adapter 传的是
	// 网关的原始帧，同一个 index 的工具名只出现在**首帧**，后续帧 name 为空
	// （只有 arguments 在长）。直接覆盖会把首帧的名字抹成空串 ——
	// 量具自己写错，量出来的是「没有名字」而不是产品缺陷。

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

	if len(idx) != 2 {
		t.Fatalf("收到的 index 集合=%v，期望两个（0 与 1）：%v", keysOf(idx), idx)
	}
	if got := idx[0]; got != "search_notes" {
		t.Errorf("index 0 的工具名=%q，期望 search_notes", got)
	}
	if got := idx[1]; got != "search_notes" {
		t.Errorf("index 1 的工具名=%q，期望 search_notes", got)
	}
	if argFrag[0] != `{"query": "北极星项目续期合同"}` {
		t.Errorf("index 0 的参数=%q", argFrag[0])
	}
	if argFrag[1] != `{"query": "评审会客户会议室"}` {
		t.Errorf("index 1 的参数=%q", argFrag[1])
	}
}

func keysOf(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
