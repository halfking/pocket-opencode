package main

// test_pi_adapter.go — pi coding agent adapter 冒烟测试
//
// 向真实 pi（PI_CLI_PATH，缺省 ~/.npm-global/bin/pi）发一个极短 prompt，
// 断言：
//  1. HealthCheck 通过
//  2. 收到 session 头事件（非空 SessionID）
//  3. 至少一个消息事件（message_chunk / message_end）
//  4. usage 非零（totalTokens > 0）
//
// 并打印事件摘要。离线自检：-fake <fixture.jsonl> 用假输出回放替代真实 pi
// （不消耗 provider 配额）。
//
// 用法：
//
//	go run ./cmd/test_pi_adapter                     # 真实 pi
//	PI_CLI_PATH=/path/to/pi go run ./cmd/test_pi_adapter
//	go run ./cmd/test_pi_adapter -fake out.jsonl     # 离线回放

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/agent"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	fake := flag.String("fake", "", "离线自检：用该 JSONL 文件回放假 pi 输出（不启动真实 pi）")
	timeout := flag.Duration("timeout", 3*time.Minute, "SendPrompt 整体超时")
	flag.Parse()

	// ---- 1. 解析 pi 路径 ----
	log.Println("=== 步骤 1: 解析 pi 可执行文件路径 ===")
	piPath := os.Getenv("PI_CLI_PATH")
	if piPath == "" {
		home, _ := os.UserHomeDir()
		piPath = filepath.Join(home, ".npm-global", "bin", "pi")
	}
	if *fake != "" {
		// 离线自检：生成一个回放 fixture 的假可执行脚本。
		dir, err := os.MkdirTemp("", "fake-pi-*")
		if err != nil {
			log.Fatalf("MkdirTemp: %v", err)
		}
		defer os.RemoveAll(dir)
		script := filepath.Join(dir, "fake-pi.sh")
		body := "#!/bin/sh\ncat \"$FAKE_PI_OUTPUT\"\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			log.Fatalf("write fake script: %v", err)
		}
		if err := os.Setenv("FAKE_PI_OUTPUT", *fake); err != nil {
			log.Fatalf("setenv: %v", err)
		}
		piPath = script
		log.Printf("离线自检模式：fake pi = %s, fixture = %s", script, *fake)
	}
	log.Printf("pi path: %s", piPath)

	// ---- 2. 构造 adapter + HealthCheck ----
	log.Println("=== 步骤 2: 构造 PiAdapter + HealthCheck ===")
	a := agent.NewPiAdapter(piPath, "")
	ref := agent.AgentRef{Type: "pi", Target: piPath}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+30*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx, ref); err != nil {
		log.Fatalf("HealthCheck failed: %v", err)
	}
	log.Println("HealthCheck passed")

	// ---- 3. Capabilities（应全 false 除 Streaming）----
	log.Println("=== 步骤 3: Capabilities ===")
	caps, err := a.Capabilities(ctx, ref)
	if err != nil {
		log.Fatalf("Capabilities: %v", err)
	}
	capsJSON, _ := json.MarshalIndent(caps, "", "  ")
	log.Printf("Capabilities:\n%s", capsJSON)

	// ---- 4. 订阅事件 ----
	log.Println("=== 步骤 4: SubscribeEvents ===")
	ch, cleanup, err := a.SubscribeEvents(ctx, ref)
	if err != nil {
		log.Fatalf("SubscribeEvents: %v", err)
	}
	defer cleanup()

	// ---- 5. 发送极短 prompt ----
	log.Println("=== 步骤 5: SendPrompt（真实 pi headless 运行）===")
	sendCtx, sendCancel := context.WithTimeout(ctx, *timeout)
	defer sendCancel()
	start := time.Now()
	result, err := a.SendPrompt(sendCtx, ref, "", &agent.SendPromptRequest{
		Text: "回复 ok 两个字母即可",
	})

	// ---- 6. 收集并断言事件（失败时也收集，用于诊断）----
	log.Println("=== 步骤 6: 收集事件并断言 ===")
	events, assertions := collectAndAssert(ch)
	log.Printf("收到 %d 个事件：", len(events))
	for i, evt := range events {
		log.Printf("  [%2d] %-18s %s", i, evt.Type, summarize(evt))
	}
	for _, line := range assertions {
		log.Println(line)
	}
	if err != nil {
		log.Printf("SendPrompt failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		log.Fatalf("=== 冒烟测试未通过（SendPrompt 失败）===")
	}
	log.Printf("SendPrompt done in %s: stopReason=%q", time.Since(start).Round(time.Millisecond), result.StopReason)

	for _, line := range assertions {
		if len(line) > 5 && line[:5] == "FAIL:" {
			log.Fatalf("=== 冒烟测试未通过 ===")
		}
	}
	log.Println("=== 冒烟测试通过 ===")
}

// collectAndAssert 排空事件通道（5s 静默窗口）并生成断言结论。
func collectAndAssert(ch <-chan agent.AgentEvent) ([]agent.AgentEvent, []string) {
	var (
		events      []agent.AgentEvent
		sessionSeen bool
		messageSeen bool
		usageTotal  int64
		errSeen     []string
	)
	deadline := time.After(5 * time.Second)
collect:
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				break collect
			}
			events = append(events, evt)
			switch evt.Type {
			case "session":
				sessionSeen = evt.SessionID != ""
			case "message_chunk", "thought_chunk":
				messageSeen = true
			case "message_end":
				messageSeen = true
				if u, ok := evt.Data["usage"].(map[string]any); ok {
					if tt, ok := u["totalTokens"].(int64); ok && tt > usageTotal {
						usageTotal = tt
					}
				}
			case "error":
				msg, _ := evt.Data["message"].(string)
				errSeen = append(errSeen, msg)
			}
		case <-deadline:
			break collect
		}
	}

	var out []string
	if sessionSeen {
		out = append(out, "PASS: session 头事件（含非空 SessionID）")
	} else {
		out = append(out, "FAIL: 未收到 session 头事件（或 SessionID 为空）")
	}
	if messageSeen {
		out = append(out, "PASS: 至少一个消息事件")
	} else {
		out = append(out, "FAIL: 未收到任何消息事件（message_chunk/message_end）")
	}
	if usageTotal > 0 {
		out = append(out, fmt.Sprintf("PASS: usage totalTokens = %d", usageTotal))
	} else {
		out = append(out, fmt.Sprintf("FAIL: usage totalTokens = %d（要求非零）", usageTotal))
	}
	if len(errSeen) > 0 {
		out = append(out, fmt.Sprintf("FAIL: 流中出现 error 事件: %v", errSeen))
	}
	return events, out
}

// summarize 生成单行事件摘要。
func summarize(evt agent.AgentEvent) string {
	switch evt.Type {
	case "session":
		return "sessionID=" + evt.SessionID
	case "message_chunk", "thought_chunk":
		if txt, ok := evt.Data["text"].(string); ok {
			return fmt.Sprintf("text=%q", truncate(txt, 40))
		}
	case "tool_call":
		return fmt.Sprintf("toolCallId=%v toolName=%v", evt.Data["toolCallId"], evt.Data["toolName"])
	case "tool_call_update":
		return fmt.Sprintf("toolCallId=%v isError=%v", evt.Data["toolCallId"], evt.Data["isError"])
	case "message_end":
		return fmt.Sprintf("text=%q stopReason=%v", truncate(str(evt.Data["text"]), 40), evt.Data["stopReason"])
	case "error":
		return "message=" + str(evt.Data["message"])
	case "done":
		return "turn finished"
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
