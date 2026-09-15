# 手机端本地智能体 Phase 2：原生 function-calling + Marketplace 集成

> 文档状态：现行方案（2026-09-15）
> 前置：[2026-09-15-mobile-local-agent.md](2026-09-15-mobile-local-agent.md)（MVP 已落地）

---

## 0. 摘要

Phase 2 在 MVP（提示词驱动 JSON 工具协议）基础上增加三项能力：

1. **后端原生 function-calling 透传**：`llmgateway/client.go` + `llmbff/service.go` + `server_llmbff.go` + `aiStreamRuntime` 扩展支持 OpenAI 原生 `tools` 字段与 `tool_calls` 响应；前端 `streamFn` 按模型能力自动降级回 JSON 协议。
2. **技能包接 marketplace**：Package.Kind="skill" 下载到 `Directory.Data/agent/skills/`，启动时扫描并注册进 `load_skill` 可发现清单。
3. **专家接 chat_agents 表**：`skill_refs` 列存技能 ID 列表，云端可配专家；前端启动时拉取 `/api/chat-agents`，专家选择器动态展示（内置 + 云端自定义）。

完成标准：`node --test src/localagent/__tests__` 全绿 + `vue-tsc` 0 错 + `build-mobile android dev` 通过 + **CDP 回归（真网关）**全 PASS（恢复 dev PG 后用真实 function-calling 跑完整场景）。

---

## 1. 原生 function-calling 透传（后端五处 + 前端自动降级）

### 1.1 llmgateway/client.go 扩展

添加 `Tool` / `ToolCall` 类型与 ChatRequest.Tools、ChatMessage.ToolCalls / ToolCallID：

```go
type Tool struct {
    Type     string                 `json:"type"` // "function"
    Function ToolFunction           `json:"function"`
}

type ToolFunction struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type"` // "function"
    Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}

// ChatMessage 扩展
type ChatMessage struct {
    Role       string     `json:"role"`
    Content    any        `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // assistant 携带
    ToolCallID string     `json:"tool_call_id,omitempty"` // tool role 携带
}

// ChatRequest 扩展
type ChatRequest struct {
    Model       string        `json:"model"`
    Messages    []ChatMessage `json:"messages"`
    Tools       []Tool        `json:"tools,omitempty"`
    Temperature float64       `json:"temperature,omitempty"`
    MaxTokens   int           `json:"max_tokens,omitempty"`
    Stream      bool          `json:"stream,omitempty"`
    User        string        `json:"user,omitempty"`
    WorkType    string        `json:"work_type,omitempty"`
}
```

### 1.2 llmgateway/stream.go 扩展

parseSSEStream 解析 `delta.tool_calls` 数组（增量模式，index + function.name / arguments 分帧到达）：

```go
type StreamDelta struct {
    Content          string     `json:"content"`
    ToolCalls        []ToolCall `json:"tool_calls,omitempty"` // 增量追加
    FinishReason     string     `json:"finish_reason"`
    Done             bool       `json:"done"`
    Model            string     `json:"model,omitempty"`
    PromptTokens     int        `json:"prompt_tokens,omitempty"`
    CompletionTokens int        `json:"completion_tokens,omitempty"`
    TotalTokens      int        `json:"total_tokens,omitempty"`
}
```

### 1.3 llmbff/service.go 扩展

```go
type Message struct {
    Role       Role       `json:"role"`
    Content    string     `json:"content"`
    Images     []string   `json:"images,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
    ID       string                 `json:"id"`
    Type     string                 `json:"type"` // "function"
    Function ToolCallFunc           `json:"function"`
}

type ToolCallFunc struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}

type Tool struct {
    Type     string                 `json:"type"` // "function"
    Function ToolFunction           `json:"function"`
}

type ToolFunction struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description,omitempty"`
    Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type ChatRequest struct {
    WorkspaceID string    `json:"workspace_id"`
    Model       string    `json:"model,omitempty"`
    Messages    []Message `json:"messages"`
    Tools       []Tool    `json:"tools,omitempty"`
    Temperature float64   `json:"temperature,omitempty"`
    MaxTokens   int       `json:"max_tokens,omitempty"`
    Stream      bool      `json:"stream,omitempty"`
    User        string    `json:"user,omitempty"`
    Kind        string    `json:"kind,omitempty"`
}

type Delta struct {
    Content      string     `json:"content,omitempty"`
    ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
    Done         bool       `json:"done"`
    FinishReason string     `json:"finish_reason,omitempty"`
    Usage        *Usage     `json:"usage,omitempty"`
    Model        string     `json:"model,omitempty"`
    Retry        string     `json:"retry,omitempty"`
}
```

### 1.4 server/llmbff_provider_adapters.go 映射

llmGatewayBFFProvider.Chat / Stream：`req.Tools` → `llmgateway.ChatRequest.Tools`；`resp.Choices[0].Message.ToolCalls` → `ChatResponse.ToolCalls`；流式 delta.tool_calls → llmbff.Delta.ToolCalls。

### 1.5 server/server_llmbff.go 入参

handleLLMBFFStream 请求体扩展 `Tools []Tool`；校验 tools 数量（≤20）与 schema 大小（单个 ≤16KB）。

### 1.6 前端 aiStreamRuntime 帧扩展

`frontend/src/native/aiStreamRuntime.ts`：

```typescript
export interface ChatStreamDelta {
  content?: string
  tool_calls?: Array<{
    index: number
    id?: string
    type?: 'function'
    function?: { name?: string; arguments?: string }
  }>
  done: boolean
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }
}
```

SSE 解析器透传 `tool_calls` 字段（保留 OpenAI 增量形状）。

### 1.7 前端 streamFn 自动降级

`frontend/src/localagent/llm-stream.ts` 新增能力探测与双协议分支：

```typescript
export interface LlmStreamOptions {
  sessionId?: string
  model?: string
  temperature?: number
  spawner?: ChatSpawner
  turnCounter?: { next(): number }
  /** 强制工具协议:"native"(原生 tools)/"json"(围栏 JSON);缺省自动探测。 */
  toolProtocol?: 'native' | 'json' | 'auto'
}

export function createLlmStreamFn(opts: LlmStreamOptions = {}): StreamFn {
  const protocol = opts.toolProtocol ?? 'auto'
  // auto 探测：首次调用时试探 /api/llm/models 响应的 supports_function_calling 元数据；
  // 或在首轮对话时发送 tools 并检测是否收到 tool_calls（收到则缓存"该模型可用原生"）。
  // MVP Phase2：先统一走 json 协议，待网关侧 function-calling 稳定后再启用 auto。
  return protocol === 'native' ? createNativeStreamFn(opts) : createJsonStreamFn(opts)
}
```

---

## 2. 技能包接 marketplace

### 2.1 Package.Kind="skill" 定义

已有 marketplace 表支持任意 kind；技能包元数据：

```json
{
  "id": "pkg-skill-deep-reading-v1",
  "kind": "skill",
  "name": "深度阅读",
  "description": "提取长文关键信息并生成结构化摘要",
  "publisher": "openpocket",
  "version": "1.0.0",
  "files": {
    "SKILL.md": "s3://bucket/skills/deep-reading/v1/SKILL.md"
  }
}
```

### 2.2 下载到 Filesystem

前端 marketplace store 下载逻辑（已有 downloadPackage）：

- Kind="skill" 时解压到 `Directory.Data/agent/skills/<package_id>/`
- SKILL.md 写入该目录根
- 其它附件（示例/配图）保留，供技能正文引用

### 2.3 启动扫描与注册

`frontend/src/localagent/skills.ts` 新增 `scanMarketplaceSkills()`：

```typescript
import { Filesystem, Directory } from '@capacitor/filesystem'

export async function scanMarketplaceSkills(): Promise<Skill[]> {
  const skills: Skill[] = []
  try {
    const { files } = await Filesystem.readdir({
      path: 'agent/skills',
      directory: Directory.Data,
    })
    for (const dir of files.filter((f) => f.type === 'directory')) {
      const md = await Filesystem.readFile({
        path: `agent/skills/${dir.name}/SKILL.md`,
        directory: Directory.Data,
        encoding: Encoding.UTF8,
      })
      const skill = parseSkillMarkdown(md.data)
      if (skill) skills.push(skill)
    }
  } catch {
    // 目录不存在或读取失败：返回空数组，不阻塞启动
  }
  return skills
}
```

内置技能 + marketplace 技能合并后注册到 `load_skill` 工具的发现清单。

---

## 3. 专家接 chat_agents 表

### 3.1 skill_refs 列语义

`chat_agents.skill_refs`（JSONB 数组）：存技能 ID 列表（marketplace package id 或内置 skill name）。前端加载专家时，按 skill_refs 解析对应技能并注入 system prompt 的「推荐技能」段落。

### 3.2 后端 API

已有 `/api/chat-agents`（GET /api/chat-agents?workspace_id=...）返回 Agent 列表，包含 skill_refs。

### 3.3 前端集成

`frontend/src/localagent/experts.ts` 扩展：

```typescript
export interface Expert {
  name: string
  description: string
  systemPrompt: string
  allowedTools?: string[]
  skillRefs?: string[] // 新增：推荐技能 ID 列表
}

export async function loadCloudExperts(workspaceId: string): Promise<Expert[]> {
  const resp = await fetch(`/api/chat-agents?workspace_id=${workspaceId}`)
  if (!resp.ok) return []
  const agents = await resp.json()
  return agents.map((a: any) => ({
    name: a.id,
    description: a.description,
    systemPrompt: a.system_prompt,
    skillRefs: a.skill_refs || [],
  }))
}
```

启动时合并内置专家 + 云端专家；专家选择器展示完整列表；选中专家时，其 skillRefs 对应的技能自动附加到会话（或在 composer 预勾选）。

---

## 4. 实施步骤

1. **后端 function-calling 透传**（五处同步改）：
   - llmgateway/client.go + stream.go：Tool / ToolCall 类型 + 解析
   - llmbff/service.go：同形类型 + Delta 扩展
   - server/llmbff_provider_adapters.go：映射逻辑
   - server/server_llmbff.go：入参校验
   - 单测：llmgateway_test / llmbff_test / adapters_test 覆盖 tools 路径
2. **前端 aiStreamRuntime 帧扩展** + llm-stream.ts 双协议分支（先 json 兜底，native 待网关稳定）
3. **技能包 marketplace 集成**：
   - 前端下载逻辑（kind="skill" → Directory.Data/agent/skills/）
   - skills.ts scanMarketplaceSkills() + 合并注册
   - 单测：模拟 Filesystem 读 SKILL.md
4. **专家云端集成**：
   - experts.ts loadCloudExperts() + 合并
   - LocalAgentView 启动时拉取 + 选择器展示
   - skill_refs 自动附加逻辑
5. **恢复 dev PG + 真网关 CDP 回归**：mock 网关切真实 llm-gateway，跑完整 function-calling 场景（calculate / write_file / http_fetch / task_plan 四类工具）

---

## 5. 风险与对策

| 风险 | 对策 |
|---|---|
| 网关 function-calling 不稳定 | 前端保留 json 协议兜底；auto 探测失败时降级 |
| marketplace 技能格式不合规 | parseSkillMarkdown 容错；解析失败跳过该技能、不阻塞启动 |
| skill_refs 引用的技能不存在 | 前端按 ID 查找，缺失时忽略（专家仍可用，只是推荐技能为空） |
| 云端专家 system_prompt 过长 | 前端截断（≤32KB）；后端已有 message 长度校验 |

---

## 6. 验收标准

- 单测：`node --test src/localagent/__tests__` 全绿（新增 native protocol 分支 + marketplace scan + cloud experts 加载，共 +15 用例）
- typecheck：`vue-tsc --noEmit` 0 错
- 构建：`build-mobile.mjs android dev` 通过
- **CDP 回归（真网关）**：恢复 dev PG 后，e2e/android/local-agent-cdp.py 用真实 /api/llm/stream + function-calling 跑 P2-P5 四场景全 PASS
- 集成验收：
  - 从 marketplace 安装一个技能包 → `agent/skills/` 下出现 SKILL.md → load_skill 可发现
  - 在 chat_agents 表插入一条云端专家（skill_refs=['深度阅读']）→ 专家选择器展示 → 选中后推荐技能自动勾选
  - 选择支持 function-calling 的模型（如 gpt-4o）→ 工具调用走原生 tool_calls → 时间线正确渲染工具卡
