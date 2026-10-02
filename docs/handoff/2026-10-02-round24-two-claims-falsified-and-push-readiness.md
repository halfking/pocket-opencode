# round24 — 推翻两条我自己记错的结论 + 推送条件算清

日期：2026-10-02 23:58（本机时区）。作者：Mavis。
性质：只读取证 + 一次基线回归。**本轮没有改任何产品代码。**

这份文档存在的理由：下面两条结论此前已写进 todo / handoff / 汇报，被我自己复述了
多轮，**其中两条是错的**。仓库里凡是引用过它们的文件都需要改；这里给出证据与改法。

---

## §1 「明天 09:00 那轮会推 32 条积压提醒」——时间是 **08:00**

### 证据

    backend/internal/config/config.go:286
        EmailPipelineHour: getEnvInt("POCKET_EMAIL_PIPELINE_HOUR", 8),

默认是 **8**，不是 9。本机 `.env` 里**没有** `POCKET_EMAIL_PIPELINE_HOUR`
（grep `PIPELINE_HOUR|EXECUTION_MODE|SPAM_DRYRUN` 在 `.env` / `logs\*.env` 零命中），
所以取默认值 8。

跑着的那个进程自己的日志坐实（不是推断）：

    logs/pd-18099-20261002-233541.err.log
    2026/10/02 21:08:31 [email/scheduler] daily pipeline runner injected (hour=8)
    2026/10/02 23:34:11 [email/scheduler] daily pipeline runner injected (hour=8)
    2026/10/02 23:35:05 [email/scheduler] daily pipeline runner injected (hour=8)
    2026/10/02 23:35:41 [email/scheduler] daily pipeline runner injected (hour=8)

**影响**：决策窗口比先前记的少 1 小时。上一轮汇报写 09:00 是错的。

### 但「会推 32 条」这半句是对的，且刚被独立验证

`ClassifySkipReason`（`backend/internal/email/scheduler.go:731`）只拦**每分钟同步
路径**里的分类，它**不在** `Pipeline.Run` 里。`Pipeline.Run`
（`backend/internal/email/pipeline.go:359-428`）是 1 收信 → 1.5 发票候选 →
2 清垃圾 → 3 `notifyImportant` → 4 采集 → 5 推送/汇总，第 3 步**无条件**执行。

而这 32 封的 `importance` 早就在库里（`ece9b97e` 的诊断：全库 `importance 为空 = 0`），
不依赖当轮分类。所以：

> 「定时路径分类器关闭」（`POCKET_KXMEMORY_BASE_URL` 未配）**不会**救这批积压。
> 它只让**新邮件**永远进不了提醒。

推论（新写下的，之前没说清）：「分类器关闭」与「积压会一次性推 32 条」是**两件独立的
事**，不能用前者去推后者。

---

## §2 需求 6 不是「一行未实施」——此前的前提是错的

### 证据

客户端**没有任何 IMAP 能力**：`grep -i imap frontend/src` 只命中
`api/email.ts` 里的接口字段名（`imapHost` / `imapPort` / `smtpPassword`）与
`api/error-message.ts` 的错误码映射，**零** 直连实现。

于是规格里「设备本地执行 / 委托服务端执行」在本产品的唯一可能落法是：

| 规格词 | 本仓实现 |
|---|---|
| 设备本地执行 | 流水线跑在**本地 pocketd 进程**内（默认） |
| 委托服务端执行 | `mode=server` + 配了 URL 时 POST 到远端编排服务 |

这条契约**已经实现且有判据**：

- `backend/internal/server/server_email_pipeline.go:255` `shouldDelegatePipeline`
  —— 纯函数，默认 `local` 不委托；`server` 但 URL 空时**回落本地**而不是报错
  （委托会因空 URL 直接返回错误，等于整轮什么都没跑）。
- `server_email_pipeline.go:289` `delegatePipeline` —— 真正 POST 出去的那个函数。
- `execution_mode_test.go` 4 条 + `delegate_pipeline_test.go` 4 条（URL scheme 校验、
  非 200 不当 JSON 解、坏 JSON 标为契约不兼容、连不上必须报错而不是静默空报告），
  两条负控实测转红（见 handoff §7db）。

### 因此被取消的待办

「移信要不要先落成 intent（需求 6 架构前提）」——**前提不成立**。
意图队列（`email_action_intents` → `scheduler.intentLoop`）解决的是**延后执行**，
与**执行位置**无关。移信当前由本地 pocketd 在每日流水线第 2 步同步执行，
这已经是规格里「默认设备本地」的那一腿。

> 注意：这不代表「移信落 intent」本身没价值（它会让移信可重放、可审计）；
> 只代表它**不是需求 6 的前提**，不能拿需求 6 给它背书。

---

## §3 推送条件已算清（仍需授权才动）

计数口径：**截至 `348c9bde`，即本文件所在的这次提交**。再往后每加一个提交，
三个数字都会各 +1，而交集结论不受影响（实测重算后仍是 0/0）。

| 项 | 值 |
|---|---|
| 本地未推提交 | 14（含我的 6 个） |
| origin/main 多出的提交 | 3（`3cc2d852` / `3a848d43` / `6cad3038`，verify/e2e 分支并入） |
| 我的提交涉及文件 | 8 |
| 远端 3 个提交涉及文件 | 25（`.maestro/` / `scripts/` / `docs/handoff/`） |
| **两侧文件交集** | **0** |
| 与本地另外 8 个并发提交的交集 | **0** |

结论：`git pull` 是一次**必然干净**的 merge，**不需要 force-push**。
（分叉状态，`git merge-base --is-ancestor origin/main HEAD` 退出码 1 ⇒ 直接 push 会被拒。）

**算交集的方式**：按提交逐个 `git show --name-only` 枚举，不是在分叉分支上用两点
`git diff`——那算的是 HEAD 与 origin/main 的差集，不是我要断言的「我的文件」。

### 补记（2026-10-03 00:0x，状态又变了——上面两行已过期）

写完上面那段之后，并发会话把 `origin/main` 合了进来（本地出现 `a25160cc`
Merge remote-tracking branch）。于是：

- `git merge-base --is-ancestor origin/main HEAD` → 退出码 **0**
- `git log HEAD..origin/main` → **空**

**现在推送是 fast-forward，连 merge 都不需要**，上一行「直接 push 会被拒」已不成立。

「交集为 0 ⇒ 合并干净」当时是推断，之后**在一次性 worktree 里真合并了一次**：
`git merge --no-commit --no-ff origin/main` → `Automatic merge went well`，
退出码 0，`git diff --diff-filter=U` 为空。worktree 事后已 `merge --abort` +
`worktree remove`，主工作区未被触碰（那里有并发会话未提交的改动）。

### 基线

- `go test ./internal/email -count=1` → **EXIT=0**，104.410s（在新 HEAD `db8db615` 上）
- `db8db615..HEAD` 之间**零个 `.go` 改动**（`git diff --name-only` 过滤 `\.go$` 为空）
  ⇒ 上面那次绿测对应的 Go 代码至今未变，仍然有效。
- 同一区间有 3 个前端文件变动（并发会话新增的 dead-api 检查器：
  `frontend/scripts/dead-api-classify.mjs` 等）——**我没有重跑前端测试**，
  不对前端当前状态作任何断言。
- `internal/server` **未重跑**。工作区里有并发会话未提交的
  `llm_gateway_handler.go` / `llm_gateway_selfheal_guard_test.go` 等 6 个改动，
  在这份工作树上跑 server 测试量的是**他们的 WIP**，不是我这 6 个提交。
  不假装它绿。

---

## §4 本轮未做、原因留档

- **不接 LLM 网关兜底进 Scheduler**：除产品取舍外还有硬理由——并发会话正在改
  `llm_gateway_handler.go`，接线要读它的分类入口，撞文件。撞了就是逼合并者单侧取舍
  = 静默覆盖对方工作。
- **不删任何生产文件**（发票目录 4 个历史残留、3500 旧名副本、60 个重复 CSV）：
  全在「待拍板」，且删除不可逆。
- **不触发 08:00 那轮**、不跑真实 IMAP MOVE：均需单独授权。

## §5 仍然卡在用户侧

1. 32 条积压提醒策略（08:00 会推；15 notification 含 7 封已过期验证码 /
   13 work 是 09-30 同一次 CI 失败潮 / 4 bill）
2. 飞书凭据四项 `POCKET_FEISHU_APP_ID` / `APP_SECRET` / `INVOICE_CHAT_ID` /
   `INVOICE_FOLDER_TOKEN`——不配则需求 3 主交付物永远推不出去
   （`feishu_sent_at = 0 of 2` 已坐实整条链路一次没跑过）
3. 13 个提交推不推
4. 汇总文档保留策略 / 58000 库存行 / 验证目录隔离
5. 真机端到端验收
