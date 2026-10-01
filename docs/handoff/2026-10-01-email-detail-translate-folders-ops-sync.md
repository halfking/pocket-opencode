# 邮件四项增强：详情自愈 / 翻译修复 / 自定义目录 / 智能整理 + 操作日志同步

> 2026-10-01 会话产出（未提交，工作树）。对应用户需求四条：
> ① 邮件详情展示不完整、翻译不生效；② 自定义邮件目录（同服务器能力）；
> ③ 智能识别系统通知类邮件归集；④ 本地迁移操作记录 log + 同步按钮（可选/全量）同步到服务器。

## 1. 详情不完整 —— 旧拍平缓存版本化自愈

`backend/internal/server/server_assistant.go`

- 正文加密缓存文件布局从「8B UID + 密文」升级为「8B UID + 1B format + 密文」。
  format：`0x01`=完整 MIME（`BODY.PEEK[]`）、`0x02`=BODY[TEXT] 兜底纯文本。
- **第 9 字节不是已知版本号 = legacy 旧缓存**（旧版密文是 base64，首字符取值域
  A–Z a–z 0–9 + / =，最小 0x2B，撞不上 0x01/0x02），读路径判为未命中 → 下次访问
  回源 IMAP 重拉完整 MIME 并以 v1 覆写。升级后旧缓存自愈，无需手工清理。
- BODY[TEXT] 兜底写 0x02：它是该服务器能给的最佳结果，不触发自愈循环。
- 回归：`email_body_cache_test.go`（legacy 未命中 / v1 v2 命中 / 截断头未命中）。
- 前端无需改动：详情页本来就「远端优先」（`pickEmailDetailBody`），服务端自愈后
  下一次打开详情即拿到完整 MIME。

## 2. 翻译不生效 —— llmgateway 增加 Anthropic Messages 适配 + 自动回退

根因在 `docs/handoff/2026-10-01-llm-gateway-chat-completions-unsupported.md`：
生产网关 llm.kxpms.cn **不提供** `/v1/chat/completions`（所有模型挂死），只有
`/v1/messages`（Anthropic）与 `/v1/responses` 可用；而 pocketd 只实现了 openai-chat。

- 新文件 `backend/internal/llmgateway/anthropic.go`：
  - `chatViaMessages`：OpenAI chat 形态 ↔ Anthropic messages 形态互译（system 抽
    顶层、多模态块转换、tool_calls/tool_result 折叠、usage 映射），鉴权用
    `x-api-key` + `anthropic-version`（附带 Bearer 兼容）。
  - `streamViaMessages` + `parseAnthropicSSE`：具名事件（message_start /
    content_block_delta / message_delta / message_stop）→ OpenAI delta 形态。
- `client.go`：`Client.Format` 分派；`Format==""`（openai-chat）失败且错误为
  **传输层失败 / 404 / 405 / 501** 时自动回退 anthropic 一次，成功后写入进程级
  `discoveredFormats`（按 BaseURL 记忆，后续请求直连可用形态，不再付 30s 超时代价）。
  业务错误（401/429/503 no_candidate）**不**回退。流式仅在「未向客户端输出过内容」
  时回退，避免重复作答。
- `llmbff_provider_adapters.go`：`clientFor` 透传网关配置的 Format。
- 效果：邮件翻译（`/api/llm/chat`）与 AI 对话（`/api/llm/stream`）在默认网关上
  开箱即用，无需用户改设置。
- 回归：`anthropic_test.go`（请求/响应翻译、404 回退+粘性、401 不回退、流式事件解析）。

## 3. 自定义邮件目录（同邮箱服务器能力）

### 后端
- 迁移（`store_folders.go`，`NewStore` 自动执行）：`emails.folder_name` 列、
  `email_folders` 表（UNIQUE(account_id, name)）、`email_ops_log` 表（§4）。
- IMAP 能力（`imapops.go`）：`ListMailboxes` / `CreateMailbox` / `DeleteMailbox` /
  `MoveUIDsToMailbox`（不存在则建；go-imap 收到 UIDSet 自动发 UID MOVE，服务器
  不支持 MOVE 时回退 COPY+\Deleted+EXPUNGE）/ `FindTrashMailbox`。
  `junk.go` 的垃圾箱清理不改动，共用底层形态。
- API（`server_email_folders.go`，路由注册 `server.go`）：
  - `GET/POST /api/email/folders`、`DELETE /api/email/folders/{id}`（只删登记，
    目录内邮件退回收件箱视图；**不**删服务器目录）。
  - `POST /api/emails/move {ids, folder}`：本地（PG）立即改 `folder_name` + 写
    操作日志 + 尽力即时 IMAP MOVE；失败留 pending 由同步按钮收口。
  - 列表 `GET /api/emails` 支持 `folder` 参数：空=收件箱、名字=该目录、`__all__`=全部。
- 规则意图 `route-folder` **真实执行**：`intent_executor.go` 注入 `email.UIDMover`
  （main.go 传 `emailFetcher`），规则命中即真实 MOVE 并同步本地归属。
- 附带收益：`InsertEmail` 冲突只刷 snippet，同步不会打回目录归属。

### 前端
- 本地镜像（`schema.ts` + `local-db.ts` 迁移 `2026-10-01-email-folders-v1`）：
  `local_emails.folder` 列、`local_email_folders`、`local_email_ops` 表。
- `email-folders-model.ts`（纯逻辑，可 node --test）+ `email-folders-store.ts`
  （DB/网络封装）。
- UI：`/email/folders`（`EmailFolderListView.vue`）目录列表/新建/删除/进入目录；
  收件箱多选模式新增「移动」按钮；详情页「更多 → 移动到目录」；
  共用 `EmailFolderPickerSheet.vue`（可顺手新建目录）。
- 收件箱支持 `/email?folder=<名>` 目录视图（KeepAlive 下用 route.query 驱动）。

## 4. 智能识别系统通知 + 操作日志同步按钮

### 智能整理
- `email/organize.go`（零成本确定性启发式，不依赖 LLM）：
  发件人形态（noreply/notify/alerts/newsletter…）+ 标题关键字（验证码/物流/订阅/
  verification/receipt…）+ **同规整化标题出现 ≥3 次的整组**（剥 Re:/数字/单号后分组）。
  已被 classify 打了 notification 标签的直接命中。
- `POST /api/emails/organize {accountId?, folder?, dryRun?}`：dryRun 预览命中数与
  原因；确认后整批走 §3 的移动链路（默认落点目录「通知」，自动登记）。
  收件箱更多菜单「智能整理」按钮：预览 → confirm → 执行 → 提示。
  刻意做成一键确认而不是后台静默搬信：误移个人邮件的代价高于漏收。

### 操作日志 + 同步按钮（可选 / 全量）
- 服务端 `email_ops_log`（`store_folders.go` + `server_email_ops.go`）：
  move/delete 两种 action；`idempotency_key` 唯一（离线重放安全）；
  `POST /api/emails/ops`（队列回放）、`GET /api/emails/ops?status=`（日志）、
  `POST /api/emails/ops/sync {ids?}`（ids=可选同步，缺省=全量 pending，按
  account+folder 分组批量 IMAP 执行）。
- **delete 的执行语义是移入账户垃圾箱**（\Trash 属性或常见命名，缺失则建 "Trash"），
  永不直接 EXPUNGE——误删可在服务商垃圾箱找回。purge（批量删除）现在也会记 delete op。
- 前端本地队列 `local_email_ops`：移动/删除时本地即时生效 + `recordOpsEntry`；
  `flushEmailOps()`（目录页「全量同步」/勾选可选/单条同步）推服务端日志 →
  `/ops/sync` 执行 → 本地置 applied；网络失败行保持 pending 不丢。
- 待同步操作的服务端快照回写保护：`upsertEmail` 发现本地有 pending op 时保留本地
  folder（`hasPendingOpsForEmail`），防后台同步把未上行移动打回收件箱。

## 5. 验证记录

- 后端：`go test ./...` 52 包全绿（含新增 `organize_test.go`、
  `store_folders_test.go`（PG 门控）、`anthropic_test.go`、`email_body_cache_test.go`）。
- 前端：`npm run gates` 全绿（typecheck / build:gate / test:native / check:vm-gaps /
  i18n / dead-api / icons）；邮件特性 `__tests__` 全部通过，新增
  `email-folders-store.test.mjs`。
- 注意：`gates` 不含邮件特性测试，跑法 `node --test src/features/email/__tests__/<file>`。

## 6. 已知边界（后续可做）

- `ListMailboxes` 已实现但「从服务器 LIST 收敛目录镜像」尚未接到任何 UI/定时任务
  （目录页目前只展示登记过的目录；服务器侧自建目录不会自动出现）。
- 目录视图内的「统一操作」目前是多选删除+移动；标记已读/星标的批量入口未做。
- `/ops/sync` 的 claim 用 FOR UPDATE SKIP LOCKED 但非显式事务，并发双击理论上
  可能重复执行同一批（MOVE 幂等、失败可重试，影响有限）。
- 收件箱自动触发 organize（后台静默整理）按「不擅自搬用户邮件」原则未开启，
  保持一键确认形态。
