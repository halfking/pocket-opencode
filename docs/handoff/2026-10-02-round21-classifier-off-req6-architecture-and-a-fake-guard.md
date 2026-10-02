# round21 — 定时路径的分类器实际是关着的；需求 6 的「服务端跳过」是个伪问题

> 2026-10-02 22:15。本轮全部结论都带取证位置；未验证项单列在最后一节。
> 本轮新增提交两个：`ece9b97e`（需求 4 积压只读诊断）、`4309ef59`（需求 8 接线判据）。
> 两个都**未推**；本地领先 origin/main 16 个提交。

## 0. 本轮最要紧的一条

**需求 4 对新邮件恒 0 条提醒，因为定时路径的分类器根本没装配。**

运行进程自己的日志（`logs/pd-18099-20261002-210827.err.log`，21:08:27 启动）：

```
21:08:31 INFO: POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled
21:08:31 Email scheduler started (fetch_enabled=true, kxmemory=false, tz_offset=28800s, oauth_providers=0)
21:32:32 [email/scheduler] 同步后**不执行**自动分类：kxmemory 未配置……后果：新到邮件的 importance 恒为空，需求 4 不会提醒。
```

链路：

1. `cmd/pocketd/main.go:400-414` —— `kxmemory.Client` 只在 `cfg.KxMemoryBaseURL != ""` 时构造，否则打上面那行 disabled 日志；
2. `backend/internal/email/scheduler.go:731` —— `ClassifySkipReason(s.kxmem != nil, userID)` 命中第一个分支，直接 `return`，**不执行分类**。

本机 `POCKET_KXMEMORY_BASE_URL` 在进程级 / 用户级 / 机器级 / `scripts/` / `.env` 五处**均未配置**（逐处查过）。

### 1.1 由此产生的不对称

| 入口 | 分类器 | 当前状态 |
|---|---|---|
| `POST /api/emails/classify`（手工） | kxmemory → 失败则 `classifyViaGateway` 兜底 | 网关在 `.env` 里配着，**可用** |
| Scheduler（每日 09:00） | 只有 kxmemory | **不可用，直接放弃** |

网关兜底在 `server_email_classify_gateway.go:118`，`server_email_classify.go:107` 是
「先 kxmemory 后网关」的串联。`email.Scheduler` 在 `internal/email` 包里拿不到
`*server.Server` 的网关对象，只能放弃。

**注意 `.env` 里那段注释是过时的**：它写「所有走网关的功能（对话、邮件分类、
STT 总结）都会 401」，容易被读成邮件分类走网关。定时路径不走。

`backend/internal/email/classify_run.go:142-153` 已经把这个不对称留档，并标注
「跨包的设计改动，需要单独拍板」——本轮只补运行时证据，不重复提方案。

### 1.2 修它需要什么

给 `email.Scheduler` 注入网关依赖。属于跨包改动，且会让每日流水线真的发出
LLM 请求（成本 + 网络 + 失败重试语义）。**未擅自实施。**

## 1. 需求 6：「服务端如何跳过」不是待定语义，是事实

我此前把这一问交给你拍板，方向问错了。查完的结论：

**服务端不跳过，它是直接动手。**

`backend/internal/email/pipeline.go:815-831`（`SpamDryRun=false` 的真实分支）：

```go
moved, err := p.Fetcher.MoveEmailsToJunk(ctx, accountID, uids)
rep.SpamMoved += moved
...
p.Store.MarkEmailsSpamByUID(ctx, accountID, uids)
```

在流水线里**同步直连 IMAP MOVE**。没有意图行、没有幂等键、没有重试、没有
设备可认领的环节。

而 `email_action_intents` 这张表的**唯一生产者是规则引擎的副作用型动作**：
`backend/internal/email/fetcher.go:454` 的 `recordActionIntent`，由
`backfill.go:330` 的 `applyInlineRules` 产出 pendingIntent 调入，承载
`route-folder` / `trigger-autoreply`（`rules/engine.go:30`）。垃圾邮件完全不沾它。

因此需求 6「默认放设备本地执行」若要成立，**移信必须先落成一条 intent**，
否则设备根本无从认领。claim 是按 `(user_id, workspace_id)` 做的，真机认领
必须带上归属身份——这也是「设备不可达时意图滞留」那个问题的根。

## 2. 需求 8：一条自称端到端、实际碰不到生产代码的判据（已修）

`account-stamp-units.test.mjs` 里那条 `normalized stamp keeps the LWW guard
meaningful`，注释写：

> 这条是**端到端**判据……若 rowToAccount 忘了调归一（或调错），它会转红。

**不成立。** 该用例自己先 `normalizeAccountStamp(localMs)` 再喂给
`planAccountSync`，全程**没有调用 `rowToAccount`**。

实测负控（2026-10-02）：把 `frontend/src/features/email/emails-store.ts:533`
的 `normalizeAccountStamp(r.updated_at)` 换成裸的 `r.updated_at`，跑
account-lww-real / account-stamp-units / account-sync /
account-push-field-symmetry 四个文件：

```
# tests 25   # pass 25   # fail 0
```

**一条都没红。** 后果不理论：§7dg 那个单位混用 bug 可以原样回来且无人报警
（上行 base 恒大于服务端现存值 → 服务端守卫 `updated_at <= base` 恒成立
→「旧的一方覆盖新的一方」被静默架空）。

新增 `frontend/src/features/email/__tests__/account-read-path-normalization.test.mjs`
（4 条，提交 `4309ef59`）：

1. `rowToAccount` 的 `updatedAt` 必须由 `normalizeAccountStamp(r.updated_at)` 产出
2. 不得把 `updated_at` 原样透传
3. **反向护栏**：`last_synced_at` 是毫秒语义，不得一起归一
4. 写侧 `saveAccount` 必须落 Unix 秒（读侧归一救得了存量，救不了新写的行）

两个实现坑：按**函数边界**切片而非手抄整段（手抄副本会与磁盘文本漂移，
anchor 静默失配后脚本变 no-op 却仍报「通过」）；切片前先归一 CRLF，否则
「函数不存在」会伪装成断言失败——第一版就踩了，测试报的是「找不到函数」。

负控：摘掉归一 → 本文件 2 条转红、`EXIT=1`；恢复后 5 个账户相关文件
**29 pass / 0 fail**。`emails-store.ts` 负控前后 `git diff` 均 0 行。

## 3. 需求 4 积压画像（`ece9b97e`）

90 天回看窗口（`importantReminderLookbackDays`）已上线但**从未真正跑过**。
只读诊断 `backend/internal/email/diag_reminder_backlog_test.go` 复用生产
`splitReminderCandidates` / `ListEmailsSince` / `CountHighImportanceOutside`
与生产常量，不重抄。

真实库（2026-10-02 22:00）：扫描 128 行、未分类 0、窗口外 0、
**RemindersPending = 32**。

| 构成 | 封数 | 说明 |
|---|---|---|
| `category=work` | 13 | 其中 12 封挤在 09-30 02:22–05:35，**同一次 CI 失败潮** |
| `category=notification` | 15 | 含 **7 封一次性凭据**：17 天前的 Zhipu 验证码、5 天前的 OpenAI 临时验证码、两次 `New sign-in to your OpenAI account`、`登录 Cursor` |
| `category=bill` | 4 | 工行对账单、AWS 账户提示 ×2、可用额度预警 |

限流对比：N=10 → work=7 bill=2 notification=1；N=20 → work=12 bill=4
notification=4；N=32 → 13/4/15。

**单轮无上限**：`notifyImportant` 与 notifycenter 两层都无限流与合并，只有
列表查询的显示上限（默认 50）。所以 32 条会全部落库、一页内全部显示。

双向负控：把 `importantReminderLookbackDays` 改回旧的 2 → 输出 32 掉到 **2**，
`pipeline.go` 前后 `git diff` 均 0 行；护栏白名单条目摘掉 →
`TestPGTestsNeverTargetTheProductionSchema` 转红并点名本文件。

## 4. 两张发票行查实（可直接决策）

```
inv_1790884695419622800_1 | status=downloaded
  file_name = 其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf
  inv_no=26332000008261110741 | date=2026-09-24 | amt=3500.00
  created=2026-10-02 03:58:15 | updated=2026-10-02 04:00:28

inv_1790903383222583800_1 | status=new | file_path=(null) | inv_no=(null)
  date=2026-10-25 | amt=58000.00 | by=rule | created=2026-10-02 09:09:43
```

- **3500 旧名副本** `其他-杭州创客家投资管理有限公司-3500.00-2026-10-01.pdf`
  （157615 字节，与 09-24 版 SHA256 相同）**没有任何数据库行引用**。当前凭证
  指向 09-24 版。删它不会断链、不会丢凭证、不影响合计。**但它仍是凭证文件，
  未擅自删。**
- **58000 误建档行**（rule 提取器从工行 Statement 建出）当前 `status=new`，
  而飞书推送与共享台账都按 `status='downloaded'` 过滤
  （`pipeline.go:377` `ListInvoicesScoped(..., "downloaded", 500)`），**完全惰性**。
  残留风险：若将来被采集，会拼出**未来日期 2026-10-25** 的凭证名。

## 5. 关闭：06ae350f 的迁移已落表

`email_action_intents` 表已存在、14 个列齐全
（`id, email_id, account_id, workspace_id, user_id, action, folder, reason,
idempotency_key, status, error, created_at, updated_at, applied_at`），
并发会话 21:08 那次重启已应用。`rows=0`（还没被写过，因为规则引擎从未命中）。
「待重启」这条可以划掉。

## 6. 本轮排除的三个假缺陷（撤回）

1. **跨工作区串数据** —— `notifyImportant` 扫描不过滤 workspace 看着可疑，
   但收件人按 `e.AccountID` 解析归属用户（`server_email_pipeline.go:156`），
   且真实库 128 封全属唯一 workspace `ws_user-admin`。不成立。
2. **规则引擎误标验证码** —— `email_accounts.rules` **5/5 全 NULL**，
   `backfill.go` 的 `ActionMarkImportant` 对真实账户一次都没生效。那 7 封
   验证码的 `high` 来自 LLM 分类器，不是规则。
3. **命名规范歧义** —— 磁盘上确有 5 段样本
   `其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf`（单位名自带连字符）。
   但查过全部消费方：`InvoiceFileName` 只拼不解析，`withInvoiceSeq` 只追加
   `-N`，`email_action_intents` 走 `email_id` 外键不靠文件名。
   **没有任何代码反向解析文件名** → 仅可读性问题，**不计为缺陷**。
   我一度想把它当 bug 报出来，收回。

## 7. 需求 7 用真实数据复核

真实库未删邮件的 distinct category 只有 4 个：
`notification(46) / work(42) / bill(25) / marketing(15)`，
全部落在 `classify.go:5-7` 的 `categoryWhitelist`
（`work, bill, notification, personal, marketing, spam`）内。
**没有筛不出、界面上看不见的邮件。** `personal` 与 `spam` 目前 0 行。

前端邮件域全量：`node --test src/features/email/__tests__/*.test.mjs` →
**421 pass / 0 fail**（3.7s）。后端 `go test ./internal/email -count=1` → EXIT=0。

## 8. 一个未收尾的判断

`action_reason` 在真实库 **128/128 全空**。

- 修复提交 `dadbd91d` 落地于 10-02 11:09:54；
- 真实库恰好 `before_fix=122 / after_fix=6`，**与该提交注释里记的「122 封
  blast radius」完全对上**；
- 但 6 封里有 5 封早于 18:52 重建的二进制，只有 18:58 那封出自修复后的
  进程——**n=1，不足以判定修复失效**。

**先不下结论。** 需要一次修复后进程写下的新分类样本才能判。

## 9. 待拍板清单

| # | 事项 | 状态 |
|---|---|---|
| 1 | **移信要不要先落成 intent**（需求 6 架构前提） | 跨包改动，未动 |
| 2 | **网关兜底要不要接进 Scheduler**（需求 4 否则恒 0 条提醒） | 跨包 + 会发 LLM 请求，未动 |
| 3 | 3500 旧名副本删不删 | 已确认零风险，仍是凭证文件 |
| 4 | 58000 误建档行怎么处理 | 当前惰性 |
| 5 | 32 条积压提醒策略 | 时敏，清单已备（§3） |
| 6 | 16 个本地提交怎么上 origin/main | 全部未推 |
| 7 | 开真实 IMAP MOVE | 需授权，依据已备（只会移 2 封阿里云周报） |
| 8 | 需求 6 设备侧三问（哪些 action 归设备 / 身份从哪来 / 不可达怎么办） | §1 已把「服务端如何跳过」从待定变成事实 |

## 10. 我造成的一处需要修复的仓库状态

用 `git commit --amend` 改提交信息时，HEAD 已被并发会话推进到它的
`af604844`，我的 amend 覆盖了对方的提交信息，产出 `f570b188`。

- **无内容丢失**：`git diff af604844 f570b188` 为空，两者树字节相同，
  `f570b188` 实为空提交，唯一区别是标题；
- 对方原信息仍在 `af604844` 对象里，reflog 也指着；
- 全部未推送，事故困在本地；
- **未自行修复**：修它要 rebase 改写共享历史，而并发会话正以每十几秒一个
  提交的节奏在同分支写——在它写的时候动 HEAD 很可能毁掉它下一个提交。

等对方空闲时：

```
git rebase --onto af604844 f570b188 main
```

## 11. 未验证（照旧直说）

- 需求 6 设备侧**一行未实施**，真机端到端**从未验证**；
- 需求 1 真实 IMAP MOVE **未授权**，从未真实发生；
- 需求 3 飞书「核验」列在**真实表格**与**真机发票页**的呈现都没看过；
- 邮件详情渲染（`54d2fc51` / `3404057b` 的 MIME 修复）**只在 node 测试验过，
  真机未重验**；
- 需求 5 A4 拼版此前验过，但用的是构造数据，非真机打印。
