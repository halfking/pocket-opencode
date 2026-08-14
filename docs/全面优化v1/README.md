# OpenPocket 全面优化 v1 · 审计修正版任务计划

> 编制日期：2026-08-14 ｜ 上层方案：`ai-native-tools/docs/全面优化v1/`
> 基线：源码审计 + `docs/reclaw/RECLAW-CHANGE-REQUEST-001-platform-integration.md`
> 状态词：`CURRENT` 已由源码证明；`PARTIAL` 已有基础但未闭环；`PLANNED` 目标能力。

> API 细化：见 [API-DETAILS.md](API-DETAILS.md)，其中定义 pocketd 当前本地 API 与目标 RedClaw façade 接入、任务映射、审批、记忆和实时流迁移。  
> Mock contract test：[MOCK-CONTRACT-TEST.md](MOCK-CONTRACT-TEST.md)，定义 Pocket 作为 consumer 的契约测试用例。

## 一、定位与事实边界

OpenPocket 是平台的移动宿主 UI 与本地 ACP runtime 宿主。当前 Android/Capacitor 与 pocketd 已可运行，但**尚未通过 RedClaw 统一 façade 接入 ACC/Memora/gateway**。v1 的核心任务是先保留现有链路稳定，再将可迁移流量逐步切到经审计的 façade 与 service JWT。

明确不做：不复制 ACC 状态机、不做 Memora 权限判定、不直连 gateway Admin API、不把本地任务缓存当作平台 SSOT。

## 二、当前真实链路

| 能力 | 当前链路 | 关键配置/路径 | 状态 | v1 目标 |
| --- | --- | --- | --- | --- |
| 本地服务 | pocketd `:8088`、frontend `:4175` | `deploy/本地方案/docker-compose.local.yml` | CURRENT | 继续保持 |
| ACC 任务 | MCP tool `acc_get_tasks` → 本地 `/api/tasks` 缓存 | `POCKET_MCP_BASE_URL`, `POCKET_MCP_API_KEY` | CURRENT/PARTIAL | 经 RedClaw façade 映射 ACC canonical tasks；MCP 保留降级 |
| Memora 分类/总结 | `/v1/notes/classify`, `/v1/emails/classify`, `/v1/emails/daily-summary` | `POCKET_KXMEMORY_BASE_URL`, `POCKET_KXMEMORY_*_PATH` | CURRENT | 新增 v2 ingest/search；legacy 双栈有 sunset |
| LLM | `POCKET_LLM_GATEWAY_URL` 优先，否则 `POCKET_LLM_BASE_URL` 直连 | `cmd/pocketd/main.go` | PARTIAL | 生产禁用直连，统一 gateway |
| RedClaw bridge | 调 `/health`, `/api/chat`, `/api/knowledge/search` | `POCKET_REDCLAW_*` | STUB/PARTIAL：与 RedClaw 当前 platform-go 路由不匹配 | 先标记 mock-only；等待 façade contract 或改客户端路径 |
| 实时 | `/ws?token=...`、`/api/mobile/sessions/{id}/event?instance_id=&after=` | query token | CURRENT | 与 RedClaw façade events 明确分工与迁移 |

## 三、v1 目标功能

1. **身份切换**（PLANNED）：pocketd 作为服务端代理持有 service JWT；客户端用户 token 只进入 pocketd，不直接调用平台后端。`tenant_id` 从 token 派生，禁止客户端覆盖。
2. **RedClaw façade 客户端**（PLANNED）：实现任务、审批、通知、记忆检索、事件流五类 API 客户端。前提是 RedClaw 先提供 contract/mock。
3. **任务映射闭环**（PLANNED）：建立 `acc_task_id`、`pocket_task_id`、`redclaw_session_id`、`opencode_instance_id`、`opencode_session_id` 的映射与状态转换。
4. **移动审批**（PLANNED）：审批请求从 ACC Gate 经 RedClaw façade 投递到 Pocket；Pocket 提交 `decision/reason/candidate_decisions[]/Idempotency-Key`。
5. **Memora ingest/search**（PLANNED）：分类结果批量回流 `POST /api/v2/memories/ingest`；检索使用 MemoryRetrieval v2。当前 `/v1/*` 分类接口仅作兼容。
6. **ACP runtime 注册 ACC Registry**（PLANNED）：当前只有本地 ACP registry；需新增 register/heartbeat/unregister、能力声明、lease、离线摘除和多设备去重。
7. **LLM 出口收敛**（PLANNED）：生产 profile 中禁用直连 provider，只允许 gateway。

## 四、标准参数与数据结构

### task mapping

```yaml
task_mapping:
  acc_task_id: string        # ACC canonical SSOT
  pocket_task_id: string     # 本地缓存 ID，可为空
  redclaw_session_id: string # façade/session 关联，可为空
  opencode_instance_id: string
  opencode_session_id: string optional
  status_source: acc|pocket|redclaw
  status_version: integer
  correlation_id: string
```

### memory ingest item

```yaml
source_system: pocket
source_type: email|note|meeting|session
source_id: string
content_ref: string optional
content_preview: string optional
classification: object
tags: string[]
scope_chain:
  project_id: string optional
  task_id: string optional
idempotency_key: string
```

### realtime contract

- Pocket local WS：`/ws?token=...`，只承载本地通知与桥接状态。
- Pocket mobile SSE：`/api/mobile/sessions/{session_id}/event?instance_id={id}&after={cursor}`，只承载 OpenCode session events。
- RedClaw façade events：PLANNED；应优先使用 SSE + `Last-Event-ID` 或 `after`，不得与本地 WS 语义混用。

## 五、任务计划

| ID | 任务 | 状态/优先级 | 验收 |
| --- | --- | --- | --- |
| PK-0.1 | 梳理并文档化当前四条外部链路、配置、默认端口和 fallback | P0 | README 与 `.env.example` 一致 |
| PK-0.2 | RedClaw bridge 标记 mock-only，避免把 `/api/chat`、`/api/knowledge/search` 写成已联通 | P0 | 联调文档说明不再使用 `localhost:8092` 作为默认 façade |
| PK-1.1 | façade contract client（task/approval/notification/memory/events）+ mock 合同测试 | P0 | RedClaw mock 通过 |
| PK-1.2 | 任务映射表/本地缓存升级：记录 ACC canonical ID、版本、source | P0 | 可从任一 UI 追溯 ACC task |
| PK-1.3 | service JWT 代理调用与 tenant 校验 | P0 | 平台调用不再依赖裸 user header |
| PK-1.4 | 移动审批闭环 | P0 | staging 完成一次 ACC Gate 决策并可审计 |
| PK-2.1 | Memora ingest/search v2 客户端与失败重试队列 | P0 | 分类结果 24h 内可检索；重复提交幂等 |
| PK-2.2 | ACP runtime 注册 ACC Registry | P1 | register/heartbeat/unregister + stale lease 回收 |
| PK-3.1 | 生产 profile 禁用 LLM 直连 | P0 | 抓包/网关日志证明 0 旁路 |
| PK-4.1 | iOS 立项评估 | P2 | 报告 |

## 六、依赖与风险

- 依赖 RedClaw façade 不是当前事实；必须先有 mock 与合同测试，再接真实 provider。
- Memora v2 身份收紧前，`/v1/*` 保持兼容但必须有审计 warning 与 sunset。
- 任务/审批/通知都必须以 ACC canonical 数据为来源，不得把 Pocket 本地缓存升级为 SSOT。
