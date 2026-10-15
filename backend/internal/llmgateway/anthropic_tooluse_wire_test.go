// anthropic_tooluse_wire_test.go —— 用**真实抓包帧**驱动的 Anthropic 工具调用门。
//
// ── 这道门补的是哪条零覆盖 ──
//
// 2026-10-06 真网关复测发现并修掉了两个静默失效：`content_block_stop` 里
// 组装好的 ToolCall **只 append 进返回值、从不 emit 给消费方**；
// 终态 usage 帧同样没发。而当时**没有一条测试能抓到它**，原因有二：
//
//  1. `anthropic_test.go` 里唯一的流式用例 `TestStreamViaMessagesParsesNamedEvents`
//     **只推文本**，夹具是手写的、只有 `index:0` 的 text_delta ——
//     **整条 tool_use 路径零覆盖**；
//  2. 即使有 tool_use 用例，若断言的是 `parseAnthropicSSE` 的**返回值**，
//     返回值一直是对的（错的只是「没推给消费者」）。判据量错了对象。
//
// ⇒ 本文件两条都补：喂**真包**，断**回调**。
//
// ★ 夹具照抄真实源（2026-10-06 从 llm.kxpms.cn/v1/messages 抓的
//   claude-haiku-4-5 原文，模型被要求一轮内并发发两个工具调用）：
//
//	START  index=0 type=text                       ← ★ 有个文本块占了 index 0
//	STOP   index=0
//	START  index=1 type=tool_use id=toolu_bdrk_016Vzob3VoY7daWBM4G1eSe name=search_notes input={}
//	DELTA  index=1 input_json_delta '{"query": '
//	DELTA  index=1 input_json_delta '"结算"}'
//	START  index=2 type=tool_use id=toolu_bdrk_011brynHmu3Tp4Tyqo9oUFyM name=search_notes input={}
//	DELTA  index=2 input_json_delta '{"query": "排'
//	DELTA  index=2 input_json_delta '期"}'
//	STOP   index=1
//	STOP   index=2
//	MSGDLT stop_reason=tool_use
//
// 真实数据里有三处「想当然就会写错」的形状：
//   1. **工具块的下标是 1 和 2，不是 0 和 1** —— 因为 index 0 被一个
//      文本块占了。任何假设「工具从 0 开始编号」或「自己数第几个工具块」
//      的实现都会串位。
//   2. **工具名只出现在 content_block_start**，后续 input_json_delta 不重复；
//      参数则分片到达。若在 delta 里找名字，两个工具都会变成空名。
//   3. `stop_reason` 是 **`tool_use`（单数）**，不是 OpenAI 的 `tool_calls`。
//   另外真实 id 是 `toolu_bdrk_<hex>`，同样**不含可用下标**。

package llmgateway

import (
	"strings"
	"testing"
)

// realAnthropicToolUseSSE 是 /v1/messages 的真实响应原文。
const realAnthropicToolUseSSE = `: keep-alive
event: message_start
data: {"message":{"content":[],"id":"msg_ac99d418239bdcb417678d2a","model":"claude-haiku-4-5","role":"assistant","stop_reason":null,"stop_sequence":null,"type":"message","usage":{"input_tokens":86,"output_tokens":0}},"type":"message_start"}

event: content_block_start
data: {"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}

event: ping
data: {"type":"ping"}

event: content_block_stop
data: {"index":0,"type":"content_block_stop"}

event: content_block_start
data: {"content_block":{"id":"toolu_bdrk_016Vzobr3VoY7daWBM4G1eSe","input":{},"name":"search_notes","type":"tool_use"},"index":1,"type":"content_block_start"}

event: content_block_delta
data: {"delta":{"partial_json":"{\"query\": ","type":"input_json_delta"},"index":1,"type":"content_block_delta"}

event: content_block_delta
data: {"delta":{"partial_json":"\"结算\"}","type":"input_json_delta"},"index":1,"type":"content_block_delta"}

event: content_block_start
data: {"content_block":{"id":"toolu_bdrk_011brynHmu3Tp4Tyqo9oUFyM","input":{},"name":"search_notes","type":"tool_use"},"index":2,"type":"content_block_start"}

event: content_block_delta
data: {"delta":{"partial_json":"{\"query\": \"排","type":"input_json_delta"},"index":2,"type":"content_block_delta"}

event: content_block_delta
data: {"delta":{"partial_json":"期\"}","type":"input_json_delta"},"index":2,"type":"content_block_delta"}

event: content_block_stop
data: {"index":1,"type":"content_block_stop"}

event: content_block_stop
data: {"index":2,"type":"content_block_stop"}

event: message_delta
data: {"delta":{"stop_reason":"tool_use","stop_sequence":null},"type":"message_delta","usage":{"output_tokens":92}}

event: message_stop
data: {"type":"message_stop"}
`

// TestRealAnthropicFramesEmitToolCallsToConsumer —— ★ 断**回调**，不是返回值。
//
// 这是 2026-10-06 那个静默失效的直接判据：工具调用必须真的推给消费方。
// 只断言 `parseAnthropicSSE` 的返回值会全绿 —— 返回值一直是对的，
// 错的是「没有推给消费者」，而那正是上层拿不到任何工具调用的原因。
func TestRealAnthropicFramesEmitToolCallsToConsumer(t *testing.T) {
	var seen []StreamDelta
	final, err := parseAnthropicSSE(strings.NewReader(realAnthropicToolUseSSE), func(d StreamDelta) bool {
		seen = append(seen, d)
		return true
	})
	if err != nil {
		t.Fatalf("解析真实 Anthropic SSE 失败: %v", err)
	}

	// 从**回调收到的帧**里收集工具调用（不看 final）。
	var viaCallback []ToolCall
	for _, d := range seen {
		viaCallback = append(viaCallback, d.ToolCalls...)
	}
	if len(viaCallback) != 2 {
		t.Fatalf("消费方回调只收到 %d 个工具调用，期望 2 个 —— "+
			"这正是「只 append 进返回值、从不 emit」那个静默失效的形状", len(viaCallback))
	}

	// 下标必须**原样是 content_block 的 1 和 2**（不是 0/1，也不是「第几个工具」）。
	gotIndex := map[int]ToolCall{}
	for _, tc := range viaCallback {
		gotIndex[tc.Index] = tc
	}
	if _, ok := gotIndex[1]; !ok {
		t.Errorf("未收到 index=1 的工具调用（实际收到的下标：%v）", indexKeys(gotIndex))
	}
	if _, ok := gotIndex[2]; !ok {
		t.Errorf("未收到 index=2 的工具调用（实际收到的下标：%v）", indexKeys(gotIndex))
	}
	if _, ok := gotIndex[0]; ok {
		t.Error("index=0 那个块是 text，不该产生工具调用 —— 文本块与工具块共用下标空间")
	}

	// 工具名只来自 content_block_start；参数来自 input_json_delta 拼接。
	want := map[int]struct {
		id, name, args string
	}{
		1: {"toolu_bdrk_016Vzobr3VoY7daWBM4G1eSe", "search_notes", `{"query": "结算"}`},
		2: {"toolu_bdrk_011brynHmu3Tp4Tyqo9oUFyM", "search_notes", `{"query": "排期"}`},
	}
	for idx, w := range want {
		tc, ok := gotIndex[idx]
		if !ok {
			continue
		}
		if tc.ID != w.id {
			t.Errorf("index=%d 的 id = %q，期望 %q", idx, tc.ID, w.id)
		}
		if tc.Function.Name != w.name {
			t.Errorf("index=%d 的工具名 = %q，期望 %q（名字只在 start 帧里出现过一次）",
				idx, tc.Function.Name, w.name)
		}
		if tc.Function.Arguments != w.args {
			t.Errorf("index=%d 的参数 = %q，期望 %q（input_json_delta 分片拼接）",
				idx, tc.Function.Arguments, w.args)
		}
	}

	// 返回值侧也应当一致（两条通道都要对）。
	if len(final.ToolCalls) != 2 {
		t.Errorf("返回值里的 ToolCalls = %d，期望 2", len(final.ToolCalls))
	}
}

// TestRealAnthropicTerminalFrameReachesConsumer —— 终态帧必须带着**消费方真正会读的两个字段**下发。
//
// ⚠⚠ 2026-10-06 的自我更正：**本文件第一版断言的是 `d.Done`，那条判据是量不到任何
// 真实行为的**（详见下面「为什么不是 d.Done」）。
//
// 真正的契约在 `llmbff_provider_adapters.go` 的适配器里：
//
//	Done: d.FinishReason != "" || d.TotalTokens > 0
//	if d.TotalTokens > 0 { delta.Usage = &u }
//
// ⇒ 消费方（meetingagent 的 `if d.Done && d.Usage != nil`）判定的「终态」
// 是**适配器算出来的**，依据是这两个字段，不是 `StreamDelta.Done`。
// 所以这里断言 FinishReason 与 TotalTokens。
//
// **为什么不是 `d.Done`（同一族的错，第二次）**：
// `StreamDelta.Done` 在回调链路上**没有任何读取方** —— 唯一写它的地方
// 就是本节第一版自己加的那一行。用例绿了，量的是「我写的那行还在」，
// 而真实链路（适配器 → llmbff.Delta → meetingagent）**根本没看它**。
// A/B 实测：加与不加 `Done:true`，适配器视角的终态帧数与工具调用数**完全相同**。
func TestRealAnthropicTerminalFrameReachesConsumer(t *testing.T) {
	var sawTerminal StreamDelta
	terminalCount := 0
	_, err := parseAnthropicSSE(strings.NewReader(realAnthropicToolUseSSE), func(d StreamDelta) bool {
		if d.FinishReason != "" || d.TotalTokens > 0 {
			terminalCount++
			sawTerminal = d
		}
		return true
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 复刻适配器的判定条件 —— 判据与被测方用**同一把尺**，否则量的是另一个东西。
	if terminalCount != 1 {
		t.Fatalf("消费方应恰好收到 1 帧终态，实际 %d", terminalCount)
	}
	// ★ 这两个字段才是消费方真正会读的：它们决定 llmbff.Delta 的 Done 与 Usage。
	if sawTerminal.FinishReason != "tool_calls" {
		t.Errorf("终态 FinishReason = %q，期望 tool_calls（真实包给的是单数 tool_use，映射后应为复数）",
			sawTerminal.FinishReason)
	}
	// TotalTokens 必须来自真包的 usage 相加：86 in + 92 out。
	// 少了它，适配器不会填 Usage ⇒ agent 的 usage 归零（那才是真实的断链方式）。
	if sawTerminal.TotalTokens != 178 {
		t.Errorf("终态 TotalTokens = %d，期望 178（86 in + 92 out，真包读数）—— "+
			"适配器靠它填 llmbff.Delta.Usage", sawTerminal.TotalTokens)
	}
	if sawTerminal.PromptTokens != 86 || sawTerminal.CompletionTokens != 92 {
		t.Errorf("终态 usage 明细 = %d in / %d out，期望 86 / 92（真包读数）",
			sawTerminal.PromptTokens, sawTerminal.CompletionTokens)
	}
	// ★ 终态帧**不得**再带一遍工具调用：参数已在 content_block_stop 逐个下发过，
	// 再带一遍会让下游按 index 累加时把参数追加两次（§11.4 的同款缺陷）。
	if len(sawTerminal.ToolCalls) != 0 {
		t.Errorf("终态帧重复携带了 %d 个工具调用，下游会按 index 累加成两倍参数", len(sawTerminal.ToolCalls))
	}
}

// TestRealAnthropicTextBlockDoesNotEmitToolCall —— 负控：文本块不得被当成工具。
//
// 真包里 index=0 是一个 text 块。若实现改成「按出现顺序数工具」而不是
// 读 content_block.type，这个文本块就会被算成第一个工具。
func TestRealAnthropicTextBlockDoesNotEmitToolCall(t *testing.T) {
	var names []string
	_, err := parseAnthropicSSE(strings.NewReader(realAnthropicToolUseSSE), func(d StreamDelta) bool {
		for _, tc := range d.ToolCalls {
			names = append(names, tc.Function.Name)
		}
		return true
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(names) != 2 || names[0] != "search_notes" || names[1] != "search_notes" {
		t.Errorf("回调收到的工具名 = %v，期望恰好两个 search_notes", names)
	}
}

func indexKeys(m map[int]ToolCall) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
