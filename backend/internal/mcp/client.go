package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/halfking/pocket-opencode/backend/internal/outbound"
	"github.com/halfking/pocket-opencode/backend/internal/sizedread"
)

// Client MCP 客户端
type Client struct {
	mu          sync.Mutex
	initMu      sync.Mutex // 串行化 initialize 尝试；**不**缓存失败（见 ensureInitialized）
	baseURL     string
	secret      string   // HMAC 签名密钥（ACC 的 AUTH_TOKEN_SECRET / pocketd 侧 = POCKET_MCP_API_KEY）
	tenantID    string   // 内部 JWT 的 tenant_id claim（POCKET_MCP_TENANT_ID）
	scopes      []string // 内部 JWT 的 custom.scopes（默认 tasks,sessions）
	httpClient  *http.Client
	requestID   atomic.Int64
	sessionID   string
	initialized bool
	initTime    time.Time
}

// ACC MCP tool 名常量。ACC 侧 acc-go/internal/mcp/server.go 的 toolSpecs
// 已注册这些 tool，pocketd 只负责调用；名字写成常量避免散落字面量漂移。
const (
	ToolGetTasks      = "acc_get_tasks"
	ToolCreateTask    = "acc_create_task"
	ToolTaskClaim     = "acc_task_claim"
	ToolTaskComplete  = "acc_task_complete"
	ToolReportSession = "acc_report_session"
	ToolListSessions  = "acc_list_sessions"
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
// TenantID returns the configured ACC tenant. It is intentionally read-only;
// consumers use it to reject cross-workspace automation before a signed MCP
// call is issued.
func (c *Client) TenantID() string {
	if c == nil {
		return ""
	}
	return c.tenantID
}

func (c *Client) Capabilities() Capabilities {
	if c == nil {
		return Capabilities{}
	}
	tools := make([]string, 0, 2+len(writeTools))
	tools = append(tools, ToolGetTasks, ToolListSessions)
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
		httpClient: outbound.NewClient(30*time.Second, outbound.WithTransport(&http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecureTLS, // 生产环境必须为 false
			},
		})),
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

// httpStatusError 携带结构化的 HTTP 状态码，供 doRaw 判断是否回退 Bearer，
// 避免对错误字符串做子串匹配（响应体由远端控制，可能碰巧包含 "HTTP 401"）。
type httpStatusError struct {
	Status int
	Body   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

// doRaw 发送原始 HTTP 请求并返回完整响应体 + 响应头。
// acc-go 吃 HS256 JWT；本机 Node ACC 吃 api_keys 静态 Bearer。
// JWT 在 Node 上是 401，经 nginx 时常见空 body 的 400，两种都回退 raw secret。
// 按结构化状态码判定（httpStatusError），不对错误字符串做子串匹配——
// 响应体由远端控制，可能碰巧包含 "HTTP 401" 等字样而误触发回退。
func (c *Client) doRaw(ctx context.Context, payload []byte) ([]byte, http.Header, error) {
	body, headers, err := c.doRawWithBearer(ctx, payload, true)
	var statusErr *httpStatusError
	if err != nil && errors.As(err, &statusErr) &&
		(statusErr.Status == http.StatusUnauthorized || statusErr.Status == http.StatusBadRequest) {
		return c.doRawWithBearer(ctx, payload, false)
	}
	return body, headers, err
}

func (c *Client) doRawWithBearer(ctx context.Context, payload []byte, useJWT bool) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	if useJWT {
		token, err := c.signJWT(ctx)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.secret)
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

	body, err := sizedread.All(resp.Body, sizedread.MaxJSONBodyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 错误信息只截取前 512 字节，防止远端返回超大响应体把错误字符串撑爆。
		msg := body
		if len(msg) > 512 {
			msg = msg[:512]
		}
		return nil, nil, &httpStatusError{Status: resp.StatusCode, Body: string(msg)}
	}

	return body, resp.Header, nil
}

// mcpSessionTTL 是 MCP 会话在客户端侧的有效期（过期后重新握手）。
const mcpSessionTTL = 5 * time.Minute

// mcpRetryAttempts 是瞬时故障的最大尝试次数（含首次）。
const mcpRetryAttempts = 3

// mcpRetryBackoff 是重试之间的基础退避（按尝试次数线性放大）。
const mcpRetryBackoff = 300 * time.Millisecond

// isDialFailure：连接**没建起来**（请求根本没到服务器）。
// 这种失败重试绝对安全 —— 服务器不可能已经执行过这次写操作。
func isDialFailure(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}

// isTransientStatus：网关类状态码（429/502/503/504）⇒ 值得重试。
func isTransientStatus(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		switch se.Status {
		case http.StatusTooManyRequests, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// isTimeoutError：请求超时。
//
// ⚠ 超时**不能**用来重试写工具：超时只说明「我们没收到响应」，
// 服务器可能已经执行了（acc_create_task 没有幂等键 ⇒ 重发会建出重复任务）。
// 超时只在**幂等**步骤（initialize）上重试。
func isTimeoutError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// isTransportEOF：传输层被掐断（EOF / unexpected EOF / 连接被重置 / 管道断）。
//
// ★ 2026-10-26 补：ACC 的 MCP 面**就是用 EOF 把委派打死的** ——
//
//	实测（两处独立证据）：
//	  ① 今日早些时候：ACC 的 MCP 面返回过一次 EOF（POST /api/v2/mcp），
//	     之后 pocketd 的委派全部 502；
//	  ② 本轮 22:09 真机夹具：委托直接 502，
//	     错误是 `acc_create_task failed: MCP not initialized: initialize failed:
//	     failed to send request: Post "http://100.106.67.198:4101/api/v2/mcp": EOF`。
//	而 initialize 当时的重试集是「dial 失败 | 429/502/503/504 | 超时」——
//	**EOF 不在里面** ⇒ 一次 EOF 就让整个进程在 ACC 恢复之后**再也初始化不了**。
//	⇒ initialize 是**幂等**的（握手而已，重做一次没有副作用）⇒ EOF 必须重试。
//	⚠ 写工具**不**因此重试：EOF 同样可能发生在「服务器已经建好任务之后」，
//	   而 acc_create_task 没有幂等键 ⇒ 重发会建出重复任务。
func isTransportEOF(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// 连接被对端重置 / 管道断开：同样是「传输层被掐断」。
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	return false
}

// doRawWithRetry 发请求并对**瞬时**故障重试。
//
// retryAmbiguous=false ⇒ 只重试「请求没到服务器」的失败（dial 失败）。
// retryAmbiguous=true  ⇒ 还可以重试 429/502/503/504 与超时；
// 只能用于幂等步骤（initialize）。
func (c *Client) doRawWithRetry(ctx context.Context, payload []byte, retryAmbiguous bool) ([]byte, http.Header, error) {
	var lastErr error
	for attempt := 1; attempt <= mcpRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		body, headers, err := c.doRaw(ctx, payload)
		if err == nil {
			return body, headers, nil
		}
		lastErr = err
		retryable := isDialFailure(err)
		if retryAmbiguous {
			// 幂等步骤（initialize）：可以重试「请求可能没被处理」的失败 ——
			// 网关类状态码、超时、**传输层被掐断（EOF/连接重置）**。
			// ⚠ 写工具走的是 retryAmbiguous=false ⇒ 这些一律不重试
			//   （acc_create_task 没有幂等键，重发会建出重复任务）。
			retryable = retryable || isTransientStatus(err) || isTimeoutError(err) || isTransportEOF(err)
		}
		if !retryable || attempt == mcpRetryAttempts {
			return nil, nil, err
		}
		wait := mcpRetryBackoff * time.Duration(attempt)
		log.Printf("[MCP] transient failure (attempt %d/%d): %v — retrying in %s",
			attempt, mcpRetryAttempts, err, wait)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, nil, lastErr
}

// ensureInitialized 确保 MCP 会话已初始化。
//
// ★ 2026-10-26：这里原来用 sync.Once + 缓存的 initErr，
//
//	那是**一次瞬时故障 = 进程生命周期内永久失效**：
//	sync.Once 保证那段闭包一辈子只跑一次 ⇒ initialize 失败一次，
//	initErr 被永久钉住 ⇒ 此后每次 CallTool 都返回
//	「MCP not initialized: initialize failed: ...」，且**没有任何自愈**。
//	实测（2026-10-26 12:00 前后）ACC 的 MCP 面返回过次 EOF，
//	之后 pocketd 的委派全部失败，日志里再没有一次成功的 initialize。
//
// 现在：失败**不缓存**。用 initMu 串行化握手（避免并发重复握手），
// 但失败后 initialized 仍为 false ⇒ 下一次调用会重新握手。
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.mu.Lock()
	if c.initialized && time.Since(c.initTime) < mcpSessionTTL {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	c.initMu.Lock()
	defer c.initMu.Unlock()

	// 双重检查：等 initMu 期间可能已经被别的 goroutine 握手好了。
	c.mu.Lock()
	if c.initialized && time.Since(c.initTime) < mcpSessionTTL {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// ★ 失败不缓存：不写回任何「永久错误」状态。
	return c.doInitialize(ctx)
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

	body, headers, err := c.doRawWithRetry(ctx, initPayload, true)
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

	body, _, err := c.doRawWithRetry(ctx, payload, false)
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

// ParseToolTasks 解析 acc_get_tasks 返回的文本列表为结构化任务。
//
// 2026-09-09 修复：ACC 的 tool 现在返回 JSON（数组或含任务数组的对象），
// 旧的按行解析器会把 JSON 文本当作 "[status] id: title" 逐行拆——在 JSON
// 字符串值内部的 ": "（如 note 正文 "heartbeat: GP2b"）处切开，产出
// id=JSON 碎片、title=整段 JSON 的垃圾任务。tasksync 把垃圾 id 写进
// pocket.opencode_pocket.tasks 后，每次同步重插同一 id，PG 日志持续报
// duplicate key tasks_pkey（2026-09-08 04:53 起约 42 小时 508 次）。
// 因此先按 JSON 解析，失败才回退旧文本格式。
//
// 2026-09-10 二次加固：JSON 形态的输入（[ 或 { 开头且 json.Valid）绝不
// 回退旧文本解析器——合法 JSON 未识别出任务时（错误信封、响应性对象如
// {"message":"No tasks found"}）返回空结果。否则任何未来新增的响应形态
// 都会被 legacy 按 ": " 撕成垃圾 id，复发 tasks_pkey。任务判定契约：
// id 与 title 都非空才算任务对象（status 缺失回退 phase，与 ACC 看板一致）。
func ParseToolTasks(text string) []ParsedTask {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if tasks, ok := parseToolTasksJSON(text); ok {
		return tasks
	}
	// JSON 形态但未识别出任务结构：返回空而不是交给 legacy——
	// legacy 对 JSON 文本的输出是垃圾 id（事故根因）。
	if json.Valid([]byte(trimmed)) {
		return nil
	}
	return parseToolTasksLegacyLines(text)
}

// parseToolTasksJSON 尝试把 tool 文本按 JSON 解析。接受三种形态：
//   - 任务对象数组：[{"id":...,"title":...}, ...]
//   - 包一层任务数组：{"tasks":[...]} / {"data":[...]}
//   - 单个任务对象：{"id":...,"title":...}
//
// 解析成功（且至少识别出 JSON 结构）返回 ok=true；文本不是 JSON 时
// ok=false，由调用方回退旧格式。
func parseToolTasksJSON(text string) ([]ParsedTask, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '[' && trimmed[0] != '{') {
		return nil, false
	}

	// accTask 的字段映射按 ACC acc_get_tasks 实际返回（id/title/status/phase/
	// owner/agent_id）；status 缺失时回退 phase（ACC 以 phase 驱动看板）。
	type accTask struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Phase     string `json:"phase"`
		Owner     string `json:"owner"`
		AgentID   string `json:"agent_id"`
		CreatedAt string `json:"created_at"`
	}
	// 任务判定契约：id 与 title 都非空才算任务对象。仅凭 id 非空会把
	// {"id":"foo","message":"No tasks found"} 这类响应性 JSON 当成任务，
	// 写库后产生空 title 的脏行；status 缺失时回退 phase（ACC 以 phase 驱动
	// 看板），不作为任务判定条件。
	mapTasks := func(rows []accTask) []ParsedTask {
		tasks := make([]ParsedTask, 0, len(rows))
		for _, r := range rows {
			if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Title) == "" {
				continue
			}
			status := r.Status
			if status == "" {
				status = r.Phase
			}
			owner := r.Owner
			if owner == "" {
				owner = r.AgentID
			}
			tasks = append(tasks, ParsedTask{
				ID:     strings.TrimSpace(r.ID),
				Title:  r.Title,
				Status: status,
				Owner:  owner,
				// created_at 解析失败**不**丢弃这条任务，只是不带时间
				// （零值 CreatedAt 会让「按时间窗认领」跳过它 —— 那正是
				// 我们想要的保守行为：没有时间戳就不敢认领）。
				CreatedAt: parseAccCreatedAt(r.CreatedAt),
			})
		}
		return tasks
	}

	var rows []accTask
	if err := json.Unmarshal([]byte(trimmed), &rows); err == nil {
		return mapTasks(rows), true
	}

	var wrapped struct {
		Tasks []accTask `json:"tasks"`
		Data  []accTask `json:"data"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapped); err == nil {
		if wrapped.Tasks != nil {
			return mapTasks(wrapped.Tasks), true
		}
		if wrapped.Data != nil {
			return mapTasks(wrapped.Data), true
		}
	}

	var single accTask
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil {
		// 单对象与数组同一判定契约：id 与 title 都非空才算任务。
		// {"id":"solo","title":"one task"}（无 status）是合法任务；
		// {"id":"foo","message":"..."} 不是。
		if strings.TrimSpace(single.ID) == "" || strings.TrimSpace(single.Title) == "" {
			return nil, false
		}
		return mapTasks([]accTask{single}), true
	}
	return nil, false
}

// parseToolTasksLegacyLines 保留 ACC 旧文本格式的解析：
//
//	[status] task-id: title (owner: xxx)
func parseToolTasksLegacyLines(text string) []ParsedTask {
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
	// CreatedAt 是 ACC 侧建任务的时刻（acc_get_tasks 返回 created_at）。
	//
	// ★ 为什么必须有它：委派写请求失败后要判断「服务器到底建没建出任务」，
	//   而**只按标题找会认错人** —— 同名的残留任务在 ACC 里能堆几十条
	//   （实测 27 条 maestro-acc-approve）⇒ 命中一条旧任务就等于
	//   把本次委托绑到别人的任务上。⇒ 必须再加一道「创建时间不早于本次请求」，
	//   才敢认领。
	CreatedAt time.Time
}

// parseAccCreatedAt 解析 ACC 的 created_at（RFC3339，可能带小数秒）。
// 解析不了就返回零值 —— 调用方据此**放弃认领**，而不是猜。
func parseAccCreatedAt(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}

// createdAtSkewTolerance 是认领时的时钟偏差容忍度。
//
// ACC 的时钟与 pocketd 的时钟可能有几秒偏差 ⇒ 认领窗口往前放宽一点。
// 5 秒足够覆盖时钟偏差，又不至于宽到能认领上一轮的陈年任务。
const createdAtSkewTolerance = 5 * time.Second

// MatchTaskCreatedSince 在 tasks 里找出「标题相同、且创建时间不早于 since」的那条任务。
//
// ★ 这是「写请求失败后确认服务器到底建没建出来」的核心判据：
//   - 只按标题找会**认错人**（ACC 里同名残留能堆几十条）；
//   - 只按时间找会**认错内容**（别人也在建任务）。
//     两个条件都要。
//
// 多个候选时取**最新**的那条：本次请求是最后发出的那一个。
// ⚠ 没有 created_at 的任务**不参与**匹配（零值 CreatedAt）——
//
//	没有时间戳就不敢认领，这是保守方向。
func MatchTaskCreatedSince(tasks []ParsedTask, title string, since time.Time) (ParsedTask, bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return ParsedTask{}, false
	}
	cutoff := since.Add(-createdAtSkewTolerance)
	var best ParsedTask
	found := false
	for _, t := range tasks {
		if strings.TrimSpace(t.Title) != title {
			continue
		}
		if t.CreatedAt.IsZero() || t.CreatedAt.Before(cutoff) {
			continue
		}
		if !found || t.CreatedAt.After(best.CreatedAt) {
			best, found = t, true
		}
	}
	return best, found
}

// FindTaskCreatedSince 从 ACC 读任务列表并按 MatchTaskCreatedSince 认领。
func (c *Client) FindTaskCreatedSince(ctx context.Context, title string, since time.Time) (ParsedTask, bool, error) {
	if c == nil {
		return ParsedTask{}, false, fmt.Errorf("mcp: MCP client not configured")
	}
	tasks, err := c.GetRemoteTasks(ctx, "", 200)
	if err != nil {
		return ParsedTask{}, false, err
	}
	t, ok := MatchTaskCreatedSince(tasks, title, since)
	return t, ok, nil
}

// IsAmbiguousWriteFailure 判断一个**写**工具的失败是不是「结果未知」。
//
// ★ 含义：请求可能已经到了服务器、只是响应没回来（EOF / 连接被重置 / 超时）。
//
//	⇒ 这种失败**不能盲目重试**（acc_create_task 没有幂等键 ⇒ 重发会建出重复任务），
//	  但也**不能直接当成失败**（服务器可能已经建好了）⇒ 必须先「问服务器」。
//
// 与 isTransportEOF 的区别：那个只认传输层掐断，这个还认超时
// （超时同样是「我们没收到响应」，语义上同样是不确定）。
func IsAmbiguousWriteFailure(err error) bool {
	if err == nil {
		return false
	}
	if isTransportEOF(err) {
		return true
	}
	return isTimeoutError(err)
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
// args 由调用方按 canonical contract 传递；返回 ACC tool 文本载荷。
func (c *Client) CreateTask(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.callWriteTool(ctx, ToolCreateTask, args)
}

// ResolveTaskRun validates and returns an existing canonical ACC task/run.
func (c *Client) ResolveTaskRun(ctx context.Context, taskID, runID string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%s failed: MCP client not configured", "acc_resolve_task_run")
	}
	return c.CallTool(ctx, "acc_resolve_task_run", map[string]interface{}{"task_id": taskID, "run_id": runID})
}

// CanonicalTaskResult is the structured subset required for Pocket binding.
type CanonicalTaskResult struct {
	TaskID      string `json:"task_id"`
	RunID       string `json:"run_id"`
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	ID          string `json:"id,omitempty"`
}

// ParseCanonicalTaskResult accepts a JSON tool payload, including common data envelopes.
func ParseCanonicalTaskResult(text string) (CanonicalTaskResult, error) {
	var direct CanonicalTaskResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &direct); err == nil {
		if direct.TaskID == "" {
			direct.TaskID = direct.ID
		}
		if direct.TaskID != "" {
			return direct, nil
		}
	}
	var env struct {
		Data CanonicalTaskResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &env); err == nil {
		if env.Data.TaskID == "" {
			env.Data.TaskID = env.Data.ID
		}
		if env.Data.TaskID != "" {
			return env.Data, nil
		}
	}
	return CanonicalTaskResult{}, fmt.Errorf("ACC create task response missing task_id and run_id")
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

// AccSession is one row from acc_list_sessions.
//
// 字段必须与 ACC 的 sessionmanager.SessionRecord **顶层** JSON 对齐
// （acc-go internal/sessionmanager/session_record.go）。2026-10-22 实测发现
// 旧定义与之几乎全不重合：ACC 返回的是
//
//	{"id","gateway_type","task_id","input","output","input_tokens","output_tokens",
//	 "agent_kind","agent_session_id","gw_session_id","session_path","dispatch_id",
//	 "tenant_id","created_at"}
//
// 而旧结构体读的是 "session_id"/"metadata"/"started_at"/"ended_at" —— 其中
// "metadata" **ACC 从不返回**。后果不是"少几个字段"，而是会话的标识全丢：
// accSessionToRow 从 Metadata 里取 agent_session_id/gw_session_id，取不到就
// 退到 row.SessionID（也是空）⇒ 拿到的会话 id 为空串。
// 实测症状：`GET /api/tasks/<acc task_id>/session-bundle` 返回
// `{"current":[{"id":"", ...}]}` —— id 是空的，会话根本没法被点开。
//
// 所以这里补齐 ACC 实际返回的字段；老的 metadata / started_at / ended_at
// 保留为向后兼容的兜底（更老的 ACC 版本或历史上报确实带过）。
type AccSession struct {
	// ID 是 ACC 给这条上报记录的主键。
	ID string `json:"id"`

	// TaskID 是本次上报归属的 ACC 任务（形如
	// "<agent-id>:dispatch_<uuid>"）。
	TaskID      string `json:"task_id"`
	GatewayType string `json:"gateway_type"`
	AgentID     string `json:"agent_id"`
	DeviceName  string `json:"device_name"`
	ModelID     string `json:"model_id"`

	// Input/Output 是本次上报的提示词与产出。
	Input        string `json:"input"`
	Output       string `json:"output"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`

	// AgentKind 是智能体种类（pi/claude/codex…）。
	AgentKind string `json:"agent_kind"`
	// AgentSessionID 是**智能体自己的**会话 id（可续跑的线程），也就是
	// agent-companion 侧的 session id —— 用它回查 companion 的 transcript。
	AgentSessionID string `json:"agent_session_id"`
	// GwSessionID 是网关侧会话 id。
	GwSessionID string `json:"gw_session_id"`
	// SessionPath 是智能体本地会话文件路径。
	SessionPath string `json:"session_path"`
	// DispatchID 是派发命令 id。
	DispatchID string `json:"dispatch_id"`

	TenantID string `json:"tenant_id"`

	// CreatedAt 是上报时刻（RFC3339）。
	CreatedAt string `json:"created_at"`

	// ---- 向后兼容兜底 ----
	// SessionID 是老版本 pocketd 期望的字段名；现在 ACC 用 "id"。
	SessionID string `json:"session_id"`
	// StartedAt/EndedAt 是老版本上报格式的时间字段；现在 ACC 用 created_at。
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	// Metadata 是老版本上报格式里携带 agent_session_id/gw_session_id 的
	// 嵌套对象；ACC 现版本不再返回它，但历史数据里可能还有。
	Metadata json.RawMessage `json:"metadata"`
}

// ListSessions 按 task_id 拉 ACC 已上报会话。
type AccRunEvent struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	TenantID  string `json:"tenant_id"`
	RunID     string `json:"run_id"`
	TaskID    string `json:"task_id,omitempty"`
	Sequence  uint64 `json:"sequence"`
	// DispatchID is the ACC command (dispatch) the event belongs to. It
	// appears on every dispatch.* event and is what a cancel must target.
	//
	// It was NOT decoded before (2026-10-25): the struct only carried the
	// event envelope, so the one canonical id a stop needs was thrown away
	// even though ACC puts it right there in the payload.
	DispatchID string `json:"dispatch_id,omitempty"`
	// ActorID/ActorType identify who acted: for dispatch.leased and
	// dispatch.started, ActorID is the **runtime-control lease holder**
	// (the companion runtime id) — the other half of the holder fence.
	ActorID   string          `json:"actor_id,omitempty"`
	ActorType string          `json:"actor_type,omitempty"`
	NewState  string          `json:"new_state,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// ListRunEvents reads canonical ACC orchestration history. The endpoint is
// intentionally HTTP, because ACC's durable event stream is not an MCP tool.
func (c *Client) ListRunEvents(ctx context.Context, runID string, after uint64) ([]AccRunEvent, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("ACC event client not configured")
	}
	tok, err := c.signJWT(ctx)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(c.baseURL, "/")
	base = strings.TrimSuffix(base, "/api/v2/mcp")
	u := base + "/api/v2/orchestration/runs/" + url.PathEscape(runID) + "/events"
	if after > 0 {
		u += "?after=" + strconv.FormatUint(after, 10)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ACC events request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("ACC events HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Events []AccRunEvent `json:"events"`
	}
	if err := json.NewDecoder(sizedread.Limit(resp.Body, sizedread.MaxJSONBodyBytes)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("ACC events decode: %w", err)
	}
	return envelope.Events, nil
}

func (c *Client) ListSessions(ctx context.Context, taskID string, limit int) ([]AccSession, error) {
	if c == nil {
		return nil, fmt.Errorf("%s failed: MCP client not configured", ToolListSessions)
	}
	if limit <= 0 {
		limit = 50
	}
	text, err := c.CallTool(ctx, ToolListSessions, map[string]interface{}{
		"task_id": taskID,
		"limit":   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", ToolListSessions, err)
	}
	var rows []AccSession
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("%s parse: %w", ToolListSessions, err)
	}
	return rows, nil
}

// ListAllSessions 列出 ACC 里**全部**已上报会话（不带 task_id 过滤）。
//
// 为什么需要它：ListSessions 只能按 task_id 查，而 ACC 的 canonical task
// 列表（/api/v2/canonical/tasks）在本机实测是空的 ⇒ ACC 里有会话、却没有
// 对应的 task 记录 ⇒ 按 task 查不到任何东西，会话于是"存在于 ACC 但在
// pocketd 里不可见"。acc_list_sessions 不带 task_id 时返回全量（租户内），
// 这条路才拿得到。
//
// limit 上限 200（ACC 侧硬上限，超出会被截到 200）。
func (c *Client) ListAllSessions(ctx context.Context, limit int) ([]AccSession, error) {
	if c == nil {
		return nil, fmt.Errorf("%s failed: MCP client not configured", ToolListSessions)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	text, err := c.CallTool(ctx, ToolListSessions, map[string]interface{}{
		"limit": limit,
	})
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", ToolListSessions, err)
	}
	var rows []AccSession
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("%s parse: %w", ToolListSessions, err)
	}
	return rows, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
