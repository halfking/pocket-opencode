package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client MCP 客户端
type Client struct {
	mu         sync.Mutex
	baseURL    string
	apiKey     string
	httpClient *http.Client
	requestID  atomic.Int64
	sessionID  string
	initialized bool
	initTime   time.Time
}

// NewClient 创建新的 MCP 客户端。
// insecureTLS=true 时跳过 TLS 证书验证（仅限开发/内网自签证书场景，生产必须 false）。
func NewClient(baseURL, apiKey string, insecureTLS bool) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: insecureTLS, // 生产环境必须为 false
				},
			},
		},
	}
}

// JSONRPCRequest JSON-RPC 2.0 请求结构
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      int64       `json:"id"`
}

// JSONRPCResponse JSON-RPC 2.0 响应结构
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

// RPCError JSON-RPC 错误结构
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// parseSSEResponse 从 SSE 格式的响应中提取 JSON
func parseSSEResponse(data string) (json.RawMessage, error) {
	// SSE 格式: event: message\ndata: {...}\n
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonStr := strings.TrimPrefix(line, "data: ")
			return json.RawMessage(jsonStr), nil
		}
	}
	// 如果不是 SSE 格式，尝试直接解析为 JSON
	if strings.HasPrefix(data, "{") {
		return json.RawMessage(data), nil
	}
	return nil, fmt.Errorf("no JSON data found in SSE response: %s", data[:min(100, len(data))])
}

// doRaw 发送原始 HTTP 请求并返回完整响应体 + 响应头
func (c *Client) doRaw(ctx context.Context, payload []byte) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("mcp-session-id", c.sessionID)
	}
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, resp.Header, nil
}

// ensureInitialized 确保 MCP 会话已初始化
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.mu.Lock()
	if c.initialized && time.Since(c.initTime) < 5*time.Minute {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// Step 1: Initialize
	initPayload, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "opencode-pocket",
				"version": "1.0.0",
			},
		},
		ID: c.requestID.Add(1),
	})

	body, headers, err := c.doRaw(ctx, initPayload)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	// 从响应头获取 session ID
	sessionID := headers.Get("mcp-session-id")
	if sessionID == "" {
		return fmt.Errorf("no mcp-session-id in initialize response")
	}

	c.mu.Lock()
	c.sessionID = sessionID
	c.mu.Unlock()

	// 解析 initialize 响应
	_, err = parseSSEResponse(string(body))
	if err != nil {
		return fmt.Errorf("failed to parse initialize response: %w", err)
	}

	// Step 2: notifications/initialized (不需要 session ID 就能成功)
	notifPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	})

	// 发送 notifications/initialized（不需要响应）
	if _, _, err := c.doRaw(ctx, notifPayload); err != nil {
		// 忽略通知错误（通知不需要响应）
		_ = err
	}

	c.mu.Lock()
	c.initialized = true
	c.initTime = time.Now()
	c.mu.Unlock()

	return nil
}

// CallTool 调用 MCP 工具（完整握手流程）
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	// 确保已初始化
	if err := c.ensureInitialized(ctx); err != nil {
		return "", fmt.Errorf("MCP not initialized: %w", err)
	}

	// Step 3: tools/call
	payload, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
		ID: c.requestID.Add(1),
	})

	body, _, err := c.doRaw(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("tools/call(%s) failed: %w", toolName, err)
	}

	// 解析 SSE 响应，提取 JSON-RPC 的 result 字段
	raw, err := parseSSEResponse(string(body))
	if err != nil {
		return "", fmt.Errorf("failed to parse tools/call response: %w", err)
	}

	// 先解析 JSON-RPC 外层（提取 result）
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal RPC response: %w", err)
	}
	if rpcResp.Error != nil {
		return "", rpcResp.Error
	}

	// 再解析 result 中的 content
	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err != nil {
		return "", fmt.Errorf("failed to unmarshal tool result: %w", err)
	}

	if len(toolResult.Content) == 0 {
		return "", fmt.Errorf("no content in tool result")
	}

	return toolResult.Content[0].Text, nil
}

// Call 直接调用 MCP 方法（简单方法调用）
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("MCP not initialized: %w", err)
	}

	reqID := c.requestID.Add(1)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      reqID,
	}

	reqBody, _ := json.Marshal(req)
	body, _, err := c.doRaw(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("Call(%s) failed: %w", method, err)
	}

	raw, err := parseSSEResponse(string(body))
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal: %w", err)
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Result, nil
}

// ParseToolTasks 解析 acc_get_tasks 返回的文本列表为结构化任务
func ParseToolTasks(text string) []ParsedTask {
	lines := strings.Split(text, "\n")
	tasks := make([]ParsedTask, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "No tasks found." {
			continue
		}

		task := ParsedTask{}

		// 格式: [status] task-id: title (owner: xxx)
		if strings.HasPrefix(line, "[") {
			closeBracket := strings.Index(line, "]")
			if closeBracket > 0 {
				task.Status = strings.TrimSpace(line[1:closeBracket])
				line = strings.TrimSpace(line[closeBracket+1:])
			}
		}

		// 提取 task-id: title
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) >= 1 {
			task.ID = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			remainder := parts[1]
			// 提取 owner
			if idx := strings.LastIndex(remainder, "(owner: "); idx > 0 {
				task.Title = strings.TrimSpace(remainder[:idx])
				owner := remainder[idx+8:]
				task.Owner = strings.TrimRight(owner, ")")
			} else {
				task.Title = strings.TrimSpace(remainder)
			}
		}

		if task.ID != "" {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// ParsedTask 解析后的任务结构
type ParsedTask struct {
	ID     string
	Title  string
	Status string
	Owner  string
}

// GetRemoteTasks 获取远程任务列表
func (c *Client) GetRemoteTasks(ctx context.Context, status string, limit int) ([]ParsedTask, error) {
	args := map[string]interface{}{
		"limit": limit,
	}
	if status != "" {
		args["status"] = status
	}

	text, err := c.CallTool(ctx, "acc_get_tasks", args)
	if err != nil {
		return nil, fmt.Errorf("acc_get_tasks failed: %w", err)
	}

	return ParseToolTasks(text), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
