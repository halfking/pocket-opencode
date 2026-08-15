package adapter

import (
	"encoding/json"
	"testing"
)

func decodeMessage(t *testing.T, raw string) OpenCodeMessage {
	t.Helper()
	var m OpenCodeMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// V1 信封格式（SDK types.gen.ts SessionMessagesResponses）：
// 用户 prompt 文本在 parts 内，info 上没有 text 字段。
func TestToMobileV1EnvelopeUser(t *testing.T) {
	m := decodeMessage(t, `{
		"info": {
			"id": "msg_u1",
			"sessionID": "ses_1",
			"role": "user",
			"time": { "created": 1755300000000 }
		},
		"parts": [
			{ "id": "prt_1", "type": "text", "text": "修复登录 bug" },
			{ "id": "prt_2", "type": "file", "mime": "text/plain", "url": "file:///tmp/log.txt", "filename": "log.txt" }
		]
	}`)
	mm := m.ToMobile()
	if mm == nil {
		t.Fatal("expected mapped message")
	}
	if mm.ID != "msg_u1" || mm.Role != "user" {
		t.Fatalf("id/role: %+v", mm)
	}
	if mm.Text != "修复登录 bug\n\n📎 log.txt" {
		t.Fatalf("text aggregation: %q", mm.Text)
	}
	if mm.Time.Created != 1755300000000 {
		t.Fatalf("created: %d", mm.Time.Created)
	}
	if len(mm.Content) != 2 {
		t.Fatalf("content parts: %+v", mm.Content)
	}
}

// V1 assistant 消息：text/reasoning/tool 混合，tool 带完整 state。
func TestToMobileV1EnvelopeAssistantTool(t *testing.T) {
	m := decodeMessage(t, `{
		"info": {
			"id": "msg_a1",
			"role": "assistant",
			"time": { "created": 1755300001000, "completed": 1755300009000 }
		},
		"parts": [
			{ "id": "prt_r1", "type": "reasoning", "text": "先看日志" },
			{ "id": "prt_t1", "type": "text", "text": "已修复" },
			{ "id": "prt_tl1", "type": "tool", "callID": "call_1", "tool": "edit",
			  "state": { "status": "completed", "input": { "file": "src/a.ts" },
			             "output": "--- a\n+++ b\n",
			             "time": { "start": 1755300002000, "end": 1755300006000 } } },
			{ "id": "prt_tl2", "type": "tool", "callID": "call_2", "tool": "bash",
			  "state": { "status": "error", "input": { "command": "rm -rf x" },
			             "error": "permission denied",
			             "time": { "start": 1755300006000, "end": 1755300007000 } } },
			{ "id": "prt_s1", "type": "step-start" },
			{ "id": "prt_s2", "type": "step-finish", "reason": "stop", "cost": 0.01 }
		]
	}`)
	mm := m.ToMobile()
	if mm == nil {
		t.Fatal("expected mapped message")
	}
	if mm.Role != "assistant" {
		t.Fatalf("role: %s", mm.Role)
	}
	// text 只聚合 text part，reasoning 不混入正文
	if mm.Text != "已修复" {
		t.Fatalf("text: %q", mm.Text)
	}
	if len(mm.Content) != 4 {
		t.Fatalf("content parts (reasoning+text+2 tools): %+v", mm.Content)
	}

	tool1 := mm.Content[2]
	if tool1.Type != "tool" || tool1.ID != "call_1" || tool1.Name != "edit" || tool1.State != "completed" {
		t.Fatalf("tool1: %+v", tool1)
	}
	if tool1.Input["file"] != "src/a.ts" || tool1.Output != "--- a\n+++ b\n" {
		t.Fatalf("tool1 input/output: %+v", tool1)
	}
	if tool1.DurationMs == nil || *tool1.DurationMs != 4000 {
		t.Fatalf("tool1 duration: %v", tool1.DurationMs)
	}

	tool2 := mm.Content[3]
	if tool2.State != "error" || tool2.Error != "permission denied" {
		t.Fatalf("tool2: %+v", tool2)
	}
	if tool2.DurationMs == nil || *tool2.DurationMs != 1000 {
		t.Fatalf("tool2 duration: %v", tool2.DurationMs)
	}

	// reasoning part 保留在 content 供前端展示
	if mm.Content[0].Type != "reasoning" || mm.Content[0].Text != "先看日志" {
		t.Fatalf("reasoning: %+v", mm.Content[0])
	}
}

// 过渡期扁平格式 {id, role, parts}。
func TestToMobileFlatRoleParts(t *testing.T) {
	m := decodeMessage(t, `{
		"id": "msg_f1",
		"role": "assistant",
		"time": { "created": 1755300010000 },
		"parts": [ { "id": "p1", "type": "text", "text": "hello" } ]
	}`)
	mm := m.ToMobile()
	if mm == nil || mm.ID != "msg_f1" || mm.Role != "assistant" || mm.Text != "hello" {
		t.Fatalf("flat mapping: %+v", mm)
	}
}

// V2 扁平 tagged union（开发版 session/message.ts）：
// user 正文在顶层 text，assistant 结构化内容在 content。
func TestToMobileV2Flat(t *testing.T) {
	user := decodeMessage(t, `{
		"id": "msg_v2u", "type": "user", "text": "列一下改动",
		"time": { "created": 1755300020000 }
	}`)
	um := user.ToMobile()
	if um == nil || um.Role != "user" || um.Text != "列一下改动" {
		t.Fatalf("v2 user: %+v", um)
	}

	assistant := decodeMessage(t, `{
		"id": "msg_v2a", "type": "assistant",
		"time": { "created": 1755300021000 },
		"content": [
			{ "type": "text", "id": "c1", "text": "两处改动" },
			{ "type": "tool", "id": "call_v2", "name": "bash",
			  "state": { "status": "completed", "input": { "command": "ls" }, "result": "a\nb\n" },
			  "time": { "created": 1755300021100, "ran": 1755300021200, "completed": 1755300021500 } }
		]
	}`)
	am := assistant.ToMobile()
	if am == nil || am.Role != "assistant" {
		t.Fatalf("v2 assistant: %+v", am)
	}
	if am.Text != "两处改动" {
		t.Fatalf("v2 text aggregation from content: %q", am.Text)
	}
	if len(am.Content) != 2 {
		t.Fatalf("v2 content: %+v", am.Content)
	}
	tool := am.Content[1]
	if tool.ID != "call_v2" || tool.Name != "bash" || tool.State != "completed" {
		t.Fatalf("v2 tool: %+v", tool)
	}
	// V2 输出在 state.result
	if tool.Output != "a\nb\n" {
		t.Fatalf("v2 tool output: %+v", tool.Output)
	}
	// V2 duration 在 part.time.{ran,completed}
	if tool.DurationMs == nil || *tool.DurationMs != 300 {
		t.Fatalf("v2 tool duration: %v", tool.DurationMs)
	}

	// agent-switched 元消息对移动端无展示价值
	meta := decodeMessage(t, `{ "id": "msg_sw", "type": "agent-switched", "agent": "build" }`)
	if meta.ToMobile() != nil {
		t.Fatal("agent-switched should be skipped")
	}
}

// 无法识别 ID 的行返回 nil，调用方跳过。
func TestToMobileUnrecognized(t *testing.T) {
	if got := (OpenCodeMessage{}).ToMobile(); got != nil {
		t.Fatalf("empty message: %+v", got)
	}
	if got := decodeMessage(t, `{"foo":"bar"}`).ToMobile(); got != nil {
		t.Fatalf("unknown shape: %+v", got)
	}
	// V1 信封但缺 id
	if got := decodeMessage(t, `{"info":{"role":"user"},"parts":[]}`).ToMobile(); got != nil {
		t.Fatalf("missing id: %+v", got)
	}
}

// 运行中工具：无 duration、无 output。
func TestToMobileRunningTool(t *testing.T) {
	m := decodeMessage(t, `{
		"info": { "id": "msg_r", "role": "assistant" },
		"parts": [ { "type": "tool", "callID": "call_r", "tool": "read",
			"state": { "status": "running", "input": { "path": "/etc/hosts" }, "time": { "start": 100 } } } ]
	}`)
	mm := m.ToMobile()
	if mm == nil || len(mm.Content) != 1 {
		t.Fatalf("running tool: %+v", mm)
	}
	tool := mm.Content[0]
	if tool.State != "running" || tool.Output != nil || tool.DurationMs != nil {
		t.Fatalf("running state: %+v", tool)
	}
	if tool.Input["path"] != "/etc/hosts" {
		t.Fatalf("running input: %+v", tool.Input)
	}
}
