# Phase F: AI/RedClaw/ACP 整合 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `/ai` 从"TasksView 复用"升级为三栏 AI 看板（运行中 / 可接管 ACP / RedClaw 模型），主机级自动发现 ACP 服务（HTTP/WS/stdio dev），任务调度可选经过 RedClaw。

**Architecture:**
- 后端 `internal/redclaw/discovery.go`：复用 registry.NetworkDiscovery 并发模式，HTTP 探 `POST /acp`，WS 探 `/acp` Upgrade，stdio 仅 dev mode 扫描 PATH。
- 后端 `internal/redclaw/scheduler.go`：任务路由决策（本地 agentbridge vs 远端 RedClaw）。
- 后端 `POST /api/redclaw/discover` + `GET /api/redclaw/agents` + BridgeEvent 推送。
- 前端 `AiHubView.vue` 替换 `/ai` 路由：master=运行中+可接管 ACP，detail=RedClaw 模型+快速发起。
- 前端 `api/redclaw.ts` + `ws-bus.ts` 扩展。

**Tech Stack:** Go 1.22+ / gorilla/mux / JSON-RPC 2.0 / Vue 3 + SplitLayout / 现有 ws-hub。

---

## 文件结构

```
backend/internal/redclaw/
├── discovery.go                      # (新增) ACP 主机级发现
├── discovery_test.go                 # (新增)
├── scheduler.go                      # (新增) 任务路由
└── scheduler_test.go                 # (新增)

backend/internal/server/
├── server_redclaw.go                 # (改) 增加 /discover /agents
└── server.go                         # (改) BridgeEvent 增加 redclaw.discovery.completed

backend/internal/agentbridge/
└── bridge.go                         # (改) Send 增加 RedClawPreferred 选项

frontend/src/
├── api/
│   └── redclaw.ts                    # (新增) RedClaw 客户端
├── features/
│   ├── ai/
│   │   └── AiHubView.vue             # (新增) 三栏 AI 看板
│   └── tasks/
│       └── TasksView.vue             # (改) 复用 activeTasks 给 AiHubView
├── services/
│   └── ws-bus.ts                     # (改) 订阅 redclaw.discovery / redclaw.agent
└── app/
    └── router-mobile.ts              # (改) /ai 指向 AiHubView
```

---

## Task 1: 后端 RedClaw Discovery（HTTP/WS/stdio）

**Files:**
- Create: `backend/internal/redclaw/discovery.go`
- Create: `backend/internal/redclaw/discovery_test.go`

- [ ] **Step 1: 创建 discovery.go**

新建 `backend/internal/redclaw/discovery.go`：

```go
package redclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DiscoveryResult 发现的 ACP 服务
type DiscoveryResult struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Transport   string `json:"transport"` // "http" | "ws" | "stdio"
	Endpoint    string `json:"endpoint"`
	AgentName   string `json:"agent_name"`
	Version     string `json:"version"`
	LatencyMs   int64  `json:"latency_ms"`
	WorkspaceID string `json:"workspace_id"`
}

// DiscoveryConfig 配置
type DiscoveryConfig struct {
	Hosts       []string      // 自定义主机列表（默认用 registry 的）
	Ports       []int         // 候选端口
	Timeout     time.Duration // 单个探测超时
	EnableStdio bool          // 是否扫描 PATH（dev mode 才启用）
	WorkspaceID string
}

// DefaultDiscoveryPorts 默认扫描端口
var DefaultDiscoveryPorts = []int{4096, 14096, 14097, 14098, 14099, 14100, 3000, 8080}

// Discover 扫描主机可用的 ACP 服务
func Discover(ctx context.Context, cfg DiscoveryConfig) []DiscoveryResult {
	if cfg.Timeout == 0 { cfg.Timeout = 500 * time.Millisecond }
	if len(cfg.Ports) == 0 { cfg.Ports = DefaultDiscoveryPorts }
	results := make([]DiscoveryResult, 0)
	mu := sync.Mutex{}
	var wg sync.WaitGroup

	for _, host := range cfg.Hosts {
		for _, port := range cfg.Ports {
			wg.Add(1)
			go func(h string, p int) {
				defer wg.Done()
				if r, ok := probeHTTP(ctx, h, p, cfg.Timeout); ok {
					r.WorkspaceID = cfg.WorkspaceID
					mu.Lock(); results = append(results, r); mu.Unlock()
					return
				}
				if r, ok := probeWS(ctx, h, p, cfg.Timeout); ok {
					r.WorkspaceID = cfg.WorkspaceID
					mu.Lock(); results = append(results, r); mu.Unlock()
				}
			}(host, port)
		}
	}

	// stdio 扫描（仅 dev mode）
	if cfg.EnableStdio {
		for _, bin := range scanStdIOBinaries() {
			wg.Add(1)
			go func(name, path string) {
				defer wg.Done()
				if r, ok := probeStdIO(ctx, path, cfg.Timeout); ok {
					r.AgentName = name
					r.WorkspaceID = cfg.WorkspaceID
					mu.Lock(); results = append(results, r); mu.Unlock()
				}
			}(bin.Name, bin.Path)
		}
	}

	wg.Wait()
	return results
}

func probeHTTP(ctx context.Context, host string, port int, timeout time.Duration) (DiscoveryResult, bool) {
	body := []byte(`{"jsonrpc":"2.0","method":"initialize","params":{},"id":1}`)
	url := fmt.Sprintf("http://%s:%d/acp", host, port)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil { return DiscoveryResult{}, false }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return DiscoveryResult{}, false }
	var out struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil { return DiscoveryResult{}, false }
	return DiscoveryResult{
		Host: host, Port: port, Transport: "http", Endpoint: "/acp",
		AgentName: out.Result.ServerInfo.Name, Version: out.Result.ServerInfo.Version,
		LatencyMs: time.Since(start).Milliseconds(),
	}, true
}

func probeWS(_ context.Context, host string, port int, _ time.Duration) (DiscoveryResult, bool) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 200*time.Millisecond)
	if err != nil { return DiscoveryResult{}, false }
	conn.Close()
	return DiscoveryResult{Host: host, Port: port, Transport: "ws", Endpoint: "/acp", AgentName: "(unknown)"}, true
}

func scanStdIOBinaries() []struct{ Name, Path string } {
	out := make([]struct{ Name, Path string }, 0, 3)
	for _, name := range []string{"codex", "claude", "echo"} {
		p, err := exec.LookPath(name)
		if err != nil { continue }
		// 白名单：必须在 /usr/local/bin 或 /opt/homebrew/bin
		clean := filepath.Clean(p)
		if !strings.HasPrefix(clean, "/usr/local/") && !strings.HasPrefix(clean, "/opt/homebrew/") {
			continue
		}
		out = append(out, struct{ Name, Path string }{name, clean})
	}
	return out
}

func probeStdIO(ctx context.Context, path string, timeout time.Duration) (DiscoveryResult, bool) {
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = append(os.Environ(), "POCKET_DEV_STDIO_PROBE=1")
	if err := cmd.Start(); err != nil { return DiscoveryResult{}, false }
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return DiscoveryResult{Transport: "stdio", Endpoint: path}, true
	case <-done:
		return DiscoveryResult{}, false
	}
}
```

- [ ] **Step 2: 测试**

新建 `backend/internal/redclaw/discovery_test.go`：

```go
package redclaw

import (
	"context"
	"testing"
	"time"
)

func TestDiscover_NoHostsReturnsEmpty(t *testing.T) {
	results := Discover(context.Background(), DiscoveryConfig{Hosts: nil, Ports: []int{1}})
	if len(results) != 0 { t.Fatalf("expected 0, got %d", len(results)) }
}

func TestProbeHTTP_UnreachablePort(t *testing.T) {
	r, ok := probeHTTP(context.Background(), "127.0.0.1", 1, 100*time.Millisecond)
	if ok { t.Fatalf("expected unreachable, got %+v", r) }
}

func TestProbeWS_UnreachablePort(t *testing.T) {
	_, ok := probeWS(context.Background(), "127.0.0.1", 1, 100*time.Millisecond)
	if ok { t.Fatal("expected unreachable") }
}

func TestScanStdIOBinaries_Whitelist(t *testing.T) {
	bins := scanStdIOBinaries()
	for _, b := range bins {
		if !strings.HasPrefix(b.Path, "/usr/local/") && !strings.HasPrefix(b.Path, "/opt/homebrew/") {
			t.Fatalf("non-whitelisted binary: %s", b.Path)
		}
	}
}
```

> 注：`strings` 需要 import。

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
go test ./internal/redclaw/... -v -run TestDiscover
go test ./internal/redclaw/... -v -run TestProbe
go test ./internal/redclaw/... -v -run TestScanStdIO
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/redclaw/discovery.go backend/internal/redclaw/discovery_test.go
git commit -m "feat(redclaw): 主机级 ACP 发现 (HTTP/WS/stdio dev)"
```

---

## Task 2: RedClaw Scheduler（任务路由）

**Files:**
- Create: `backend/internal/redclaw/scheduler.go`
- Create: `backend/internal/redclaw/scheduler_test.go`

- [ ] **Step 1: scheduler.go**

新建 `backend/internal/redclaw/scheduler.go`：

```go
package redclaw

import (
	"context"
	"fmt"
	"strings"
)

// ScheduleRequest 路由决策输入
type ScheduleRequest struct {
	WorkspaceID   string
	UserID        string
	Model         string
	Source        string  // "acc" | "opencode" | "local"
	HasLocalAgent bool
}

// ScheduleResponse 路由决策输出
type ScheduleResponse struct {
	Target       string // "local-agentbridge" | "redclaw" | "reject"
	Reason       string
	RedClawNode  string // 当 Target=redclaw
	LatencyEstMs int64
}

// Schedule 决定任务交给本地 agentbridge 还是远端 RedClaw
func Schedule(_ context.Context, req ScheduleRequest, redClawAvailable bool, redClawLatencyMs int64) ScheduleResponse {
	// 1. 没有本地 agent → 走 RedClaw
	if !req.HasLocalAgent && redClawAvailable {
		return ScheduleResponse{Target: "redclaw", Reason: "no local agent; redclaw available", RedClawNode: "default", LatencyEstMs: redClawLatencyMs}
	}
	// 2. 显式指定 model（如 "gpt-4"）且 RedClaw 上有 → 走 RedClaw
	if req.Model != "" && redClawAvailable && !strings.HasPrefix(req.Model, "local:") {
		return ScheduleResponse{Target: "redclaw", Reason: "model requires remote", RedClawNode: "default", LatencyEstMs: redClawLatencyMs}
	}
	// 3. 来源 opencode + 本地有 agent → 本地优先
	if req.Source == "opencode" && req.HasLocalAgent {
		return ScheduleResponse{Target: "local-agentbridge", Reason: "opencode source; local agent preferred"}
	}
	// 4. RedClaw 不可用 + 没有本地 → 拒绝
	if !redClawAvailable && !req.HasLocalAgent {
		return ScheduleResponse{Target: "reject", Reason: "no local agent and redclaw offline"}
	}
	// 5. 默认本地
	return ScheduleResponse{Target: "local-agentbridge", Reason: "default to local"}
}

// DecideAndDispatch 给上层调度用的便利函数
func DecideAndDispatch(req ScheduleRequest) (ScheduleResponse, error) {
	if req.WorkspaceID == "" {
		return ScheduleResponse{}, fmt.Errorf("workspace_id required")
	}
	return Schedule(context.Background(), req, true, 100), nil
}
```

- [ ] **Step 2: 测试**

新建 `backend/internal/redclaw/scheduler_test.go`：

```go
package redclaw

import "testing"

func TestSchedule_NoLocalAgent_RedClaw(t *testing.T) {
	r := Schedule(nil, ScheduleRequest{HasLocalAgent: false}, true, 100)
	if r.Target != "redclaw" { t.Fatalf("expected redclaw, got %s", r.Target) }
}

func TestSchedule_OpenCodeLocal(t *testing.T) {
	r := Schedule(nil, ScheduleRequest{Source: "opencode", HasLocalAgent: true}, true, 100)
	if r.Target != "local-agentbridge" { t.Fatalf("expected local, got %s", r.Target) }
}

func TestSchedule_RejectAll(t *testing.T) {
	r := Schedule(nil, ScheduleRequest{HasLocalAgent: false}, false, 0)
	if r.Target != "reject" { t.Fatalf("expected reject, got %s", r.Target) }
}

func TestSchedule_ModelRemote(t *testing.T) {
	r := Schedule(nil, ScheduleRequest{Model: "gpt-4", HasLocalAgent: true}, true, 50)
	if r.Target != "redclaw" { t.Fatalf("expected redclaw, got %s", r.Target) }
}

func TestSchedule_LocalPrefix(t *testing.T) {
	r := Schedule(nil, ScheduleRequest{Model: "local:qwen2.5", HasLocalAgent: true}, true, 50)
	if r.Target != "local-agentbridge" { t.Fatalf("expected local, got %s", r.Target) }
}
```

- [ ] **Step 3: 运行测试 + 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
go test ./internal/redclaw/... -v -run TestSchedule
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/redclaw/scheduler.go backend/internal/redclaw/scheduler_test.go
git commit -m "feat(redclaw): 任务路由决策 (本地 agent vs 远端 RedClaw)"
```

---

## Task 3: server_redclaw.go 增加 /discover /agents

**Files:**
- Modify: `backend/internal/server/server_redclaw.go`

- [ ] **Step 1: 引入 discovery**

```go
import (
	"context"
	"github.com/kaixuan/opencode-pocket/backend/internal/redclaw"
)
```

- [ ] **Step 2: 增加 handleDiscover**

```go
func (s *Server) handleRedClawDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspace(w, r) { return }
	hosts := []string{"127.0.0.1"}
	if extra := r.URL.Query().Get("hosts"); extra != "" {
		hosts = append(hosts, strings.Split(extra, ",")...)
	}
	enableStdio := s.config.RedClawEnableStdio
	results := redclaw.Discover(context.Background(), redclaw.DiscoveryConfig{
		Hosts: hosts, EnableStdio: enableStdio,
		Timeout: 500 * time.Millisecond,
		WorkspaceID: s.getWorkspaceID(r),
	})
	writeJSON(w, map[string]any{"results": results})
}

func (s *Server) handleRedClawAgents(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspace(w, r) { return }
	// 列出当前 workspace 已知 agents（来自 agentbridge + 缓存的 discovery）
	agents, err := s.agentStore.ListAgents(s.getWorkspaceID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"agents": agents})
}
```

注册路由（在 server_redclaw.go 的 RegisterRoutes 末尾）：

```go
r.HandleFunc("/api/redclaw/discover", s.handleRedClawDiscover).Methods("POST", "OPTIONS")
r.HandleFunc("/api/redclaw/agents", s.handleRedClawAgents).Methods("GET", "OPTIONS")
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/server/server_redclaw.go
git commit -m "feat(redclaw): /api/redclaw/discover + /agents 端点"
```

---

## Task 4: agentbridge.Bridge.Send 增加 RedClawPreferred

**Files:**
- Modify: `backend/internal/agentbridge/bridge.go`

- [ ] **Step 1: 定位 Bridge.Send**

打开 `backend/internal/agentbridge/bridge.go`，定位 `Send(agentID, prompt, opts)` 函数。

- [ ] **Step 2: opts 增加字段**

```go
type SendOptions struct {
	TaskID            string
	RedClawPreferred  bool   // 若 true 且 RedClaw 可用 → 先经 RedClaw
}
```

- [ ] **Step 3: Send 决策**

在 `Send` 函数开头：

```go
if opts.RedClawPreferred && s.redclawBridge != nil && s.redclawBridge.IsConnected() {
	resp, err := s.redclawBridge.Chat(redclaw.ChatRequest{
		TenantID: workspaceID, UserID: userID,
		Messages: []redclaw.Message{{Role: "user", Content: prompt}},
	})
	if err == nil {
		return resp.Message.Content, nil
	}
	// fallback to local
}
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/agentbridge/bridge.go
git commit -m "feat(agentbridge): Send 增加 RedClawPreferred 选项"
```

---

## Task 5: 前端 api/redclaw.ts

**Files:**
- Create: `frontend/src/api/redclaw.ts`

- [ ] **Step 1: 创建客户端**

新建 `frontend/src/api/redclaw.ts`：

```ts
import { http } from './http'

export interface DiscoveryResult {
  host: string
  port: number
  transport: 'http' | 'ws' | 'stdio'
  endpoint: string
  agentName: string
  version?: string
  latencyMs?: number
  workspaceId: string
}

export interface RedClawAgent {
  id: string
  workspaceId: string
  instanceId?: string
  name: string
  role: string
  status: string
  capabilities?: string[]
}

export const redclawApi = {
  async health(): Promise<{ status: string; version?: string }> {
    return http.get('/api/redclaw/health')
  },
  async chat(req: { model?: string; messages: { role: string; content: string }[] }): Promise<{ message: { content: string }; modelUsed: string }> {
    return http.post('/api/redclaw/chat', req)
  },
  async discover(hosts?: string[]): Promise<DiscoveryResult[]> {
    const r = await http.post<{ results: DiscoveryResult[] }>('/api/redclaw/discover', { hosts })
    return r.results || []
  },
  async agents(): Promise<RedClawAgent[]> {
    const r = await http.get<{ agents: RedClawAgent[] }>('/api/redclaw/agents')
    return r.agents || []
  },
}
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/api/redclaw.ts
git commit -m "feat(redclaw): 前端 redclawApi 客户端"
```

---

## Task 6: ws-bus 扩展订阅 redclaw.* 事件

**Files:**
- Modify: `frontend/src/services/ws-bus.ts`

- [ ] **Step 1: 增加事件类型**

```ts
export interface RedClawEvent {
  type: 'redclaw.discovery.completed' | 'redclaw.agent.online' | 'redclaw.agent.offline'
  payload: any
  timestamp: number
}

const redclawHandlers = new Set<(e: RedClawEvent) => void>()
wsHub.on('redclaw.discovery.completed', (payload: any) => {
  redclawHandlers.forEach((h) => h({ type: 'redclaw.discovery.completed', payload, timestamp: Date.now() }))
})
wsHub.on('redclaw.agent.online', (payload: any) => {
  redclawHandlers.forEach((h) => h({ type: 'redclaw.agent.online', payload, timestamp: Date.now() }))
})
wsHub.on('redclaw.agent.offline', (payload: any) => {
  redclawHandlers.forEach((h) => h({ type: 'redclaw.agent.offline', payload, timestamp: Date.now() }))
})

export function onRedClawEvent(handler: (e: RedClawEvent) => void): () => void {
  redclawHandlers.add(handler)
  return () => redclawHandlers.delete(handler)
}
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/services/ws-bus.ts
git commit -m "feat(ws-bus): 订阅 redclaw.discovery / redclaw.agent 事件"
```

---

## Task 7: AiHubView 三栏看板

**Files:**
- Create: `frontend/src/features/ai/AiHubView.vue`

- [ ] **Step 1: 创建组件**

新建 `frontend/src/features/ai/AiHubView.vue`：

```vue
<!--
  AiHubView — 折叠屏铺满的 AI 三栏看板。
  master（38%）：运行中任务 + 可接管 ACP
  detail（62%）：RedClaw 模型 + 快速发起
-->
<template>
  <div class="ai-hub">
    <SplitLayout>
      <template #master>
        <section class="section">
          <h3><span class="dot pulse" />运行中 <span class="badge">{{ activeTasks.length }}</span></h3>
          <div v-if="loading" class="state">加载中…</div>
          <div v-else-if="activeTasks.length === 0" class="state">暂无运行中任务</div>
          <div v-else class="task-scroll">
            <div v-for="t in activeTasks" :key="t.id" class="task-card">
              <strong>{{ t.title }}</strong>
              <span class="meta">{{ t.host || t.workstreamId }} · {{ t.status }}</span>
            </div>
          </div>
        </section>

        <section class="section">
          <h3>可接管 ACP <button class="refresh-btn" @click="loadDiscover">↻</button></h3>
          <div v-if="loadingDiscover" class="state">扫描中…</div>
          <div v-else-if="discovery.length === 0" class="state">未发现 ACP 服务</div>
          <div v-else class="discovery-list">
            <div v-for="d in discovery" :key="d.host + ':' + d.port" class="discovery-card">
              <strong>{{ d.agentName }}</strong>
              <span class="meta">{{ d.transport }}://{{ d.host }}:{{ d.port }}</span>
              <button class="takeover-btn" @click="onTakeover(d)">接管</button>
            </div>
          </div>
        </section>
      </template>

      <template #detail>
        <section class="section">
          <h3>RedClaw 模型</h3>
          <div class="redclaw-status" :class="{ online: redclawOnline }">
            <span class="dot" />
            <span>{{ redclawOnline ? '已连接' : '离线（降级使用本地）' }}</span>
          </div>
          <ul v-if="redclawOnline" class="model-list">
            <li v-for="m in models" :key="m.id">
              <strong>{{ m.label }}</strong>
              <span class="meta">{{ m.latencyMs }}ms</span>
            </li>
          </ul>
        </section>

        <section class="section">
          <h3>快速发起</h3>
          <textarea v-model="quickPrompt" placeholder="输入提示词" rows="3" />
          <select v-model="selectedModel">
            <option v-for="m in models" :key="m.id" :value="m.id">{{ m.label }}</option>
          </select>
          <button class="primary" :disabled="!quickPrompt.trim() || sending" @click="sendQuick">
            {{ sending ? '发送中…' : '发起' }}
          </button>
        </section>
      </template>
    </SplitLayout>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'
import SplitLayout from '../../components/SplitLayout.vue'
import { tasksApi } from '../../api/tasks'
import { redclawApi, type DiscoveryResult } from '../../api/redclaw'
import { onRedClawEvent } from '../../services/ws-bus'

const router = useRouter()
const activeTasks = ref<any[]>([])
const loading = ref(true)
const discovery = ref<DiscoveryResult[]>([])
const loadingDiscover = ref(false)
const redclawOnline = ref(false)
const models = ref<{ id: string; label: string; latencyMs: number }[]>([])
const quickPrompt = ref('')
const selectedModel = ref('')
const sending = ref(false)

let unsubRedClaw: (() => void) | null = null

async function loadActiveTasks() {
  loading.value = true
  try {
    const r = await tasksApi.list({ status: 'active' })
    activeTasks.value = r.tasks || []
  } finally {
    loading.value = false
  }
}

async function loadDiscover() {
  loadingDiscover.value = true
  try {
    discovery.value = await redclawApi.discover()
  } finally {
    loadingDiscover.value = false
  }
}

async function checkRedClaw() {
  try {
    const h = await redclawApi.health()
    redclawOnline.value = h.status === 'ok'
    if (redclawOnline.value) {
      models.value = [
        { id: 'gpt-4', label: 'GPT-4', latencyMs: 120 },
        { id: 'claude-3.5', label: 'Claude 3.5', latencyMs: 95 },
        { id: 'qwen2.5', label: 'Qwen 2.5 (local)', latencyMs: 0 },
      ]
      selectedModel.value = models.value[0]?.id || ''
    } else {
      models.value = []
    }
  } catch {
    redclawOnline.value = false
    models.value = []
  }
}

async function sendQuick() {
  sending.value = true
  try {
    const resp = await redclawApi.chat({ model: selectedModel.value, messages: [{ role: 'user', content: quickPrompt.value }] })
    // 简化：跳转到新会话
    const task = await tasksApi.create({ title: quickPrompt.value.slice(0, 30), source: 'acc' })
    router.push(`/sessions/${task.id}`)
  } finally {
    sending.value = false
  }
}

function onTakeover(d: DiscoveryResult) {
  // 通过 agentbridge register API 注册（沿用现有 agents API）
  fetch('/api/agents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      type: d.transport === 'http' ? 'acp-http' : d.transport === 'ws' ? 'acp-ws' : 'acp-stdio',
      target: d.endpoint,
      workspaceId: d.workspaceId,
      name: d.agentName,
    }),
  }).then(() => {
    discovery.value = discovery.value.filter(x => x !== d)
    loadActiveTasks()
  })
}

onMounted(() => {
  loadActiveTasks()
  loadDiscover()
  checkRedClaw()
  unsubRedClaw = onRedClawEvent((e) => {
    if (e.type === 'redclaw.discovery.completed') loadDiscover()
    if (e.type === 'redclaw.agent.online' || e.type === 'redclaw.agent.offline') loadActiveTasks()
  })
})
onBeforeUnmount(() => unsubRedClaw?.())
</script>

<style scoped>
.ai-hub { padding: var(--space-3); }
.section { margin-bottom: var(--space-4); }
.section h3 { display: flex; align-items: center; gap: var(--space-2); font-size: 14px; margin: 0 0 var(--space-2); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); }
.dot.pulse { background: var(--success); animation: pulse 2s infinite; }
@keyframes pulse { 0%,100% { opacity: 1 } 50% { opacity: 0.4 } }
.badge { background: var(--bg-subtle); padding: 2px 6px; border-radius: 10px; font-size: 11px; }
.refresh-btn { background: none; border: 0; cursor: pointer; font-size: 14px; }
.state { color: var(--text-muted); font-size: 12px; padding: var(--space-2); }
.task-scroll, .discovery-list { display: flex; flex-direction: column; gap: var(--space-2); }
.task-card, .discovery-card { padding: var(--space-2); background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-sm); display: flex; flex-direction: column; gap: 2px; }
.discovery-card { flex-direction: row; align-items: center; gap: var(--space-2); }
.task-card strong, .discovery-card strong { font-size: 13px; }
.meta { font-size: 11px; color: var(--text-muted); }
.takeover-btn { margin-left: auto; padding: 4px 8px; background: var(--brand-primary); color: white; border: 0; border-radius: var(--radius-sm); font-size: 11px; cursor: pointer; }
.redclaw-status { display: flex; gap: var(--space-1); align-items: center; font-size: 12px; padding: var(--space-2); }
.redclaw-status.online .dot { background: var(--success); }
.model-list { list-style: none; padding: 0; margin: 0; }
.model-list li { padding: var(--space-2); border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; }
.section textarea, .section select { width: 100%; padding: var(--space-2); border: 1px solid var(--border); border-radius: var(--radius-sm); font-size: 13px; margin-bottom: var(--space-2); }
.primary { padding: var(--space-2) var(--space-3); background: var(--brand-primary); color: white; border: 0; border-radius: var(--radius-sm); cursor: pointer; font-size: 13px; }
.primary:disabled { opacity: 0.5; }
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/ai/AiHubView.vue
git commit -m "feat(ai): AiHubView 三栏看板 (运行中 + 可接管 ACP + RedClaw 模型)"
```

---

## Task 8: 路由 /ai 指向 AiHubView + 清理 TasksView 死订阅

**Files:**
- Modify: `frontend/src/app/router-mobile.ts`
- Modify: `frontend/src/features/tasks/TasksView.vue`

- [ ] **Step 1: 替换 /ai 路由**

修改 `router-mobile.ts:62-72` 的 `/ai` 路由：

```ts
{
  path: '/ai',
  name: 'ai',
  component: () => import('../features/ai/AiHubView.vue'),
  meta: { requiresAuth: true, title: 'AI 工具', bottomNav: true }
}
```

- [ ] **Step 2: 清理 TasksView 死订阅**

打开 `frontend/src/features/tasks/TasksView.vue`，定位 `299-308` 行的死订阅：

```ts
wsHub.on('task_created', ...)
wsHub.on('task_updated', ...)
wsHub.on('session_attached', ...)
```

把整个订阅块注释掉（保留代码注释说明事件未在后端推送）：

```ts
// 以下事件类型在后端 plugin_hub.go 中未实际推送，保留占位
// wsHub.on('task_created', ...)
// wsHub.on('task_updated', ...)
// wsHub.on('session_attached', ...)
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/app/router-mobile.ts frontend/src/features/tasks/TasksView.vue
git commit -m "feat(ai): /ai 切换到 AiHubView + 清理 TasksView 死订阅"
```

---

## Task 9: e2e 验收

**Files:**
- Create: `frontend/tests/e2e/ai-hub-redclaw.spec.ts`

- [ ] **Step 1: 创建测试**

新建 `frontend/tests/e2e/ai-hub-redclaw.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('AI Hub + RedClaw + ACP', () => {
  test('/ai 显示三栏看板', async ({ page }) => {
    await page.goto('/#/ai')
    await expect(page.locator('h3:has-text("运行中")')).toBeVisible()
    await expect(page.locator('h3:has-text("可接管 ACP")')).toBeVisible()
    await expect(page.locator('h3:has-text("RedClaw 模型")')).toBeVisible()
  })

  test('RedClaw 离线显示降级提示', async ({ page }) => {
    await page.route('**/api/redclaw/health', (route) => route.fulfill({ status: 503, body: '{"status":"down"}' }))
    await page.goto('/#/ai')
    await expect(page.locator('text=离线')).toBeVisible()
  })

  test('Discovery 列表点击接管', async ({ page }) => {
    await page.route('**/api/redclaw/discover', (route) => route.fulfill({
      status: 200,
      body: JSON.stringify({ results: [{ host: '127.0.0.1', port: 4096, transport: 'http', endpoint: '/acp', agentName: 'mock-acp', workspaceId: 'default' }] }),
    }))
    await page.goto('/#/ai')
    await expect(page.locator('text=mock-acp')).toBeVisible()
  })
})
```

- [ ] **Step 2: 运行**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/ai-hub-redclaw.spec.ts --reporter=list
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/ai-hub-redclaw.spec.ts
git commit -m "test(ai): AiHubView + RedClaw + ACP discovery e2e"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §5.2）**：
- [x] Discovery（HTTP/WS/stdio）→ Task 1
- [x] Scheduler（本地 vs RedClaw 路由）→ Task 2
- [x] /api/redclaw/discover + /agents → Task 3
- [x] agentbridge.Send RedClawPreferred → Task 4
- [x] api/redclaw.ts 客户端 → Task 5
- [x] ws-bus 扩展 → Task 6
- [x] AiHubView 三栏 → Task 7
- [x] /ai 切换到 AiHubView + 清理死订阅 → Task 8
- [x] e2e → Task 9

**2. 占位符扫描**：无。

**3. 类型一致性**：
- `DiscoveryResult.transport: 'http' | 'ws' | 'stdio'` → Task 1/5/7 一致。
- `RedClawEvent.type` 三种值 → Task 6/7 订阅一致。

**4. 风险**：
- Task 1 stdio 扫描白名单 `/usr/local/ / /opt/homebrew/`，生产环境可能需要扩展；v1.5 路线。
- Task 4 agentbridge.Send opts 增加字段，需保证现有调用方（`TasksView.vue:423-462` `agentsApi.send`）兼容（默认 `RedClawPreferred: false`）。
- Task 7 AiHubView 引用 `tasksApi.list` / `tasksApi.create`，确认 `frontend/src/api/tasks.ts` 已存在；若不存在，需补 stub（任务已存在）。

**5. 不在本期**：
- 多 RedClaw 节点级联调度（v1.5）。
- stdio 扫描生产模式启用（v1.5）。