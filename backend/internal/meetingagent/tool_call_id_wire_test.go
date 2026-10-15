// tool_call_id_wire_test.go —— 「工具结果回灌」这条消息的协议门。
//
// ── 为什么这道门是 2026-10-06 才补的 ──
//
// 真网关实测（见设计文档 §15.5）确认：agent 的收尾能力**完全依赖**
// `tool` 消息上的 `tool_call_id` 把结果绑回上一次的调用。
// 没有它，OpenAI 兼容端点会拒收该轮请求，整个循环拿不到总结。
//
// 而在此之前，**全仓没有任何一条测试断言过 ToolCallID 被填上**：
// `fakeChat.seen` 的注释写着「用于断言协议正确性」，但从没人拿它断言这个字段。
// 也就是说：把 `ToolCallID: c.ID` 删掉，全部单测照样全绿 ——
// 而真网关上会直接断链。
//
// 与 §15.2 是同一族的洞：那一条是 `Index` 没被解析覆盖，这一条是
// `ToolCallID` 没被回灌覆盖。**两个字段都是「只有真网关会告诉你」的那类。**

package meetingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// realCallID 是 2026-10-06 从真实网关（llm.kxpms.cn / gpt-4o）抓到的
// 真实 tool_call id 之一。刻意用真实形状而不是 `call_1` 这种好读的串：
// id 是**上游给的**，本仓无权选择它的格式。
const (
	realCallID0 = "call_0h41APEzT0i6HG1nLWvEzoow"
	realCallID1 = "call_RghdGj885bERcJ0Xe7TAQSKZ"
)

// TestToolResultMessageCarriesRealToolCallID —— 工具结果必须带着真实 id 回灌。
//
// 判据锚在「**回灌给下一轮的消息**」上，而不是「代码里写了 ToolCallID」：
// 前者是消费方真正看到的东西，后者与 §7.2 的 `if (false)` 变异同型
// （字面量还在文件里，但没人能等到它）。
func TestToolResultMessageCarriesRealToolCallID(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{{Done: false, ToolCalls: []llmbff.ToolCall{
			{Index: 0, ID: realCallID0, Type: "function",
				Function: llmbff.ToolCallFunc{Name: "search_notes", Arguments: `{"query":"结算"}`}},
			{Index: 1, ID: realCallID1, Type: "function",
				Function: llmbff.ToolCallFunc{Name: "search_notes", Arguments: `{"query":"排期"}`}},
		}}},
		{{Content: "总结正文", Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", out: "命中"}

	if _, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool}); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if len(chat.seen) < 2 {
		t.Fatalf("应至少发起两轮（第一轮要工具、第二轮收尾），实际 %d", len(chat.seen))
	}

	// 第二轮 = 回灌工具结果那一轮。
	msgs := chat.seen[1]
	var toolMsgs []llmbff.Message
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("第二轮应回灌 2 条 tool 消息（并发两个调用），实际 %d", len(toolMsgs))
	}

	// ★ 核心断言：每条 tool 消息都必须带上对应调用的真实 id。
	// 真实网关靠它把结果绑回调用；缺一个就是断链。
	got := map[string]string{}
	for _, m := range toolMsgs {
		if m.ToolCallID == "" {
			t.Errorf("tool 消息缺 tool_call_id —— 真网关会拒收该轮，agent 拿不到总结")
			continue
		}
		got[m.ToolCallID] = m.Content
	}
	if _, ok := got[realCallID0]; !ok {
		t.Errorf("未按 %s 回灌（真实 id 必须原样带回，不能改写）", realCallID0)
	}
	if _, ok := got[realCallID1]; !ok {
		t.Errorf("未按 %s 回灌（真实 id 必须原样带回，不能改写）", realCallID1)
	}
	// 工具输出必须真的进了消息（不只是 id 对了、内容是空的）。
	for id, content := range got {
		if strings.TrimSpace(content) == "" {
			t.Errorf("id=%s 的 tool 消息内容为空，模型收不到检索结果", id)
		}
	}
}

// TestAssistantTurnEchoedBackWithToolCalls —— assistant 那轮必须原样记回消息序列。
//
// OpenAI 协议要求：回灌 tool 结果之前，要先把发出工具调用的那条 assistant
// 消息（含 tool_calls）放回 messages。漏掉它，上游会认为
// 「凭空冒出一个 tool 结果」，从而拒收或让模型困惑。
func TestAssistantTurnEchoedBackWithToolCalls(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{{Done: false, ToolCalls: []llmbff.ToolCall{{
			Index: 0, ID: realCallID0, Type: "function",
			Function: llmbff.ToolCallFunc{Name: "search_notes", Arguments: `{"query":"结算"}`},
		}}}},
		{{Content: "总结正文", Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", out: "命中"}

	if _, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool}); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	msgs := chat.seen[1]

	var assistant *llmbff.Message
	for i := range msgs {
		if msgs[i].Role == "assistant" {
			assistant = &msgs[i]
			break
		}
	}
	if assistant == nil {
		t.Fatalf("第二轮消息里没有 assistant 那轮 —— OpenAI 协议要求原样记回")
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant 消息的 tool_calls = %d 条，期望 1", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != realCallID0 {
		t.Errorf("assistant.tool_calls[0].ID = %q，期望原样带回 %q", tc.ID, realCallID0)
	}
	if tc.Function.Name != "search_notes" {
		t.Errorf("assistant.tool_calls[0].name = %q，期望 search_notes", tc.Function.Name)
	}
	// 顺序：assistant 必须在 tool 之前（协议要求先有调用，再有结果）。
	ai, ti := -1, -1
	for i, m := range msgs {
		if m.Role == "assistant" && ai < 0 {
			ai = i
		}
		if m.Role == "tool" && ti < 0 {
			ti = i
		}
	}
	if ai < 0 || ti < 0 || ai > ti {
		t.Errorf("消息顺序不对：assistant@%d 应在 tool@%d 之前", ai, ti)
	}
}
