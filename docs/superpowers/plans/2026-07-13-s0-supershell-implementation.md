# S0 终端底座 (SuperShell) — 实施计划

> **对应设计稿**: `docs/superpowers/specs/2026-07-13-personal-super-terminal-design.md` §3
> **状态**: 待执行
> **日期**: 2026-07-13
> **预估周期**: 8-10 周 (单人全职)

---

## Problem Statement

作为"一人随身公司"的 owner，我需要一个移动端底座，把 llm-gateway-go-3、Redclaw、opencode CLI、本地加密存储的能力收敛成移动端可消费的统一接口，并提供身份、同步、推送、AI 代理等横向能力。当前 opencode-pocket 虽然有 auth/vault/llmgateway/notification 等模块雏形，但它们是单租户、单协议、弱同步的，无法支撑 S1/S2/S3 的业务需求。

## Solution

在现有 pocketd BFF + Vue 前端骨架之上，构建 5 个互相通过契约通讯的子系统 (S0-A~E)，每个子系统单一职责、独立可测试。所有新表带 `workspace_id`，所有 AI 调用走统一 LLM BFF，所有本地数据走 Lobster Vault 统一抽象。

## User Stories

### S0-A: Identity Core
1. 作为 owner，我想用一个主密码解锁本地加密数据，这样我打开 App 就能看笔记
2. 作为 owner，我想创建我的默认 workspace，这样所有数据归属于"我的公司"
3. 作为 owner，我想邀请最多 3 人加入我的 workspace，这样偶尔能协作
4. 作为 owner，我想撤销某个被邀请者的访问，这样失控时能止损
5. 作为 owner，我想看到所有登录过我账号的设备列表，这样能发现异常登录
6. 作为 owner，我想在多设备间刷新 token 而不重新输密码，这样体验流畅
7. 作为 owner，我想给每个被邀请者设定 role (owner/invitee)，这样权限可控
8. 作为 owner，我想让被邀请者的访问有过期时间，这样临时协作自动收回

### S0-B: LLM BFF
9. 作为 owner，我想配置 llm-gateway-go-3 的 endpoint 和 admin token，这样移动端能调 AI
10. 作为 owner，我想在移动端发起 chat 请求并看到流式回复，这样能实时对话
11. 作为 owner，我想生成 embedding 用于本地向量检索，这样 PKM 能做 RAG
12. 作为 owner，我想查看我的 token/成本用量，这样能控制花费
13. 作为 owner，我想测试网关连通性，这样配置出错时能快速发现
14. 作为 owner，我想让所有 AI 调用走同一个 BFF 端点，这样换网关时前端无感

### S0-C: Lobster Vault
15. 作为 owner，我想把笔记/录音/图片统一存为 asset，这样不用关心底层存储
16. 作为 owner，我想在本地全文搜索所有 asset，这样能快速找到内容
17. 作为 owner，我想做向量语义搜索，这样能用"意思"而非"关键词"找笔记
18. 作为 owner，我想私密数据端到端加密、离线可写，这样地铁里也能记笔记
19. 作为 owner，我想协作数据自动同步到云端，这样多设备一致
20. 作为 owner，我想冲突时能看到 diff 并选择保留哪个版本，这样不会丢数据
21. 作为 owner，我想大文件 (录音/视频) 分块加密存储，这样不会卡 UI

### S0-D: Agent Bridge
22. 作为 owner，我想看到所有可用的远端 Agent 列表，这样知道能调度谁
23. 作为 owner，我想给一个 Agent 发任务并看流式回复，这样能远程指挥开发
24. 作为 owner，我想一个 task 拆给多个 Agent 协同，这样开发→测试→审查能流水线
25. 作为 owner，我想 Agent 产物自动回流到 Task 和 Note，这样不用手动搬
26. 作为 owner，我想 Agent 离线时收到通知，这样能及时处理

### S0-E: Notification Center
27. 作为 owner，我想前台收到实时通知 (WS)，这样开会时能看到新邮件提醒
28. 作为 owner，我想后台/锁屏收到推送 (APNs/FCM)，这样不漏紧急事件
29. 作为 owner，我想设免打扰时段，这样睡觉不被打扰
30. 作为 owner，我想按事件类型配不同通知渠道，这样紧急的响铃、普通的静默
31. 作为 owner，我想在 App 内看到通知历史，这样错过的能回看
32. 作为 owner，我想从通知直接跳转到对应业务 (邮件/任务/会议)，这样一键处理

## Implementation Decisions

### 模块映射 (复用现有 + 新增)

| 子系统 | 复用现有 | 新增/改造 |
|---|---|---|
| S0-A Identity | `backend/internal/auth`, `frontend/src/stores/auth.ts` | workspace 表 + 邀请流程 + 设备管理 + shadow_workspace |
| S0-B LLM BFF | `backend/internal/llmgateway`, `backend/internal/aigate`, `/api/llm/chat` `/api/embed` | 统一 LLMClient 抽象 + admin_token 安全持有 + usage 落表 |
| S0-C Lobster Vault | `frontend/src/native/{local-db,schema,crypto,vector,lobster-init}`, `backend/internal/{vault,notes}` | assets 统一表 + blob 分块 + sync_log + AssetSync 引擎 + sqlite-vec 集成 |
| S0-D Agent Bridge | `backend/internal/opencode`, `backend/internal/adapter/opencode_http`, `/api/opencode/dispatch`, `/api/mobile/sessions` | agents 表 + task_agents 表 + ACP 网关层 + attachSession 回填 |
| S0-E Notification | `backend/internal/notification`, `backend/internal/websocket`, `frontend/src/services/ws-bus` | notification_rules + notifications 表 + APNs/FCM sender + Notification Store |

### Schema 变更 (迁移)

**全表加 workspace_id** (增量, 对 spec §1 兼容性要点的回应):
```sql
-- 对以下表执行 (已带 workspace_id 的 notes 跳过):
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE task_session_links ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE vault_sync ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE emails ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE daily_summaries ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE opencode_sessions ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE opencode_session_history ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
ALTER TABLE llm_gateway_configs ADD COLUMN IF NOT EXISTS workspace_id TEXT DEFAULT 'default';
```
迁移走现有 `backend/internal/migration/` 机制, 每条 ALTER 包成独立 migration step, 支持回滚。

**新增表** (按 spec §3.3 的 Schema 草图, 此处不重复)。

### 关键接口契约

**LLMClient (S0-B 对外统一接口)**:
```
LLMClient.chat(req: ChatRequest): Promise<ChatResponse>
LLMClient.stream(req: ChatRequest): AsyncIterable<ChatDelta>   // SSE relay
LLMClient.embed(texts: string[]): Promise<number[][]>
LLMClient.usage(ws_id): Promise<Usage>
```
内部实现: protocol adapter (OpenAI shape → llm-gateway-go-3 的 /v1/chat/completions)。admin_token 从 `llm_gateway_profiles` 读, 永不下发前端。

**Vault (S0-C 对外统一接口)**:
```
Vault.upsert(asset: Asset): Promise<AssetId>
Vault.get(id: AssetId): Promise<Asset>
Vault.search(query: {fts?: string, vec?: number[], kinds?: AssetKind[], limit}): Promise<Asset[]>
Vault.sync(mode: SyncMode): Promise<SyncResult>
```
Asset 的 blobs 走 AES-GCM 分块加密; 元数据 + FTS + vec 走 SQLCipher。

**AgentService (S0-D 对外统一接口)**:
```
AgentService.list(ws_id): Promise<Agent[]>
AgentService.send(agentId, prompt): Promise<SessionId>
AgentService.acpCall(sessionId, method, params): Promise<Result>
AgentService.attachTask(taskId, sessionId, role): Promise<void>  // 回填 task_session_links
```

### 协议适配 (事件信封统一)

移动端 SSE 已支持 OpenCode V2 envelope (`frontend/src/api/sse.ts:97-125`, 27 个 `session.next.*` 事件)。llm-gateway-go-3 输出 OpenAI delta shape。两者需要一个 thin adapter:
- Agent Bridge 路径: V2 envelope 直通 (已是移动端原生格式)
- LLM BFF chat 路径: OpenAI delta → 包装成 `session.next.text.delta` envelope, 复用现有前端 store 解析

### 安全边界
- llm-gateway admin_token **永不下发前端**, 只存在 pocketd 的 `llm_gateway_profiles` (建议加密 at rest)
- 私密 asset 的 encryption key 由用户主密码派生 (PBKDF2/scrypt), 服务端只见密文
- 危险运营操作 (重启 Agent/停凭据) 在 pocketd 层做 RBAC 校验 + 审计
- APNs/FCM token 与 device_id 绑定, 设备注销时撤销

### 现有路由保留/改造清单

**保留不动**: `/api/instances` `/api/config/*` `/ws` `/api/app/*` `/callback/feishu` `/api/stt/transcribe` `/api/embed` `/api/llm/chat` `/api/migration*` `/api/plugin/*` `/api/vault/sync/*` `/api/notes*` `/api/email*`

**改造**:
- `/api/tasks` 三源聚合 enum 扩展 `agent` (S0-D 落地时)
- `/api/opencode/dispatch` 补 attachSession 回写 task_session_links
- `/api/mobile/sessions*` 增加 workspace_id 寻址维度 (可选新路由 `/api/agents/workspaces/{wid}/sessions`)
- `/api/llm-gateway/*` 已有 config/test/models, 补 `/usage`

**新增**:
- `/api/workspaces` `/api/workspaces/{wid}/members` `/api/workspaces/{wid}/devices`
- `/api/agents` `/api/agents/{id}/sessions`
- `/api/notifications` `/api/notifications/rules` `/api/devices/push-token`

## Testing Decisions

### 测试哲学
只测外部行为, 不测实现细节。优先用最高 seam (HTTP API 层 / 集成测试), 减少 fragile 的单元测试。

### 测试 seam

| 子系统 | 测试 seam | 工具 |
|---|---|---|
| S0-A Identity | HTTP 集成测试 (起 test server + test DB) | Go `testing` + testcontainers (Postgres) |
| S0-B LLM BFF | HTTP 集成测试, mock llm-gateway (httptest.Server 录制/回放 SSE) | Go + httptest |
| S0-C Lobster Vault | 前端: Vitest + sqlcipher 内存库; 后端: Go 集成测 sync 冲突 | Vitest + Go testcontainers |
| S0-D Agent Bridge | HTTP 集成测试, mock ACP server | Go + httptest |
| S0-E Notification | HTTP 集成测试, mock APNs/FCM sender interface | Go + interface mock |

### 现有测试 prior art
- `backend/internal/migration/prompts_test.go` — migration 测试范式
- 前端 `src` 下暂无 `__tests__` 目录 — S0 需建立前端测试基线 (Vitest + @vue/test-utils)

### 关键测试用例 (示例)
- Identity: 邀请→接受→访问资源→撤销, 全流程 HTTP 测试
- LLM BFF: 流式 chat, 验证 SSE delta 顺序 + lastSeq 续传
- Vault: 三种 sync_mode 各写一条, 验证本地/云端一致性 + 冲突 resolution
- Agent Bridge: dispatch task → 收到 session_id → 验证 task_session_links 已回填
- Notification: 配置 quiet_hours → 在静默时段发事件 → 验证不推送

## Out of Scope

- S1/S2/S3 业务模块 (各自独立 plan)
- iOS Share Extension / Audio Session 的原生插件开发 (S1/S2 阶段)
- CodeUp OAuth 流程 (S1-CodeView 阶段)
- Redclaw orchestrator 的改造 (S3-Console 阶段, 只做只读镜像 + 轻控)
- PC 端 PWA/Electron 全功能运营台 (S3 阶段)
- 银行 API / 支付网关集成 (S3 帐本 v3+)
- 多租户 SaaS 化 (明确不做)

## Further Notes

### 执行顺序 (建议)
1. **Week 1-2**: Schema 迁移 (全表加 workspace_id) + Identity Core MVP (单 workspace, 无邀请)
2. **Week 3-4**: LLM BFF (LLMClient 抽象 + 现有 aigate 收敛) + usage 落表
3. **Week 5-6**: Lobster Vault (assets 统一表 + blob 分块 + sqlite-vec + sync engine)
4. **Week 7-8**: Agent Bridge (ACP 网关 + attachSession 回填 + 前端 Agent Store)
5. **Week 9-10**: Notification Center (APNs/FCM + WS 双通道 + 前端通知中心) + 集成测试

每周末做一次 demoable 验收 (对应 spec §9 验收标准)。

### 风险触发条件
- 若 sqlite-vec 在 iOS Capacitor 下性能不达标 → 回退到纯 FTS, 向量检索延后
- 若 ACP 协议适配比预期复杂 → S0-D 先只支持 opencode HTTP dispatch, ACP 延后到 S1
- 若 workspace_id 全表迁移影响生产数据 → 灰度, 先在新表上用, 旧表 lazy 迁移

### 与现有代码的兼容承诺
- 现有 5 个空壳路由 (meetings/approvals/relations/resume/notifications) 保持 ComingSoonView, 不破坏
- 现有 notes/email/vault 模块继续工作, workspace_id 默认 'default' 透明兼容
- 现有 `/api/mobile/sessions*` 路由保留, 新 Agent 路由并行存在
