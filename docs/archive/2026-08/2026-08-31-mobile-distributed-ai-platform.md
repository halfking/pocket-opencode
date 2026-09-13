# 移动分布式 AI 工作平台：上下文交接

## 1. 任务概要（Mission Summary）

在 OpenPocket 现有 workspace、scheduled task、Agent Bridge、chat agent、RedClaw 与移动端 Vue 架构上，落地“本地轻量级智能体 + 云端 ACC 分发 + 技能/智能体市场”的第一阶段骨架，并对现有代码做审计修复。

## 2. 当前方案（Approach / Plan）

- **本地执行**：通过 `backend/internal/localagent` 抽象统一的 Backend/Runtime 契约，先提供可测试的 MockBackend，后续接入 WASM、Python、ACP stdio。
- **分发编排**：通过 `backend/internal/orchestrator` 提供 LocalFirst、CloudFirst、Hybrid、LocalOnly、CloudOnly 五种策略，并支持失败兜底链与超时控制。
- **技能市场**：通过 `backend/internal/marketplace` 建立 Package/Manifest/Version、审核、发布、安装、撤销和评分接口，以及 PostgreSQL 持久化骨架。
- **智能体市场元数据**：通过 `backend/internal/chatagent/marketplace_migration.go` 为角色增加 `marketplace_id`、`skill_refs`、`publisher`、`version`、`tags` 字段。
- **兼容现有系统**：不替换现有 Agent Bridge 或 scheduled task 执行器；新模块以接口和适配器方式接入，避免破坏已有远程 OpenCode 路径。

## 3. 任务进度（Progress）

- [x] 分析现有前后端、Agent Bridge、Registry、scheduled task、RedClaw 与 chat agent 架构。
- [x] 完成 RedClaw、workbuddy、Pi Agent 等参考模式分析。
- [x] 设计技能市场、智能体市场、本地轻量级智能体和本地/云端编排方案。
- [x] 修复 Agent Bridge 成功 dispatch 后状态永久卡在 `busy` 的问题。
- [x] 修复 Agent Bridge 创建 session 失败时误标 `busy` 的问题。
- [x] 修复 Agent Bridge 空 `AgentName` 未回退到角色名称的问题。
- [x] 修复 RedClaw GET 请求不必要的 `Content-Type` 头。
- [x] 修复 AI chat 停止流式响应后未持久化消息状态的问题。
- [x] 新增 marketplace、localagent、orchestrator 模块骨架。
- [x] 为 localagent 与 orchestrator 增加单元测试。
- [x] 完成 backend build、vet、gofmt、目标包测试及 frontend vue-tsc 验证。
- [x] 提交并推送到 `main`。
- [ ] 完成 marketplace HTTP handler 与 server wiring。
- [ ] 将 chat agent marketplace 字段接入 PG/SQLite 的读写 SQL 和 API 输入输出。
- [ ] 接入 scheduled task 的 `local_agent` / `cloud_dispatch` executor。
- [ ] 实现真实 ACC HTTP 客户端、任务状态订阅/轮询和认证配置。
- [ ] 接入真实 WASM/Python/ACP stdio 后端。
- [ ] 完成移动端 Skill Market、Agent Market、Workbuddy 页面和导航入口。
- [ ] 补充 marketplace 端到端测试、迁移测试、权限/签名/依赖校验测试。

## 4. 当前状态（Current State）

- 分支：`main`
- HEAD：`9b6fe6b4547b6df3e428964292ef3a1ec4db516a`
- 远端：`origin/main`，已推送且工作区干净。
- 最新提交：`feat: 移动分布式AI工作平台骨架 — 技能市场/编排器/本地智能体 + 审计修复`
- 远端同步：`main...origin/main` 无 ahead/behind 差异。
- 代码快照状态：已提交并推送。

## 5. 下一步（Next Steps）

1. 新建 `backend/internal/server/server_marketplace.go`，按现有 `requireAuth`、workspace scope 和 `writeJSON/writeError` 约定提供：
   - `GET /api/marketplace/packages`
   - `GET /api/marketplace/packages/{id}/versions`
   - `POST /api/marketplace/submit`
   - `POST /api/marketplace/review`
   - `POST /api/marketplace/publish`
   - `POST /api/marketplace/install`
   - `POST /api/marketplace/revoke`
   - `POST /api/marketplace/rate`
2. 在 `backend/internal/server/server.go` 增加 marketplace Store 字段、初始化/注入方法和路由注册；所有请求强制从认证上下文取得 workspace，不信任 body 中的 workspace。
3. 完成 `chatagent.Store` 与 `SQLiteStore` 的 marketplace 字段迁移、scan、create、update、list，并更新对应单测。
4. 将 `orchestrator.LocalAgentDispatcher` 接到 scheduled task executor registry；保留已有 executor 行为，失败兜底由编排器负责。
5. 实现 ACC dispatcher：使用现有配置体系和 HTTP 客户端规范，避免把 API key 写入日志；增加 request ID、超时、取消、重试上限和幂等键。
6. 前端新增 `SkillMarketView.vue`、`AgentMarketView.vue`、`WorkbuddyView.vue`，先采用现有移动页面样式；在 router-mobile 和 BottomNav 的“更多”面板中接入，不直接改变主导航数量，除非有明确产品决定。
7. 增加 API client/store、加载/空态/错误态、安装确认、权限摘要和本地/云端执行策略选择。
8. 使用真实 PostgreSQL 测试 DSN 运行 chatagent/store 与 marketplace migration 测试；当前环境未提供可用的 `pocket_test` 数据库，相关失败不是本次代码编译错误。
9. 在完成 server/UI 后再次执行完整审计：权限隔离、SSRF、路径遍历、包 digest/签名校验、依赖混淆、任务重复执行、取消泄漏和敏感信息日志。

## 6. 关键事实（Key Facts）

- 当前仓库为 Git 项目，远端为 GitHub SSH origin；当前分支为 `main`。
- `go build ./...` 通过。
- `go vet ./...` 通过。
- 目标包测试通过：`orchestrator`、`localagent`、`marketplace` 编译、`agentbridge`、`redclaw`。
- `npx vue-tsc --noEmit` 通过。
- chatagent 的 PostgreSQL 集成测试需要外部测试库；在无有效 DSN/凭据的环境中会失败连接，不应通过跳过测试掩盖迁移问题。
- handoff 文档和后续提示词不得包含任何环境变量值、API key、密码、token 或个人身份信息；仅记录变量名或“已脱敏”。
- 代码中的 WASM、Python、ACP stdio 与 ACC dispatcher 仍是明确标注的骨架，不应对外宣称已经具备生产执行能力。

## 7. 阻塞 / 风险（Blockers / Risks）

- **外部数据库**：需要提供可复现的 PostgreSQL 测试实例和 `POCKET_TEST_POSTGRES_DSN`，才能验证 chatagent 与 marketplace 的真实 SQL。
- **签名与包存储**：marketplace 当前保存 digest/signature 元数据，但尚未实现 blob 存储、签名验证、依赖解析和安全解包；在这些能力完成前不得开放公共安装。
- **执行沙箱**：WASM/Python/ACP 尚未接入，不能执行不受信任的市场技能。
- **ACC API 契约**：需确认 ACC 的任务创建、状态查询、取消、幂等和错误码协议后再实现客户端。
- **兼容性**：chatagent 的主表 schema 目前由既有初始化逻辑维护，marketplace migration 必须在正式 migration runner 中注册，不能只依赖手动调用。

## 8. 建议加载的 skills（Suggested Skills）

- `session-init`：新会话开始时读取本交接文档。
- `handoff`：下一阶段结束时再次生成交接。
- Android/iOS simulator skill：实现移动 UI 后做真实 viewport 验证。
- 现有项目测试/部署相关 skill：运行 Postgres、后端和前端集成验证。

## 9. 引用（References）

- `backend/internal/marketplace/marketplace.go`
- `backend/internal/localagent/localagent.go`
- `backend/internal/localagent/localagent_test.go`
- `backend/internal/orchestrator/orchestrator.go`
- `backend/internal/orchestrator/orchestrator_test.go`
- `backend/internal/chatagent/marketplace_migration.go`
- `backend/internal/agentbridge/bridge.go`
- `backend/internal/agentbridge/bridge_test.go`
- `backend/internal/redclaw/client.go`
- `frontend/src/features/ai-chat/aiChatStore.ts`
- `frontend/src/app/router-mobile.ts`
- `frontend/src/components/BottomNav.vue`
