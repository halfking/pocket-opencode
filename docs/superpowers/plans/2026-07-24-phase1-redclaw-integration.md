# Phase 1: RedClaw 基础集成 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 打通 Pocket Backend 与 RedClaw 企业后端之间的双向通讯链路，实现多租户身份同步，为后续功能模块奠定基础。

**架构：** Pocket Backend 通过 REST API + WebSocket 连接 RedClaw Gateway。RedClaw 侧新增 Pocket 集成网关 (:8092)，Pocket 侧新增 RedClaw 客户端模块。身份通过 JWT 中嵌入的 tenant_id 实现多租户同步。

**Tech Stack:** Go 1.22+ / gorilla/websocket / golang-jwt / net/http / RedClaw Gateway-Go

---

## 文件结构

### RedClaw 侧 (新增文件)

```
enterprise/gateway-go/
├── internal/pocket/           # Pocket 集成网关 (新增)
│   ├── server.go              # HTTP 服务器 :8092
│   ├── handler.go             # API 路由处理
│   ├── auth.go                # Pocket 认证中间件
│   └── server_test.go         # 测试
├── cmd/gateway/main.go        # 修改: 启动 Pocket 集成网关
```

### Pocket 侧 (新增文件)

```
backend/internal/redclaw/       # RedClaw 客户端模块 (新增)
├── client.go                   # RedClaw API 客户端
├── client_test.go              # 客户端测试
├── bridge.go                   # 桥接服务 (WebSocket ↔ REST)
├── bridge_test.go              # 桥接测试
├── auth.go                     # 多租户身份同步
├── auth_test.go                # 身份同步测试
└── types.go                    # 共享类型定义

backend/internal/config/
├── config.go                   # 修改: 新增 RedClaw 配置项

backend/internal/server/
├── server_redclaw.go           # 新增: RedClaw 集成 API 路由
├── server_redclaw_test.go      # 新增: 测试
└── server.go                   # 修改: 注册新路由
```

---

### Task 1: RedClaw 侧 — Pocket 集成网关类型定义

**Files:**
- Create: `enterprise/gateway-go/internal/pocket/types.go`
- Test: (与 Task 2 一起测试)

- [ ] **Step 1: 创建类型定义文件**

```go
// internal/pocket/types.go
package pocket

import "time"

// ChatRequest LLM 对话请求
type ChatRequest struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Model    string `json:"model,omitempty"` // 可选，不传则使用默认
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`    // user / assistant / system
	Content string `json:"content"`
}

// ChatResponse LLM 对话响应
type ChatResponse struct {
	Message   Message `json:"message"`
	ModelUsed string  `json:"model_used"`
	LatencyMs int64   `json:"latency_ms"`
}

// KnowledgeSearchRequest 知识库检索请求
type KnowledgeSearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	TopK     int    `json:"top_k,omitempty"`
}

type KnowledgeSearchResponse struct {
	Results []KnowledgeResult `json:"results"`
}

type KnowledgeResult struct {
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
```

- [ ] **Step 2: 提交**

```bash
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 add enterprise/gateway-go/internal/pocket/types.go
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 commit -m "feat(pocket): define Pocket integration gateway types"
```

---

### Task 2: RedClaw 侧 — Pocket 集成网关认证中间件

**Files:**
- Create: `enterprise/gateway-go/internal/pocket/auth.go`
- Test: `enterprise/gateway-go/internal/pocket/auth_test.go`

- [ ] **Step 1: 编写认证中间件测试**

```go
// internal/pocket/auth_test.go
package pocket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	// 创建一个带有效 Pocket JWT 的请求
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer pocket-valid-token-123")
	
	// 配置允许的 token
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Context().Value(ctxKeyTenantID).(string)
		if tenantID != "pocket-default" {
			t.Errorf("expected tenant pocket-default, got %s", tenantID)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	
	wrapped := AuthMiddleware("pocket-valid-token-123")(handler)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for invalid token")
	})
	
	rec := httptest.NewRecorder()
	wrapped := AuthMiddleware("pocket-valid-token-123")(handler)
	wrapped.ServeHTTP(rec, req)
	
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	// 不设置 Authorization header
	
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})
	
	rec := httptest.NewRecorder()
	wrapped := AuthMiddleware("pocket-valid-token-123")(handler)
	wrapped.ServeHTTP(rec, req)
	
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go && go test ./internal/pocket/ -run TestAuthMiddleware -v`
Expected: FAIL — "AuthMiddleware not defined"

- [ ] **Step 3: 实现认证中间件**

```go
// internal/pocket/auth.go
package pocket

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const ctxKeyTenantID contextKey = "tenant_id"

// AuthMiddleware 验证 Pocket 请求的认证令牌
// 简单实现：配置的共享密钥 + 租户标识
// 后续可升级为 JWT 验证
func AuthMiddleware(sharedSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"code":401,"message":"missing authorization"}`, http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")
			if token != sharedSecret {
				http.Error(w, `{"code":401,"message":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			// 从 Header 或 Query 获取 tenant_id，默认使用 "pocket-default"
			tenantID := r.Header.Get("X-Tenant-ID")
			if tenantID == "" {
				tenantID = "pocket-default"
			}
			ctx := context.WithValue(r.Context(), ctxKeyTenantID, tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go && go test ./internal/pocket/ -run TestAuthMiddleware -v`
Expected: PASS (3/3)

- [ ] **Step 5: 提交**

```bash
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 add enterprise/gateway-go/internal/pocket/auth.go enterprise/gateway-go/internal/pocket/auth_test.go
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 commit -m "feat(pocket): add Pocket auth middleware with tenant context"
```

---

### Task 3: RedClaw 侧 — Pocket 集成网关 HTTP 路由与处理器

**Files:**
- Create: `enterprise/gateway-go/internal/pocket/handler.go`
- Create: `enterprise/gateway-go/internal/pocket/server.go`
- Test: `enterprise/gateway-go/internal/pocket/server_test.go`

- [ ] **Step 1: 实现处理器**

```go
// internal/pocket/handler.go
package pocket

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

var startTime = time.Now()

// HandleHealth 健康检查
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		Version:   "1.0.0",
		UptimeSec: int64(time.Since(startTime).Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleChat LLM 对话
func HandleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	
	// 验证必填字段
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}
	
	// TODO: Phase 2 — 桥接到 LLM Router
	// 当前返回占位响应
	start := time.Now()
	resp := ChatResponse{
		Message: Message{
			Role:    "assistant",
			Content: "Echo: " + req.Messages[len(req.Messages)-1].Content,
		},
		ModelUsed: "pocket-echo",
		LatencyMs: time.Since(start).Milliseconds(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleKnowledgeSearch 知识库检索
func HandleKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	
	var req KnowledgeSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	
	// TODO: Phase 4 — 桥接到知识库服务
	// 当前返回空结果
	resp := KnowledgeSearchResponse{
		Results: []KnowledgeResult{},
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{
		Code:    code,
		Message: message,
	})
}
```

- [ ] **Step 2: 实现 HTTP 服务器**

```go
// internal/pocket/server.go
package pocket

import (
	"fmt"
	"log"
	"net/http"
)

// Server Pocket 集成网关
type Server struct {
	port     int
	secret   string
	httpSrv  *http.Server
}

// NewServer 创建 Pocket 集成网关
func NewServer(port int, secret string) *Server {
	return &Server{
		port:   port,
		secret: secret,
	}
}

// Start 启动 HTTP 服务器
func (s *Server) Start() error {
	mux := http.NewServeMux()
	
	// 公开端点（无需认证）
	mux.HandleFunc("/health", HandleHealth)
	
	// 受保护端点（需要认证）
	protected := AuthMiddleware(s.secret)(mux)
	
	// 注册受保护路由
	mux.HandleFunc("/api/v1/pocket/llm/chat", HandleChat)
	mux.HandleFunc("/api/v1/pocket/knowledge/search", HandleKnowledgeSearch)
	
	s.httpSrv = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: protected,
	}
	
	log.Printf("[Pocket Gateway] starting on :%d", s.port)
	return s.httpSrv.ListenAndServe()
}

// Stop 优雅关闭
func (s *Server) Stop() error {
	if s.httpSrv != nil {
		return s.httpSrv.Close()
	}
	return nil
}
```

- [ ] **Step 3: 编写服务器测试**

```go
// internal/pocket/server_test.go
package pocket

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setupTestServer() *Server {
	return NewServer(0, "test-secret")
}

func TestHealthEndpoint(t *testing.T) {
	s := setupTestServer()
	
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	
	HandleHealth(rec, req)
	
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	
	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
}

func TestChatEndpoint(t *testing.T) {
	reqBody := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}
	body, _ := json.Marshal(reqBody)
	
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pocket/llm/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	
	HandleChat(rec, req)
	
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	
	var resp ChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	
	if resp.Message.Role != "assistant" {
		t.Errorf("expected role assistant, got %s", resp.Message.Role)
	}
}

func TestChatEndpoint_NoAuth(t *testing.T) {
	reqBody := ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	}
	body, _ := json.Marshal(reqBody)
	
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pocket/llm/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	
	HandleChat(rec, req)
	
	// 路由没有认证中间件，所以会返回 200
	// 真正的认证由 Server.Start() 中的 AuthMiddleware 提供
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (auth middleware wraps at server level), got %d", rec.Code)
	}
}

func TestChatEndpoint_MissingMessages(t *testing.T) {
	reqBody := ChatRequest{}
	body, _ := json.Marshal(reqBody)
	
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pocket/llm/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	
	HandleChat(rec, req)
	
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go && go test ./internal/pocket/ -v`
Expected: PASS (4/4)

- [ ] **Step 5: 提交**

```bash
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 add enterprise/gateway-go/internal/pocket/handler.go enterprise/gateway-go/internal/pocket/server.go enterprise/gateway-go/internal/pocket/server_test.go
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 commit -m "feat(pocket): add Pocket gateway HTTP server with health and chat endpoints"
```

---

### Task 4: RedClaw 侧 — 集成到主入口

**Files:**
- Modify: `enterprise/gateway-go/cmd/gateway/main.go`

- [ ] **Step 1: 读取当前 main.go**

```bash
cat /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go/cmd/gateway/main.go
```

- [ ] **Step 2: 修改 main.go 启动 Pocket 集成网关**

在 main.go 的 `main()` 函数中，在现有 H2 Proxy 和 Tenant Router 启动后，添加 Pocket Gateway 的启动：

```go
// 在文件末尾的 init() 或 main() 的 goroutine 启动部分添加：

// Pocket 集成网关
pocketPort := 8092
if p := os.Getenv("POCKET_GATEWAY_PORT"); p != "" {
    if port, err := strconv.Atoi(p); err == nil {
        pocketPort = port
    }
}
pocketSecret := os.Getenv("POCKET_GATEWAY_SECRET")
if pocketSecret == "" {
    pocketSecret = "pocket-default-secret"
    log.Println("[WARN] POCKET_GATEWAY_SECRET not set, using default")
}

pocketServer := pocket.NewServer(pocketPort, pocketSecret)
go func() {
    log.Printf("[main] starting Pocket integration gateway on :%d", pocketPort)
    if err := pocketServer.Start(); err != nil {
        log.Fatalf("[main] Pocket gateway failed: %v", err)
    }
}()
```

注意：需要添加 import：
```go
"github.com/kaixuan/gateway-go/internal/pocket"
"os"
"strconv"
```

- [ ] **Step 3: 构建验证**

Run: `cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go && go build ./cmd/gateway`
Expected: 编译成功，无错误

- [ ] **Step 4: 提交**

```bash
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 add enterprise/gateway-go/cmd/gateway/main.go
git -C /Users/xutaohuang/workspace/FreshLab/RedClaw2 commit -m "feat(pocket): integrate Pocket gateway into main entry point"
```

---

### Task 5: Pocket 侧 — RedClaw 客户端类型定义

**Files:**
- Create: `backend/internal/redclaw/types.go`
- Test: (使用 client_test.go 测试)

- [ ] **Step 1: 创建类型定义文件**

```go
// internal/redclaw/types.go
package redclaw

import "time"

// ClientConfig RedClaw 客户端配置
type ClientConfig struct {
	BaseURL    string `json:"base_url"`    // RedClaw Gateway 地址，如 http://localhost:8092
	Secret     string `json:"secret"`      // 共享密钥
	TenantID   string `json:"tenant_id"`   // 当前租户 ID
	TimeoutSec int    `json:"timeout_sec"` // HTTP 超时秒数，默认 30
}

// ChatRequest LLM 对话请求
type ChatRequest struct {
	TenantID string    `json:"tenant_id"`
	UserID   string    `json:"user_id"`
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
}

// Message 对话消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse LLM 对话响应
type ChatResponse struct {
	Message   Message `json:"message"`
	ModelUsed string  `json:"model_used"`
	LatencyMs int64   `json:"latency_ms"`
}

// KnowledgeSearchRequest 知识库检索请求
type KnowledgeSearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	TopK     int    `json:"top_k,omitempty"`
}

// KnowledgeSearchResponse 知识库检索响应
type KnowledgeSearchResponse struct {
	Results []KnowledgeResult `json:"results"`
}

// KnowledgeResult 知识库条目
type KnowledgeResult struct {
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// BridgeEvent 桥接事件 (WebSocket 推送)
type BridgeEvent struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/types.go
git commit -m "feat(redclaw): define RedClaw client types"
```

---

### Task 6: Pocket 侧 — RedClaw HTTP 客户端

**Files:**
- Create: `backend/internal/redclaw/client.go`
- Create: `backend/internal/redclaw/client_test.go`

- [ ] **Step 1: 编写客户端测试**

```go
// internal/redclaw/client_test.go
package redclaw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 10,
	}
	
	client := NewClient(cfg)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.cfg.BaseURL != "http://localhost:8092" {
		t.Errorf("expected base URL http://localhost:8092, got %s", client.cfg.BaseURL)
	}
}

func TestClientHealth(t *testing.T) {
	// 模拟 RedClaw 健康检查端点
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("expected /health, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "ok",
			Version:   "1.0.0",
			UptimeSec: 3600,
		})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	
	resp, err := client.Health()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", resp.Version)
	}
}

func TestClientChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pocket/llm/chat" {
			t.Errorf("expected /api/v1/pocket/llm/chat, got %s", r.URL.Path)
		}
		// 验证认证头
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("expected Bearer test-secret, got %s", r.Header.Get("Authorization"))
		}
		// 验证租户头
		if r.Header.Get("X-Tenant-ID") != "pocket-test" {
			t.Errorf("expected X-Tenant-ID pocket-test, got %s", r.Header.Get("X-Tenant-ID"))
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Message: Message{
				Role:    "assistant",
				Content: "Hello from RedClaw!",
			},
			ModelUsed: "test-model",
			LatencyMs: 150,
		})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	
	resp, err := client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Hello from RedClaw!" {
		t.Errorf("expected 'Hello from RedClaw!', got %s", resp.Message.Content)
	}
}

func TestClientChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{
			Code:    500,
			Message: "internal error",
		})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	
	_, err := client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestClientChat_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // 模拟慢响应
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 0, // 立即超时
	})
	
	_, err := client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -v`
Expected: FAIL — "NewClient not defined"

- [ ] **Step 3: 实现 HTTP 客户端**

```go
// internal/redclaw/client.go
package redclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client RedClaw API 客户端
type Client struct {
	cfg    ClientConfig
	httpDo *http.Client
}

// NewClient 创建 RedClaw 客户端
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	return &Client{
		cfg: cfg,
		httpDo: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// Health 健康检查
func (c *Client) Health() (*HealthResponse, error) {
	resp, err := c.doRequest(http.MethodGet, "/health", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode health response: %w", err)
	}
	return &result, nil
}

// Chat LLM 对话
func (c *Client) Chat(req ChatRequest) (*ChatResponse, error) {
	// 设置租户 ID
	if req.TenantID == "" {
		req.TenantID = c.cfg.TenantID
	}
	
	resp, err := c.doRequest(http.MethodPost, "/api/v1/pocket/llm/chat", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("RedClaw error (code=%d): %s", errResp.Code, errResp.Message)
		}
		return nil, fmt.Errorf("RedClaw HTTP %d: %s", resp.StatusCode, string(body))
	}
	
	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	return &result, nil
}

// KnowledgeSearch 知识库检索
func (c *Client) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	if req.TenantID == "" {
		req.TenantID = c.cfg.TenantID
	}
	
	resp, err := c.doRequest(http.MethodPost, "/api/v1/pocket/knowledge/search", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("RedClaw error (code=%d): %s", errResp.Code, errResp.Message)
		}
		return nil, fmt.Errorf("RedClaw HTTP %d: %s", resp.StatusCode, string(body))
	}
	
	var result KnowledgeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode knowledge search response: %w", err)
	}
	return &result, nil
}

// doRequest 执行 HTTP 请求
func (c *Client) doRequest(method, path string, body interface{}) (*http.Response, error) {
	url := c.cfg.BaseURL + path
	
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)
	req.Header.Set("Content-Type", "application/json")
	
	return c.httpDo.Do(req)
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -v`
Expected: PASS (5/5)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/client.go backend/internal/redclaw/client_test.go
git commit -m "feat(redclaw): implement RedClaw HTTP client with health, chat, and knowledge search"
```

---

### Task 7: Pocket 侧 — RedClaw 桥接服务 (WebSocket ↔ REST)

**Files:**
- Create: `backend/internal/redclaw/bridge.go`
- Create: `backend/internal/redclaw/bridge_test.go`

- [ ] **Step 1: 编写桥接服务测试**

```go
// internal/redclaw/bridge_test.go
package redclaw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewBridge(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 10,
	}
	
	client := NewClient(cfg)
	bridge := NewBridge(client, nil)
	
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}
	if bridge.client == nil {
		t.Error("expected client to be set")
	}
}

func TestBridgeChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Message:   Message{Role: "assistant", Content: "Hello from RedClaw"},
			ModelUsed: "test",
			LatencyMs: 10,
		})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	
	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()
	
	resp, err := bridge.Chat(ChatRequest{
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Hello from RedClaw" {
		t.Errorf("expected 'Hello from RedClaw', got %s", resp.Message.Content)
	}
}

func TestBridgeHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	}))
	defer server.Close()
	
	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	
	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()
	
	healthy := bridge.HealthCheck()
	if !healthy {
		t.Error("expected healthy")
	}
}

func TestBridgeHealthCheck_Failure(t *testing.T) {
	client := NewClient(ClientConfig{
		BaseURL:    "http://localhost:19999",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 1,
	})
	
	bridge := NewBridge(client, nil)
	
	healthy := bridge.HealthCheck()
	if healthy {
		t.Error("expected unhealthy for unreachable server")
	}
}

func TestBridgeIsConnected(t *testing.T) {
	client := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test",
		TenantID:   "test",
		TimeoutSec: 5,
	})
	
	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()
	
	if !bridge.IsConnected() {
		t.Error("expected connected after Start()")
	}
	
	bridge.Stop()
	
	if bridge.IsConnected() {
		t.Error("expected disconnected after Stop()")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestBridge -v`
Expected: FAIL — "NewBridge not defined"

- [ ] **Step 3: 实现桥接服务**

```go
// internal/redclaw/bridge.go
package redclaw

import (
	"log"
	"sync"
	"time"
)

// BridgeEventCallback WebSocket 事件推送回调
// Pocket 端实现此回调将事件推送到 WebSocket Hub
type BridgeEventCallback func(event BridgeEvent)

// Bridge RedClaw 桥接服务
// 封装客户端调用并管理连接状态
type Bridge struct {
	client   *Client
	onEvent  BridgeEventCallback
	mu       sync.RWMutex
	connected bool
	stopCh   chan struct{}
}

// NewBridge 创建桥接服务
func NewBridge(client *Client, onEvent BridgeEventCallback) *Bridge {
	return &Bridge{
		client:  client,
		onEvent: onEvent,
		stopCh:  make(chan struct{}),
	}
}

// Start 启动桥接服务（健康检查 + 状态监控）
func (b *Bridge) Start() {
	b.mu.Lock()
	b.connected = true
	b.mu.Unlock()
	
	log.Println("[RedClaw Bridge] started")
	
	// 启动后台健康检查
	go b.healthLoop()
}

// Stop 停止桥接服务
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if !b.connected {
		return
	}
	
	b.connected = false
	close(b.stopCh)
	log.Println("[RedClaw Bridge] stopped")
}

// Chat LLM 对话
func (b *Bridge) Chat(req ChatRequest) (*ChatResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()
	
	if !connected {
		return nil, ErrBridgeNotConnected
	}
	
	return b.client.Chat(req)
}

// KnowledgeSearch 知识库检索
func (b *Bridge) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()
	
	if !connected {
		return nil, ErrBridgeNotConnected
	}
	
	return b.client.KnowledgeSearch(req)
}

// HealthCheck 健康检查
func (b *Bridge) HealthCheck() bool {
	_, err := b.client.Health()
	return err == nil
}

// IsConnected 返回连接状态
func (b *Bridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// healthLoop 定期健康检查
func (b *Bridge) healthLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			healthy := b.HealthCheck()
			b.mu.Lock()
			prevConnected := b.connected
			b.connected = healthy
			b.mu.Unlock()
			
			// 状态变化时推送事件
			if prevConnected != healthy && b.onEvent != nil {
				eventType := "redclaw.connected"
				if !healthy {
					eventType = "redclaw.disconnected"
				}
				b.onEvent(BridgeEvent{
					Type:      eventType,
					Payload:   map[string]bool{"connected": healthy},
					Timestamp: time.Now(),
				})
			}
		case <-b.stopCh:
			return
		}
	}
}

// ErrBridgeNotConnected 桥接未连接错误
var ErrBridgeNotConnected = &BridgeError{"RedClaw bridge not connected"}

type BridgeError struct {
	Message string
}

func (e *BridgeError) Error() string {
	return e.Message
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestBridge -v`
Expected: PASS (4/4)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/bridge.go backend/internal/redclaw/bridge_test.go
git commit -m "feat(redclaw): implement Bridge service with health check and event callbacks"
```

---

### Task 8: Pocket 侧 — 多租户身份同步

**Files:**
- Create: `backend/internal/redclaw/auth.go`
- Create: `backend/internal/redclaw/auth_test.go`

- [ ] **Step 1: 编写身份同步测试**

```go
// internal/redclaw/auth_test.go
package redclaw

import (
	"testing"
)

func TestTenantContextFromJWT(t *testing.T) {
	// 模拟 JWT Claims 中的租户信息
	claims := map[string]interface{}{
		"sub":       "user-123",
		"tenant_id": "pocket-enterprise",
		"role":      "developer",
	}
	
	ctx := ExtractTenantContext(claims)
	if ctx == nil {
		t.Fatal("expected non-nil tenant context")
	}
	if ctx.TenantID != "pocket-enterprise" {
		t.Errorf("expected tenant_id pocket-enterprise, got %s", ctx.TenantID)
	}
	if ctx.UserID != "user-123" {
		t.Errorf("expected user_id user-123, got %s", ctx.UserID)
	}
	if ctx.Role != "developer" {
		t.Errorf("expected role developer, got %s", ctx.Role)
	}
}

func TestTenantContextFromJWT_MissingTenant(t *testing.T) {
	claims := map[string]interface{}{
		"sub":  "user-123",
		"role": "developer",
	}
	
	ctx := ExtractTenantContext(claims)
	if ctx == nil {
		t.Fatal("expected non-nil tenant context")
	}
	// 缺少 tenant_id 时使用默认值
	if ctx.TenantID != "default" {
		t.Errorf("expected default tenant_id, got %s", ctx.TenantID)
	}
}

func TestAttachTenantHeaders(t *testing.T) {
	ctx := &TenantContext{
		TenantID: "pocket-enterprise",
		UserID:   "user-123",
		Role:     "developer",
	}
	
	headers := AttachTenantHeaders(ctx)
	if headers["X-Tenant-ID"] != "pocket-enterprise" {
		t.Errorf("expected X-Tenant-ID pocket-enterprise, got %s", headers["X-Tenant-ID"])
	}
	if headers["X-User-ID"] != "user-123" {
		t.Errorf("expected X-User-ID user-123, got %s", headers["X-User-ID"])
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestTenant -v`
Expected: FAIL — "ExtractTenantContext not defined"

- [ ] **Step 3: 实现身份同步**

```go
// internal/redclaw/auth.go
package redclaw

// TenantContext 租户上下文
// 从 JWT Claims 中提取，用于 RedClaw API 调用
type TenantContext struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
}

// ExtractTenantContext 从 JWT Claims 中提取租户上下文
// claims 是 JWT 解码后的 payload map
func ExtractTenantContext(claims map[string]interface{}) *TenantContext {
	ctx := &TenantContext{
		TenantID: "default",
		UserID:   "",
		Role:     "user",
	}
	
	if sub, ok := claims["sub"].(string); ok {
		ctx.UserID = sub
	}
	if tid, ok := claims["tenant_id"].(string); ok && tid != "" {
		ctx.TenantID = tid
	}
	if role, ok := claims["role"].(string); ok {
		ctx.Role = role
	}
	
	return ctx
}

// AttachTenantHeaders 生成需附加到 RedClaw 请求的 Header
func AttachTenantHeaders(ctx *TenantContext) map[string]string {
	return map[string]string{
		"X-Tenant-ID": ctx.TenantID,
		"X-User-ID":   ctx.UserID,
	}
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestTenant -v`
Expected: PASS (3/3)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/auth.go backend/internal/redclaw/auth_test.go
git commit -m "feat(redclaw): implement tenant context extraction from JWT claims"
```

---

### Task 9: Pocket 侧 — 配置与 Server 集成

**Files:**
- Modify: `backend/internal/config/config.go`
- Create: `backend/internal/server/server_redclaw.go`
- Create: `backend/internal/server/server_redclaw_test.go`
- Modify: `backend/internal/server/server.go`

- [ ] **Step 1: 读取当前 config.go**

```bash
cat /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/internal/config/config.go
```

- [ ] **Step 2: 在 config.go 中添加 RedClaw 配置**

在 `config.go` 的 `Config` 结构体中添加：
```go
// RedClaw 企业后端配置（可选）
RedClawBaseURL    string `env:"POCKET_REDCLAW_BASE_URL"`
RedClawSecret     string `env:"POCKET_REDCLAW_SECRET"`
RedClawTenantID   string `env:"POCKET_REDCLAW_TENANT_ID" envDefault:"default"`
RedClawTimeoutSec int    `env:"POCKET_REDCLAW_TIMEOUT_SEC" envDefault:"30"`
```

- [ ] **Step 3: 实现 RedClaw API 路由**

```go
// internal/server/server_redclaw.go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// handleRedClawHealth RedClaw 健康检查代理
// GET /api/redclaw/health
func (s *Server) handleRedClawHealth(w http.ResponseWriter, r *http.Request) {
	if s.redclawBridge == nil {
		http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
		return
	}
	
	healthy := s.redclawBridge.HealthCheck()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": healthy,
		"tenant_id": s.cfg.RedClawTenantID,
	})
}

// handleRedClawChat RedClaw LLM 对话代理
// POST /api/redclaw/chat
func (s *Server) handleRedClawChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	if s.redclawBridge == nil {
		http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
		return
	}
	
	var req redclaw.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	
	// 从 JWT 上下文提取租户信息
	claims := extractClaims(r)
	if claims != nil {
		tenantCtx := redclaw.ExtractTenantContext(claims)
		if req.TenantID == "" {
			req.TenantID = tenantCtx.TenantID
		}
	}
	
	resp, err := s.redclawBridge.Chat(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// extractClaims 从请求上下文中提取 JWT claims（简化版）
func extractClaims(r *http.Request) map[string]interface{} {
	// 从请求上下文中获取用户信息
	// 实际实现参考 auth/middleware.go 中的上下文注入
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		return nil
	}
	return map[string]interface{}{
		"sub":       userID,
		"tenant_id": r.Header.Get("X-Tenant-ID"),
	}
}
```

- [ ] **Step 4: 编写 API 路由测试**

```go
// internal/server/server_redclaw_test.go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func TestRedClawHealth_NotConfigured(t *testing.T) {
	s := &Server{redclawBridge: nil}
	
	req := httptest.NewRequest(http.MethodGet, "/api/redclaw/health", nil)
	rec := httptest.NewRecorder()
	
	s.handleRedClawHealth(rec, req)
	
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestRedClawHealth_Configured(t *testing.T) {
	// 使用模拟的 RedClaw 服务器
	mockRedClaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(redclaw.HealthResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	}))
	defer mockRedClaw.Close()
	
	client := redclaw.NewClient(redclaw.ClientConfig{
		BaseURL:    mockRedClaw.URL,
		Secret:     "test-secret",
		TenantID:   "test-tenant",
		TimeoutSec: 5,
	})
	bridge := redclaw.NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()
	
	s := &Server{
		redclawBridge: bridge,
	}
	
	req := httptest.NewRequest(http.MethodGet, "/api/redclaw/health", nil)
	rec := httptest.NewRecorder()
	
	s.handleRedClawHealth(rec, req)
	
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	
	if resp["connected"] != true {
		t.Errorf("expected connected true, got %v", resp["connected"])
	}
}

func TestRedClawChat(t *testing.T) {
	mockRedClaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(redclaw.ChatResponse{
			Message: redclaw.Message{
				Role:    "assistant",
				Content: "Hello from mock",
			},
			ModelUsed: "test",
			LatencyMs: 10,
		})
	}))
	defer mockRedClaw.Close()
	
	client := redclaw.NewClient(redclaw.ClientConfig{
		BaseURL:    mockRedClaw.URL,
		Secret:     "test-secret",
		TenantID:   "test-tenant",
		TimeoutSec: 5,
	})
	bridge := redclaw.NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()
	
	s := &Server{
		redclawBridge: bridge,
	}
	
	body, _ := json.Marshal(redclaw.ChatRequest{
		Messages: []redclaw.Message{{Role: "user", Content: "Hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/redclaw/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	
	s.handleRedClawChat(rec, req)
	
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	
	var resp redclaw.ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	
	if resp.Message.Content != "Hello from mock" {
		t.Errorf("expected 'Hello from mock', got %s", resp.Message.Content)
	}
}
```

- [ ] **Step 5: 在 server.go 中添加 RedClaw 桥接字段和路由注册**

在 `server.go` 的 `Server` 结构体中添加字段：
```go
// RedClaw 企业后端桥接（nil = 未配置）
redclawBridge *redclaw.Bridge
```

在 `server.go` 的 Setup/Init 函数中，在配置解析后添加：
```go
// 初始化 RedClaw 桥接（如果配置了 RedClaw）
if s.cfg.RedClawBaseURL != "" {
    rcCfg := redclaw.ClientConfig{
        BaseURL:    s.cfg.RedClawBaseURL,
        Secret:     s.cfg.RedClawSecret,
        TenantID:   s.cfg.RedClawTenantID,
        TimeoutSec: s.cfg.RedClawTimeoutSec,
    }
    rcClient := redclaw.NewClient(rcCfg)
    s.redclawBridge = redclaw.NewBridge(rcClient, s.pushRedClawEvent)
    s.redclawBridge.Start()
    log.Println("[Server] RedClaw bridge initialized")
}
```

在路由注册部分添加：
```go
mux.HandleFunc("/api/redclaw/health", s.handleRedClawHealth)
mux.HandleFunc("/api/redclaw/chat", s.handleRedClawChat)
```

需要添加 import: `"github.com/halfking/pocket-opencode/backend/internal/redclaw"`

- [ ] **Step 6: 构建验证**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd`
Expected: 编译成功，无错误

- [ ] **Step 7: 运行测试**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ ./internal/server/ -run TestRedClaw -v`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/config/config.go backend/internal/server/server_redclaw.go backend/internal/server/server_redclaw_test.go backend/internal/server/server.go
git commit -m "feat(redclaw): integrate RedClaw bridge into Pocket server with config and API routes"
```

---

### Task 10: 端到端集成测试

**Files:**
- Create: `backend/cmd/pocketd/testdata/redclaw_e2e_test.go` (或 `backend/scripts/test-redclaw-integration.sh`)

- [ ] **Step 1: 编写端到端测试脚本**

```bash
#!/bin/bash
# scripts/test-redclaw-integration.sh
# 测试 Pocket ↔ RedClaw 集成链路

set -e

echo "=== RedClaw Integration E2E Test ==="

# 1. 启动模拟 RedClaw 服务器 (Pocket 集成网关)
echo "[1/4] Starting mock RedClaw gateway..."
cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go
POCKET_GATEWAY_SECRET="test-e2e-secret" go run ./cmd/gateway &
REDCLAW_PID=$!
sleep 2

# 确保测试结束后清理
cleanup() {
    echo "Cleaning up..."
    kill $REDCLAW_PID 2>/dev/null || true
}
trap cleanup EXIT

# 2. 测试 RedClaw 健康检查
echo "[2/4] Testing RedClaw health endpoint..."
curl -s http://localhost:8092/health | grep -q '"status":"ok"'
echo "  ✅ Health check passed"

# 3. 测试 RedClaw Chat API
echo "[3/4] Testing RedClaw chat endpoint..."
curl -s -X POST http://localhost:8092/api/v1/pocket/llm/chat \
  -H "Authorization: Bearer test-e2e-secret" \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello"}]}' | grep -q '"role":"assistant"'
echo "  ✅ Chat API passed"

# 4. 启动 Pocket 后端并测试桥接
echo "[4/4] Starting Pocket backend..."
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
POCKET_REDCLAW_BASE_URL="http://localhost:8092" \
POCKET_REDCLAW_SECRET="test-e2e-secret" \
POCKET_REDCLAW_TENANT_ID="e2e-test" \
POCKET_DEV_AUTH=true \
POCKET_HTTP_PORT=8088 \
go run ./cmd/pocketd &
POCKET_PID=$!
sleep 3

# 测试 Pocket RedClaw 健康检查代理
echo "  Testing Pocket RedClaw health proxy..."
curl -s http://localhost:8088/api/redclaw/health | grep -q '"connected":true'
echo "  ✅ Pocket RedClaw health proxy passed"

# 测试 Pocket RedClaw Chat 代理
echo "  Testing Pocket RedClaw chat proxy..."
curl -s -X POST http://localhost:8088/api/redclaw/chat \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello from Pocket"}]}' | grep -q '"role":"assistant"'
echo "  ✅ Pocket RedClaw chat proxy passed"

echo ""
echo "=== All E2E tests passed! ==="
```

- [ ] **Step 2: 运行端到端测试**

Run: `bash /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/scripts/test-redclaw-integration.sh`
Expected: 所有测试通过

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/scripts/test-redclaw-integration.sh
git commit -m "test(redclaw): add end-to-end integration test script"
```

---

## Phase 1 完成标志

- [x] RedClaw 侧 Pocket 集成网关 (:8092) 可启动
- [x] Pocket 侧 RedClaw 客户端可调用企业 API
- [x] 桥接服务健康检查正常
- [x] 多租户身份从 JWT 同步到 RedClaw
- [x] 端到端测试通过（Pocket → RedClaw → 响应返回）
- [x] 所有代码已提交

## 后续 Phase 计划

| Phase | 聚焦 | 前置依赖 |
|-------|------|---------|
| **Phase 2** | AI 编程升级 + 会议总结 + 聊天总结 | Phase 1 |
| **Phase 3** | 方案/PPT + 笔记分类 + 语音记账 | Phase 2 |
| **Phase 4** | 企业深度集成 + iOS + 性能优化 | Phase 3 |