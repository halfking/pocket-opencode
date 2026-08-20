package main

// test_acp_stdio.go — 手动测试 ACP stdio transport + agent_echo
//
// 用途：验证我们的 StdioTransport 能否正确与 agent_echo 交互。

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/agent"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	
	// 1. 创建 stdio transport（连接到 agent_echo）
	tr := agent.NewStdioTransport(agent.TransportConfig{
		AgentPath: "/tmp/agent_echo",
		AgentArgs: []string{"-echo"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. 启动 transport
	log.Println("启动 StdioTransport...")
	if err := tr.Start(ctx); err != nil {
		log.Fatalf("Start failed: %v", err)
	}
	defer tr.Close()
	log.Println("✅ StdioTransport started")

	// 3. 调用 echo 方法（agent_echo 会原样返回 params）
	log.Println("发送 JSON-RPC call: echo")
	var result map[string]any
	err := tr.Call(ctx, "echo", map[string]any{
		"text": "hello from StdioTransport test",
		"timestamp": time.Now().Unix(),
	}, &result)
	
	if err != nil {
		log.Fatalf("Call failed: %v", err)
	}
	
	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	log.Printf("✅ Call succeeded, result:\n%s", resultJSON)

	// 4. 测试 notification（无返回值）
	log.Println("发送 JSON-RPC notification: log")
	err = tr.Notify(ctx, "log", map[string]any{
		"level": "info",
		"message": "test notification from StdioTransport",
	})
	if err != nil {
		log.Fatalf("Notify failed: %v", err)
	}
	log.Println("✅ Notify succeeded")

	// 5. 再次 call 验证连接稳定
	log.Println("再次发送 call: ping")
	var pingResult map[string]any
	err = tr.Call(ctx, "ping", nil, &pingResult)
	if err != nil {
		log.Fatalf("Ping failed: %v", err)
	}
	log.Printf("✅ Ping succeeded: %+v", pingResult)

	log.Println("\n=== 所有测试通过 ===")
}