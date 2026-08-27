# AI 对话与网关管理（2026-08-27）

本文档登记「AI 对话」功能与网关管理扩展的最终态：能力范围、配置方式、后端端点与前端入口、验证证据。

## 1. 能力范围

### 1.1 AI 对话（豆包式，路由 `/ai-chat`，底部导航「对话」Tab）

| 能力 | 说明 |
|------|------|
| 多轮会话 | 会话本地存储（`pocket:ai-chat:v1`），新建/切换/删除/自动命名 |
| 模型选择 | 顶栏模型选择器；`auto · 智能路由` 为默认（网关智能路由） |
| 流式输出 | `POST /api/llm/stream`（SSE，OpenAI delta shape），可中途停止 |
| 参数控制 | 温度（0–2）、最大输出 Token、系统提示词、默认模型 |
| 对比模式 | 同一问题并行发给多个模型，分栏比较，逐卡复制/重生成 |
| 跨模型优化 | 任一回答可「用另一模型检查并优化」（评审 + 改写为新的助手消息） |
| **多模态（图片）** | 输入框附加图片（≤4 张、单张 ≤4MB），组装 OpenAI content parts；后端校验 ≤4 张/≤6MB、仅 `https://` 与 `data:image/` |
| **语音输入** | 复用全局 STT（本地 sherpa 优先、云转写兜底），转写文本追加到输入框 |
| **按模态默认模型** | 设置面板为 文本/图像/语音/视频/嵌入 五个模态各配默认模型，可逐模态选 `auto`；会话模型为 auto 时：发图→vision 默认，纯文本→text 默认，逐层兜底到 auto |

模态目录优先来自网关 `GET /api/routing/available-models`（官方 modality 标注，需已配置 admin 节点）；未配置时按模型命名启发式推断（`inferModality`），仅影响排序/徽标，不影响正确性。

### 1.2 网关管理（llm-gateway-go 核心控制面，入口 `/gateway/:nodeId`）

在既有 供应商/凭据/模型/实时 之外新增：

| 视图 | 路由 | 数据源（上游端点） |
|------|------|--------------------|
| 模型目录 | `/gateway/:nodeId/catalog` | `GET /api/routing/available-models`（家族/版本/模态/上下文窗口/精选） |
| 路由配置 · 任务类型 | `/gateway/:nodeId/routing-config` | `GET /api/admin/work-types`、`PUT /api/admin/work-types/{key}/routes`（8 个 L1 任务类型及其模型路由） |
| 路由配置 · 默认路由 | 同上 | `GET/POST /api/admin/auto-route/defaults`、`PATCH/DELETE …/{id}`（任务类型×偏好×层级→模型） |
| 路由配置 · 策略 | 同上 | `GET /api/routing/policy`、`GET/POST /api/routing/featured`（可编辑）、`GET /api/routing/scoring-weights`、`GET /api/config/default-limits` |

**已知边界**：llm-gateway-go **没有分词/tokenizer 管理端点**（token 用量为上游响应透传字段）。用户诉求中的「分词」在网关侧重映射为任务识别（work_types）与任务默认路由，均已接入；如网关未来补充分词端点，在 `gatewayProxyRoutes` 白名单表加一行即可。

## 2. 配置

```bash
# 数据面（对话）：默认即 https://llmgo.kxpms.cn，写不写 /v1 均可（自动归一化）
POCKET_LLM_GATEWAY_API_KEY=<data_key>          # 必需（或在 App 设置→AI 模型 里保存）

# 控制面（管理页）：需在 App「网关节点」配置 admin 用户名/密码（super_admin 角色）
# 节点存储依赖 PostgreSQL + POCKET_EMAIL_MASTER_KEY
```

网关凭据只存在于 pocketd（数据 key 与 admin JWT 均不下发前端）；LLM BFF 为**动态 Provider**——每次请求按 workspace 解析配置（env 默认值 + `/api/llm-gateway/config` 运行时配置），设置页改完无需重启。

## 3. 后端变更清单

| 文件 | 内容 |
|------|------|
| `internal/llmgateway/client.go` | `normalizeBaseURL`（剥尾部 `/v1`，修复双 `/v1`）；`ListModels`；`ChatMessage.Content` 改 `any` + `ContentParts`（多模态 parts） |
| `internal/llmbff/service.go` | `Message.Images` |
| `internal/server/llmbff_provider_adapters.go` | `dynamicGatewayBFFProvider`（env+运行时配置动态解析）；images→parts 转换 |
| `internal/server/server_llmbff.go` | `/api/llm/stream` 图片校验（≤4 张/≤6MB/scheme 白名单） |
| `internal/server/server_llm_models.go` | `GET /api/llm/models`（实时模型目录） |
| `internal/server/llm_gateway_nodes_handler.go` | 白名单 +16 条（catalog/work-types/auto-route defaults/policy/featured/scoring-weights/default-limits） |
| `internal/server/llm_gateway_nodes_proxy.go` | `{wkey}`（字符串 key，charset 校验防路径注入）与 `{did}` 占位符；PATCH body 读取 |
| `cmd/pocketd/main.go` | BFF 永远接线动态 Provider；legacy 适配器回退默认网关地址 |

安全不变量：代理仍为**显式白名单**（无通用 pass-through）；写操作要求 pocket admin 角色 + 审计；`wkey` 限 `[A-Za-z0-9_-]` 挡 `..` 注入；query 走 allowedQuery/forcedQuery 白名单。

## 4. 前端变更清单

- `features/ai-chat/`（AIChatView.vue + aiChatStore.ts）：对话全功能 + 多模态 + 语音 + 模态默认模型
- `api/llm-bff.ts`：`streamChat`/`listModels`/`ChatMessage.images`
- `api/gateway.ts`：available-models / work-types / task-defaults / policy / featured / scoring-weights / default-limits 客户端
- `features/gateway/GatewayAvailableModelsView.vue`、`GatewayRoutingConfigView.vue`
- 路由 `/ai-chat`（底部导航「对话」Tab）与 `/gateway/:nodeId/{catalog,routing-config}`

## 5. 验证证据（2026-08-27）

- `go build ./...` ✅；`go test ./internal/{server,llmgateway,llmbff}` ✅（含新增 `llm_gateway_proxy_routes_test.go`、`server_llmbff_multimodal_test.go`）
- `vue-tsc --noEmit` ✅；`npm run build` ✅
- 运行时冒烟（本地起 pocketd，指向 llmgo.kxpms.cn）：
  - `GET /api/llm/models` → 200，返回完整模型目录（含 doubao/deepseek/glm/qwen/kimi…），`base_url` 归一化为 `https://llmgo.kxpms.cn` ✅
  - `POST /api/llm/stream`（占位 key）→ SSE 错误帧 `401 invalid_key`（证明链路与错误通道正确）✅
  - 多模态校验：`http://` 图片→400；>4 张→400；>6MB→400（全局 body 上限先拦，纵深防御）；合法 data:image→放行至 BFF ✅
  - 新代理路由分发：无 PG 时返回 503 registry not configured（而非 404）✅
- 正式出答案需有效 data key（上列冒烟用占位 key，仅验证链路）。
