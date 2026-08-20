package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Client MCP 客户端
type Client struct {
	mu          sync.Mutex
	baseURL     string
	secret      string   // HMAC 签名密钥（ACC 的 AUTH_TOKEN_SECRET / pocketd 侧 = POCKET_MCP_API_KEY）
	tenantID    string   // 内部 JWT 的 tenant_id claim（POCKET_MCP_TENANT_ID）
	scopes      []string // 内部 JWT 的 custom.scopes（默认 tasks,sessions）
	httpClient  *http.Client
	requestID   atomic.Int64
	sessionID   string
	initialized bool
	initTime    time.Time
	initOnce    sync.Once // 确保 initialize 只执行一次
	initErr     error     // 缓存初始化错误
}

// ACC MCP tool 名常量。ACC 侧 acc-go/internal/mcp/server.go 的 toolSpecs
// 已注册这些 tool，pocketd 只负责调用；名字写成常量避免散落字面量漂移。
const (
	ToolGetTasks      = "acc_get_tasks"
	ToolCreateTask    = "acc_create_task"
	ToolTaskClaim     = "acc_task_claim"
	ToolTaskComplete  = "acc_task_complete"
	ToolReportSession = "acc_report_session"
)

// writeTools 是 pocketd 会主动调用的 ACC 写 tool 列表（Capabilities 与
// 实际方法必须同源，避免声明与实现漂移）。
var writeTools = []string{ToolCreateTask, ToolTaskClaim, ToolTaskComplete, ToolReportSession}

// Capabilities 描述当前 MCP 客户端声明的能力。
//
// T1.2 双向化：除只读的 GetRemoteTasks（acc_get_tasks）之外，pocketd 现在
// 还会调用 ACC 已注册的写 tool（acc_create_task / acc_task_claim /
// acc_task_complete / acc_report_session），因此 Write 置 true，Tools 同步
// 列出全部可调用 tool。
//
// 字段命名/语义与 docs/优化v4/04 §7.4 一致；写路径的鉴权与租户隔离由 ACC
// 侧 MCP server（Bearer token + scope + RLS）负责，pocketd 只透传。
func (c *Client) Capabilities() Capabilities {
	if c == nil {
		return Capabilities{}
	}
	tools := make([]string, 0, 1+len(writeTools))
	tools = append(tools, ToolGetTasks)
	tools = append(tools, writeTools...)
	return Capabilities{
		Connector: "acc",
		Read:      true,
		Write:     true,
		Tools:     tools,
	}
}

// Capabilities 是 MCP connector 能力声明。
type Capabilities struct {
	Connector string   `json:"connector"` // 例如 "acc"
	Read      bool     `json:"read"`
	Write     bool     `json:"write"`
	Tools     []string `json:"tools,omitempty"` // 当前实际可调用的 tool 列表
}

// NewClient 兼容旧调用点的包装：把 apiKey 当作 HMAC 密钥（ACC token secret），
// 不再发送静态 bearer 而是签署内部 JWT；tenant_id 留空、scopes 走默认
// （tasks,sessions）。新代码应改用 NewClientWithAuth。
func NewClient(baseURL, apiKey string, insecureTLS bool) *Client {
	return NewClientWithAuth(baseURL, apiKey, "", nil, insecureTLS)
}

// NewClientWithAuth 创建带 HMAC 内部 JWT 签名的 MCP 客户端。
// secret = ACC token secret（pocketd 侧即 POCKET_MCP_API_KEY），tenantID =
// POCKET_MCP_TENANT_ID（ACC 要求非空），scopes 缺省为 {"tasks","sessions"}。
func NewClientWithAuth(baseURL, secret, tenantID string, scopes []string, insecureTLS bool) *Client {
	if len(scopes) == 0 {
		scopes = []string{"tasks", "sessions"}
	}
	scopesCopy := make([]string, len(scopes))
	copy(scopesCopy, scopes)
	return &Client{
		baseURL:  baseURL,
		secret:   secret,
		tenantID: tenantID,
		scopes:   scopesCopy,
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

// mcpClaims 是 pocketd 签发给 ACC 的内部 JWT payload，字段与 acc-go
// internal/auth.Claims 对齐（见 auth.go ResolveToken / VerifyInternal）。
// scopes 放在 Custom["scopes"]，ACC 的 scopeAllowed 从 Custom 读取。
type mcpClaims struct {
	UserID   string         `json:"sub,omitempty"`
	Username string         `json:"username,omitempty"`
	Roles    []string       `json:"roles,omitempty"`
	IsAdmin  bool           `json:"isAdmin,omitempty"`
	TenantID string         `json:"tenant_id,omitempty"`
	Custom   map[string]any `json:"custom,omitempty"`
	jwt.RegisteredClaims
}

// signJWT 用 secret（ACC 的 AUTH_TOKEN_SECRET）签署 HS256 内部 JWT。
// 每次调用都生成新 token（携带当前 iat/exp），claims 含 tenant_id 与
// custom.scopes = {tasks, sessions}，匹配 ACC mcp/server.go 的 tool scope：
//   - 任务类 tool（acc_create_task / acc_task_claim / acc_task_complete / acc_get_tasks）→ "tasks"
//   - acc_report_session → "sessions"
func (c *Client) signJWT(ctx context.Context) (string, error) {
	if c.secret == "" {
		return "", fmt.Errorf("mcp: HMAC signing secret (ACC token secret / POCKET_MCP_API_KEY) not configured")
	}
	now := time.Now()
	claims := mcpClaims{
		UserID:   "pocketd",
		TenantID: c.tenantID,
		Custom:   map[string]any{"scopes": c.scopes},
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			Issuer:    "pocketd",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(c.secret))
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

	// Gap 3: ACC 要求 HMAC 签名的内部 JWT（HS256），而非静态 bearer。
	// 每次请求重新签署（TTL 15min），claims 携带 tenant_id 与 scopes。
	token, err := c.signJWT(ctx)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

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

// ensureInitialized 确保 MCP 会话已初始化（并发安全，使用 sync.Once 确保单次执行）
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.mu.Lock()
	if c.initialized && time.Since(c.initTime) < 5*time.Minute {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// 使用 sync.Once 确保多个并发调用只有一个执行初始化
	c.initOnce.Do(func() {
		c.initErr = c.doInitialize(ctx)
	})

	return c.initErr
}

// doInitialize 执行实际的初始化流程（由 sync.Once 保证单次执行）
func (c *Client) doInitialize(ctx context.Context) error {
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
		// 忽略通知错误（MCP 协议允许客户端不处理 notifications 响应）
		// 但记录到日志以便调试连接问题
		log.Printf("[MCP] notifications/initialized failed (ignored): %v", err)
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
		IsError bool `json:"isError"`
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

	// MCP 的 tool 级错误走 result.isError（而非 JSON-RPC error），文本载荷是
	// 错误信息本身。不识别它会把 "forbidden: scope tasks required" 当成成功
	// 返回值——写路径尤其危险，所以在这里统一转成 Go error。
	if toolResult.IsError {
		return "", fmt.Errorf("tools/call(%s) returned tool error: %s", toolName, toolResult.Content[0].Text)
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

	text, err := c.CallTool(ctx, ToolGetTasks, args)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", ToolGetTasks, err)
	}

	return ParseToolTasks(text), nil
}

// ---- T1.2 双向 MCP：ACC 写 tool 调用 ----
//
// 这四个方法是 pocketd → ACC 的写路径，全部复用 CallTool 的握手/SSE 解析
// плumbing，只固定 tool 名。args 直接透传给 ACC（ACC 侧 dispatch 自行取
// 需要的字段），因此 pocketd 不重复一遍 ACC 的入参 schema——ACC 改字段时
// 这里无需跟着改。
//
// 返回值是 tool 的文本载荷（ACC 用 toolJSON 序列化的 JSON 字符串），调用方
// 按需 json.Unmarshal。租户/scope 校验在 ACC 侧完成（Bearer token → claims）。

// callWriteTool 是四个写方法的公共入口：统一 nil 客户端守卫、args 兜底与
// 错误包装。
func (c *Client) callWriteTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%s failed: MCP client not configured", toolName)
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	text, err := c.CallTool(ctx, toolName, args)
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", toolName, err)
	}
	return text, nil
}

// CreateTask 在 ACC 建任务（acc_create_task）。
// 常用 args：kind / title / description。
func (c *Client) CreateTask(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.callWriteTool(ctx, ToolCreateTask, args)
}

// ClaimTask 为本机 agent 认领 ACC 任务（acc_task_claim）。
// 常用 args：task_id / agent_id。
func (c *Client) ClaimTask(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.callWriteTool(ctx, ToolTaskClaim, args)
}

// CompleteTask 上报任务尝试完成（acc_task_complete）。
// 常用 args：task_id / status / result。
func (c *Client) CompleteTask(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.callWriteTool(ctx, ToolTaskComplete, args)
}

// ReportSession 把本机聚合到的会话上报 ACC（acc_report_session）。
// 常用 args：session_id / agent / title / project_path / message_count。
// disk adapter（internal/adapter/disk）产出的会话元数据经此上送。
func (c *Client) ReportSession(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.callWriteTool(ctx, ToolReportSession, args)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
