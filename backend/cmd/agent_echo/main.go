// agent_echo — 测试用 mock ACP agent
//
// 用法：
//
//	agent_echo -echo-only    仅启动不响应（用于 StartClose 测试）
//	agent_echo -echo         把每个 request 的 params 原样返回（用于 Call 测试）
//	agent_echo -hang         启动后永不响应（用于 Timeout 测试）
//
// 协议：JSON-RPC 2.0 over stdio，camelCase。
// 不实现完整 ACP — 只回显 params 作为 result（如果有 id）。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	var (
		echoOnly = flag.Bool("echo-only", false, "only start, do not respond")
		echo     = flag.Bool("echo", false, "echo back params as result")
		hang     = flag.Bool("hang", false, "do not respond (test timeouts)")
	)
	flag.Parse()

	mode := "echo"
	if *echoOnly {
		mode = "echo-only"
	} else if *hang {
		mode = "hang"
	} else if *echo {
		mode = "echo"
	}

	log.SetOutput(os.Stderr)
	log.Printf("agent_echo started mode=%s", mode)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var frame struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			// 坏帧：忽略继续
			continue
		}

		switch mode {
		case "echo-only", "hang":
			// 不响应
			continue
		case "echo":
			if frame.ID == nil {
				continue
			}
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      frame.ID,
				"result": map[string]any{
					"echoed": frame.Params,
				},
			}
			out, _ := json.Marshal(resp)
			fmt.Fprintf(os.Stdout, "%s\n", out)
		}
	}
}
