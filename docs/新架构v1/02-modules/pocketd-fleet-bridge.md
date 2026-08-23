# 模块：pocketd Fleet Bridge（v3 更新版）

> v3 重大变化：pocketd fleetbridge 增加 `internal/zag/` 子包，专门对接 ZAgentGateway。

---

## 1. 包结构（v3）

```
backend/internal/fleetbridge/
├── client.go              # 统一 HTTP 客户端基类
├── acc_client.go          # acc-go typed client
├── memora_client.go       # Memora typed client
├── llmgw_client.go        # llm-gateway-go typed client
├── zag_client.go          # NEW: ZAgentGateway typed client
├── intent.go              # POST /api/fleet/intent
├── build.go               # 透传 ZAG
├── task.go                # 透传 ZAG
├── permission.go          # 透传 ZAG
├── cost.go                # 聚合 llm-gateway-go usage
├── ws_bridge.go           # ZAG SSE → pocketd WS Hub
├── charter.go             # Memora
├── skill.go               # Memora
├── pod.go                 # NEW: 透传 ZAG (PC device)
├── agent.go               # NEW: 透传 ZAG (OpenClaw / IDE agents)
├── ide.go                 # NEW: 透传 ZAG (zcode / vscode / cursor / opencode)
├── schedule.go            # Memora + cron worker
├── fallback.go            # ZAG/acc-go 不可用时
└── api.go                 # 路由注册
```

```
backend/internal/zag/                  # NEW: ZAG 客户端封装
├── client.go                          # 通用 HTTP 客户端
├── pods.go
├── agents.go
├── ide.go
├── sessions.go
├── tasks.go
├── permissions.go
└── types.go
```

总计 ~2.5k 行 Go（v3 比 v2 多 ~500 行，主要是 zag 子包）。

---

## 2. ZAGClient（新增）

### 2.1 基类

```go
// backend/internal/zag/client.go
package zag

import (
    "context"
    "net/http"
    "time"

    "github.com/halfking/pocket-opencode/backend/internal/identity"
)

type Client struct {
    BaseURL string
    TokenIssuerKeyRef string
    HTTPDo  *http.Client
}

func New(baseURL, secret string) *Client {
    return &Client{
        BaseURL: baseURL,
        TokenIssuerKeyRef: secret, // 仅表示密钥引用，不是全局租户凭据
        HTTPDo:  &http.Client{Timeout: 30 * time.Second},
    }
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
    req, _ := http.NewRequestWithContext(ctx, method, c.BaseURL+path, jsonBody(body))
    req.Header.Set("Authorization", "Bearer "+c.issueDelegatedToken(ctx))
    req.Header.Set("Content-Type", "application/json")
    if claims, ok := identity.FromContext(ctx); ok {
        req.Header.Set("X-Tenant-ID", claims.WorkspaceID) // consistency check only
        req.Header.Set("X-User-ID", claims.UserID)         // consistency check only
    }
    // ... send + decode; reject upstream claims/header/body mismatch
}

```

### 2.2 Pods

```go
// backend/internal/zag/pods.go
type Pod struct {
    ID         string   `json:"id"`
    FleetID    string   `json:"fleetId"`
    Name       string   `json:"name"`
    Hostname   string   `json:"hostname"`
    OS         string   `json:"os"`
    Status     string   `json:"status"`
    CPUs       int      `json:"cpus"`
    MemoryGB   int      `json:"memoryGB"`
    GPU        string   `json:"gpu,omitempty"`
    Agents     []string `json:"agents"`
    IDEs       []string `json:"ides"`
    Region     string   `json:"region"`
    LastSeen   time.Time `json:"lastSeen"`
}

func (c *Client) ListPods(ctx context.Context, req ListPodsRequest) ([]Pod, error) {
    var resp struct {
        Items []Pod `json:"items"`
    }
    path := "/api/v1/pods?fleetId=" + req.FleetID
    if req.Status != "" { path += "&status=" + req.Status }
    if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
        return nil, err
    }
    return resp.Items, nil
}

func (c *Client) GetPod(ctx context.Context, podID string) (*Pod, error) {
    var pod Pod
    if err := c.do(ctx, http.MethodGet, "/api/v1/pods/"+podID, nil, &pod); err != nil {
        return nil, err
    }
    return &pod, nil
}

func (c *Client) ControlPod(ctx context.Context, podID string, req ControlPodRequest) error {
    return c.do(ctx, http.MethodPost, "/api/v1/pods/"+podID+"/control", req, nil)
}

type ControlPodRequest struct {
    Kind   string `json:"kind"`   // pause / resume / restart / upgrade / rollback / terminate
    Reason string `json:"reason"`
}
```

### 2.3 Agents

```go
// backend/internal/zag/agents.go
type Agent struct {
    ID            string    `json:"id"`
    FleetID       string    `json:"fleetId"`
    PodID         string    `json:"podId"`
    Name          string    `json:"name"`
    Kind          string    `json:"kind"`
    Runtime       string    `json:"runtime"`
    Status        string    `json:"status"`
    Capabilities  []string  `json:"capabilities"`
    Harness       string    `json:"harness"`
    Model         string    `json:"model"`
    LastSeen      time.Time `json:"lastSeen"`
}

func (c *Client) ListAgents(ctx context.Context, req ListAgentsRequest) ([]Agent, error) {
    // GET /api/v1/agents?fleetId=&status=&podId=&kind=
}

func (c *Client) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
    // GET /api/v1/agents/:id
}

func (c *Client) InvokeAgent(ctx context.Context, agentID string, req InvokeRequest) (*InvokeResult, error) {
    // POST /api/v1/agents/:id/invoke
}
```

### 2.4 IDE

```go
// backend/internal/zag/ide.go
type IDEStatus struct {
    Name         string    `json:"name"`
    Version      string    `json:"version"`
    Running      bool      `json:"running"`
    Workspace    string    `json:"workspace,omitempty"`
    Extensions   []string  `json:"extensions,omitempty"`
    LastCommand  string    `json:"lastCommand,omitempty"`
    LastActivity time.Time `json:"lastActivity"`
}

type IDECommand struct {
    Command string         `json:"command"`
    Args    map[string]any `json:"args,omitempty"`
}

func (c *Client) ListIDEs(ctx context.Context, req ListIDEsRequest) ([]IDEStatus, error) {
    // GET /api/v1/ide
}

func (c *Client) GetIDEStatus(ctx context.Context, name string) (*IDEStatus, error) {
    // GET /api/v1/ide/:name/status
}

func (c *Client) ExecuteIDECommand(ctx context.Context, name string, req IDECommand) (*ExecutionReceipt, error) {
    // POST /api/v1/ide/:name/command
}
```

### 2.5 Tasks / Permissions

```go
// backend/internal/zag/tasks.go
type Task struct {
    ID         string    `json:"id"`
    FleetID    string    `json:"fleetId"`
    SessionID  string    `json:"sessionId"`
    PodID      string    `json:"podId"`
    AgentID    string    `json:"agentId"`
    Goal       string    `json:"goal"`
    Status     string    `json:"status"`
    StartedAt  *time.Time `json:"startedAt,omitempty"`
    FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func (c *Client) SubmitTask(ctx context.Context, req SubmitTaskRequest) (*Task, error) {
    // POST /api/v1/tasks
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
    // GET /api/v1/tasks/:id
}

func (c *Client) SubscribeTaskEvents(ctx context.Context, taskID string) (<-chan Event, error) {
    // GET /api/v1/tasks/:id/events (SSE)
}

func (c *Client) CancelTask(ctx context.Context, taskID string) error {
    // POST /api/v1/tasks/:id/cancel
}

func (c *Client) ReplyPermission(ctx context.Context, permID string, req ReplyPermissionRequest) error {
    // POST /api/v1/permissions/:id/reply
}
```

---

## 3. Intent Handler（v3 更新）

```go
// backend/internal/fleetbridge/intent.go
type IntentHandler struct {
    acc      *ACCClient
    zag      *zag.Client
    fallback *FallbackHandler
}

func (h *IntentHandler) Handle(ctx context.Context, req IntentRequest) (*IntentResponse, error) {
    // 1. 先试 acc-go 路径（包含 Chief 拆分）
    resp, err := h.acc.CreateCanonicalTask(ctx, toACCTask(req))
    if err == nil {
        return &IntentResponse{
            TaskID:    resp.TaskID,
            MissionID: resp.MissionID,
            Status:    resp.Status,
        }, nil
    }

    // 2. ACC 不可用时只进入 queue-only/只读降级，不绕过授权执行高危任务。
    return nil, fmt.Errorf("acc-go unavailable: queue-only fallback required: %w", err)
}
```

> 只有在 ZAG、身份链、幂等、审计和安全合同全部通过 M0/M1 门禁后，才可以增加显式的 direct-ZAG fallback；该路径也不能跳过对象授权、审批或 reconciliation。

---

## 4. Pod Handler（v3 新增）

```go
// backend/internal/fleetbridge/pod.go
type PodHandler struct {
    zag *zag.Client
}

func (h *PodHandler) List(c echo.Context) error {
    claims, _ := identity.FromContext(c.Request().Context())
    
    pods, err := h.zag.ListPods(c.Request().Context(), zag.ListPodsRequest{
        FleetID: claims.WorkspaceID,
        Status:  c.QueryParam("status"),
    })
    if err != nil { return jsonError(c, err) }
    
    return c.JSON(200, map[string]any{
        "code": 0,
        "data": pods,
    })
}

func (h *PodHandler) Get(c echo.Context) error {
    podID := c.Param("podId")
    pod, err := h.zag.GetPod(c.Request().Context(), podID)
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, map[string]any{"code": 0, "data": pod})
}

func (h *PodHandler) Control(c echo.Context) error {
    podID := c.Param("podId")
    var body struct {
        Kind   string `json:"kind"`
        Reason string `json:"reason"`
    }
    if err := c.Bind(&body); err != nil {
        return c.JSON(400, errorResp("bad_request"))
    }
    
    // 透传到 ZAG
    if err := h.zag.ControlPod(c.Request().Context(), podID, zag.ControlPodRequest{
        Kind:   body.Kind,
        Reason: body.Reason,
    }); err != nil { return jsonError(c, err) }
    
    return c.JSON(200, map[string]any{"code": 0})
}
```

---

## 5. Agent Handler（v3 新增）

```go
// backend/internal/fleetbridge/agent.go
type AgentHandler struct {
    zag *zag.Client
}

func (h *AgentHandler) List(c echo.Context) error {
    agents, err := h.zag.ListAgents(c.Request().Context(), zag.ListAgentsRequest{
        FleetID: claims.WorkspaceID,
        Status:  c.QueryParam("status"),
        PodID:   c.QueryParam("podId"),
        Kind:    c.QueryParam("kind"),
    })
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, map[string]any{"code": 0, "data": agents})
}

func (h *AgentHandler) Invoke(c echo.Context) error {
    agentID := c.Param("agentId")
    var body zag.InvokeRequest
    if err := c.Bind(&body); err != nil {
        return c.JSON(400, errorResp("bad_request"))
    }
    
    result, err := h.zag.InvokeAgent(c.Request().Context(), agentID, body)
    if err != nil { return jsonError(c, err) }
    
    return c.JSON(200, map[string]any{"code": 0, "data": result})
}
```

---

## 6. IDE Handler（v3 新增）

```go
// backend/internal/fleetbridge/ide.go
type IDEHandler struct {
    zag *zag.Client
}

func (h *IDEHandler) List(c echo.Context) error {
    ides, err := h.zag.ListIDEs(c.Request().Context(), zag.ListIDEsRequest{
        FleetID: claims.WorkspaceID,
    })
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, map[string]any{"code": 0, "data": ides})
}

func (h *IDEHandler) Status(c echo.Context) error {
    name := c.Param("name")
    status, err := h.zag.GetIDEStatus(c.Request().Context(), name)
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, map[string]any{"code": 0, "data": status})
}

func (h *IDEHandler) Command(c echo.Context) error {
    name := c.Param("name")
    var body zag.IDECommand
    if err := c.Bind(&body); err != nil {
        return c.JSON(400, errorResp("bad_request"))
    }
    
    receipt, err := h.zag.ExecuteIDECommand(c.Request().Context(), name, body)
    if err != nil { return jsonError(c, err) }
    
    return c.JSON(200, map[string]any{"code": 0, "data": receipt})
}
```

---

## 7. WebSocket Bridge（v3 扩展事件类型）

```go
// backend/internal/fleetbridge/ws_bridge.go
func (b *WSBridge) SubscribeTaskEvents(ctx context.Context, fleetID, taskID string) error {
    // 1. 订阅 ZAG SSE /api/v1/tasks/:id/events
    // 2. 转换为 pocketd WS Hub 事件
    // 3. 推送 mobile
    
    // v3 新增：同时订阅 ZAG SSE /api/v1/agents/:id/events
    //       订阅 ZAG SSE /api/v1/pods/:id/events
    //       订阅 ZAG SSE /api/v1/ide/:name/events
}
```

### 7.1 事件映射（v3）

| ZAG Event | pocketd WS Hub Event |
|---|---|
| `task.update` | `task.update` |
| `task.completed` | `build.completed` |
| `task.failed` | `build.failed` |
| `agent.message` | `agent.message` |
| `agent.tool_call` | `agent.tool_call` |
| `agent.tool_result` | `agent.tool_result` |
| `agent.status` | `agent.status` |  ← v3 新增
| `session.created` | `session.created` |  ← v3 新增
| `session.message` | `session.message` |  ← v3 新增
| `permission.request` | `permission.request` |
| `permission.resolved` | `permission.resolved` |
| `pod.status` | `pod.status` |  ← v3 新增
| `ide.status` | `ide.status` |  ← v3 新增
| `control.signal` | `control.signal` |  ← v3 新增
| `cost.tick` | `cost.tick` |

---

## 8. 路由注册（v3 完整）

```go
// backend/internal/fleetbridge/api.go
func RegisterRoutes(e *echo.Echo, h *Handlers, authMW echo.MiddlewareFunc) {
    g := e.Group("/api/fleet", authMW)
    
    // Intent / Task / Build
    g.POST("/intent", h.Intent.Handle)
    g.GET("/tasks", h.Task.List)
    g.GET("/tasks/:taskId", h.Task.Get)
    g.POST("/tasks/:taskId/cancel", h.Task.Cancel)
    g.POST("/tasks/:taskId/pause", h.Task.Pause)
    g.POST("/tasks/:taskId/resume", h.Task.Resume)
    g.POST("/tasks/:taskId/retry", h.Task.Retry)
    g.GET("/builds", h.Build.List)
    g.GET("/builds/:buildId", h.Build.Get)
    g.POST("/builds/:buildId/cancel", h.Build.Cancel)
    g.POST("/builds/:buildId/follow-up", h.Build.FollowUp)
    g.GET("/builds/:buildId/permissions", h.Permission.List)
    g.POST("/builds/:buildId/permissions/:pid/reply", h.Permission.Reply)
    g.GET("/builds/:buildId/diff", h.Build.Diff)
    g.GET("/builds/:buildId/artifacts", h.Build.Artifacts)
    
    // Pod (v3 新增：透传 ZAG)
    g.GET("/pods", h.Pod.List)
    g.GET("/pods/:podId", h.Pod.Get)
    g.POST("/pods/:podId/control", h.Pod.Control)
    
    // Agent (v3 新增：透传 ZAG)
    g.GET("/agents", h.Agent.List)
    g.GET("/agents/:agentId", h.Agent.Get)
    g.POST("/agents/:agentId/invoke", h.Agent.Invoke)
    g.POST("/agents/:agentId/restart", h.Agent.Restart)
    
    // IDE (v3 新增：透传 ZAG)
    g.GET("/ide", h.IDE.List)
    g.GET("/ide/:name/status", h.IDE.Status)
    g.POST("/ide/:name/command", h.IDE.Command)
    
    // Charter / Skill / Cost / Schedule (不变)
    g.GET("/charter", h.Charter.Get)
    g.PUT("/charter", h.Charter.Put)
    g.GET("/skills", h.Skill.List)
    g.POST("/skills", h.Skill.Create)
    g.POST("/skills/import", h.Skill.Import)
    g.PUT("/skills/:id", h.Skill.Update)
    g.DELETE("/skills/:id", h.Skill.Delete)
    g.POST("/skills/:id/pin", h.Skill.Pin)
    g.GET("/cost", h.Cost.Aggregate)
    g.GET("/cost/daily", h.Cost.Daily)
    g.GET("/schedules", h.Schedule.List)
    g.POST("/schedules", h.Schedule.Create)
    g.PUT("/schedules/:id", h.Schedule.Update)
    g.DELETE("/schedules/:id", h.Schedule.Delete)
    
    // WebSocket
    g.GET("/ws", h.WS.HandleWS)
}
```

---

## 9. 配置（v3 新增）

```bash
# 目标配置（尚未实现）
POCKET_ACC_BASE_URL=https://acc.internal
POCKET_ACC_INTERNAL_SECRET=<delegated-token issuer/key reference>
POCKET_ZAG_BASE_URL=https://zag.internal
POCKET_ZAG_AUDIENCE=zagent-gateway
POCKET_ZAG_MTLS_CA=/run/secrets/zag-ca.crt
# 不使用全局 shared tenant secret 作为授权依据
POCKET_REDCLAW_BASE_URL=                         # legacy bridge only
```

---

## 10. 一句话总结

**pocketd fleetbridge v3 目标增加 `internal/zag/` 子包；在 ZAG 未实现和合同未验证前不注册真实写路由。ACC 故障只允许队列/只读降级，不得绕过控制面执行高危操作。**
