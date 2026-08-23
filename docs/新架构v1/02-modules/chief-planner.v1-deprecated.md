# 模块设计：Chief Planner（规划器）

---

## 1. 定位

Chief 是 PocketFleet 的"领队" Agent —— **只规划、不执行**。

它把用户的一句话目标拆解成可执行的 PocketTask 列表，并把每个任务派给合适的 Agent / Pod。完成后回到"待命"状态，等待下一句指令。

参考 todos.dev Chief：写 charter → 看 charter → 拆目标 → 派活 → 监督 → 不主动醒来。

## 2. 接口

```go
package fleet

// Chief 是规划器；它本身不直接生成代码 / 命令，只产 PocketTask。
type Chief interface {
    // Plan 接收用户输入，输出 PocketTask 列表。原子操作，不下派。
    Plan(ctx context.Context, req PlanRequest) (*PlanResponse, error)

    // Sharpen 追问澄清，返回问题列表（如果不需澄清返回空）。
    Sharpen(ctx context.Context, goal string) ([]ClarifyQuestion, error)

    // Assign 给已批准的任务分配合适的 Agent + Pod（可选；可被人工覆盖）。
    Assign(ctx context.Context, taskID string) ([]Assignment, error)

    // Review 监听 build 完成事件，可触发二次 review（Architect/QA Agent）。
    Review(ctx context.Context, buildID string) (*ReviewVerdict, error)
}

type PlanRequest struct {
    FleetID    string
    UserID     string
    Goal       string
    PinnedPod  string             // 可选
    Preferences PlanPreferences   // model, maxBuilds, parallel, etc.
    Context    PlanContext        // 用户提供的代码片段、截图、文件
}

type PlanResponse struct {
    Tasks         []PocketTask
    ClarifyQs     []ClarifyQuestion   // 非空时表示 Chief 拒答，要求用户补充
    TraceID       string
    UsedModel     string
    PromptTokens  int
    OutputTokens  int
}

type ClarifyQuestion struct {
    ID    string
    Text  string
    Options []QuestionOption  // 单选/多选
    Required bool
}

type Assignment struct {
    TaskID   string
    AgentID  string
    PodID    string
    Priority int
    Reason   string   // "least loaded" / "pinned" / "specialty match"
}

type ReviewVerdict struct {
    Verdict   string  // "approve" | "request_changes" | "reject"
    Comments  string
    ActionItems []string
}
```

## 3. 工作流

```
用户输入 "给 X 增加 OAuth 登录"
   │
   ▼
Chief.Sharpen("...")  // 第一遍：是否需要澄清？
   │                  // 输出 clarify[] (例如：是否需要 refresh token? 支持哪些 provider?)
   ▼                  // 如果用户已经回答，跳过
Chief.Plan(...)       // 第二遍：拿到完整 spec → 拆 PocketTask[]
   │
   ▼
返回给 mobile / 后端
   │
   ▼  用户在 UI 上"接受 / 修改 / 拒绝"
   │
   ▼
Chief.Assign(...)     // 选 Agent + Pod
   │
   ▼
BuildEngine.dispatch
   │
   ▼
Build 完成事件
   │
   ▼
Chief.Review(...)     // 可选：拉 QA Agent 复审
   │
   ▼
合并 / 拒收 / 重做
```

## 4. Prompt 设计（精简版）

Chief 的 system prompt 主要分三段：

```text
# 1. 角色
你是 PocketFleet Chief —— 一个不做代码、只做规划的"领队"。
你的产出是 JSON 格式的 PocketTask 列表，每个任务包含 goal / boundaries / DoD。
你必须在不明确时主动澄清，而不是猜测。

# 2. Charter
{ charter.md content from kxmemory }

# 3. 能力约束
- 不超过 {maxBuilds} 个任务；
- 每个任务必须有 1 个 owner Agent；
- 必须考虑并行度（parallel <= available pods）；
- 边界（boundaries）必须可执行（"只动 backend/" 而不是 "代码要好"）。

# 4. 输出 Schema
{ PocketTask JSON Schema }
```

## 5. 与 LLM 的对接

- 默认模型：`deepseek-v4-pro`（reasoning 强），如果 Charter 写"省 token"则降级到 `deepseek-v4-flash`。
- 调用走 `internal/aigate` 的 DeepSeek provider，**流式**返回（SSE → UI 渐进显示）。
- Strict JSON mode（DeepSeek Beta）：开启，强制输出符合 PocketTask schema。
- Re-try：JSON parse 失败 → 3 次重试 → 第 4 次降级到 "请用户手填"。

## 6. 持久化

- PlanResponse → kxmemory `fleet/{id}/plan/{trace_id}`（永久）。
- PocketTask → PostgreSQL `pocket_task` 表（沿用既有迁移脚本风格）。
- Build Event 流 → kxmemory `fleet/{id}/build/{id}/events`（30 天滚动）。

## 7. 并发与限流

- 单 Fleet 同时只允许 1 个 Chief plan 进行中。
- Chief LLM 调用走 `internal/quota` 的 LLM 限速。
- Plan timeout 60s；超时返回 `chief.timeout`，允许用户"用上次的建议"。

## 8. 失败模式与降级

| 失败 | 行为 |
|---|---|
| LLM 5xx | 重试 3 次；最终失败 → 返回模板化拆分（"未指明需求，按 3 个通用角色拆"） |
| LLM 4xx（输入太长） | 截断 charter / 历史，再试 |
| JSON parse 失败 | 用 repair prompt 重试 2 次 |
| 用户长时间不批准 | 24h 后 Chief 提示"任务待批"；7 天后自动过期 |

## 9. 复用现有

- `internal/aigate/deepseek.go`（新增）—— DeepSeek provider。
- `internal/identity` —— 拿当前用户 + Fleet。
- `internal/quota` —— LLM 用量限额。
- `internal/kxmemory` —— 持久化 + audit。

## 10. 测试策略

- 单元测试：mock LLM；给定 goal → 期望 PocketTask 列表。
- 集成测试：跑通真实 DeepSeek API（少量 sample case）。
- E2E：手机发起语音 → Chief plan → 接受 → 1 个 Pod 上 Pi Agent 执行 → 完成。
- 注入测试：故意构造恶意 Charter，验证 Chief 不被劫持产生危险任务。

---

> **⚠️ DEPRECATED (2026-08-23)**：本文档是 v1 方案（自建 Chief Planner），已被 v2 方案取代。
>
> **v2 方案**：直接复用现有 `services/agent-control-center/acc-go/` 服务作为 Chief。详见：
> - [chief-as-acc.md](chief-as-acc.md)
> - [architecture-decision-records.md §ADR-001](../architecture-decision-records.md)
>
> 本文件保留作为"v1 设计思路"的参考，但不构成当前方案。
