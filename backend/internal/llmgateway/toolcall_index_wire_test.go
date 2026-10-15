// toolcall_index_wire_test.go —— 用**真实抓包帧**驱动 SSE 解析器的回归门。
//
// ── 这条门为什么以前不存在 ──
//
// 设计文档 §11.4 记过一个真实 bug：一轮里并发两个 tool_call 时，
// 分片合并把字段追加两遍，工具名变成 "search_notessearch_notes"，
// 模型明明给对了却报「未知工具」。
//
// 修完之后，下游 `meetingagent` 的单测一直是绿的。但那些用例
// **直接构造 llmbff.Delta**，压根不经过 SSE 解析这一层 ——
// 也就是说「`ToolCall.Index` 有没有真的从线上那个 JSON 字段解出来」
// **从来没有被任何一条用例覆盖过**。
// 把 `Index: tc.Index` 改成 `Index: 0`，下游全部单测照样全绿。
//
// ── 本门量的是那一跳 ──
//
// 帧是 2026-10-06 从真实网关（llm.kxpms.cn，gpt-4o）**原样抓下来的**，
// 未做任何改写；断言落在解析器的输出上。
//
// ★ 抓的是「有害形态」而不是「典型形态」：这套夹具是一次**并发两个**
//   工具调用。真实 id 是 `call_<hex>`：
//     index 0 → call_0h41APEzT0i6HG1nLWvEzoow
//     index 1 → call_RghdGj885bERcJ0Xe7TAQSKZ
//   **两个都不含可用的下标信息**（第一个碰巧以 "0" 开头，第二个以 "R" 开头）。
//   ⇒ 任何「从 id 字符串里反解下标」的实现在这套数据上都会恒定失败或恒定撞对，
//   绝不能靠它们通过。唯一可靠的分组依据是协议自带的 `index`。
//
// 这与设计文档 §9 记的那条教训同源：**夹具的键形状要照抄真实源**。

package llmgateway

import (
	"strings"
	"testing"
)

// realConcurrentToolCallsSSE 是真实网关返回的 SSE 原文（节选自
// /v1/chat/completions，model=gpt-4o，stream=true，带一个 search_notes 工具）。
//
// 形状要点（都来自真包，不是推测）：
//  1. 首个分片带**整帧**：id + type + function.name + 空 arguments；
//  2. 后续分片**只带 arguments 增量**，且**仍然带 index**；
//  3. 两个并发调用分别以 index=0 / index=1 开头，帧**不交错**；
//  4. 终帧 finish_reason="tool_calls"；
//  5. id 是 `call_<hex>`，不含可用下标。
const realConcurrentToolCallsSSE = `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"","name":"search_notes"},"id":"call_0h41APEzT0i6HG1nLWvEzoow","index":0,"type":"function"}]},"index":0}],"created":1791273564,"id":"chatcmpl-344b5753","model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"qu"},"index":0}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"ery\":"},"index":0}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":" \"结算\"}"},"index":0}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"","name":"search_notes"},"id":"call_RghdGj885bERcJ0Xe7TAQSKZ","index":1,"type":"function"}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"qu"},"index":1}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"ery\":"},"index":1}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":" \"排期\"}"},"index":1}]},"index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls","index":0}],"model":"gpt-4o","object":"chat.completion.chunk"}

data: [DONE]

`

// TestRealWireToolCallsCarryProtocolIndex —— 真包里的 index 必须**逐片**解出来。
//
// 这是 §11.4 那类缺陷的最后一道防线。若解析时把 index 丢掉（恒为 0），
// 两个并发调用会落进同一个桶，工具名与参数被追加两遍，
// 上层报「未知工具」——而模型明明给对了。
func TestRealWireToolCallsCarryProtocolIndex(t *testing.T) {
	var frames []StreamDelta
	_, err := parseSSEStream(strings.NewReader(realConcurrentToolCallsSSE), func(d StreamDelta) bool {
		if len(d.ToolCalls) > 0 {
			frames = append(frames, d)
		}
		return true
	})
	if err != nil {
		t.Fatalf("解析真实 SSE 失败: %v", err)
	}
	if len(frames) != 8 {
		t.Fatalf("应解析出 8 个带 tool_calls 的分片，实际 %d", len(frames))
	}

	// 逐片核对 index：真实包里**每一片**都带 index，包括只带 arguments 的增量片。
	wantIndex := []int{0, 0, 0, 0, 1, 1, 1, 1}
	for i, f := range frames {
		if len(f.ToolCalls) != 1 {
			t.Fatalf("第 %d 片有 %d 个 tool_call，真实包里每片恰好 1 个", i, len(f.ToolCalls))
		}
		if got := f.ToolCalls[0].Index; got != wantIndex[i] {
			t.Errorf("第 %d 片 index = %d，期望 %d —— "+
				"index 是分组的唯一依据（真实 id 是 call_<hex>，不含下标）", i, got, wantIndex[i])
		}
	}

	// 首个分片必须带整帧（id / type / name），这是「整帧一次到齐」形态。
	first := frames[0].ToolCalls[0]
	if first.ID != "call_0h41APEzT0i6HG1nLWvEzoow" {
		t.Errorf("首个分片 id = %q，期望真实 id", first.ID)
	}
	if first.Function.Name != "search_notes" {
		t.Errorf("首个分片工具名 = %q，期望 search_notes", first.Function.Name)
	}
	if first.Type != "function" {
		t.Errorf("首个分片 type = %q，期望 function", first.Type)
	}
	// 增量片**不得**带 id/name（真实包里就没有），否则下游会误当成新调用。
	if got := frames[1].ToolCalls[0].ID; got != "" {
		t.Errorf("增量片不应带 id，实际 %q", got)
	}
}

// TestRealWireToolCallsReassemblePerIndex —— 按 index 归并后必须得到两条独立调用。
//
// 这一条直接复刻 §11.4 的那个 bug：如果归并时不分桶（或按 id 猜下标），
// 两条会被并成一条，工具名变成 "search_notessearch_notes"、参数变成两个 JSON 拼接。
func TestRealWireToolCallsReassemblePerIndex(t *testing.T) {
	var frames []StreamDelta
	_, err := parseSSEStream(strings.NewReader(realConcurrentToolCallsSSE), func(d StreamDelta) bool {
		frames = append(frames, d)
		return true
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	type acc struct {
		id, name, args string
	}
	order := []int{}
	byIndex := map[int]*acc{}
	for _, f := range frames {
		for _, tc := range f.ToolCalls {
			cur, ok := byIndex[tc.Index]
			if !ok {
				cur = &acc{}
				byIndex[tc.Index] = cur
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				cur.id = tc.ID
			}
			if tc.Function.Name != "" {
				cur.name += tc.Function.Name
			}
			cur.args += tc.Function.Arguments
		}
	}

	if len(byIndex) != 2 {
		t.Fatalf("按 index 分桶后应有 2 条调用，实际 %d（§11.4 的 bug 就是并成了 1 条）", len(byIndex))
	}
	for _, idx := range order {
		got := byIndex[idx]
		if got.name != "search_notes" {
			t.Errorf("index=%d 工具名 = %q，期望恰好 search_notes（出现两遍就是 §11.4 的 bug）", idx, got.name)
		}
	}
	if byIndex[0].args != `{"query": "结算"}` {
		t.Errorf("index=0 参数 = %q，期望 {\"query\": \"结算\"}", byIndex[0].args)
	}
	if byIndex[1].args != `{"query": "排期"}` {
		t.Errorf("index=1 参数 = %q，期望 {\"query\": \"排期\"}", byIndex[1].args)
	}
	if byIndex[0].id == byIndex[1].id {
		t.Error("两个并发调用不应共享同一个 id")
	}
}

// TestRealWireFinishReasonIsToolCalls —— 终帧的 finish_reason 必须是 tool_calls。
//
// agent 的有界循环靠「模型不再要工具」判断收尾。终态帧丢了，
// 循环就既不知道该停、也不知道该继续 —— 属于「静默退化」那一类。
func TestRealWireFinishReasonIsToolCalls(t *testing.T) {
	var finish string
	_, err := parseSSEStream(strings.NewReader(realConcurrentToolCallsSSE), func(d StreamDelta) bool {
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
		return true
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if finish != "tool_calls" {
		t.Errorf("finish_reason = %q，期望 tool_calls", finish)
	}
}
