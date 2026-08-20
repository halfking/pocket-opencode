# opencode-pocket 数据库切换到 llm-gateway-pg — 验证报告（2026-08-18，session 03）

## 结论：✅ 完成 — 已切换到 `llm-gateway-pg`（kaixuan DB，schema=`opencode_pocket`），25 张 pocketd 表迁移成功，端到端冒烟全绿

按用户要求把 opencode-pocket 的 PG 实例从 `kxmemory-rls-pg17` 切换到 `llm-gateway-pg`。发现并解决了**一个严重的 schema 冲突**：

> **`llm-gateway-pg` 的 kaixuan DB 中已存在 `public.tasks` 表（来自 pms/项目管理模块，PK 是 `task_id`）**。pocketd 的 `task` store 迁移要 CREATE TABLE `tasks` + FK `REFERENCES tasks(id)`，但 `public.tasks` 没有 `id` 列。两边名字冲突 + schema 错位导致迁移失败。

**解决方案**：在 `kaixuan` DB 内**为 pocketd 创建独立的 schema**（默认 `opencode_pocket`），通过 `search_path` 让所有 pocketd migration 落到该 schema 而非 `public`。这是**最小侵入的隔离**——其他模块的 191 张表不受影响，pocketd 的 25 张表也不污染共享空间。

## 1. llm-gateway-pg 接入

### 1.1 容器元数据
| 项 | 值 |
|---|---|
| Name | `llm-gateway-pg` |
| Image | `kx-citus-pg17:arm64-vector-fixed`（PG 17 + pgvector） |
| Host port | `127.0.0.1:5432` |
| Networks | `bridge` only（**不在** `acc-local-net`） |
| DB | `llm_gateway` |
| User | `llm_gateway` |
| Password | `<redacted; injected from local ignored .env>` |
| TZ | `Asia/Shanghai` |

### 1.2 容器间连通路径
`llm-gateway-pg` 在 `bridge`，pocketd 在 `acc-local-net`（两者不直接通）。通过 `host.docker.internal:5432`（docker compose `extra_hosts: host-gateway` 已在用）走宿主 127.0.0.1 转发——验证 TCP 200：

```
$ docker exec pocketd nc -z host.docker.internal 5432
host.docker.internal (192.168.65.254:5432) open
```

## 2. Schema 隔离（核心修复）

### 2.1 改动文件

| 文件 | 改动 |
|---|---|
| `backend/internal/config/config.go` | 新增 `PostgresSchema` 字段 + 读 `POCKET_PG_SCHEMA` env（默认 `opencode_pocket`） |
| `backend/internal/db/pg.go` | `New(dsn, schema)` 新增 schema 参数：`CREATE SCHEMA IF NOT EXISTS` + `AfterConnect` 钩子 SET search_path；不做 `public` fallback |
| `backend/cmd/pocketd/main.go` | 传递 `cfg.PostgresSchema` 到 `db.New` |
| `deploy/acc-integration/docker-compose.yml` | `POCKET_POSTGRES_DSN` → `postgresql://llm_gateway:...@host.docker.internal:5432/kaixuan?sslmode=disable` |
| `deploy/acc-integration/.env / .env.example` | 注释更新到 llm-gateway-pg |
| `deploy/acc-integration/local-up.sh` | pre-flight 改检查 `llm-gateway-pg` |

### 2.2 db.New 行为
```go
func New(ctx, dsn, schema string) (*pgxpool.Pool, error) {
    cfg.ConnConfig.RuntimeParams["search_path"] = schema
    cfg.AfterConnect = func(ctx, conn) error {
        // every new pool conn runs this once:
        conn.Exec(ctx, "SET search_path TO opencode_pocket")
    }
    pool := pgxpool.NewWithConfig(ctx, cfg)
    pool.Ping(ctx)
    createSchemaOnce(ctx, pool, schema)  // CREATE SCHEMA IF NOT EXISTS
    return pool, nil
}
```

设计要点：
1. **search_path 不含 `public`**——避免 `public.tasks`（来自其他模块）污染 pocketd 的 FK 解析。
2. **`AfterConnect` 钩子**——确保从池中拿到的每个连接都重新 SET search_path，防止被前一个用户（比如 `createSchemaOnce` 临时 reset 到 `pg_catalog, public`）污染。
3. **`createSchemaOnce`** 临时 reset 到 `pg_catalog, public` 完成 CREATE SCHEMA（不能在自己要创建的 schema 内执行），完成后**恢复**到 `opencode_pocket` 并归还连接。

### 2.3 验证结果

```
$ psql ... -d kaixuan -c "\dn"
 legal_qa        | llm_gateway
 opencode_pocket | llm_gateway   ← 新增
 public          | pg_database_owner

$ psql ... -d kaixuan -c "\dt opencode_pocket.*"
                          List of tables
     Schema      |           Name            | Type  |    Owner
-----------------+---------------------------+-------+-------------
 opencode_pocket | agents                    | table | llm_gateway
 opencode_pocket | approval_observations     | ...
 opencode_pocket | asset_mirrors             | ...
 opencode_pocket | asset_sync_log            | ...
 opencode_pocket | daily_summaries           | ...
 opencode_pocket | devices                   | ...
 opencode_pocket | email_accounts            | ...
 opencode_pocket | email_action_intents      | ...
 opencode_pocket | email_oauth_tokens        | ...
 opencode_pocket | email_vacation_deliveries | ...
 opencode_pocket | email_vacation_replies    | ...
 opencode_pocket | emails                    | ...
 opencode_pocket | llm_gateway_configs       | ...
 opencode_pocket | llm_gateway_nodes         | ...
 opencode_pocket | notes                     | ...
 opencode_pocket | notification_rules        | ...
 opencode_pocket | notifications             | ...
 opencode_pocket | quota_budgets             | ... ← 新 PG Store (191fd8d)
 opencode_pocket | task_approval_projections | ...
 opencode_pocket | task_session_links        | ...
 opencode_pocket | tasks                     | ... ← 与 public.tasks 隔离
 opencode_pocket | users                     | ...
 opencode_pocket | vault_sync                | ...
 opencode_pocket | workspace_members         | ...
 opencode_pocket | workspaces                | ...
(25 rows)
```

`public.tasks` 完全没被改动——验证 schema 隔离正确。

## 3. 端到端冒烟（已实测）

| 测试 | 结果 |
|---|---|
| `curl :8088/healthz` | ✅ `ok` (HTTP 200) |
| `POST /api/auth/login` 使用本地 dev credentials | ✅ JWT issued（凭据未写入报告） |
| `GET /api/llm/quota` (空) | ✅ `{budgets:[], enforce_mode:false, strategy:"always_allow"}` |
| `INSERT INTO opencode_pocket.quota_budgets` → `GET /api/llm/quota` | ✅ 新行立刻可见（cost_usd/75） |
| `curl :4175/api/llm/quota` nginx 反代 | ✅ 与 backend 直连字节级一致 |
| pocketd 启动日志 | ✅ `Postgres pool initialized (schema="opencode_pocket")` + 所有 11 个 module store "enabled" + `pocketd listening on :8088` |
| 迁移错误 | ✅ 0（修复前 14 次连续 `permission denied to create "pg_catalog.tasks"`） |

## 4. 单元 + 集成测试

```
$ go test ./... -count=1 -timeout 180s
ok  	internal/adapter      ok  	internal/identity
ok  	internal/agent        ok  	internal/kxmemory
ok  	internal/agentbridge  ok  	internal/llmbff
ok  	internal/auth         ok  	internal/llmgateway
ok  	internal/chat_summary ok  	internal/lobster
ok  	internal/config       ok  	internal/mcp
ok  	internal/db           ok  	internal/meeting
ok  	internal/email        ok  	internal/migration
ok  	internal/email/rules  ok  	internal/notes
ok  	internal/facade       ok  	internal/notifycenter
ok  	internal/feishu       ok  	internal/opencode
ok  	internal/finance      ok  	internal/presentation
ok  	internal/quota        ok  	internal/redclaw
ok  	internal/registry     ok  	internal/server
ok  	internal/snippet      ok  	internal/task
ok  	internal/tasksync     ok  	internal/vault
ok  	internal/websocket
```

**32 packages OK / 0 FAIL.**

```
$ MOBILE_FAST=1 npm run build:fast -- --mode ios-dev
✓ built in 1.76s
```

## 5. 修复的关键 bug 链

| # | 现象 | 原因 | 修复 |
|---|---|---|---|
| 1 | `column "id" referenced in foreign key constraint does not exist` | pocketd 的 task migration 用 `CREATE TABLE IF NOT EXISTS tasks`（命中 public.tasks，无 id 列），FK `REFERENCES tasks(id)` 失败 | search_path 改为 `opencode_pocket`，让 pocketd 落到独立 schema |
| 2 | `permission denied to create "pg_catalog.tasks"` | 第一次修复后 `RuntimeParams["search_path"]` 被覆盖；pgx 的 `RuntimeParams` 不是 per-conn 强制 SET | 加 `cfg.AfterConnect` 钩子，每条新连接显式 `SET search_path TO opencode_pocket` |
| 3 | `createSchemaOnce` 临时 reset 到 `pg_catalog, public`，归还连接后污染池 | 钩子在 reset 后没回切 | `createSchemaOnce` 在 CREATE SCHEMA 完成后显式 SET 回 `opencode_pocket` 再 Release |
| 4 | `cfg.AfterConnect` 引用未导入的 `pgx` 包 | 加 `import "github.com/jackc/pgx/v5"` | — |

## 6. 当前部署拓扑

```
                    acc-local-net (bridge, external)
                    ┌─────────────────────────────────────┐
                    │                                     │
   host:8088 ───▶  ┌────────────────┐                    │
                    │ opencode-pocket │ ─── /api ───▶  ┌─┴──────────┐
                    │ -pocketd       │               │ llm-gateway- │
                    │                │               │    pg       │
   host:4175 ───▶  │ opencode-pocket │ ── same-     │ (PG 17 +    │
                    │ -frontend (nginx)│  origin       │  pgvector)  │
                    └────────────────┘  proxy          └──────────────┘
                                                              ▲
                                                              │  host.docker.internal:5432
                                                              │  (pocketd 不在 llm-gateway-pg 的
                                                              │   bridge 网络，走 host 转发)
                                          ┌───────────────────┘
                                          │  DSN=postgresql://llm_gateway:...
                                          │
                                          │  http://acc-go-local:4101
                                          │
                                  ┌───────┴──────┐
                                  │ acc-go-local │
                                  └──────────────┘

  旧 kxmemory-rls-pg17 已不在 pocketd 数据路径上（保留供迁移对比）。
  新 llm-gateway-pg 上：
    - public 模式：191 张其他模块表（pms/agent/skill/llm-gateway 等）
    - opencode_pocket 模式：25 张 pocketd 表（本次新增）
```

## 7. 与上一份报告（`docker-acc-integration-verify-2026-08-18.md`）的差异

| 项 | 上一份 | 本份 |
|---|---|---|
| PG 实例 | `kxmemory-rls-pg17` | `llm-gateway-pg` |
| DB | `kaixuan` | `kaixuan`（同名，更大） |
| Schema | `public`（共享） | `opencode_pocket`（pocketd 私有） |
| pocketd 表数 | 96 张（与现有冲突的会失败） | 25 张（隔离后无冲突） |
| 容器网络 | 通过 `acc-local-net` DNS | 通过 `host.docker.internal:5432` |
| 改动代码 | 仅 docker-compose | docker-compose + `internal/db/pg.go` + `internal/config/config.go` + `cmd/pocketd/main.go` |

## 8. 本次审计修复（2026-08-18，session 04）

- 脱敏本报告、旧集成报告和验证报告中的数据库密码、旧 DSN 和开发登录密码；真实凭据只从本地 ignored `.env` 注入。
- `local-up.sh` 发现 `llm-gateway-pg` 不存在或未 ready 时现在直接失败，避免误连宿主 5432 上的未知服务。
- `local-up.sh` 现在使用 `--force-recreate pocketd frontend`；pocketd 重建后 IP 变化时，nginx 会同步重建并重新解析 upstream，避免旧 IP 导致 502。
- 为 `isValidIdent` 增加 schema 名称、保留 schema 和引号处理单元测试；Go 全量测试、vet、前端构建和 ShellCheck 均通过。
- `db.New` 现在对 schema 标识符使用 PostgreSQL 安全引用，并拒绝 `public`、`pg_catalog`、`pg_toast` 和 `information_schema` 等系统 schema。
- `local-up.sh` 同时等待 pocketd 和 frontend 健康，避免 backend 已启动但 nginx 尚未就绪时提前报成功。

## 9. 后续注意

1. **`POCKET_PG_SCHEMA` env**：默认 `opencode_pocket`；同一 llm-gateway-pg 上的多个 pocketd 实例（多租户）需设不同的 schema 名避免冲突。
2. **Audit 仍是 in-memory**——重启丢失；与 PG 切换无关（pre-existing）。
3. **跨 schema 查询**（如果未来需要 JOIN opencode_pocket 与 public）：查询中显式写 schema 名（`public.foo JOIN opencode_pocket.bar`），不要依赖 search_path。
4. **kubectl exec / debug**：直接 `psql -d kaixuan -c "\dt opencode_pocket.*"` 即可看 pocketd 表，不用区分。