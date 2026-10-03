# Round 19 — 需求 2/3/4/7 量化 + 两次自我推翻

> 2026-10-02 20:42–21:45。本轮没有新增需求实现，全部产出是**把「未知」变成有数字的事实**，
> 外加 3 个已修缺陷和 2 条被数据否掉的结论。
>
> **本轮 5 个提交尚未推上 origin/main**，原因见 §6。

## 0. 两条被数据否掉的结论（先说这个）

### 0.1 「已建档但缺开票日期的发票永远不会被补」——错

我原本的推理：`extractInvoiceCandidates` 里幂等跳过（`GetInvoiceByEmailID` 无错就
`continue`）发生在 `invoiceBodyReason` 的日期判定**之前**，所以一张已经建档、
`invoice_date` 为空的发票不会 ever 再进 `date` 分支拉原文补日期。

**实测否掉了**：真实库里 3500 那张的 `invoice_date` 已经是 `2026-09-24`，
`updated_at` 10-02 04:00（建于 03:58）——它当天就被补上了。新写的诊断里
`reason=date` 为 **0 封**，正说明当前没有缺日期的行。**不作为缺陷上报。**

### 0.2 「台账漏了一张 1280 的发票」——错，而且差点报出去

`data/email-invoices/ws_user-admin/` 下有 6 个 PDF，库里只有 1 行有 `file_name`。
按「无对应行 = 业务损失」读，结论会是「台账漏了 5 张」。逐个打开后：

| 文件 | 真实性质 |
|---|---|
| `云服务开票中心-1280.00-2026-09-28.pdf`（1537B） | **IMAP 测试夹具残留**，正文含 `VAT E-INVOICE (IMAP fixture)` / `Fixture Cloud Services Co., Ltd.` / `Openpocket Demo Co., Ltd.`，由 `gen_fixture_invoice_test.go:29-37` 生成 |
| `云服务开票中心-发票抬头-1280.00-2026-09-28.pdf`（1537B） | 同上 |
| `财务部-0.00-2026-09-30.pdf` / `-2026-10-01.pdf`（69B） | 退化件：只有 Catalog、**0 页**（`/Type/Page` 计数 0） |
| `杭州创客家…-3500.00-2026-10-01.pdf`（157615B） | 真实发票的旧名副本，与下一行 SHA256 **完全相同** |
| `杭州创客家…-3500.00-2026-09-24.pdf`（157615B） | 真实发票，**库里有对应行** |

台账没算那 1280 是**对的**。真正值得看的只有 1 个：3500 的旧名副本。

### 0.3 顺带撤回一条归因

我曾说旧名副本是「日期纠正后改名留下的残留」。**错的**：`UpsertInvoice` 的
`ON CONFLICT (email_id) DO UPDATE` 子句**不含** `status` / `file_name` /
`file_path` / `created_at`，`RETURNING id, created_at` 回的是库里那个
`created_at`。所以行的创建时刻 10-02 03:58 是真的，而那份文件写于
**10-01 20:11——比行还早**。它是「没有对应行的文件」，来源很可能是那 29 组
IMAP/POP3 重复副本中被去重掉的那一侧（见 `diag_merge_plan_test.go`），不是改名。

## 1. 需求 1：20:24 那次同步不是调度器干的（已定论）

`accountDueForSync`（`scheduler.go:640`）只在 `now - last >= 900` 时返回 true，
`pollLoop` 每 60s 一轮（`scheduler.go:300`），所以**调度器写出的相邻水位间隔必然
落在 900–960s**。实测：

| 时间 | 间隔 |
|---|---|
| 20:14:07 → 20:24:20 | **613s** |
| 20:24:20 → 20:35:31~33 | **672s** |

都 < 900s，**机制上不可能**是它。`email_action_intents` 之外的两个写水位路径都不带
到期判据：定时流水线（`POCKET_EMAIL_PIPELINE_HOUR=9`，见
`scripts/start-pocketd-email-verify.ps1:29`）与手工 API 触发。20:24 既不是 09:00，
所以是**手工触发**。

**没有证明的部分**：具体由谁触发拿不到直接证据——运行实例的 stdout 没落在任何
可读文件里（`logs/pocketd.log` 是 0 字节、mtime 19:11:50，那是我误报的秒退进程
留下的）。

**比原问题更值得报的副作用**：每次手工同步都把水位刷成新起点，
`accountDueForSync` 要求的「静默 900s」就永远不满足，**定时轮询一次都不会触发**。
有人手工验证期间，需求 1 的「每天定时」那条腿等于停用，而从水位里看不出来。

## 2. 需求 2：预演报 1 封，真跑会移 2 封（`dc372be1`，已修）

`cleanSpam` 的 dry-run 分支在**账户**循环里 `rep.SpamDryRun++`，数的是账户数；
而字段注释写「这 SpamDryRun 封」、日志写「%d mail(s) would be moved」、真实分支
`SpamMoved += moved` 数的也是**邮件**。三处口径不一致，方向是**报少**——而预演
恰恰是「开 MOVE 前看的那个数」。

真实库当场抓到（新增的只读诊断跑**生产代码路径** `Pipeline.cleanSpam` 本身，
不是重抄判定）：85 封在 7 天窗口内，**1 个账户里 2 封**「【阿里云】云安全中心周报」
被判垃圾（依据=退订特征:取消订阅），旧实现报 **1 封**，真跑会移 **2 封**。修后
同一份数据报 2。

**为什么既有测试没抓到**：`spam_clean_real_branch_test.go` 那条 `SpamDryRun != 1`
用的是**单封**夹具，账户数与邮件数恰好都是 1，新旧两种语义都通过。要分开它们
必须让两者不等——新判据在**同一个账户**放 3 封。负控：退回 `++` 后转红，
`SpamDryRun = 1, want 3`，与真实库症状同形。

### 需求 2 的当前状态（全部实测）

| 项 | 值 |
|---|---|
| 7 天窗口内邮件 | 85 封 |
| 判定为垃圾 | **2 封**（同一账户的阿里云周报 ×2） |
| 近门槛（卡在 100 分线下） | **0 封** |
| 真实 MOVE 发生次数 | **0** |
| `email_action_intents` | **0 行** |
| `email_accounts.rules` | **5 个全 NULL**（→ route-folder 意图无从产生） |

所以「授权开真实 MOVE」现在有依据了：**真开只会移那 2 封**。但该不该把它们从收件箱
移走是产品决定（规则说它们是广告，因为发件人带退订链接）。

## 3. 需求 3：积压量化 + 候选判据在真实数据上精确率 0/5

新增两个只读诊断（门禁 `POCKET_DIAG_INVOICE_BACKLOG=1`，连接上先
`SET default_transaction_read_only = on`，只读由**数据库强制**而不是靠读代码保证）。

**积压**（`e51708b7`）：窗口内 128 封、已建档 2 封、未建档 126 封；需要拉 IMAP
原文的 `reason=candidate` **5 封**、`reason=date` **0 封**。判定链复用生产函数
（`ListEmailsSince(90d,2000)` → `GetInvoiceByEmailID` 幂等跳过 → `ExtractInvoice`
→ `invoiceBodyReason`），不重抄。

**那 5 封没有一封是发票**：

```
[candidate] xiaomi.com    Xiaomi MiMo API 开放平台扣款成功通知
[candidate] amazonaws.com  所需操作：AWS 账户提示
[candidate] amazonaws.com  Amazon Web Services Account Alert
[candidate] amazonaws.com  AWS 账户提醒
[candidate] apple.com      来自 Apple 西湖商务团队的问候
```

即候选判据在当前真实数据上精确率 **0/5**，而每命中一封的代价是一次完整 IMAP
会话（dial/login/select/fetch），预算 `maxInvoiceBodyFetches=24/轮`。这个数字
此前谁都没有：既有测试用合成夹具，报告里的 `InvoiceCandidatesScanned` 要等真跑
一轮才可见，而定时流水线每天只跑一次。

**顺带一个真发现**：58000 那张发票行是**规则提取器**（`extracted_by='rule'`，不是
AI）从一封 ICBC **Statement**（对账单）建出来的，`invoice_date='2026-10-25'`
——**比采集当天晚 23 天**，`invoice_no` 为空。好消息是它**不进合计**
（`InvoiceCountsTowardTotal` 要求 `status ∈ {downloaded, filed}` 且有文件，
`status=new` 被排除），所以没污染金额；风险在文件名：采集成功后规范名里的日期会是
23 天后。**未验证**：规则提取器到底从**正文**抓到了什么才判成发票——正文在库里
加密（`body_path`），没解开，所以只报「从一封 Statement 建了发票行」这个已证事实。

### 3.1 判据自己踩了一次坑（`b0f14e10`）

`classifyOrphanPDF` 第一版只扫裸字节，于是把两份夹具报成「疑似真实」——
**这份诊断自己产出了它本该防止的那个假结论**。形态是「判据指向了容器而不是
内容」：那句 `IMAP fixture` 在 /FlateDecode 流里，裸字节根本搜不到（人手工解压
才看得到）。

只打日志的诊断没有负控，所以补了单测 `TestClassifyOrphanPDF`。样本用一份
**事先验证过的定长压缩载荷**（base64 内嵌，裸字节搜不到标记、解压后才有）——
前提不能交给 deflate：短文本会被原样存成字面量，标记仍留在压缩字节里，那是会随
zlib 版本变的实现细节。**负控**：摘掉解压 → 转红，报的正是
`classifyOrphanPDF = "真实候选"`，与真实误判同形。

## 4. 需求 4：32 条积压提醒会一次性推出（`d6f1ce1b` 加了可见性）

真实库 128 封**全部已分类**（high 56 / medium 46 / low 26，无空值）——我此前
「importance 恒为空」的说法已被 `pipeline.go:923-931` 记为证伪。`email.important`
通知 **24 条，全部集中在 10-02 05 点**，之后 0 条；未提醒的 high **32 封**。

为什么 05:38 之后没有新提醒，两个原因都不是链路坏了：

1. **今天 09:09 确实跑过一次定时流水线**——证据是那张工行 58000 的发票在
   09:09:43 建档（来自 09:07 收到的那封）。它产出 0 条提醒，是因为那封当时还没
   被分类。
2. **提醒窗口在今天 16:02 才从 2 天放宽到 90 天**（`37c53e6d`），
   `importantReminderLookbackDays = 90`（`pipeline.go:854`）。在那之前跑的都是
   2 天窗口版本。

**时敏推论**：现在跑着的二进制（18:52 构建）**已经包含 90 天窗口，而这个窗口
一次都还没跑过**。用与生产等价的判据算出候选是 **32 封**，而 `notifyImportant`
（`pipeline.go:894-901`）**没有任何限流**。**下一次流水线跑起来会一次性推 32 条
提醒。** 这是从代码+数据推出来的，不是观测到的。

`d6f1ce1b` 加了 `RemindersPending`：在进入推送循环**之前**赋值，于是推送失败时
`Pending > Sent`，差值就是「本该提醒却没提醒出去」的条数。`RemindersOutOfWindow`
救不了这个场景——它数的是窗口**外**的，而积压全在窗口**内**。负控：把赋值挪进
推送循环 → 转红（`RemindersPending=0, want 5`）。

## 5. 需求 7：分类筛选无缺口；邮件详情修复是重复实现

**分类筛选：三层取值完全一致**——后端 `categoryWhitelist`（`classify.go:5-7`）、
前端 `EmailCategory`（`api/email.ts:77-78`）、`INBOX_CATEGORY_CHIPS` 都是同样 6 个
值（work/bill/notification/personal/marketing/spam），连别名表（ad/ads/advertisement/
promo → marketing）都同源。真库里现有的 4 个值全部可筛。**查了，是好的。**

**邮件详情首屏全是协议头**：我独立实现了修复（`looksLikeMime` 按结构找头尾而不是
按位置截 4000 字符），随后发现 origin/main 上已有 `54d2fc51` 做同一件事。逐行比对
后**对方是超集**：

| | 我的 8cf0f1ca（已丢弃） | 对方 54d2fc51 |
|---|---|---|
| 头部判据 | `splitHeadBody(s).headers`，无上界 | `splitHeadBody(s.slice(0, 256KB)).headers` |
| 上界理由 | 无 | 明确写成**畸形报文的硬边界**，不是「预计头部长度」 |
| `mime-version` 判据 | **没有** | **有** |
| 测试断言样本顶出 4000 | 有 | 有 |

所以我把 `8cf0f1ca` **reset 掉了**。推一个近重复上去只会制造合并冲突，然后逼
合并的人在两版里挑一个——那正是「静默覆盖对方工作」。

## 6. 五个提交尚未推上 origin/main

```
b0f14e10  磁盘/库对账：4/5「孤儿」是测试数据
e51708b7  需求 3 发票积压诊断
dc372be1  SpamDryRun 单位缺陷（预演报 1 实移 2）
d6f1ce1b  RemindersPending
9bb9605e  设备本地模式发票合计恒 ¥0.00
```

**技术条件已齐**：与 origin/main 现有提交（含并发会话新加的 6 个）**零文件交集**，
合并不会冲突。**唯一障碍**是在主工作区直接 `git merge` 会被并发会话对
`frontend/src/features/email/__tests__/invoice-totals-chain.test.mjs` 的未提交改动
挡住。没做 stash、也没 checkout 它——并行会话的 `git stash -u` 卷走过未提交工作，
那是已吃过的亏。

另建了本地锚点分支 `mvs/email-fixes-20261002` → `b0f14e10`，防止并发会话动 main
时这 5 个提交变孤儿。

⚠️ **提醒**：并发会话工作区里那份 `invoice-totals-chain.test.mjs` 比它**自己已经
推上 origin/main 的版本更旧**（origin/main 那份是 4 行夹具 + 断言计入张数，9565
字节；它本地那份 9361 字节、2 行、无张数断言）。它此刻若 `git commit -a` 会用弱
版本覆盖强版本。

## 7. 待拍板清单

| # | 事项 | 性质 | 备注 |
|---|---|---|---|
| 1 | 5 个提交怎么上 main | 授权 | 零冲突，只差推送授权 |
| 2 | 3500 的 2026-10-01 旧名副本（157KB）删不删 | 授权 | 是凭证文件，不擅自删 |
| 3 | 58000 误建档行怎么处理 | 产品 | rule 从 Statement 建出发票，未来日期 23 天 |
| 4 | 32 条积压提醒要不要限流 | 产品·时敏 | 不限流就会一次性推 32 条 |
| 5 | 需求 6 归属路由三问 | 产品 | 见 `2026-10-02-demand6-ondevice-plan.md` |
| 6 | `06ae350f` 迁移落列的重启 | 授权 | 已定归属：由并发会话重启，本轮未动 PID 13656 |

**需求 6 设备侧仍是一行未实施；真机端到端从未验证。** 本轮所有结论都来自单元测试
与只读诊断，**没有一条经过真机**。

## 8. 验证口径

- `internal/email` 全量：ok 100.3s / 104.9s / 101.8s（三次，`-count=1`，0 cached）
- `internal/server` 全量：ok 42.0s；PG 隔离护栏每次转绿
- 前端：`test:all` fail 0、`typecheck` 干净
- 负控：3 处（`SpamDryRun` 单位、`RemindersPending` 位置、夹具解压）全部实测转红
- go vet 两包干净

注：全量 `npm run test:all` 曾在 3 条 `error-message.test.mjs`（gatewayAdminMissing
相关）上红，属并发会话 21:18–21:20 正在改 `error-message.ts` + 9 个语言包的中间态，
单跑该文件 18/18 全绿。
