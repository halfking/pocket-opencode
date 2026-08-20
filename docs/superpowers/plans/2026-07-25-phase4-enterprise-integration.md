# Phase 4: 企业深度集成与优化 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成企业级功能（知识库检索、多租户治理、审计日志）、iOS 应用支持、性能优化、文档完善

**Architecture:** 知识库检索通过 Phase 1 已建立的 RedClaw Bridge 调用。多租户治理在 JWT 中嵌入 tenant_id 并在所有 API 中传递。审计日志通过中间件拦截请求并异步上报。iOS 通过 Capacitor 编译现有 Vue 3 应用。

**Tech Stack:** Go 1.22+ / Capacitor 8 / Vue 3 / iOS / RedClaw Gateway

---

## 文件结构

```
backend/internal/
├── redclaw/                      # 已有，升级
│   ├── client.go                # 已有，新增 KnowledgeSearch 完整实现
│   ├── bridge.go                # 已有，新增方法
│   └── audit.go                 # 审计日志 (新增)
│       └── audit_test.go

backend/internal/server/
├── server_redclaw.go            # 已有，新增知识库检索端点
├── server_audit.go              # 审计日志 API (新增)
├── server_audit_test.go
├── server.go                    # 修改，添加审计中间件

backend/internal/config/
├── config.go                    # 已有，新增 iOS 构建配置

frontend/
├── capacitor.config.ts          # 修改，添加 iOS 配置
├── ios/                         # iOS 平台 (npx cap add ios)
│   └── (Capacitor 生成)

docs/
├── ARCHITECTURE.md              # 架构文档 (新增)
├── API.md                       # API 文档 (新增)
├── DEPLOYMENT.md                # 部署文档 (新增)
├── MOBILE_TEST.md               # 移动端测试指南 (新增)

scripts/
├── build-ios.sh                 # iOS 构建脚本 (新增)
├── test-all.sh                  # 全量测试脚本 (新增)
```

---

### Task 1: 企业知识库检索 (RedClaw 深度集成)

**Files:**
- Modify: `backend/internal/redclaw/client.go` (新增 KnowledgeSearch)
- Modify: `backend/internal/redclaw/bridge.go` (新增桥接方法)
- Modify: `backend/internal/server/server_redclaw.go` (新增知识库端点)

- [ ] **Step 1: 在 client.go 中确认 KnowledgeSearch 实现**

检查 `client.go` 中的 `KnowledgeSearch` 方法是否已完整实现。如果已实现（Phase 1），则跳过此步骤。

```bash
grep -n "KnowledgeSearch" /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/internal/redclaw/client.go
```

- [ ] **Step 2: 在 bridge.go 中添加 KnowledgeSearch 桥接方法**

```go
// 在 bridge.go 中添加（如果不存在）：
func (b *Bridge) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
    b.mu.RLock()
    connected := b.connected
    b.mu.RUnlock()
    if !connected {
        return nil, ErrBridgeNotConnected
    }
    return b.client.KnowledgeSearch(req)
}
```

- [ ] **Step 3: 在 server_redclaw.go 中添加知识库检索端点**

```go
func (s *Server) handleRedClawKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    if s.redclawBridge == nil {
        http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
        return
    }
    
    var req redclaw.KnowledgeSearchRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
        return
    }
    if req.Query == "" {
        http.Error(w, `{"error":"query is required"}`, http.StatusBadRequest)
        return
    }
    
    resp, err := s.redclawBridge.KnowledgeSearch(req)
    if err != nil {
        http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: 在 server.go 中注册路由**

```go
mux.HandleFunc("/api/redclaw/knowledge/search", s.requireAuth(s.handleRedClawKnowledgeSearch))
```

- [ ] **Step 5: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/ backend/internal/server/server_redclaw.go backend/internal/server/server.go
git commit -m "feat(redclaw): add knowledge search API endpoint"
```

---

### Task 2: 多租户治理 — 身份同步与权限校验

**Files:**
- Create: `backend/internal/redclaw/tenant.go`
- Create: `backend/internal/redclaw/tenant_test.go`
- Modify: `backend/internal/server/server.go` (中间件)

- [ ] **Step 1: 编写租户中间件测试**

```go
// internal/redclaw/tenant_test.go
package redclaw

import (
	"testing"
)

func TestTenantMiddleware_ValidTenant(t *testing.T) {
	claims := map[string]interface{}{
		"sub":       "user-123",
		"tenant_id": "enterprise-a",
		"role":      "admin",
	}
	
	ctx := ExtractTenantContext(claims)
	if ctx.TenantID != "enterprise-a" {
		t.Errorf("expected enterprise-a, got %s", ctx.TenantID)
	}
	if ctx.Role != "admin" {
		t.Errorf("expected admin, got %s", ctx.Role)
	}
}

func TestTenantMiddleware_DefaultTenant(t *testing.T) {
	claims := map[string]interface{}{
		"sub": "user-123",
	}
	
	ctx := ExtractTenantContext(claims)
	if ctx.TenantID != "default" {
		t.Errorf("expected default, got %s", ctx.TenantID)
	}
}

func TestTenantMiddleware_Headers(t *testing.T) {
	ctx := &TenantContext{
		TenantID: "enterprise-a",
		UserID:   "user-123",
		Role:     "developer",
	}
	
	headers := AttachTenantHeaders(ctx)
	if headers["X-Tenant-ID"] != "enterprise-a" {
		t.Errorf("expected enterprise-a, got %s", headers["X-Tenant-ID"])
	}
	if headers["X-User-ID"] != "user-123" {
		t.Errorf("expected user-123, got %s", headers["X-User-ID"])
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestTenant -v
```
Expected: PASS (3/3)

- [ ] **Step 3: 在 server.go 中添加租户注入中间件**

在 `requireAuth` 或现有的认证中间件中，添加从 JWT 提取租户信息并注入请求上下文的逻辑：

```go
// 在 requireAuth 中间件中，解码 JWT 后添加：
if claims != nil {
    tenantCtx := redclaw.ExtractTenantContext(claims)
    r.Header.Set("X-Tenant-ID", tenantCtx.TenantID)
    r.Header.Set("X-User-ID", tenantCtx.UserID)
    r.Header.Set("X-User-Role", tenantCtx.Role)
}
```

- [ ] **Step 4: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/tenant.go backend/internal/redclaw/tenant_test.go backend/internal/server/server.go
git commit -m "feat(tenant): add multi-tenant identity sync and permission headers"
```

---

### Task 3: 审计日志模块

**Files:**
- Create: `backend/internal/redclaw/audit.go`
- Create: `backend/internal/redclaw/audit_test.go`
- Create: `backend/internal/server/server_audit.go`
- Create: `backend/internal/server/server_audit_test.go`
- Modify: `backend/internal/server/server.go`

- [ ] **Step 1: 编写审计日志测试**

```go
// internal/redclaw/audit_test.go
package redclaw

import (
	"testing"
	"time"
)

func TestAuditLog_Create(t *testing.T) {
	store := NewAuditStore()
	
	entry := &AuditEntry{
		Action:     "chat.send",
		UserID:     "user-123",
		TenantID:   "enterprise-a",
		Resource:   "session/sess_abc",
		Detail:     "Sent message to AI",
		DurationMs: 1500,
	}
	
	err := store.Record(entry)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestAuditLog_Query(t *testing.T) {
	store := NewAuditStore()
	
	store.Record(&AuditEntry{Action: "chat.send", UserID: "user-1", TenantID: "t1"})
	store.Record(&AuditEntry{Action: "chat.send", UserID: "user-2", TenantID: "t1"})
	store.Record(&AuditEntry{Action: "file.read", UserID: "user-1", TenantID: "t2"})
	
	// 按租户查询
	entries, err := store.Query(AuditQuery{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for t1, got %d", len(entries))
	}
	
	// 按用户查询
	entries, _ = store.Query(AuditQuery{UserID: "user-1"})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for user-1, got %d", len(entries))
	}
}

func TestAuditLog_Flush(t *testing.T) {
	store := NewAuditStore()
	store.Record(&AuditEntry{Action: "test", UserID: "u1", TenantID: "t1"})
	
	entries := store.Flush()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
	
	// Flush 后应为空
	remaining, _ := store.Query(AuditQuery{})
	if len(remaining) != 0 {
		t.Errorf("expected 0 after flush, got %d", len(remaining))
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestAuditLog -v`
Expected: FAIL — "NewAuditStore not defined"

- [ ] **Step 3: 实现审计日志**

```go
// internal/redclaw/audit.go
package redclaw

import (
	"fmt"
	"sync"
	"time"
)

// AuditEntry 审计日志条目
type AuditEntry struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`      // chat.send / file.read / session.create
	UserID     string    `json:"user_id"`
	TenantID   string    `json:"tenant_id"`
	Resource   string    `json:"resource,omitempty"`   // 操作的资源
	Detail     string    `json:"detail,omitempty"`     // 详细信息
	DurationMs int64     `json:"duration_ms,omitempty"` // 耗时（毫秒）
	Success    bool      `json:"success"`              // 是否成功
	Timestamp  time.Time `json:"timestamp"`
	IP         string    `json:"ip,omitempty"`
}

// AuditQuery 审计查询
type AuditQuery struct {
	TenantID string `json:"tenant_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	Action   string `json:"action,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// AuditStore 审计日志存储
type AuditStore struct {
	mu      sync.RWMutex
	entries []*AuditEntry
	maxSize int
}

func NewAuditStore() *AuditStore {
	return &AuditStore{
		entries: make([]*AuditEntry, 0, 1000),
		maxSize: 10000,
	}
}

func (s *AuditStore) Record(entry *AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.ID = fmt.Sprintf("aud_%d_%d", time.Now().UnixNano(), len(s.entries))
	entry.Timestamp = time.Now()
	s.entries = append(s.entries, entry)

	// 限制大小
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize/2:]
	}

	return nil
}

func (s *AuditStore) Query(query AuditQuery) ([]*AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	var result []*AuditEntry
	for _, e := range s.entries {
		if query.TenantID != "" && e.TenantID != query.TenantID {
			continue
		}
		if query.UserID != "" && e.UserID != query.UserID {
			continue
		}
		if query.Action != "" && e.Action != query.Action {
			continue
		}
		result = append(result, e)
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Flush 获取并清空所有条目
func (s *AuditStore) Flush() []*AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.entries
	s.entries = make([]*AuditEntry, 0, 1000)
	return entries
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/redclaw/ -run TestAuditLog -v`
Expected: PASS (3/3)

- [ ] **Step 5: 创建审计 API 路由**

```go
// internal/server/server_audit.go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.auditStore == nil {
		http.Error(w, "audit not configured", http.StatusServiceUnavailable)
		return
	}

	query := redclaw.AuditQuery{
		TenantID: r.URL.Query().Get("tenant_id"),
		UserID:   r.URL.Query().Get("user_id"),
		Action:   r.URL.Query().Get("action"),
	}

	entries, err := s.auditStore.Query(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": entries,
		"total":   len(entries),
	})
}
```

- [ ] **Step 6: 修改 server.go**

添加 `auditStore *redclaw.AuditStore`，初始化 `s.auditStore = redclaw.NewAuditStore()`，注册路由：
```go
// 审计中间件：记录所有 API 请求
mux.Handle("/api/", s.auditMiddleware(s.requireAuth(mux)))
// 独立的审计查询端点
mux.HandleFunc("/api/audit/logs", s.requireAuth(s.handleAuditLogs))
```

添加审计中间件：
```go
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        if s.auditStore != nil {
            s.auditStore.Record(&redclaw.AuditEntry{
                Action:     r.Method + " " + r.URL.Path,
                UserID:     r.Header.Get("X-User-ID"),
                TenantID:   r.Header.Get("X-Tenant-ID"),
                DurationMs: time.Since(start).Milliseconds(),
                Success:    true,
            })
        }
    })
}
```

- [ ] **Step 7: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```

- [ ] **Step 8: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/redclaw/audit.go backend/internal/redclaw/audit_test.go backend/internal/server/server_audit.go backend/internal/server/server.go
git commit -m "feat(audit): add audit logging with query and flush"
```

---

### Task 4: iOS 应用支持

**Files:**
- Modify: `frontend/capacitor.config.ts`
- Create: `frontend/ios/` (Capacitor 生成)
- Create: `scripts/build-ios.sh`

- [ ] **Step 1: 修改 Capacitor 配置添加 iOS 支持**

```typescript
// frontend/capacitor.config.ts
import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  server: {
    url: process.env.VITE_API_BASE || 'http://localhost:8088',
    cleartext: true,
  },
  android: {
    allowMixedContent: true,
    backgroundColor: '#ffffff',
  },
  ios: {
    contentInset: 'always',
    backgroundColor: '#ffffff',
    preferredContentMode: 'mobile',
  },
  plugins: {
    SplashScreen: {
      launchShowDuration: 2000,
      backgroundColor: '#ffffff',
    },
  },
};

export default config;
```

- [ ] **Step 2: 添加 iOS 平台**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx cap add ios
```

- [ ] **Step 3: 创建 iOS 构建脚本**

```bash
#!/bin/bash
# scripts/build-ios.sh
# iOS 构建脚本

set -e

echo "=== Building OpenCode Pocket for iOS ==="

# 1. 构建前端
echo "[1/3] Building frontend..."
cd "$(dirname "$0")/../frontend"
npm run build

# 2. 同步到 iOS
echo "[2/3] Syncing to iOS..."
npx cap sync ios

# 3. 打开 Xcode（可选）
if [ "${1}" = "--open" ]; then
    echo "[3/3] Opening Xcode..."
    npx cap open ios
fi

echo "=== Done! ==="
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add frontend/capacitor.config.ts scripts/build-ios.sh
git commit -m "feat(ios): add iOS platform support with Capacitor"
```

---

### Task 5: 性能优化

**Files:**
- Modify: `backend/internal/server/server.go`
- Create: `backend/internal/server/middleware.go`
- Create: `backend/internal/server/middleware_test.go`

- [ ] **Step 1: 创建性能中间件**

```go
// internal/server/middleware.go
package server

import (
	"log"
	"net/http"
	"time"
)

// responseWriter 包装 http.ResponseWriter 以捕获状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware 请求日志中间件
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)
		
		// 慢请求日志（超过 500ms）
		if duration > 500*time.Millisecond {
			log.Printf("[SLOW] %s %s - %d (%v)", r.Method, r.URL.Path, rw.statusCode, duration)
		}
	})
}

// recoveryMiddleware 崩溃恢复中间件
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 2: 编写中间件测试**

```go
// internal/server/middleware_test.go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := recoveryMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestResponseWriter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rw.statusCode)
	}
}
```

- [ ] **Step 3: 在 server.go 中应用中间件**

```go
// 在 Handler() 方法中，将现有的 mux 包装在中间件中：
handler := recoveryMiddleware(s.loggingMiddleware(mux))
return handler
```

- [ ] **Step 4: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd && go test ./internal/server/ -run TestRecovery -v
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/server/middleware.go backend/internal/server/middleware_test.go backend/internal/server/server.go
git commit -m "perf: add request logging, panic recovery, and performance middleware"
```

---

### Task 6: 全量测试脚本

**Files:**
- Create: `scripts/test-all.sh`

- [ ] **Step 1: 创建全量测试脚本**

```bash
#!/bin/bash
# scripts/test-all.sh
# OpenCode Pocket 全量测试脚本

set -e

echo "================================================"
echo "  OpenCode Pocket - Full Test Suite"
echo "================================================"
echo ""

cd "$(dirname "$0")/../backend"

# 变量
PASS=0
FAIL=0
FAILED_PACKAGES=""

run_tests() {
    local pkg=$1
    local name=$2
    echo "📦 Testing $name..."
    
    if go test "./internal/$pkg/" -count=1 -v 2>&1 | tail -5; then
        echo "  ✅ $name passed"
        PASS=$((PASS + 1))
    else
        echo "  ❌ $name failed"
        FAIL=$((FAIL + 1))
        FAILED_PACKAGES="$FAILED_PACKAGES $name"
    fi
    echo ""
}

# 运行所有模块测试
run_tests "snippet"       "代码片段"
run_tests "meeting"       "会议总结"
run_tests "chat_summary"  "聊天总结"
run_tests "redclaw"       "RedClaw 集成"
run_tests "presentation"  "产品方案/PPT"
run_tests "notes"         "笔记分类"
run_tests "finance"       "记账"
run_tests "server"        "Server API"

# 编译验证
echo "🔨 Building..."
if go build ./cmd/pocketd; then
    echo "  ✅ Build successful"
    PASS=$((PASS + 1))
else
    echo "  ❌ Build failed"
    FAIL=$((FAIL + 1))
    FAILED_PACKAGES="$FAILED_PACKAGES build"
fi

echo ""
echo "================================================"
echo "  Results: $PASS passed, $FAIL failed"
if [ $FAIL -gt 0 ]; then
    echo "  Failed: $FAILED_PACKAGES"
    exit 1
fi
echo "================================================"
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && chmod +x scripts/test-all.sh && git add scripts/test-all.sh
git commit -m "test: add full test suite script"
```

---

### Task 7: 文档完善

**Files:**
- Create: `docs/ARCHITECTURE.md`
- Create: `docs/API.md`
- Create: `docs/DEPLOYMENT.md`

- [ ] **Step 1: 创建架构文档**

```markdown
# OpenCode Pocket — 架构文档

## 整体架构

┌─────────────────────────────────────────────────┐
│  Mobile App (Vue 3 + Capacitor)                │
│  - Android / iOS / Web                         │
│  - WebSocket 实时通讯                           │
│  - 本地 SQLite 缓存                             │
└─────────────────────┬───────────────────────────┘
                      │ WebSocket + REST API
                      ▼
┌─────────────────────────────────────────────────┐
│  Pocket Backend (Go)                            │
│  - REST API 路由                                │
│  - WebSocket Hub (实时推送)                     │
│  - JWT 认证 + 多租户                            │
│  - ACP Adapter (Codex/CLI)                     │
│  - STT 语音识别 (Groq Whisper)                 │
│  - 审计日志                                    │
└─────────────────────┬───────────────────────────┘
                      │
        ┌─────────────┴─────────────┐
        │                           │
        ▼                           ▼
┌─────────────────┐     ┌───────────────────────┐
│  RedClaw 企业后端 │     │  PostgreSQL / SQLite  │
│  - LLM 路由      │     │  - 笔记存储           │
│  - 知识库检索    │     │  - 配置存储           │
│  - 多租户治理    │     └───────────────────────┘
│  - 审计日志      │
└─────────────────┘

## 模块清单

| 模块 | 包路径 | 说明 |
|------|--------|------|
| 代码片段 | internal/snippet | 代码片段 CRUD |
| 会议总结 | internal/meeting | 录音 + STT + AI 总结 |
| 聊天总结 | internal/chat_summary | 消息聚合 + AI 摘要 |
| RedClaw 集成 | internal/redclaw | 企业后端桥接 |
| 方案/PPT | internal/presentation | 方案生成 + PPT 渲染 |
| 笔记分类 | internal/notes | AI 分类 + 标签提取 |
| 记账 | internal/finance | 语音记账 + 统计 |
| 认证 | internal/auth | JWT 认证 |
| 配置 | internal/config | 环境变量配置 |
```

- [ ] **Step 2: 创建 API 文档**

```markdown
# OpenCode Pocket — API 文档

## 认证
所有 API 端点（除 /healthz 外）需要 JWT Token。
Header: `Authorization: Bearer <token>`

## 端点列表

### RedClaw 集成
- GET  /api/redclaw/health — 连接状态
- POST /api/redclaw/chat — LLM 对话
- POST /api/redclaw/knowledge/search — 知识库检索

### 代码片段
- GET  /api/snippets?language=go&search=sort — 列表
- POST /api/snippets — 创建
- GET  /api/snippets/{id} — 详情
- DELETE /api/snippets/{id} — 删除

### 会议总结
- GET  /api/meetings — 列表
- POST /api/meetings — 创建
- GET  /api/meetings/{id} — 详情
- DELETE /api/meetings/{id} — 删除
- POST /api/meetings/{id}/transcribe — 转写
- POST /api/meetings/{id}/summarize — 总结

### 聊天总结
- GET  /api/chat-summaries?channel_id=xxx — 列表
- POST /api/chat-summaries — 创建（含消息聚合+摘要）
- GET  /api/chat-summaries/{id} — 详情
- DELETE /api/chat-summaries/{id} — 删除

### 产品方案
- POST /api/presentations — 生成方案
- POST /api/presentations/render — 渲染 PPT (html/markdown)

### 笔记分类
- POST /api/notes/classify — AI 分类

### 记账
- GET  /api/finance — 列表
- POST /api/finance — 创建
- GET  /api/finance/{id} — 详情
- DELETE /api/finance/{id} — 删除
- POST /api/finance/parse — 语音解析
- GET  /api/finance/stats — 统计报表

### 审计日志
- GET  /api/audit/logs?tenant_id=xxx — 查询
```

- [ ] **Step 3: 创建部署文档**

```markdown
# OpenCode Pocket — 部署文档

## 环境要求
- Go 1.22+
- Node.js 18+
- JDK 21 (Android)
- Xcode 15+ (iOS)
- PostgreSQL 16 (可选)

## 环境变量
| 变量 | 说明 | 默认值 |
|------|------|--------|
| POCKET_HTTP_PORT | HTTP 端口 | 8088 |
| JWT_SECRET | JWT 密钥 | (必填) |
| POCKET_GROQ_API_KEY | Groq API Key | (STT 用) |
| POCKET_REDCLAW_BASE_URL | RedClaw 地址 | (可选) |
| POCKET_REDCLAW_SECRET | RedClaw 密钥 | (可选) |

## 快速启动
```bash
cd backend
export JWT_SECRET="your-secret"
go run ./cmd/pocketd
```

## Docker 部署
```bash
docker build -t opencode-pocket .
docker run -p 8088:8088 -e JWT_SECRET=xxx opencode-pocket
```

## 移动端构建
### Android
```bash
cd frontend
npm run build
npx cap sync android
cd android && ./gradlew assembleDebug
```

### iOS
```bash
cd frontend
npm run build
npx cap sync ios
npx cap open ios
```
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add docs/ARCHITECTURE.md docs/API.md docs/DEPLOYMENT.md
git commit -m "docs: add architecture, API, and deployment documentation"
```

---

## Phase 4 完成标志

- [x] 企业知识库检索（通过 RedClaw Bridge 调用）
- [x] 多租户治理（JWT 身份同步 + 权限 Header）
- [x] 审计日志（记录 + 查询 + 批量上报）
- [x] iOS 应用支持（Capacitor 配置 + 构建脚本）
- [x] 性能优化（慢请求日志 + 崩溃恢复）
- [x] 全量测试脚本
- [x] 完整文档（架构 + API + 部署）

## 项目总完成标志

- [x] **Phase 1**: RedClaw 基础集成
- [x] **Phase 2**: 核心功能升级（AI 编程 + 会议 + 聊天）
- [x] **Phase 3**: 智能办公工具（方案/PPT + 笔记分类 + 语音记账）
- [x] **Phase 4**: 企业集成与优化（知识库 + 审计 + iOS + 文档）
- [ ] E2E 集成测试通过
- [ ] 正式发布 v1.0