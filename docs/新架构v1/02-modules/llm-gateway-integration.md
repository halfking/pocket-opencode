# 模块：LLM Gateway 集成

> **核心结论**：PocketFleet 的所有 LLM 调用都通过 **llm-gateway-go**（生产就绪的企业 LLM 网关）发起。pocketd 不直接连任何 Provider；acc-go 也不直接连 Provider；只有 llm-gateway-go 知道怎么连 DeepSeek / OpenAI / Claude / Gemini。

---

## 1. 为什么是 llm-gateway-go？

`services/llm-gateway-go/`（端口 `:8781`）是 monorepo 内**唯一**的生产就绪企业 LLM 网关：

| 能力 | 说明 |
|---|---|
| **多协议兼容** | OpenAI Chat Completions / Anthropic Messages / OpenAI Responses / Gemini native |
| **Provider Routing** | `model=auto` 任务分类 + candidate-model 选择 + tier/billing/sticky sessions |
| **P2C / Bandit 算法** | 多 Provider 之间按性能 / 成本分发 |
| **URSM** | Unified Routing State Machine，跨 Provider 状态保持 |
| **多 Credential Pool** | 多账户轮换 / 健康检查 / 速率限制 / fingerprint slot 管理 |
| **Streaming SSE** | 原生 OpenAI SSE relay，4MB scanner buffer |
| **WAL** | Write-Ahead Log（每个请求持久化） |
| **Audit** | 完整审计 |
| **Prometheus + OTel** | 可观测性 |
| **Session Cache + Compression** | session 缓存与压缩 |
| **License 管理** | 企业 license 校验 |
| **DeepSeek 一等公民** | 因为兼容 OpenAI API，加一个 Provider 即可 |

直接的好处：

- **DeepSeek 自动支持** —— DeepSeek 兼容 OpenAI API，只需在 llm-gateway-go 配置一个 OpenAI-compatible endpoint。
- **故障转移** —— DeepSeek 限流时自动切到 OpenAI / Claude。
- **成本控制** —— llm-gateway-go 已有 quota / budget / per-tenant billing。
- **审计** —— 每个 LLM 调用都进 WAL，pocketd 直接读 `GET /api/admin/usage`。

---

## 2. LLM 调用路径

### 2.1 三条路径

PocketFleet 场景下有三种 LLM 调用需求：

| 场景 | 调用方 | 路径 |
|---|---|---|
| **Chief 规划** | acc-go taskdecompose | `acc-go → llm-gateway-go /v1/chat/completions` |
| **Agent 工具调用循环** | acc-go agent loop | `acc-go → llm-gateway-go /v1/chat/completions` 或 `/v1/messages` |
| **Mobile 直连 fallback** | pocketd 直接调 | `pocketd → llm-gateway-go /v1/chat/completions` |

### 2.2 acc-go 的封装

acc-go 已经有 `internal/llm/` 模块，作为 llm-gateway-go 的 typed client + 反向代理：

```go
// 来自 acc-go main.go 的代码（片段）：
LLMGateway: llmGw,  // llm-gateway-go 的 client
// 反向代理：
ANY  /api/llm/gw/*  →  http://llm-gateway-go:8780/*
```

**所以 acc-go 已经是 PocketFleet 的 LLM 网关中间层**。

### 2.3 pocketd 的封装

pocketd 已经有 `internal/llmgateway/client.go`：

```go
// 来自 pocketd internal/llmgateway/client.go（推测）
type Client struct {
    BaseURL string
    APIKey  string
    HTTPDo  *http.Client
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
func (c *Client) ListModels(ctx context.Context) ([]Model, error)
```

pocketd fleetbridge 直接复用这个 client；不需要新写。

---

## 3. Provider 注册（DeepSeek 一等公民）

### 3.1 在 llm-gateway-go 里加 DeepSeek

由于 DeepSeek 兼容 OpenAI API，加 Provider 的步骤：

```yaml
# llm-gateway-go 的 providers.yaml（示例）
providers:
  - name: deepseek
    type: openai_compat
    base_url: https://api.deepseek.com/v1
    api_key: ${DEEPSEEK_API_KEY}
    models:
      - id: deepseek-v4-flash
        tier: economy
        context_window: 1000000
        max_output: 384000
      - id: deepseek-v4-pro
        tier: premium
        context_window: 1000000
        max_output: 384000
        thinking:
          enabled: true
          param: reasoning_effort  # DeepSeek 特有
```

### 3.2 Model 选择策略

```
pocketd 用户偏好 model = "deepseek-v4-flash"
   ↓
acc-go 收到意图 → 调 llm-gateway-go
   ↓
llm-gateway-go 路由：指定模型 → DeepSeek endpoint
   ↓
如果 DeepSeek 失败 / 限流 → fallback 到下一候选（OpenAI / Claude）
```

### 3.3 在 PocketFleet UI 里选模型

```
Mobile UI:
  Model: ◉ deepseek-v4-flash  ◯ deepseek-v4-pro  ◯ auto
```

`auto` 让 llm-gateway-go 自己根据任务特征选（task classification）。

---

## 4. Streaming 集成

### 4.1 为什么 streaming 重要

PocketFleet 的 mobile Live UI 必须能 streaming 显示 Agent 的输出（"正在读取 OAuth 配置..."、"跑测试..."）。

### 4.2 acc-go 的 streaming

acc-go 的 Agent Loop 已经支持 streaming：

```go
// 简化伪代码（acc-go agent/loop）
func (l *Loop) Run(ctx context.Context, task Task) error {
    stream, err := l.llm.ChatStream(ctx, llm.ChatRequest{
        Model:    task.Model,
        Messages: l.messages,
        Tools:    l.tools,
        Stream:   true,
    })
    for event := range stream.Events() {
        switch event.Type {
        case "delta":
            l.sse.Publish("agent.message", event)  // → SSE → pocketd WS Hub → mobile
        case "tool_call":
            l.sse.Publish("agent.tool_call", event)
        case "done":
            return nil
        }
    }
}
```

### 4.3 pocketd ws_bridge.go 的角色

```go
// backend/internal/fleetbridge/ws_bridge.go (伪代码)
func (b *WSBridge) SubscribeMission(ctx context.Context, missionID string) error {
    sseURL := b.accBaseURL + "/api/v2/missions/" + missionID + "/events"
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
    req.Header.Set("Accept", "text/event-stream")
    req.Header.Set("Authorization", "Bearer "+b.accSecret)

    resp, err := b.httpDo.Do(req)
    // ... read SSE stream line by line ...

    scanner := bufio.NewScanner(resp.Body)
    scanner.Buffer(make([]byte, 1<<20), 1<<24) // 16MB buffer for big tool args

    for scanner.Scan() {
        line := scanner.Bytes()
        if bytes.HasPrefix(line, []byte("data: ")) {
            payload := bytes.TrimPrefix(line, []byte("data: "))
            // 转换为 pocketd WS Hub 事件
            b.hub.BroadcastToFleet(fleetID, parseSSEType(payload), json.RawMessage(payload))
        }
    }
}
```

---

## 5. Cost 聚合

### 5.1 数据来源

- llm-gateway-go 的每个请求都进 WAL + audit；
- 通过 `GET /api/admin/usage?from=&to=&groupBy=...` 查；
- 包含：model、provider、input_tokens、output_tokens、cents、tenant_id。

### 5.2 实时 cost tick

```
acc-go Agent Loop 调用 LLM
   ↓
llm-gateway-go 记录 usage 到 WAL
   ↓
pocketd fleetbridge.cost.go 周期 poll（每 60s）
   GET llm-gateway-go /api/admin/usage?last=60s
   ↓
累加到 in-memory 计数
   ↓
通过 WebSocket Hub 推送 `cost.tick` 事件给 mobile
   ↓
Mobile UI 实时显示 "本次任务消耗 ¥0.42"
```

### 5.3 持久化

每分钟 / 每小时聚合后，写 Memora `pocketfleet/cost/{date}/{fleet_id}`。

---

## 6. Tool Use / Function Calling

### 6.1 工具定义

Agent Loop 调 LLM 时附带 tool 定义：

```json
{
  "model": "deepseek-v4-flash",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "file_read",
        "description": "Read a file from the workspace",
        "parameters": {
          "type": "object",
          "properties": {
            "path": { "type": "string", "description": "File path relative to workspace root" }
          },
          "required": ["path"],
          "additionalProperties": false
        }
      }
    },
    {
      "type": "function",
      "function": {
        "name": "shell_run",
        "description": "Run a shell command in the workspace",
        "parameters": {
          "type": "object",
          "properties": {
            "cmd": { "type": "string" },
            "timeout": { "type": "integer", "default": 300 }
          },
          "required": ["cmd"]
        }
      }
    },
    {
      "type": "function",
      "function": {
        "name": "git_push",
        "description": "Push commits to a remote branch",
        "parameters": {
          "type": "object",
          "properties": {
            "remote": { "type": "string", "default": "origin" },
            "branch": { "type": "string" }
          },
          "required": ["branch"]
        }
      }
    }
  ]
}
```

### 6.2 Tool Schema 的统一

工具定义在 acc-go `internal/agent/tools/` 维护（已有）；所有 Agent 共享。**PocketFleet 不重新定义工具**。

### 6.3 DeepSeek 注意事项

DeepSeek strict mode 不支持 `minLength/maxLength/minItems/maxItems`；acc-go 现有 schema 设计应该已经规避了这些限制。

---

## 7. 故障转移 / Fallback

### 7.1 Provider 故障

llm-gateway-go 已有：

- 凭据池自动切换；
- 速率限制自动 retry；
- 健康检查自动剔除。

### 7.2 llm-gateway-go 整个故障

```
pocketd 调用 llm-gateway-go 超时 / 5xx
   ↓
pocketd fallback 链：
   1. 重试 3 次（指数退避）
   2. 切到 pocketd 内置的简单 LLM 客户端（如有）
   3. 返回错误给用户，建议稍后重试
```

### 7.3 acc-go 整个故障

pocketd 已经设计了 fallback 模式（见 chief-as-acc.md §6）。

---

## 8. 与 RedClaw 的关系

### 8.1 RedClaw 的定位

RedClaw Gateway（`:8092`，外部仓库）当前是 **echo stub**。它的设计意图是作为"移动端专用 Edge Gateway"，可以增加：

- 移动端特有缓存；
- 移动端流量整形；
- 移动端租户隔离增强；
- 离线消息暂存。

### 8.2 三种部署模式

| 模式 | 路径 | 用途 |
|---|---|---|
| **A：直接模式** | pocketd → acc-go → llm-gateway-go | 默认，**生产推荐** |
| **B：Edge 模式** | pocketd → RedClaw Gateway → acc-go → llm-gateway-go | 移动端流量大时启用 RedClaw 作为 edge 缓存 |
| **C：直连 LLM** | pocketd → llm-gateway-go | acc-go 不可用时的 fallback |

### 8.3 v2 建议

**默认走 A 模式**。B 模式作为可选优化（等 RedClaw Gateway 真正实现 `/api/v1/pocket/llm/chat` 之后再启用）。

---

## 9. 与 PocketFleet 现有 aigate 的关系

### 9.1 pocketd 已有 aigate

pocketd `internal/aigate/` 已经有 LLM 代理模块（含 DeepSeek Provider 雏形）。

### 9.2 v2 的取舍

- **保留** `internal/aigate/` 用于简单场景（聊天、补全、嵌入）；
- **主路径**走 acc-go → llm-gateway-go（用于 PocketFleet 任务场景）；
- **不重复** Provider 实现 —— llm-gateway-go 已经覆盖。

---

## 10. 测试策略

### 10.1 单元测试

- fleetbridge 的 LLM 调用用 mock LLMGateway client；
- 验证：模型选择 / 失败 fallback / streaming 解析。

### 10.2 集成测试

- 完整链路：pocketd → acc-go → llm-gateway-go → DeepSeek；
- 验证：DeepSeek API key 正常、response 正确、cost 正确。

### 10.3 故障注入

- DeepSeek 5xx → 验证 fallback；
- DeepSeek 限流 → 验证凭据池切换；
- llm-gateway-go 整个挂 → 验证 pocketd 错误传播。

---

## 11. 相关文件位置

- llm-gateway-go 入口：`services/llm-gateway-go/cmd/gateway/main.go`
- llm-gateway-go 数据面：`services/llm-gateway-go/internal/` (streaming, providers, routing)
- pocketd 客户端：`opencode-pocket/backend/internal/llmgateway/client.go`
- pocketd aigate：`opencode-pocket/backend/internal/aigate/`
- acc-go 客户端：`services/agent-control-center/acc-go/internal/llm/`
- acc-go 反代：`services/agent-control-center/acc-go/cmd/acc/main.go` (mux.Handle `/api/llm/gw/*`)

---

## 12. 一句话总结

**LLM 路由 = llm-gateway-go。pocketd / acc-go / Memora / 算力舱 / Mobile 都不直接连任何 Provider；都通过 llm-gateway-go。DeepSeek 作为第一个一等公民 Provider。**
