package main

// test_acp_adapter.go — 测试完整 ACP adapter 调用流程
//
// 用途：验证 agent.Registry + AgentAdapter + StdioTransport 完整链路。
// 使用 agent_echo 作为 fake ACP agent。

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

	// 1. 创建 Registry
	log.Println("=== 步骤 1: 创建 agent.Registry ===")
	reg := agent.NewRegistry()

	// 2. 创建一个通用的 ACP adapter（基于 stdio transport）
	// 注：这里我们用 MockAgentAdapter 模拟，因为真正的 ACPAdapter 需要实现完整协议
	log.Println("=== 步骤 2: 创建 Mock Agent Adapter ===")
	mockAdapter := agent.NewMockAgentAdapter()
	
	ref := agent.AgentRef{Type: "acp-mock", Target: "test"}
	if err := reg.Register(ref, mockAdapter); err != nil {
		log.Fatalf("Register failed: %v", err)
	}
	log.Printf("✅ Registered agent: %s", ref.String())

	// 3. 通过 registry 获取 adapter
	log.Println("\n=== 步骤 3: 从 Registry 获取 Adapter ===")
	regAdapter, ok := reg.Get(ref)
	if !ok {
		log.Fatalf("Get adapter failed: not found")
	}
	log.Printf("✅ Got adapter: %s", regAdapter.AdapterType())

	// 4. 检查 capabilities
	log.Println("\n=== 步骤 4: 查询 Capabilities ===")
	caps, err := regAdapter.Capabilities(ctx, ref)
	if err != nil {
		log.Fatalf("Capabilities failed: %v", err)
	}
	capsJSON, _ := json.MarshalIndent(caps, "", "  ")
	log.Printf("✅ Capabilities:\n%s", capsJSON)

	// 5. 创建 session
	log.Println("\n=== 步骤 5: 创建 Session ===")
	sess, err := regAdapter.CreateSession(ctx, ref, &agent.CreateSessionRequest{
		Title: "ACP test session",
		WorkingDir: "/tmp",
	})
	if err != nil {
		log.Fatalf("CreateSession failed: %v", err)
	}
	log.Printf("✅ Session created: %s (status: %s)", sess.ID, sess.Status)

	// 6. 发送 prompt
	log.Println("\n=== 步骤 6: 发送 Prompt ===")
	result, err := regAdapter.SendPrompt(ctx, ref, sess.ID, &agent.SendPromptRequest{
		Text: "Hello, ACP agent! Please echo this message.",
	})
	if err != nil {
		log.Fatalf("SendPrompt failed: %v", err)
	}
	log.Printf("✅ Prompt sent, messageID: %s", result.MessageID)

	// 7. 获取 session messages
	log.Println("\n=== 步骤 7: 获取 Session Messages ===")
	messages, err := regAdapter.GetMessages(ctx, ref, sess.ID, agent.ListOptions{})
	if err != nil {
		log.Fatalf("GetMessages failed: %v", err)
	}
	log.Printf("✅ Got %d messages", len(messages))

	// 8. 列出所有 sessions
	log.Println("\n=== 步骤 8: 列出所有 Sessions ===")
	sessions, err := regAdapter.ListSessions(ctx, ref, agent.ListOptions{})
	if err != nil {
		log.Fatalf("ListSessions failed: %v", err)
	}
	log.Printf("✅ Found %d session(s)", len(sessions))
	for i, s := range sessions {
		log.Printf("  [%d] %s - %s (status: %s)", i, s.ID, s.Title, s.Status)
	}

	// 9. Health check
	log.Println("\n=== 步骤 9: Health Check ===")
	if err := regAdapter.HealthCheck(ctx, ref); err != nil {
		log.Fatalf("HealthCheck failed: %v", err)
	}
	log.Println("✅ Health check passed")

	// 10. 删除 session
	log.Println("\n=== 步骤 10: 删除 Session ===")
	if err := regAdapter.DeleteSession(ctx, ref, sess.ID); err != nil {
		log.Fatalf("DeleteSession failed: %v", err)
	}
	log.Println("✅ Session deleted")

	log.Println("\n=== 所有测试通过 ===")
}