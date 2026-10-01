# 6 个 PG 测试助手会直接改生产库——已修，并加结构性护栏

日期：2026-10-02
范围：`backend/` 全部 327 个 `_test.go`

## 一句话

本仓库的惯例是**同一个 DSN 既喂服务也喂测试**。有 6 个测试文件没有任何 schema
隔离，直接在 DSN 指向的 schema 上建表、写入、**删数据**，而测试报告 `ok`。

## 触发条件

`POCKET_TEST_POSTGRES_DSN` 的 `search_path` 就是生产 schema。生产部署里这是常态，
所以「本机一边跑服务一边 `go test ./...`」就足以命中，不需要任何额外配置。

其中两个文件更糟：它们读的**根本不是** `POCKET_TEST_POSTGRES_DSN`，而是
`POCKET_POSTGRES_DSN`——服务自己的生产连接串。零开关、零提示。

## 实测影响面（对生产库直接查询得到）

| 文件 | 读哪个 DSN | 隔离 | 会做什么 |
|---|---|---|---|
| `internal/chatagent/store_test.go` | `POCKET_TEST_…` | **无** | 对**活表** `DELETE FROM chat_agents` |
| `internal/finance/pg_store_concurrent_test.go` | `…TEST_…`，**空则回落到 `POCKET_POSTGRES_DSN`** | **无** | 建表迁移 + 写 `finance_transactions` |
| `internal/finance/pg_store_conflict_test.go` | 同上 | **无** | 同上 |
| `internal/scheduledtask/maintenance_test.go` | **只有 `POCKET_POSTGRES_DSN`** | **无** | 建表 + 写 `scheduled_tasks` |
| `internal/server/scheduled_task_integration_test.go` | **只有 `POCKET_POSTGRES_DSN`** | **无** | 建表 + 跑调度器 + 写审计（2 处） |
| `internal/quota/pg_store_test.go` | `POCKET_TEST_…` | **失效** | 见下 |

生产库里 `opencode_pocket.chat_agents`（277 行）与 `opencode_pocket.finance_transactions`
两张表**正是上面这些助手建出来的**——它们已经不在测试 schema 里了。

### 具体的误删

`chatagent` 的清理语句是 `id LIKE 'custom%'`，作者的本意是覆盖 `custom`、
`custom-a/b` 这几个测试自己造的 id。实测生产库：

```
id                        | name        | is_builtin
customer-success-manager  | 客户成功经理 | 1
```

`customer-success-manager` 是**内置** agent，只因为 id 以 "custom" 开头就被
`LIKE 'custom%'` 扫进来。跑一次 `go test ./internal/chatagent/` 打到生产库就会
删掉它，测试照样 `ok`。

### `quota` 是另一种失效：看起来隔离了，其实没有

它写的是 `pgxpool.New(ctx, dsn+"&search_path="+schema)`。DSN 里**已经**有
`search_path` 时，拼接产生两个同名参数，pgx 取第一个——于是隔离静默失效，
`NewPGStore` 的建表迁移落到 DSN 指向的 schema。它 schema 名是对的，所以任何
「检查有没有隔离」的粗筛都会放行它。

## 证据：献祭 schema A/B 对照

在独立的 `pocket_test` 库里造一个 `sacrificial_prod` schema，里面放：

- 与生产完全相同的 `chat_agents` DDL（照抄 `internal/chatagent/store.go:65`）
- 2 行 canary，其中一行是 `customer-success-manager`
- 一张 `canary_marker` 证明 schema 本身还活着

然后把 `POCKET_TEST_POSTGRES_DSN` 的 `search_path` 指向它——这正是生产形态。

| 观测项 | 修复前 | 修复后 |
|---|---|---|
| `customer-success-manager` | **被删除** | 存活 |
| 往被指 schema 新建的表 | **+4**（`finance_transactions`、`scheduled_tasks`、`scheduled_task_runs`、`scheduled_task_tombstones`） | **+0** |
| 遗留的 `finance_transactions` 行 | 1 | 表本身不存在 |
| 测试自建的隔离 schema | **0 个**（完全没有隔离） | 自建并自清，残留 0 |
| 测试输出 | `ok` | `ok` |

修复前那一栏里，`chatagent` 与 `finance` 两个包都是 `ok`。**"测试通过"和
"数据被删"同时成立**——这是这类缺陷最危险的地方。

## 修法

与仓库里另外 20 个已隔离的助手（task / identity / vault / meeting / redclaw …）
保持同一套做法：自己生成 `*_test_<随机>` schema → `CREATE SCHEMA` → 把
`search_path` 覆盖式钉上去 → cleanup 只 DROP 自己生成的那一个名字。

三处「纵深防御」：cleanup 里先断言 schema 名有预期前缀，前缀不符就跳过 DROP。
这样即使将来有人把生成逻辑改回去，cleanup 也不会变成删生产库。

- `internal/chatagent/store_test.go`：新增隔离；并把 `id LIKE 'custom%'` 改成
  显式枚举 `('custom','custom-a','custom-b', …)`，从根上消掉误删内置 agent 的可能
- `internal/finance/`：新增 `pg_isolated_test.go` 提供 `newIsolatedPGPool`；
  **删掉** `testPGDSN` 里回落到 `POCKET_POSTGRES_DSN` 的兜底
- `internal/scheduledtask/maintenance_test.go`：改用 `POCKET_TEST_POSTGRES_DSN` + 隔离
- `internal/server/scheduled_task_integration_test.go`：同上，2 处
- `internal/quota/pg_store_test.go`：改成 `ParseConfig` + `RuntimeParams["search_path"]`

## 顺带暴露的：3 个休眠测试本身是坏的

上面两个文件此前**只读 `POCKET_POSTGRES_DSN`**，而 CI 只设
`POCKET_TEST_POSTGRES_DSN`——所以它们在 CI 里一直是 skip，从未真正执行过。
改成读测试 DSN 之后立刻转红，暴露出三处与现契约不符：

1. `scheduledtask` 夹具漏了 `user_id` / `workspace_id`。`InsertRun`（`store.go:422`）
   直接写结构体字段、不从 task 回填，而 `ListRuns` 按这两列过滤 ⇒
   `expected 1 run, got 0`。与陈旧判定无关。
2. `server` 集成测试的请求体用 snake_case（`schedule_kind` / `schedule_expr` /
   `timeout_sec`），而契约是 camelCase（`TaskInput`，`types.go:163`）。
   handler 开了 `DisallowUnknownFields`，直接 400。
3. 同文件的审计断言 `>= 2` **永远不可能成立**：scheduler 每次运行只写一条终态
   审计（`scheduler.go:436`），任务"创建"的审计走 server 自己的 `s.Write`，
   不经过注入的 auditor。DELETE 断言期望 204，而 handler 返回 200 +
   `{"deleted":true}`（前端 `scheduledTasksApi.remove()` 只要求非 2xx，无不一致）。

这三条都按**现契约**修正，并写明理由。修完之后 `TestScheduledTaskEndToEnd`
首次真正跑完完整闭环：创建 → 认领 → 执行（status=success）→ 审计 → WebSocket
→ 更新 → 删除。这与本会话前端那次「114 个孤儿测试从不执行」是同一类缺陷，
只是这一侧没人注意过。

## 结构性护栏

`backend/internal/server/pg_test_isolation_guard_test.go` 把结论固化成结构性质，
不再依赖人记得逐个文件检查。扫描全部 327 个 `_test.go`，三条规则：

1. 任何测试都不得读 `POCKET_POSTGRES_DSN`（生产连接串）
2. 任何打开 PG 连接的测试都必须把 `search_path` 钉到自建的 `*_test_` schema
3. 不得靠拼接 DSN 字符串（`dsn+"&search_path="`）来隔离 —— 这条正是为了堵住
   `quota` 那种"名字对、机制错"的漏网

匹配前先剥掉纯注释行，规则不会对文档里的反例触发；护栏自身跳过（它的注释里
必然含规则 3 的反例字面量）。

6 条 allowlist，每条都写明可核查的理由，**不是**"应该没事"：两个只读真实库
探针（已确认全文无任何写语句 + 双重显式开关）、一个需 build tag、一个 vendored
第三方需 `-tags=integration`。

### 护栏的负控（证明它不是永远绿）

| 负控 | 手法 | 结果 |
|---|---|---|
| A | 把 `scheduledtask` 的 env 改回 `POCKET_POSTGRES_DSN` | 规则 1 精确报错 |
| B | 新建一个无隔离的 PG 测试文件 | 规则 2 精确点名该文件 |
| C | 把 `quota` 改回拼接 DSN 串 | 规则 3 精确点名该文件 |

另外护栏在上线前就自己抓出了两个我分类不严的文件
（`internal/email/diag_kxpms_test.go`、`internal/email/spam_realdata_test.go`），
核对后确认是只读探针并按精确理由进 allowlist。

## 遗留（未做，需拍板）

- `internal/email` 与 `internal/adapter/disk` 里其余的 PG/SQLite 测试没有逐一
  人工复核；护栏只保证"开了 PG 连接就必须隔离"，不保证测试自身逻辑正确。
- 这 6 个文件此前在 CI 里是 skip 或静默写生产，因此**它们历史上的"通过"不构成
  任何回归保护**。本次之后它们才第一次在 CI 里真正执行，可能还会暴露更多
  与现契约不符的断言——那属于正常暴露，不是本次改动引入的回归。
