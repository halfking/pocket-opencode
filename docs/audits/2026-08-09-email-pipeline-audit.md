# 审计与修复记录：前端 SMTP 编辑态、IMAP 抓取链路首次跑通

日期：2026-08-09
范围：`a32d2c4..HEAD`（承接 2026-08-08 审计的 §5 待办 1、2、3）
前序文档：`docs/audits/2026-08-08-cross-workspace-audit.md`

## 1. 结论

本轮的起点是补前端 SMTP 编辑态，落点却是**整条 IMAP 抓取链路从未跑通过**——上一轮修掉 `InsertEmail`
之后，这条链路仍然在更早的位置 panic，而且那个 panic 会带走整个进程。

发现并修复 8 个问题：

| 严重度 | 问题 | 位置 |
|---|---|---|
| P0 | `client.Fetch(&uidSet)` 传指针 → `NumSetKind` panic，任何抓到新邮件的 Sync 必崩 | `email/fetcher.go` |
| P0 | scheduler 裸 goroutine 无 `recover()` → 上述 panic 直接杀进程 | `email/scheduler.go` |
| P1 | `GetAccountByID` 不选 `workspace_id` → 所有邮件一律以 `'default'` 落库 | `email/store.go` |
| P1 | `GetSyncStatusScoped` 用反范式列关联 → 非 default workspace 未读数恒为 0 | `email/store.go` |
| P1 | 云端账户从不出现在列表里 → 无编辑入口，SMTP 编辑态对目标账户不可达 | `EmailAccountSetup.vue` |
| P2 | `ListAccountsScoped` 不选 `smtp_host/smtp_port` → 编辑态无法预填 | `email/store.go` |
| P2 | `createEmailAccount` 完全忽略 SMTP 字段 → 新建时填了会被静默丢弃 | `server_assistant.go` |
| P2 | `lastSyncedAt` 类型契约错误（unix 秒当 ISO 字符串解析，恒为 `NaN`） | `api/email.ts` |

附带：`GetAccountByID` 中三个声明了却从不 Scan 的 SMTP 变量（死代码）、`created_at` 语义定论、
`UpsertSMTPSettingsScoped` 过时注释。

## 2. 关键缺陷分析

### 2.1 `Fetch` 传指针导致 panic（P0）

`backend/internal/email/fetcher.go`

```go
messages, err := client.Fetch(&uidSet, fetchOpts).Collect()
```

`imapwire.NumSetKind` 对 `imap.NumSet` 做类型 switch，只认 `imap.SeqSet` / `imap.UIDSet`
**值类型**，其余一律走 default 分支：

```go
default:
    panic("imap: invalid NumSet type")
```

`*imap.UIDSet` 落到 default。含义：只要 UID search 搜到了新邮件，`Sync` 必然 panic —— 这比
上一轮修的 `InsertEmail` 更早，所以即使 `InsertEmail` 修好了，抓取链路依然一封都存不下来。

修复：按值传 `uidSet`。

### 2.2 scheduler 裸 goroutine 无 recover（P0）

`backend/internal/email/scheduler.go:308`

```go
go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    if _, err := s.fetcher.Sync(ctx, accountID); err != nil { ... }
}()
```

Go 里任一 goroutine 的未捕获 panic 会终止整个进程。与 2.1 叠加后的实际后果是：
**任何启用了邮箱账户的用户一有新邮件，后端进程即崩溃**。定时循环会对每个到期账户起一个这样的
goroutine，所以这不是边缘路径。

修复：goroutine 入口加 `recover()` 并记账户 id。理由不止于兜住 2.1——go-imap 在解析服务器响应的
多处都有 panic 路径，单个账户遇到畸形响应不应该让整个后端下线。

### 2.3 `GetAccountByID` 不选 workspace_id（P1）

`backend/internal/email/store.go`

SELECT 列表里没有 `workspace_id`，因此 `Account.WorkspaceID` 恒为 `""`。`Fetcher.Sync` 用它给邮件
打标记，`InsertEmail` 的 `defaultWorkspace()` 兜底把空值转成 `'default'` ——**所有用户、所有
workspace 抓下来的邮件全部写成 `'default'`**。

不是读取越权：`ListEmailsScoped` 与 `ListEmailsByDayScoped` 都经 `email_accounts` JOIN 判定归属，
与 `emails.workspace_id` 无关。真正的受害者是 `GetSyncStatusScoped`：

```sql
SELECT COUNT(*) FROM emails e WHERE e.account_id=a.id AND e.workspace_id=a.workspace_id AND e.is_read=FALSE
```

对任何非 `default` workspace 的账户，这个条件永不成立，未读数恒为 0。

修复三处：

1. SELECT 补 `workspace_id`（以及本来就声明了变量却没进 Scan 的 `smtp_host`/`smtp_port`）；
2. `Fetcher.Sync` 显式写 `WorkspaceID: acc.WorkspaceID`，符合「后台任务从数据行自带作用域派生」的约定；
3. `GetSyncStatusScoped` 去掉 `e.workspace_id=a.workspace_id`。`e.account_id=a.id` 已唯一确定账户，
   归属由外层 `a.user_id`/`a.workspace_id` 谓词保证，多这一个依赖反范式列的条件只增加失败模式。
   去掉之后历史脏数据也不再影响计数。

另加一条幂等迁移，把 `emails.workspace_id` 按所属账户回填对齐（见 §4 风险）。

### 2.4 云端账户从不出现在列表（P1）

`frontend/src/features/email/EmailAccountSetup.vue`

原逻辑是「本地优先，失败才查云端」，但：

- `emailsStore.listAccounts()` 在本地库为空时返回 `[]`，**不抛异常**，因此云端分支永不触发；
- 云端创建的账户从不写本地库，只有 `addAccount` 失败后的本地回落路径才写。

两者叠加：云账户永远不在列表里 → 没有编辑按钮 → **SMTP 编辑态对真正支持 SMTP 的那批账户完全
不可达**。这是实现本轮主任务时撞上的阻塞项，不修则功能等于没做。

修复：两边都取并按 id 合并，云端优先（服务端是云账户的权威来源，且只有它带 SMTP 字段），
按 `createdAt` 排序保证顺序稳定。

### 2.5 `lastSyncedAt` 类型契约错误（P2）

后端 `email.Account.LastSyncedAt` 是 `int64`（unix 秒），前端 API 类型却声明为 `string?`，
`toLocal` 按 ISO 字符串处理：

```ts
lastSyncedAt: a.lastSyncedAt ? Date.parse(a.lastSyncedAt) : null
```

`Date.parse` 收到数字会先转字符串，`Date.parse("1754...")` → `NaN`，"上次同步" 永远显示不出来。
`createdAt` 则干脆没进 API 类型，`toLocal` 用 `Date.now()` 顶替。

这两个 bug 此前是休眠的——云账户根本不显示，走不到 `toLocal`。2.4 修好后立刻显形，所以一并修掉：
API 类型改为 `number` 并补 `createdAt`，`toLocal` 按秒转毫秒。

### 2.6 SMTP 写入入口缺失（P2）

- `ListAccountsScoped` 不选 `smtp_host`/`smtp_port`，而 `GetAccountByIDScoped` 选了 —— 同一张表的
  两个读方法契约不一致，列表接口因此永远不返回 SMTP 配置，编辑态无从预填。
- `createEmailAccount` 的 body 结构体完全没有 SMTP 字段，新建时填写会被静默丢弃。

修复：列表补选；POST 接受 `smtpHost/smtpPort/smtpPassword`，复用与 PUT 相同的
`UpsertSMTPSettingsScoped` 写入（不改 `InsertAccount` 签名，避免影响其它调用方）。

## 3. 契约与语义定论

### 3.1 SMTP 字段语义（前后端一致）

- 只有携带 `smtpHost` 才会写 SMTP 列；单独传 `smtpPort`/`smtpPassword` 以前被静默丢弃，
  现在返回 400 —— 静默成功比报错更难排查。
- `smtpHost` 传空字符串 = 清空 SMTP 配置，此时 port 一并归零，不留「空 host + 陈旧端口」的半配置状态。
  `smtpPort==0` 也仅在这种情况下合法。
- `smtpPassword` 省略 → 保留原凭证；传 `''` → 清空；传非空 → 重新加密写入。
  前端对应「留空表示不变更」+ 一个显式的「清空已保存的 SMTP 密码」勾选框。

校验逻辑抽成 `validateSMTPInput`，原先内嵌在 handler 里无法单测。

### 3.2 `created_at` 语义：保持 `time.Now()`

上一轮遗留的待确认项。结论是**不改**：

- `emails.date` = 邮件原始时间（IMAP envelope），所有排序、按日窗口、过滤都用它；
- `emails.created_at` = 本行入库时间；
- 全仓库没有任何读路径消费 `created_at`。

改成 `e.Date` 只会复制 `date` 并丢掉入库时间。已在 `InsertEmail` 就地注释，避免再被翻出来讨论。

### 3.3 前端按钮文案

编辑态原文案是「测试连接并保存」，但编辑只发一个 PUT，不触发任何连接探测。改为「保存」；
新建仍走 `addAccount + syncNow`（会真连一次 IMAP），保留原文案。

`/test-smtp` 读的是库里已保存的配置，所以「测试 SMTP」按钮只对已存在的云端账户显示，
且先保存再探测——否则用户改了输入框却测到旧配置，结果具有误导性。

## 4. 验证

新增 10 个测试函数（`store_smtp_test.go` 4 个、`fetcher_pipeline_test.go` 5 个、
`server_assistant_smtp_test.go` 1 个表驱动），其中 9 个触达真实 Postgres、4 个触达真实 IMAP 协议：

- `internal/email/store_smtp_test.go`（4 个）：SMTP 设置在 list / get 两个读路径的往返一致性；
  凭证只能经 `GetSMTPCredentialScoped` 取出；`updateCredential` 的保留/清空语义；跨 user 与跨
  workspace 写入均返回 `ErrNotFound` 且不污染属主行。
- `internal/email/fetcher_pipeline_test.go`（5 个）：抓取 → 规则 → 落库 → scoped 读 的完整链路；
  workspace 标记正确且不跨 workspace 可见；账户规则在落库前生效且 category 过滤能读到；
  按日摘要输入源可见新邮件且跨 workspace 隔离；迁移回填幂等。
- `internal/server/server_assistant_smtp_test.go`（1 个表驱动，14 个用例，不需要 DB）：
  `validateSMTPInput` 的全部边界，含 port 0 仅在清空时合法。

IMAP 侧用 go-imap 自带的 `imapmemserver`，通过临时自签证书跑真实 TLS，进程内完成：不需要任何真实
凭证、无网络出口，可直接进 CI。为此在 `Fetcher` 上加了一个可替换的 `dialTLS` 字段（nil 时用
`imapclient.DialTLS`），因为原代码把拨号写死了，无法指向本地 server —— 这正是这条链路长期无法
自动化验证的直接原因。生产路径行为不变。

命令与结果：

```
cd backend
go build ./...                                   # ok
go vet ./...                                     # ok
POCKET_TEST_POSTGRES_DSN=... go test ./...       # 全部 ok
POCKET_TEST_POSTGRES_DSN=... go test -race \
  ./internal/email/... ./internal/server/...     # 全部 ok

cd frontend
npm run typecheck                                # ok
npm run build                                    # ok
```

Postgres 用一次性容器（`postgres:17-alpine` @ 55433），跑完删除；本机 5432 上运行中的
`llm-gateway-pg` 未被触碰。前端没有配置测试运行器（无 `npm test` 脚本）。

## 5. 风险与部署注意

- **迁移含对 `emails` 的批量 UPDATE**。只更新反范式列与所属账户不一致的行，幂等且收敛后为 no-op，
  但在体量大的生产 `emails` 表上是一次重写操作。读路径从来没受这个列影响，所以这次回填**不紧急**，
  可以与代码分开发布、择低峰执行。建议先 `SELECT COUNT(*)` 估算待修行数。
- **PUT 契约收紧**：`smtpPassword` 不带 `smtpHost` 从「静默丢弃」变为 400。仓库内只有本前端一个
  调用方且始终同时发送二者，但外部客户端若依赖旧的静默成功行为会开始报错。
- 2.2 的 `recover()` 只保证进程存活并记日志，不做重试。该账户本轮同步丢失，下一个周期重来。

## 6. 仍未闭环

承接上一轮 §5，本轮完成 1（前端 SMTP 编辑态）、2（抓取链路验证）、3（`created_at` 语义）。剩余：

- **SMTP 发信与假期自动回复**：目前只有探测（`/test-smtp`），发信未实现；`email_vacation_replies`
  仅存配置，没有任何消费方。
- **规则引擎的其余动作**：`archive` / `route-folder` / `trigger-autoreply` 仍统一返回
  `ActionUnsupported`。本轮验证的是 `mark-important` 与 `label-category` 两条真实生效的路径。
- **AI 分类与每日摘要的实际生成**：本轮验证到「摘要输入源查询能看到新邮件」为止，
  `kxmemory.DailySummary` 的调用与产出未覆盖。
- **共享实例配置语义**：`WorkspaceID == ""` 的共享实例已排除在租户推送之外，但「谁来配置」无方案。
- **真实 OAuth 授权验证**：上一轮的 `client_id` 修复仍未经 Google/Microsoft 实际授权流验证。
- **CI 门禁**：`.github/workflows/backend.yml` 存在却没拦住 main 编译不过，需核查；同时应确保
  `POCKET_TEST_POSTGRES_DSN` 已配置，否则本轮新增的 DB-gated 测试会静默跳过——email 包正是因为
  长期没有触达数据库的测试，才让 `InsertEmail` 和本轮这批缺陷一路存活。

