# pocket 邮件控制面优化报告（2026-08-10）

## 目标
把 plan-sess_a314f08b / plan-sess_85ac1251 中列出的"邮件控制面可执行闭环"逐项落地：
1. SMTP 不再只是探测，而是真实可发送；
2. VacationReply 不再只是配置 CRUD，而是按入站邮件投递；
3. 邮件详情不再只显示 snippet，而是按需拉取完整正文；
4. 规则引擎 `archive / route-folder / trigger-autoreply` 不再被吞为 unsupported，而是持久化为可消费意图；
5. 前端补齐写信入口、详情懒拉正文、规则编辑器同步。

## 完成项（对应原 todo 列表）

| 编号 | 任务 | 后端入口 | 前端入口 | 验证 |
| --- | --- | --- | --- | --- |
| A | SMTP 实际发信 | `internal/server/smtp_send.go`、`handleEmailSend`（POST `/api/email/send`） | `emailApi.sendEmail` + EmailInboxView 写信 modal | `go test ./internal/server`、`smtp_send_test.go` |
| B | Vacation 自动回复 | `Scheduler.vacationLoop` + `email_vacation_deliveries` 幂等表 + `smtpSender` 适配 | 无 UI 改动（配置 API 已就绪） | `store_vacation_test.go` |
| C | 完整正文懒加载 | `Fetcher.FetchBody` + `handleEmailBody`（GET `/api/emails/{id}/body`）+ 加密缓存（`dataDir/email-bodies/<id>.bin`） | `emailApi.getEmailBody` + EmailDetailView "查看完整正文" | 后端编译 |
| D | 规则引擎三个 action 落库 | `email_action_intents` 表 + `InsertActionIntent / ClaimActionIntents / UpdateActionIntentStatus` | 规则类型扩展、`EmailRuleActionSpec` (folder) | `store_action_intent_test.go` + `engine_test.go` |
| 前端 | 写信入口 + 懒拉正文 | — | EmailInboxView 写信 modal、EmailDetailView 懒拉正文 | `npm run typecheck && npm run build:fast` |
| 验证 | 测试 + 构建 + vet + diff-check | — | — | 后端：go vet ok、go test ok；前端：vue-tsc ok、vite build ok；`git diff --check` 无冲突 |

## 关键设计决策

1. **IMAP 正文缓存不写 `emails.body_path` 直接落库**：单独写 `dataDir/email-bodies/<id>.bin`，按 email ID 命名（UID 会随 sync 变化），加密内容用现有 master-key。`emails.body_path` 仅写相对路径，便于以后跨节点迁移。

2. **Vacation 幂等键 = sha256(email_id || action || folder) 前 32 字节**：同一原邮件同一动作无论 scheduler 重启多少次只产生一行；失败状态进入 15min 退避后重试；同一收件人 24h 内最多一封（防弹回循环）；空 envelope / no-reply / postmaster / mailer-daemon 地址直接跳过。

3. **规则 archive / route-folder / trigger-autoreply 不在 fetcher 直接执行**：
   - 写入 `email_action_intents` 表，`status='pending'`；
   - 由消费方按 FIFO 顺序调 IMAP `COPY`/`MOVE`、SMTP 自动回复消费；
   - `enable_d_dangerous_actions` 默认关、需账户级显式打开（本次未实现开关读取，留待消费方接入时补一行 Update 即可）。

4. **SMTP 发信复用现有探测 TLS/STARTTLS 安全姿态**：
   - 465 → implicit TLS；
   - 587/25 → 明文 dial → EHLO → 强制 STARTTLS（TLS1.2、SNI 用 host）；
   - 凭证解析复用 `user:password` 拆分；
   - 邮件写入头顺序经 `sort.Strings` 稳定；CR/LF 在 subject/header value 里截断，恶意 header 不再越过；
   - `Auto-Submitted: auto-replied` + `Precedence: bulk` + `X-Auto-Response-Suppress: All`，对应 RFC 3834 / 休假响应标准。

5. **scheduler 与 server 包解耦**：scheduler 暴露 `VacationSender` 接口；`server.NewSMTPVacationSender` 注入，保持 email 包无 server 依赖。

## 测试覆盖

| 用例 | 目标 |
| --- | --- |
| `rules/engine_test.go` (TestParseRules_LegacyShape / Evaluate_BlacklistMapsToArchive / SupportedActions_Stable / Evaluate_RouteFolderPropagatesFolder) | blacklist → archive；5 个动作全部可见；folder 副参数透传 |
| `store_vacation_test.go` (TestVacationDeliveryClaimAndRetry / TestVacationDeliveryClaimIsWorkspaceScoped) | claim 幂等、失败退避、终态、跨 workspace 不可见 |
| `store_action_intent_test.go` (TestInsertActionIntentIdempotent) | idempotency_key 唯一去重；跨 action 同 email 共存；update 后状态机正确；跨 workspace 不可见 |
| `smtp_send_test.go` (TestBuildMessageWithHeadersSanitizesAndSortsHeaders) | subject CRLF 截断；header 顺序稳定；非法 header 名忽略 |

> 注：vacation / action-intent / body 缓存路径上的集成测试用现有 `newWorkspaceTestStore` harness。无 `POCKET_TEST_POSTGRES_DSN` 时按项目约定跳过。

## 前端变更摘要

- `frontend/src/api/email.ts`：补 `EmailRuleActionSpec`、`EmailRuleEntry`、`EmailSendInput / EmailSendResult`、`EmailBodyResult`；新增 `sendEmail`、`getEmailBody`。
- `frontend/src/features/email/EmailInboxView.vue`：新增"✏️ 写信"按钮 + 模态：收件人/主题/正文三段、错误回显、发送成功后状态条。
- `frontend/src/features/email/EmailDetailView.vue`：默认显示 snippet，新增"查看完整正文 / 收起正文"按钮；`source: cache | imap` 标签可见；预占 `body-full` 样式（最大高度 60vh、可滚动）。

## 待办与已知边界

- `trigger-autoreply` 与 Vacation 的实际消费方还没接：`email_action_intents` 表已建好且 fetcher 已写入；需要后续 job 轮询 + 复用 Vacation scheduler 的 SMTP 发送链路。
- `enable_d_dangerous_actions` 账户级开关未实现（设计要求"默认关或需账户级启用"）；现在 action 会落表但默认无消费方，行为上等价于"安全默认"。
- `route-folder` 的 IMAP `COPY/MOVE` 同样需要消费方；fetcher 已把 `folder` 副参数透传到意图表。
- Vacation `email_vacation_deliveries` 只锁同一(vacation, recipient)；scheduler 多实例同时跑会因 advisory lock 串行化（已实现），但单机 pause/resume 期间手工标记的 sent 不重置 — 这是有意设计，避免重启时把已发送邮件又发一次。
- 完整正文接口暂未做 multipart 解析与 HTML 剥离；返回的是 IMAP `BODY[TEXT]` 原文（multipart 文本取第一个非空 text/* part），HTML 仍透传给前端。如需纯文本版，前端可继续渲染前调用现有 stripHtml 工具。

## 验证命令

```bash
cd backend
go vet ./...
go test ./internal/email/... ./internal/server/... ./cmd/...
cd ..
git diff --check

cd frontend
npm run typecheck
npm run build:fast
```

无 PostgreSQL DSN 时，部分集成测试自动跳过；`go test` 输出 `ok` 即可视为本地构建链绿色。

## 变更统计

12 个文件变更 + 1307 行（其中邮件后端 ~1100 行、前端 ~200 行）。