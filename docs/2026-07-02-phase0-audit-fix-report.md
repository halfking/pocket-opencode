# 📋 Phase 0 审计修复报告

**日期**: 2026-07-02
**范围**: Phase 0（审计修复 + 基础设施闭环）
**验证**: `go build ./...` ✅ / `go vet ./...` ✅ / `go test ./internal/server -run TestHealthz` ✅ / `vite build` ✅

---

## 12 个审计问题闭环情况

### 🔴 阻塞项（3 个）

#### #1 0 路由接入 server.go / 0 store 在 main.go 构造 — ✅ 已闭环
- `server.go:Handler()` 注册 **12 个新路由**：`/api/auth/login`、`/api/notes`(+子树)、`/api/email/accounts`(+子树)、`/api/email/summaries`(+子树)、`/api/emails`(+`/sync`+子树)、`/api/vault/sync/`(子树)、`/api/stt/transcribe`
- `main.go` 构造：PG 连接池 → 4 个 store（task/notes/email/vault）→ STT transcriber（条件）→ ACC MCP client（条件）→ 全部传入 `server.New`
- handler 骨架在 `internal/server/server_assistant.go`，每个 handler 做 store CRUD 或返回明确的 Phase 2/3 提示

#### #2 kxmemory API 契约文档缺失 — ⏳ 留 Phase 1
- Phase 0 不处理（Phase 1 专项产出）。`server_assistant.go` 的 TODO 注释明确标注了每个 kxmemory 调用点。

#### #3 Auth 设计缺失 — ✅ 已闭环（基础）
- `config.go` 增加 `JWTSecret`（`POCKET_JWT_SECRET`）
- `/api/auth/login` handler 落地（`handleAuthLogin`），目前保留 admin/admin 兼容，TODO 标注接入用户表 + JWT 签发
- 前端 `LoginView.vue` 改造：调用真实 `/api/auth/login`，写入 `useAuthStore`（`setAuth(token,user)`），404 时回退 legacy localStorage 兼容
- `api/http.ts` 自动注入 `Authorization: Bearer`（已有）

### 🟠 高优先级（4 个）

#### #4 BottomNav 死链 `/meetings` — ✅ 已闭环
- 新增 `features/common/ComingSoonView.vue` 占位组件
- 路由注册 `/meetings` → ComingSoonView（props 传 icon/title/desc/phase）
- 不再死链，显示"Phase 6A 开发中"

#### #5 会议/声纹模块无设计文档 — ⏳ 留 Phase 6A
- Phase 0 不处理。占位路由已避免死链。

#### #6 聊天聚合模块 0% — ⏳ 留 Phase 6B（仅设计）
- Phase 0 不处理，按决策本轮仅补设计文档。

#### #7 App.vue 未包裹 AppLayout — ⚠️ 渐进迁移
- 决策：**不强制现在改造 App.vue**。原因：6 个旧 view 各有完整的 top-bar + bottom-nav 标记，强行全局包裹 AppLayout 会双重渲染导航，破坏现有页面。3 个新 view（NoteListView/EmailInboxView/VaultListView）已自带 AppLayout。改造 App.vue 留待旧 view 逐个迁移时一并做。功能不受影响。

### 🟡 中优先级（5 个）

#### #8 文档契约漂移（8 处）— ✅ 已闭环
- 主方案目录树：修正 `email/classifier.go`/`summarizer.go` 归属（迁移到 kxmemory，pocketd 不实现）
- 主方案目录树：新增 `internal/db/`、`internal/stt/`、`meeting/model.go`
- 主方案数据层：标注"Phase 0 起 SQLite→PostgreSQL"
- 邮箱文档：`pocketd SQLite` → `pocketd PostgreSQL`，DDL 确认为 PG 方言（`CHECK`/partial index 原生支持，`summary_date DATE` 在 PG 语境正确）
- 密码箱文档：vault 路由明确为子树 `/api/vault/sync/` + `/api/vault/sync/latest`，并说明 Go ServeMux 子树匹配机制
- 密码箱文档：CRDT 承诺对齐实现（见 #9）

#### #9 密码箱 CRDT 承诺与实现不符 — ✅ 已闭环
- `vault/store.go` schema 增加 `is_current BOOLEAN` 标记 + `ListVersions()` 方法
- `PutLatest()` 改为事务：先把旧版本 `is_current=FALSE`，再插入新当前版本（UPSERT 防重放）——保留历史版本，支持冲突双版本展示
- 新增 `vault/model.go` 的 `Version` 类型

#### #10 STT audioPath 是 blob URL — ⏳ 留 Phase 3
- Phase 0 不处理（录音持久化属于 Phase 3）。`handleSttTranscribe` 返回明确"Phase 3 audio file handling"提示。

#### #11 cap-sherpa/cap-keystore 自研插件风险 — ⏳ 留 Phase 3/4
- Phase 0 不处理。STT 用 Groq 云端先跑通（Phase 3），密码箱原生插件在 Phase 4。

#### #12 email classifier.go/summarizer.go 主方案承诺 vs 代码迁移 — ✅ 已闭环
- 见 #8，主方案目录树已修正说明。

---

## 数据层迁移（SQLite → PostgreSQL）— 核心交付

| 文件 | 变更 |
|------|------|
| `internal/db/pg.go`（新） | 统一 `*pgxpool.Pool` 连接池，`db.New(ctx, dsn)` |
| `internal/config/config.go` | 增加 `PostgresDSN`（`POCKET_POSTGRES_DSN`/`DATABASE_URL`）|
| `internal/task/store.go` | SQLite→PG：`?`→`$N`、`INSERT OR REPLACE`→`ON CONFLICT`、`INTEGER`→`BIGINT`、新增 `source` 列 |
| `internal/task/task.go` | `Task` 增加 `Source` 字段（Phase 5 ACC 整合用）|
| `internal/notes/store.go` | 同上 PG 改写 + `scanNotes` 抽取 |
| `internal/email/store.go` | PG 改写 + `auth_type CHECK`/`importance` + partial index `WHERE is_read = FALSE` |
| `internal/vault/store.go` | PG 改写 + 多版本（`is_current`）+ `ListVersions` |
| `cmd/pocketd/main.go` | 构造池 → 4 store → transcriber → MCP client，缺 DSN 直接 fatal |

依赖：`github.com/jackc/pgx/v5`（+ puddle、pgservicefile 等传递依赖）

---

## 仍待办（按计划阶段）

| 项 | 阶段 |
|----|------|
| kxmemory API 契约文档 + PG 版 8 表迁移 SQL | Phase 1 |
| App.vue 全局包裹 AppLayout（旧 view 渐进迁移）| Phase 1+ |
| JWT 用户表 + 真实登录（替换 admin/admin 兼容）| Phase 1+ |
| STT 录音文件持久化 | Phase 3 |
| cap-sherpa / cap-keystore 原生插件 | Phase 3/4 |
| 会议/声纹模块设计 + 实现 | Phase 6A |
| 聊天聚合设计文档 | Phase 6B |

---

## Phase 0 新增/修改文件清单

**后端**（新增 4、修改 8）
- 新：`internal/db/pg.go`、`internal/server/server_assistant.go`、`internal/vault/model.go`、`internal/stt/transcribe.go`（Phase 0 前）
- 改：`internal/config/config.go`、`internal/task/{store,task}.go`、`internal/notes/store.go`、`internal/email/store.go`、`internal/vault/store.go`、`internal/server/server.go`、`cmd/pocketd/main.go`、`internal/server/server_test.go`

**前端**（新增 1、修改 2）
- 新：`features/common/ComingSoonView.vue`
- 改：`features/auth/LoginView.vue`（接 auth store + 真实 login API）、`app/router-mobile.ts`（+`/meetings` 占位 + ComingSoonView）

**文档**（修改 5）
- `docs/2026-07-02-android-personal-assistant-plan.md`（目录树 + 数据层）
- `docs/2026-07-02-password-vault-design.md`（vault 路由子树说明）
- `docs/2026-07-02-email-assistant-design.md`（SQLite→PG 标注）
- `kxmemory/docs/2026-07-01-voice-notion-app-plan.md`（+4 份）加 SUPERSEDED 标记
