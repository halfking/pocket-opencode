# 审计与修复记录：跨 workspace 隔离、SMTP、OAuth、邮件持久化

日期：2026-08-08
范围：`07ebcb4..06bdf44`（feat/pocket-foldable-redclaw-integration → main）
提交：`e5777fc`、`e127af9`（merge）、`06bdf44`

## 1. 结论

发现并修复 9 个问题，其中 2 个是「功能从未生效」级别的缺陷：

| 严重度 | 问题 | 状态 |
|---|---|---|
| P0 | `InsertEmail` 列/占位符不匹配 + `created_at` 缺失 → 邮件永远无法落库 | 已修 `06bdf44` |
| P0 | OAuth 授权 URL `client_id` 恒为空 → 邮箱 OAuth 绑定永远失败 | 已修 `e5777fc` |
| P1 | notes `GET /api/notes` 跨 workspace 可读；`POST` 可写入任意 workspace | 已修 `e5777fc` |
| P1 | LLM gateway 把租户 APIKey 推给运维共享实例 → 跨租户泄露与互相覆盖 | 已修 `e5777fc` |
| P1 | `daily_summaries` 唯一约束缺 workspace → 多 workspace 摘要互相覆盖 | 已修 `06bdf44` |
| P1 | scheduler 全链路无作用域（账户列表 / 按日邮件 / 摘要 / OAuth revoke） | 已修 `06bdf44` |
| P1 | SMTP 探测无阶段超时 → 静默服务可长期占住 HTTP handler | 已修 `06bdf44` |
| P2 | 事件流 `StreamStatus` 复合键回归 → 恒返回未找到 | 已修 `e5777fc` |
| P2 | gateway `SaveConfig` 并发可产生多个 active row | 已修 `06bdf44` |

附带：SMTP 配置 API 闭环、`POCKET_OPENCODE_CONFIG_TOKEN` 文档化、移除两处恒真断言。

## 2. 关键缺陷分析

### 2.1 `InsertEmail` 从未成功（P0）

`backend/internal/email/store.go`

两个独立缺陷叠加，导致该语句每次调用都被 Postgres 拒绝：

1. 18 个目标列对应 19 个占位符 → `INSERT has more expressions than target columns (42601)`
2. `created_at BIGINT NOT NULL` 无默认值且从不写入 → `null value in column "created_at" violates not-null constraint (23502)`

含义：IMAP 抓取链路无法持久化任何邮件。之前未被发现，因为 email 包没有任何触达真实数据库的测试——单元测试全部只覆盖纯函数。

发现方式：为验证 workspace 隔离而新增 Postgres 集成测试，`InsertEmail` 立即失败。这说明该缺陷只能靠真实数据库暴露。

### 2.2 OAuth `client_id` 恒为空（P0）

`backend/internal/email/oauth.go:49`

```go
q.Set("client_id", "") // 调用方填充
```

注释声称由调用方填充，但 `BuildAuthURL` 没有 `clientID` 参数，调用方也无从填充。handler 确实接收了 `body.ClientID` 并存入 pending entry，却从未传给 URL 构造。Google/Microsoft 会直接拒绝该授权请求。

修复：`BuildAuthURL` 增加 `clientID` 参数并对空值 fail loud，而不是生成一个注定失败的 URL。

### 2.3 共享实例凭据泄露（P1）

`backend/internal/server/llm_gateway_handler.go`

`Registry.ListInstancesForWorkspace` 按设计同时返回 `WorkspaceID == ""` 的运维共享实例（静态配置与网络发现产生的实例都属于此类）。`pushConfigToOpenCode` 遍历该列表并推送当前租户的 `APIKey`，因此：

- 每个租户的保存操作都会覆盖同一个共享进程的配置；
- 该租户的 APIKey 因此对所有租户生效。

修复：推送目标限制为 `inst.WorkspaceID == workspaceID`。共享实例的全局配置语义需要单独设计，不能由任一租户的保存隐式决定。

### 2.4 `daily_summaries` 唯一约束（P1）

原始 schema 为 `UNIQUE(user_id, summary_date)`。同一用户属于两个 workspace 时，第二次写入通过 `ON CONFLICT` 覆盖第一次。

迁移顺序（不可颠倒）：

1. 先按 `(user_id, workspace_id, summary_date)` 去重历史行，`ctid` 作为 tiebreaker——`created_at` 相同时仍能保证每组恰好留一行；
2. 再 `DROP CONSTRAINT IF EXISTS daily_summaries_user_id_summary_date_key`；
3. 最后创建 `idx_daily_summaries_user_ws_date` 唯一索引。

若顺序颠倒，唯一索引会在历史重复数据上创建失败。

### 2.5 SMTP 阶段超时（P1）

`net/smtp` 通过 `textproto.Conn` 驱动 `Hello`/`StartTLS`/`Auth`/`Quit`，且不接受 `context`。因此 `net.Dialer.Timeout` 只覆盖建连阶段：服务器接受 TCP 连接后在 banner 阶段静默挂住，HTTP handler 会被无限期占用。

修复：用一个绝对 socket deadline 覆盖全部阶段（含 `Quit`）。超时值提为 `var` 以便测试注入，生产代码不修改它们。

### 2.6 gateway active row 并发（P2）

`SaveConfig` 采用「先 UPDATE 置 false，再 INSERT 置 true」。两个并发事务各自只能看到并停用自己可见的行，随后都插入 active row，形成多行 active；`LoadConfig` 在 `created_at` 相同时会任意挑选。

修复：`(workspace_id) WHERE is_active` 部分唯一索引 + 事务级 `pg_advisory_xact_lock`。advisory lock 是进程级的，多个 pocketd 副本竞争同一 workspace 时同样有效。

## 3. 验证

| 项目 | 结果 |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./...`（带 Postgres） | exit 0，无 FAIL |
| `go test -race`（email/server/opencode/notes/registry） | exit 0，无 DATA RACE |
| 前端 `npm run typecheck` + `build` | exit 0 |

Postgres 集成测试用一次性容器执行（`postgres:17-alpine`，端口 55432，测试后删除），未触碰本机 5432 上运行中的 `llm-gateway-pg`。

新增测试：
- `backend/internal/email/store_workspace_test.go` — 摘要跨 workspace 隔离、幂等、账户 workspace 回填、revoke 归属校验、按日邮件过滤、迁移后约束状态
- `backend/internal/opencode/event_stream_workspace_test.go` — 外部 workspace 订阅拒绝（且不建上游连接）、共享实例事件不串租户、`StreamStatus` 复合键
- `backend/internal/server/llm_gateway_shared_instance_test.go` — 共享实例与外部 workspace 实例都不作为推送目标
- `backend/internal/email/oauth_authurl_test.go` — 授权 URL 携带真实 `client_id`、空值报错
- `backend/internal/server/smtp_probe_test.go` — 内网目标拒绝、静默服务超时

## 4. 过程中的两次自我纠正

1. **首次合并后验证误报通过。** 验证脚本用了容错分隔符，`MERGED_BUILD_OK` 等标记在编译实际失败时仍被打印。发现后改为显式捕获退出码重跑，并用干净 worktree 复核。
2. **误判编译失败的归因。** 起初认为合并破坏了构建，实际是工作区里未跟踪的 gateway-nodes 开发中文件引用了当时尚不存在的 `Server` 字段。用 `git worktree` 隔离验证合并提交本身，确认其编译通过。

另外确认：`origin/main` 在本次合并**之前**就已编译不过（其 `server.go` 引用了只存在于功能分支的 `handleEmailVacations`、`ListInstancesForWorkspace` 等）。本次合并把实现带入后修复了该状态。

## 5. 仍未闭环

- **邮件规则引擎**：`backend/internal/email/rules/engine.go` 有单元测试，但自动归类与自动回复的端到端链路未验证。
- **SMTP 实际发信**：只有探测（`/test-smtp`），发信与假期自动回复未实现。假期回复表仅存配置。
- **前端 SMTP 编辑态**：后端 `PUT /api/email/accounts/{id}` 已接受 `smtpHost`/`smtpPort`/`smtpPassword`，`EmailAccountSetup.vue` 尚未提供对应输入与保存/测试交互。
- **`InsertEmail` 修复后的抓取链路**：修复已由集成测试覆盖插入本身，但完整 IMAP 抓取→分类→摘要链路未在真实邮箱上跑通。
- **真实 OAuth 授权**：`client_id` 修复未经 Google/Microsoft 实际授权流验证。
- **共享实例配置语义**：目前共享实例被排除在租户推送之外，但「共享实例该由谁配置」没有设计与实现。
- **`created_at` 语义**：`InsertEmail` 现在写入 `time.Now()`（入库时间）。若期望语义是邮件原始时间，应改为复用 `e.Date`。
