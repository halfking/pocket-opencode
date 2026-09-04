package agent

// adapter_pi_test.go — PiAdapter 测试
//
// 用假的可执行 shell 脚本模拟 pi 的 JSONL 输出（不依赖真实 pi）；真实 pi
// 只在 cmd/test_pi_adapter 冒烟工具里用一次。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeFakePi 写一个模拟 pi 的 shell 脚本并返回其路径。
//
// 脚本把全部参数写入 $FAKE_PI_ARGS_FILE（供 resume 参数断言），然后把
// $FAKE_PI_OUTPUT 指向的文件内容逐行打到 stdout，最后以 $FAKE_PI_EXIT
// 退出（缺省 0）。
func writeFakePi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pi.sh")
	script := `#!/bin/sh
# 模拟 pi CLI：回放固定 JSONL + 可配置退出码
if [ -n "$FAKE_PI_ARGS_FILE" ]; then
  printf '%s\n' "$*" >> "$FAKE_PI_ARGS_FILE"
fi
if [ -n "$FAKE_PI_OUTPUT" ]; then
  cat "$FAKE_PI_OUTPUT"
fi
exit "${FAKE_PI_EXIT:-0}"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	return path
}

// piFixturePath 把一段 JSONL 内容写入临时文件并返回路径。
func piFixturePath(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// setFakeEnv 在测试期间设置 fake pi 相关环境变量。
func setFakeEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// collectEvents 从事件通道收集 n 个事件（超时 fail）。
func collectEvents(t *testing.T, ch <-chan AgentEvent, n int) []AgentEvent {
	t.Helper()
	var out []AgentEvent
	deadline := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-deadline:
			t.Fatalf("timed out collecting events: got %d/%d", len(out), n)
		}
	}
	return out
}

// ---- SendPrompt 正常流 ----

// TestPiAdapter_SendPromptHappyPath 验证完整事件流映射 + SendPromptResult。
func TestPiAdapter_SendPromptHappyPath(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, strings.Join([]string{
		`{"type":"session","version":3,"id":"sess-1234","cwd":"/tmp"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"user","content":[]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"He"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"llo"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"hmm"}}`,
		`{"type":"tool_execution_start","toolCallId":"tc-1","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"tc-1","result":{"output":"a.txt"},"isError":false}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}],"usage":{"input":10,"output":5,"totalTokens":15,"cost":{"total":0.01}},"stopReason":"stop"}}`,
		`{"type":"agent_end"}`,
		"",
	}, "\n"))
	setFakeEnv(t, map[string]string{"FAKE_PI_OUTPUT": fixture})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ref := AgentRef{Type: "pi", Target: fake}
	ch, cleanup, err := a.SubscribeEvents(ctx, ref)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	defer cleanup()

	res, err := a.SendPrompt(ctx, ref, "", &SendPromptRequest{Text: "hi"})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", res.StopReason)
	}

	events := collectEvents(t, ch, 8)
	gotTypes := make([]string, 0, len(events))
	for _, e := range events {
		gotTypes = append(gotTypes, e.Type)
	}
	wantTypes := []string{
		"session", "message_chunk", "message_chunk", "thought_chunk",
		"tool_call", "tool_call_update", "message_end", "done",
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}

	// session 头 → 会话 ID 提取 + 广播到后续事件。
	if events[0].SessionID != "sess-1234" {
		t.Errorf("session event SessionID = %q, want sess-1234", events[0].SessionID)
	}
	for i, e := range events[1:] {
		if e.SessionID != "sess-1234" {
			t.Errorf("events[%d].SessionID = %q, want sess-1234", i+1, e.SessionID)
		}
	}

	// text_delta → message_chunk 文本。
	if txt, _ := events[1].Data["text"].(string); txt != "He" {
		t.Errorf("message_chunk text = %q, want He", txt)
	}
	// tool_execution_start → tool_call。
	tc := events[4]
	if id, _ := tc.Data["toolCallId"].(string); id != "tc-1" {
		t.Errorf("tool_call toolCallId = %q, want tc-1", id)
	}
	if name, _ := tc.Data["toolName"].(string); name != "bash" {
		t.Errorf("tool_call toolName = %q, want bash", name)
	}
	// message_end → 消息完成 + usage。
	me := events[6]
	if txt, _ := me.Data["text"].(string); txt != "Hello" {
		t.Errorf("message_end text = %q, want Hello", txt)
	}
	usage, ok := me.Data["usage"].(map[string]any)
	if !ok {
		t.Fatalf("message_end missing usage: %v", me.Data)
	}
	if usage["input"].(int64) != 10 || usage["output"].(int64) != 5 {
		t.Errorf("usage tokens = %v, want input=10 output=5", usage)
	}
	if usage["totalTokens"].(int64) != 15 {
		t.Errorf("usage totalTokens = %v, want 15", usage["totalTokens"])
	}
	if cost, _ := usage["cost"].(float64); cost != 0.01 {
		t.Errorf("usage cost = %v, want 0.01", usage["cost"])
	}
	if sr, _ := me.Data["stopReason"].(string); sr != "stop" {
		t.Errorf("message_end stopReason = %q, want stop", sr)
	}

	// 真实 session UUID 回填会话状态（resume 用）。
	a.mu.Lock()
	_, hasReal := a.sessions["sess-1234"]
	a.mu.Unlock()
	if !hasReal {
		t.Error("real session id not recorded after run")
	}
}

// TestPiAdapter_SendPromptResume 验证 resume 时 `--session <id>` 参数注入，
// 新会话时不注入。
func TestPiAdapter_SendPromptResume(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, `{"type":"session","id":"new-sess","cwd":"/tmp"}`+"\n")
	argsFile := filepath.Join(t.TempDir(), "args.log")
	setFakeEnv(t, map[string]string{
		"FAKE_PI_OUTPUT":    fixture,
		"FAKE_PI_ARGS_FILE": argsFile,
	})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := AgentRef{Type: "pi", Target: fake}

	// 新会话：不注入 --session。
	if _, err := a.SendPrompt(ctx, ref, "", &SendPromptRequest{Text: "hi"}); err != nil {
		t.Fatalf("SendPrompt(new): %v", err)
	}
	// resume：注入 --session。
	if _, err := a.SendPrompt(ctx, ref, "old-sess-uuid", &SendPromptRequest{Text: "again"}); err != nil {
		t.Fatalf("SendPrompt(resume): %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 invocations, got %d: %q", len(lines), raw)
	}
	if strings.Contains(lines[0], "--session") {
		t.Errorf("new session should not pass --session: %q", lines[0])
	}
	if !strings.Contains(lines[0], "--mode json") {
		t.Errorf("missing --mode json: %q", lines[0])
	}
	if !strings.Contains(lines[1], "--session old-sess-uuid") {
		t.Errorf("resume missing --session old-sess-uuid: %q", lines[1])
	}
}

// TestPiAdapter_ProviderErrorExitZero 验证关键陷阱：pi exit 0 但流中有
// provider 错误（stopReason=="error"）→ SendPrompt 返回 AGENT_UPSTREAM，
// 且订阅者收到 error 事件。
func TestPiAdapter_ProviderErrorExitZero(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, strings.Join([]string{
		`{"type":"session","id":"sess-err","cwd":"/tmp"}`,
		`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"auth failed for provider kxpms"}}`,
		`{"type":"agent_end"}`,
		"",
	}, "\n"))
	setFakeEnv(t, map[string]string{"FAKE_PI_OUTPUT": fixture})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := AgentRef{Type: "pi", Target: fake}
	ch, cleanup, err := a.SubscribeEvents(ctx, ref)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	defer cleanup()

	_, err = a.SendPrompt(ctx, ref, "", &SendPromptRequest{Text: "hi"})
	if err == nil {
		t.Fatal("expected error despite exit 0 (provider error trap)")
	}
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("err type = %T, want *agent.Error", err)
	}
	if ae.Code != "AGENT_UPSTREAM" {
		t.Errorf("error code = %q, want AGENT_UPSTREAM", ae.Code)
	}
	if !strings.Contains(err.Error(), "auth failed for provider kxpms") {
		t.Errorf("error should carry provider message, got: %v", err)
	}

	events := collectEvents(t, ch, 3)
	var errEvents int
	for _, e := range events {
		if e.Type == "error" {
			errEvents++
			if msg, _ := e.Data["message"].(string); !strings.Contains(msg, "auth failed") {
				t.Errorf("error event message = %q", msg)
			}
		}
	}
	if errEvents != 1 {
		t.Errorf("got %d error event(s), want 1", errEvents)
	}
}

// TestPiAdapter_NonZeroExit 验证进程非零退出 → AGENT_UPSTREAM（含 stderr 摘要）。
func TestPiAdapter_NonZeroExit(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, `{"type":"session","id":"s","cwd":"/tmp"}`+"\n")
	setFakeEnv(t, map[string]string{
		"FAKE_PI_OUTPUT": fixture,
		"FAKE_PI_EXIT":   "1",
	})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := a.SendPrompt(ctx, AgentRef{Type: "pi", Target: fake}, "", &SendPromptRequest{Text: "hi"})
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != "AGENT_UPSTREAM" {
		t.Fatalf("err = %v, want AGENT_UPSTREAM", err)
	}
}

// TestPiAdapter_InterruptSession 验证 InterruptSession 取消进行中的运行。
func TestPiAdapter_InterruptSession(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-pi-slow.sh")
	// 模拟慢 agent：睡 30s 后才输出（会被 kill，不会真的等 30s）。
	// 用 exec 让 sleep 替换 sh 进程本身——否则被 kill 的 sh 留下持有 stdout
	// 写端的孤儿 sleep，管道 EOF 永远不来（与真实 pi 单进程模型一致）。
	script := `#!/bin/sh
exec sleep 30
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := AgentRef{Type: "pi", Target: fake}

	done := make(chan error, 1)
	go func() {
		_, err := a.SendPrompt(ctx, ref, "sess-int", &SendPromptRequest{Text: "hi"})
		done <- err
	}()
	// 等进程起来再中断。
	time.Sleep(300 * time.Millisecond)
	if err := a.InterruptSession(ctx, ref, "sess-int"); err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}
	select {
	case err := <-done:
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != "AGENT_CANCELLED" {
			t.Fatalf("err = %v, want AGENT_CANCELLED", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendPrompt not interrupted in time")
	}
}

// TestPiAdapter_CreateSessionPendingAlias 验证 pending 占位 ID 在首轮运行后
// 别名到 pi 真实 UUID，且 cwd 记录用于 resume。
func TestPiAdapter_CreateSessionPendingAlias(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, `{"type":"session","id":"real-uuid","cwd":"/tmp"}`+"\n")
	argsFile := filepath.Join(t.TempDir(), "args.log")
	setFakeEnv(t, map[string]string{
		"FAKE_PI_OUTPUT":    fixture,
		"FAKE_PI_ARGS_FILE": argsFile,
	})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := AgentRef{Type: "pi", Target: fake}

	sess, err := a.CreateSession(ctx, ref, &CreateSessionRequest{
		Title: "t", WorkingDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !strings.HasPrefix(sess.ID, piPendingPrefix) {
		t.Fatalf("pending ID = %q, want prefix %s", sess.ID, piPendingPrefix)
	}
	if _, err := a.SendPrompt(ctx, ref, sess.ID, &SendPromptRequest{Text: "hi"}); err != nil {
		t.Fatalf("SendPrompt(pending): %v", err)
	}
	// 占位 ID 不应作为 --session 传给 pi（pending → 新会话）。
	raw, _ := os.ReadFile(argsFile)
	if strings.Contains(string(raw), "--session "+sess.ID) {
		t.Errorf("pending ID leaked to CLI args: %q", raw)
	}
	// 真实 UUID 可查，且与 pending 状态共享 cwd。
	a.mu.Lock()
	stReal, okReal := a.sessions["real-uuid"]
	stPend, okPend := a.sessions[sess.ID]
	a.mu.Unlock()
	if !okReal || !okPend {
		t.Fatalf("session states missing: real=%v pending=%v", okReal, okPend)
	}
	if stReal != stPend {
		t.Error("pending and real states should alias to the same state")
	}
	if stReal.CWD != "/tmp" {
		t.Errorf("state CWD = %q, want /tmp", stReal.CWD)
	}
}

// ---- Capabilities / HealthCheck / 降级 ----

// TestPiAdapter_Capabilities 验证能力位：除 Streaming 外全部 false，
// 且不实现 PermissionCapable / QuestionCapable。
func TestPiAdapter_Capabilities(t *testing.T) {
	a := NewPiAdapter("unused", "")
	caps, err := a.Capabilities(context.Background(), AgentRef{Type: "pi", Target: "x"})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Streaming {
		t.Error("Streaming should be true (SendPrompt 事件流)")
	}
	all := map[string]bool{
		"LoadSession": caps.LoadSession, "ListSessions": caps.ListSessions,
		"DeleteSession": caps.DeleteSession, "SetMode": caps.SetMode,
		"SetConfigOption": caps.SetConfigOption, "PromptImage": caps.PromptImage,
		"PromptAudio": caps.PromptAudio, "PromptEmbedCtx": caps.PromptEmbedCtx,
		"MCPHTTP": caps.MCPHTTP, "MCPSSE": caps.MCPSSE,
		"Permission": caps.Permission, "Question": caps.Question,
	}
	for name, v := range all {
		if v {
			t.Errorf("caps.%s = true, want false", name)
		}
	}
	var iface AgentAdapter = a
	if _, ok := iface.(PermissionCapable); ok {
		t.Error("PiAdapter should not implement PermissionCapable")
	}
	if _, ok := iface.(QuestionCapable); ok {
		t.Error("PiAdapter should not implement QuestionCapable")
	}
}

// TestPiAdapter_UnsupportedMethods 验证不支持的方法统一返回 AGENT_CAPABILITY。
func TestPiAdapter_UnsupportedMethods(t *testing.T) {
	a := NewPiAdapter("unused", "")
	ctx := context.Background()
	ref := AgentRef{Type: "pi", Target: "x"}
	check := func(name string, err error) {
		t.Helper()
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != "AGENT_CAPABILITY" {
			t.Errorf("%s err = %v, want AGENT_CAPABILITY", name, err)
		}
	}
	_, e1 := a.ListSessions(ctx, ref, ListOptions{})
	check("ListSessions", e1)
	_, e2 := a.LoadSession(ctx, ref, "s")
	check("LoadSession", e2)
	_, e3 := a.GetMessages(ctx, ref, "s", ListOptions{})
	check("GetMessages", e3)
	check("DeleteSession", a.DeleteSession(ctx, ref, "s"))
	check("SetSessionMode", a.SetSessionMode(ctx, ref, "s", "plan"))
}

// TestPiAdapter_HealthCheck 验证二进制探测。
func TestPiAdapter_HealthCheck(t *testing.T) {
	fake := writeFakePi(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a := NewPiAdapter(fake, "")
	if err := a.HealthCheck(ctx, AgentRef{Type: "pi", Target: fake}); err != nil {
		t.Errorf("HealthCheck(existing) = %v, want nil", err)
	}
	missing := NewPiAdapter("/nonexistent/pi-binary", "")
	if err := missing.HealthCheck(ctx, AgentRef{Type: "pi", Target: "x"}); err == nil {
		t.Error("HealthCheck(missing) = nil, want error")
	}
}

// TestPiAdapter_SubscribeCleanup 验证 cleanup 后通道关闭、事件不再投递。
func TestPiAdapter_SubscribeCleanup(t *testing.T) {
	fake := writeFakePi(t)
	fixture := piFixturePath(t, `{"type":"session","id":"s","cwd":"/tmp"}`+"\n")
	setFakeEnv(t, map[string]string{"FAKE_PI_OUTPUT": fixture})

	a := NewPiAdapter(fake, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := AgentRef{Type: "pi", Target: fake}
	ch, cleanup, err := a.SubscribeEvents(ctx, ref)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	cleanup()
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after cleanup")
	}
	// cleanup 幂等（ctx 自动清理路径共享同一实现）。
	cleanup()

	// SendPrompt 正常完成，不再有订阅者也不阻塞。
	if _, err := a.SendPrompt(ctx, ref, "", &SendPromptRequest{Text: "hi"}); err != nil {
		t.Fatalf("SendPrompt without subscribers: %v", err)
	}
}

// ---- piParseLine 纯解析单元测试 ----

// TestPiParseLine_Mapping 验证逐行映射语义（与 agent-companion 的 PiJSONParser
// 用例同源）。
func TestPiParseLine_Mapping(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantType  string // 空 = 不产生事件
		wantInfo  bool
		sessionID string
	}{
		{"session", `{"type":"session","id":"u1","cwd":"/tmp"}`, "session", true, "u1"},
		{"text_delta", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"abc"}}`, "message_chunk", false, ""},
		{"text_delta_empty", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":""}}`, "", false, ""},
		{"thinking_delta", `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"h"}}`, "thought_chunk", false, ""},
		{"tool_start", `{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a"}}`, "tool_call", false, ""},
		{"tool_end", `{"type":"tool_execution_end","toolCallId":"t1","isError":false}`, "tool_call_update", false, ""},
		{"msg_end_assistant", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"stopReason":"stop"}}`, "message_end", true, ""},
		{"msg_end_error", `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}}`, "error", true, ""},
		{"msg_end_user", `{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"q"}]}}`, "", false, ""},
		{"agent_end", `{"type":"agent_end"}`, "done", false, ""},
		{"top_error", `{"type":"error","message":"provider down"}`, "error", true, ""},
		{"not_json", `plain text line`, "", false, ""},
		{"empty", ``, "", false, ""},
		{"unknown_type", `{"type":"future_event"}`, "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt, info := piParseLine(tc.line)
			if tc.wantType == "" {
				if evt != nil {
					t.Errorf("event type = %q, want nil", evt.Type)
				}
				return
			}
			if evt == nil {
				t.Fatalf("event = nil, want %s", tc.wantType)
			}
			if evt.Type != tc.wantType {
				t.Errorf("event type = %q, want %q", evt.Type, tc.wantType)
			}
			if tc.wantInfo != (info != nil) {
				t.Errorf("info presence = %v, want %v", info != nil, tc.wantInfo)
			}
			if tc.sessionID != "" && evt.SessionID != tc.sessionID {
				t.Errorf("session id = %q, want %q", evt.SessionID, tc.sessionID)
			}
		})
	}
}

// TestPiParseLine_UsageExtraction 验证 usage/cost 提取。
func TestPiParseLine_UsageExtraction(t *testing.T) {
	line := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input":3,"output":2,"cacheRead":0,"totalTokens":5,"cost":{"total":0.5}},"stopReason":"stop"}}`
	_, info := piParseLine(line)
	if info == nil || !info.hasFinal {
		t.Fatalf("info = %+v, want hasFinal", info)
	}
	if info.text != "ok" {
		t.Errorf("text = %q, want ok", info.text)
	}
	if info.usage == nil || info.usage.Input != 3 || info.usage.Output != 2 ||
		info.usage.TotalTokens != 5 || info.usage.CostUSD != 0.5 {
		t.Fatalf("usage = %+v", info.usage)
	}
}

// TestPiMapStopReason 验证 stopReason → ACP 风格映射。
func TestPiMapStopReason(t *testing.T) {
	cases := map[string]string{
		"stop":    "end_turn",
		"length":  "max_tokens",
		"aborted": "cancelled",
		"error":   "error",
		"":        "",
		"future":  "future",
	}
	for in, want := range cases {
		if got := piMapStopReason(in); got != want {
			t.Errorf("piMapStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}
