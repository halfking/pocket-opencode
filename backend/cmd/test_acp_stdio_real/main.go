package main

// test_acp_stdio_real.go — 测试真实 ACP stdio adapter + agent_echo
//
// 用途：验证 ACPStdioAdapter 能否通过 StdioTransport 与 agent_echo 交互。

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/agent"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. 创建 ACP stdio adapter
	log.Println("=== 步骤 1: 创建 ACPStdioAdapter ===")
	acpAdapter := agent.NewACPStdioAdapter()
	defer acpAdapter.Close()

	// 2. 定义 agent ref（指向 agent_echo）
	ref := agent.AgentRef{
		Type:   "acp-stdio",
		Target: "/tmp/agent_echo", // agent_echo 路径
	}
	log.Printf("✅ Agent ref: %s", ref.String())

	// 3. 健康检查（会调用 initialize）
	log.Println("\n=== 步骤 2: HealthCheck (initialize) ===")
	if err := acpAdapter.HealthCheck(ctx, ref); err != nil {
		log.Fatalf("HealthCheck failed: %v", err)
	}
	log.Println("✅ HealthCheck passed")

	// 4. 查询 capabilities
	log.Println("\n=== 步骤 3: Capabilities ===")
	caps, err := acpAdapter.Capabilities(ctx, ref)
	if err != nil {
		log.Fatalf("Capabilities failed: %v", err)
	}
	capsJSON, _ := json.MarshalIndent(caps, "", "  ")
	log.Printf("✅ Capabilities:\n%s", capsJSON)

	// 5. 创建 session
	log.Println("\n=== 步骤 4: CreateSession (session/new) ===")
	sess, err := acpAdapter.CreateSession(ctx, ref, &agent.CreateSessionRequest{
		Title: "Test ACP session",
		Agent: "default",
	})
	if err != nil {
		log.Fatalf("CreateSession failed: %v", err)
	}
	log.Printf("✅ Session created: %s (%s)", sess.ID, sess.Title)

	// 6. 发送 prompt
	log.Println("\n=== 步骤 5: SendPrompt (session/prompt) ===")
	result, err := acpAdapter.SendPrompt(ctx, ref, sess.ID, &agent.SendPromptRequest{
		Text: "Hello from ACPStdioAdapter test!",
	})
	if err != nil {
		log.Fatalf("SendPrompt failed: %v", err)
	}
	log.Printf("✅ Prompt sent, messageID: %s", result.MessageID)

	// 7. 列出 sessions
	log.Println("\n=== 步骤 6: ListSessions (session/list) ===")
	sessions, err := acpAdapter.ListSessions(ctx, ref, agent.ListOptions{})
	if err != nil {
		log.Fatalf("ListSessions failed: %v", err)
	}
	log.Printf("✅ Found %d session(s)", len(sessions))
	for i, s := range sessions {
		log.Printf("  [%d] %s - %s", i, s.ID, s.Title)
	}

	// 8. 删除 session
	log.Println("\n=== 步骤 7: DeleteSession (session/delete) ===")
	if err := acpAdapter.DeleteSession(ctx, ref, sess.ID); err != nil {
		log.Fatalf("DeleteSession failed: %v", err)
	}
	log.Println("✅ Session deleted")

	log.Println("\n=== 所有测试通过 ===")
	log.Println("注：agent_echo 只是回显 params，不是真实 ACP agent")
	log.Println("真实 agent（Codex/Claude）需要实现完整 ACP 协议")
}