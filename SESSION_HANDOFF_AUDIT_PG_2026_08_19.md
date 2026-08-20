# 上下文交接 — Audit PG 持久化续做

> 本文档供「下一段会话」直接恢复上下文；并按要求给出**多子代理并行**续做方案的复制即用提示词。

## 1. 任务总览与当前状态

| 原 handoff 任务 | 状态 | 说明 |
|----------------|------|------|
| P1 验证审计导出（cursor + same-ts 消歧） | ✅ 完成 | 真实 PG（kaixuan）全绿，含 `-race` |
| P1 把 `audit_entries` DDL 移入 `internal/migration` | ⚠️ 决策：**不做** | `internal/migration` 是会话迁移包，非 schema；保持内联，符合 16 个 store 既有约定 |
| P2 并发测试 N goroutine Record+QueryRange 对 PG | ✅ 完成 | 新增 `TestPGAuditStore_ConcurrentRecordQueryRange`；**暴露并修复** Record 并发丢数据 bug |
| P2 决定 `Flush()` 语义 | ⚠️ 决策：**no-op 正确** | PG 即时持久化无缓冲可刷，已文档化 |
| P2 E2E 启动 + 命中 `/api/audit` + 确认 PG 持久化 | ✅ 完成 | 真实 `pocketd` 启动于 8099 + `kaixuan`，导出落 PG 并回读成功 |
| P3 用 PG-backed store 重跑 audit_writer email/ACC 测试 | ✅ 完成 | 新增 server PG-backed writer 测试，覆盖 claims/IP/租户、脱敏、email/tasksync/vault/LLM/ACC、截断和 system tenant；既有纯单测仍保持内存态。 |

| `PGAuditStoreWithPool` 死代码审计 | ✅ 决策 A：已删除 | 生产侧未使用（`server.go` 直接调 `NewPGAuditStore`）；wrapper 的 `(nil, nil)` 合约与主构造函数不一致，调用方已显式处理 nil pool |
| audit DDL 收敛 | ✅ 决策：保持各 store 内联 | 不移入 `internal/migration`（会话迁移包），暂不引入统一 bootstrap 包；当前 store-local、幂等 DDL 与独立初始化/隔离 schema 测试范式一致 |

## 2. HEAD 与提交

- **当前 HEAD**: `4583f80`（main，已合并本次 PG persistence audit；待推送 `origin/main`）
- 当前本地工作树应保持干净；本轮真实 PG、无 DSN、race、vet、build 已通过。
- 关键提交：
  - `aa96bba` ci: run PostgreSQL integration tests
  - `1b21466` wip(audit): checkpoint PG persistence integration
  - `4583f80` merge: complete PG persistence audit

## 3. 运行测试的命令（带/不带 DSN）

```bash
cd backend
# 带真实 PG（llm-gateway-pg 容器的 kaixuan 库；llm_gateway 库有 citus_columnar 触发器，勿用）
export POCKET_TEST_POSTGRES_DSN='postgresql://pocket_runtime:<从 docker inspect opencode-pocket-pocketd 取 POCKET_POSTGRES_DSN 的密码>@127.0.0.1:5432/kaixuan?sslmode=disable'
go test ./internal/redclaw/... ./internal/server/... -race -count=1   # 期望 ok / ok
# 不带 DSN（CI 路径，PG 测试自动 skip）
go test ./internal/redclaw/... ./internal/server/... -count=1          # 期望 ok / ok
```

> 密码获取：`docker inspect opencode-pocket-pocketd --format '{{range .Config.Env}}{{println .}}{{end}}' | grep POCKET_POSTGRES_DSN`，把其中的 host 改为 `127.0.0.1` 即成本地可达 DSN。

## 4. 工作准则（下一段会话必须遵守）

1. 在 `main` 上工作；**绝不重写他人历史 / 不 force-push**；他人提交领先时先 `git fetch` + `merge --ff-only` 或 `merge`（不 rebase 他人提交）。
2. 沿用既有测试模式（lobster/identity 的「隔离 schema + 无 DSN 则 skip」）。
3. 改动 `audit_pg_test.go` 既有用例需谨慎；新增测试放新文件。
4. 提交用 conventional-commit，推送 `origin/main` 后回报 HEAD SHA。

## 5. 本轮实施结果与后续加固项

本轮四个并行工作包已完成并合并：

- **Server P3**：新增 `backend/internal/server/audit_pg_test.go` 与 `audit_writer_pg_test.go`，以隔离 schema 验证 PG-backed writer、桥接、脱敏、租户和 system tenant；既有纯单测仍保持内存态。
- **PG store 同构审计**：修复 email ID 并发唯一性、quota 内存读写竞争、agentbridge/identity 容量检查事务化、lobster workspace revision、vault current 唯一性，并补回归测试。
- **CI**：`backend/Makefile:test-pg` 和 `.github/workflows/backend-pg.yml` 已接入；CI 缺少专用 DSN 时 fail-closed，本地无 DSN 时明确 skip。
- **决策**：删除 `PGAuditStoreWithPool`；保持各 store 自治的内联 DDL，不引入统一 bootstrap。

后续可选加固项（不阻塞本轮交付）：

1. 为旧 UnixMilli cursor 增加显式版本/单位兼容转换，并让非法 cursor 返回 400 而非静默回到第一页。
2. 为 `PGAuditStore` 数据库操作引入请求/操作级 timeout；当前实现使用 `context.Background()`。
3. 增加 PG-backed `/api/audit/logs`/`export` 的 HTTP 分页回归测试，以及 notes PG helper 的真实 schema 隔离覆盖。
4. 评估生产 PG audit 初始化失败时是否应在 production 配置下 fail-closed，而非回退内存 store。

## 6. 历史复制提示词（已完成；仅供追溯）

> 以下提示词记录本轮并行实施的原始分工，不应作为新的待办直接重复执行。

---

### 🔹 Agent-A 提示词（P3：audit_writer 测试 PG 化）

```
你在仓库 /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket 工作（Go 模块在 backend/，先 cd backend）。

目标：把 internal/server 的 audit_writer 单元测试改成「可选 PG-backed」，
在真实 PostgreSQL 上验证审计写入，收口 P3 任务。

背景：
- auditStore 接口由 redclaw.AuditStore（内存）与 redclaw.PGAuditStore（PG）共同实现，server 层通过 Server.auditStore 字段持有。
- redclaw 已有 PG 测试范式：newTestPGAuditStore(t) 在无 POCKET_TEST_POSTGRES_DSN 时 t.Skip，有则从 DSN 建隔离 schema 并在 cleanup 中 DROP SCHEMA CASCADE。
- 请勿改 internal/redclaw/audit_pg_test.go 的既有用例；新测试放新文件。

步骤：
1. 在 internal/server 提供一个类似的测试 helper（如 newTestPGAuditStore），复用 redclaw.NewPGAuditStore + 隔离 schema 模式。
2. 为现有 audit_writer 测试（audit_writer_test.go、audit_writer_email_test.go、audit_writer_tasksync_test.go、audit_writer_vault_test.go、audit_model_calls_test.go、integration_acc_post_test.go 等）增加「当 DSN 存在时注入 PGAuditStore」的分支，使同一断言在内存与 PG 两种 backend 下都跑。
3. 获取 DSN：docker inspect opencode-pocket-pocketd 的 POCKET_POSTGRES_DSN，host 改 127.0.0.1，库用 kaixuan（不要用 llm_gateway 库，它有 citus_columnar 触发器会导致建表失败）。
4. 运行：
   export POCKET_TEST_POSTGRES_DSN='postgresql://pocket_runtime:<密码>@127.0.0.1:5432/kaixuan?sslmode=disable'
   go test ./internal/server/... -run 'Audit|ACC|Email|Vault|Model' -race -count=1
   以及不带 DSN 的基线（应 skip PG 部分且全绿）。
5. 修复过程中发现的任何真实 bug（如 audit_writer 对 store 的假设不成立），先写失败测试再修，并在汇报中说明。

准则：在 main 上工作，不重写他人历史、不 force-push；遵循 conventional-commit；提交并推 origin/main 后回报 HEAD SHA 与测试结果。
```

---

### 🔹 Agent-B 提示词（同构并发/精度审计：其余 PG store）

```
你在仓库 /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket 工作（Go 模块在 backend/）。

目标：对除 redclaw 外的 PG-backed store 做「同构缺陷」审计与修复。
已知 redclaw 刚修了两个 P0 缺陷，请检查其他 store 是否有同类问题：
  (a) 并发 Record/Insert 时用 time.Now().UnixNano()（或其派生）生成主键 ID，
      导致并发写入碰撞、ON CONFLICT DO NOTHING 静默丢数据（redclaw 的 bug）。
  (b) 分页/游标用毫秒精度编码时间戳，导致跨页重复计数或同毫秒消歧失败（redclaw 的 bug）。
  (c) 共享可变状态缺少 mutex / 读写锁保护（数据竞争）。

审计范围（重点）：
  backend/internal/identity, lobster, quota, task, email, vault, notes, opencode
  （以及相关 *store.go 中的 migrate / Record / Insert / QueryRange / 分页游标）。

步骤：
1. 逐个 store 静态审查上述 (a)(b)(c) 三类模式，定位问题函数与行号。
2. 对每个确认的缺陷：先写失败的单测（内存态或 PG 态，沿用该 store 既有测试范式；PG 测试用隔离 schema + 无 DSN 则 skip），再修复。
3. 用真实 PG 验证：(密码取自 docker inspect opencode-pocket-pocketd 的 POCKET_POSTGRES_DSN，host 改 127.0.0.1，库用 kaixuan)
   export POCKET_TEST_POSTGRES_DSN='postgresql://pocket_runtime:<密码>@127.0.0.1:5432/kaixuan?sslmode=disable'
   go test ./internal/<pkg>/... -race -count=1
4. 产出审计清单：每个 store 的发现、是否修复、测试覆盖。

准则：main 上工作，不重写他人历史、不 force-push；不改动无关代码；
遵循 conventional-commit（fix(<pkg>): ...）；提交推 origin/main 后回报 HEAD SHA、受影响文件、测试结果。
```

---

### 🔹 Agent-C 提示词（CI：PG 集成测试接入）

```
你在仓库 /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket 工作（Go 模块在 backend/）。

目标：让 PG 集成测试进入 CI，使 audit（及后续）的 PG 测试在流水线中自动运行。

背景：
- 现有 PG 测试约定：设 POCKET_TEST_POSTGRES_DSN 才跑，否则 t.Skip；每个测试建隔离 schema、cleanup 中 DROP SCHEMA CASCADE。
- 本地 PG 为 docker 容器 llm-gateway-pg，库 kaixuan（host 映射 127.0.0.1:5432），密码取 docker inspect opencode-pocket-pocketd 的 POCKET_POSTGRES_DSN。llm_gateway 库有 citus_columnar 触发器，勿用。
- 仓库已有 .github/ 与 Makefile（如有）等 CI 配置模式，请先勘察再对齐。

步骤：
1. 在 backend/Makefile（或根 Makefile）新增目标，如：
   test-pg: export POCKET_TEST_POSTGRES_DSN=$(PG_DSN)
            go test ./internal/redclaw/... ./internal/server/... -race -count=1
   并说明 PG_DSN 的来源。
2. 新增 GitHub Actions workflow（对齐 .github/workflows 现有风格），job 中：
   - 启动/复用 PG（kaixuan，或 ephemeral docker postgres 容器）；
   - 注入 POCKET_TEST_POSTGRES_DSN；
   - 运行上述 go test（带与不带 -race 至少各一次，或仅 -race）。
3. 在 CI 中验证该 workflow 能本地 dry-run（如 act）或至少在本地用真实 DSN 跑通对应命令。
4. 不要破坏既有 CI；如改动 workflow 需说明影响范围。

准则：main 上工作，不重写他人历史、不 force-push；遵循 conventional-commit（ci: ...）；
提交推 origin/main 后回报 HEAD SHA 与 workflow/Makefile 改动。
```

---



## 7. 决策记录（2026-08-19）

### `PGAuditStoreWithPool`：选择 A，删除

`PGAuditStoreWithPool` 只被 `TestPGAuditStore_WithPoolNil` 引用；生产构造路径在
`internal/server/server.go` 已先检查 `pool != nil`，然后直接调用 `NewPGAuditStore`。
因此 wrapper 没有调用方，也额外定义了主构造函数不具备的 `(nil, nil)` 成功语义。删除
wrapper 与仅覆盖它的测试，保留调用方现有的内存 store 回退逻辑。

### audit DDL：不收敛到统一 bootstrap 包

不将 `audit_entries` 放入 `internal/migration`：该包只负责跨主机会话迁移，不拥有数据库
schema。也暂不新增 `internal/db/schema` 一类全局 bootstrap：现有多个 PG store 都在构造时
执行自己拥有表的幂等 DDL，统一注册需要跨包依赖、全局执行顺序和错误传播约定，并会扩大
每个 store 的启动和测试影响范围。`audit_entries` 继续由 `PGAuditStore.migrate` 管理，保持
每个集成测试创建独立 schema、缺失 DSN 时 skip 的现有范式。待需要版本化迁移、跨表原子
变更或集中运维 bootstrap 时，再以单独的 schema migration 设计统一演进所有 store，而非
仅迁移 audit DDL。
