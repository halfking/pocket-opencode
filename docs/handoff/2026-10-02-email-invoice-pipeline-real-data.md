# 邮件发票链路：真实数据首次跑通（2026-10-02 04:00）

前一份 handoff 记的是摘要缺陷与 master key 事故
（`2026-10-02-email-summary-mime-and-master-key.md`）。本份记发票链路。

## 起点：`email_invoices` 0 行

需求「收取发票类的邮件…解析整理、下载、命名」在真实数据上**从未产出过任何一条**：

- 真实库 `email_invoices` 0 行；`GET /api/emails/invoices` 返回 `{"total":0}`
- 手工触发 `POST /api/email/pipeline/run`：
  `accountsSynced=5 newEmails=2`，但 `invoices` 全 0，总耗时仅 1.4s
- 服务端日志里 `[email/pipeline]` **一次都没出现过**——整条流水线从未跑过

## 排查：逐条件在真实数据上复现，不靠推理

1. 手工跑一轮流水线 → 1.4s 就结束，说明连一封原文都没拉，候选一个都没命中。
2. 拿真实库 120 封邮件直接喂给检测函数：
   - `InvoiceCandidate`（主题+摘要关键词）命中 **6** 封；
   - `ExtractInvoice`（envelope）命中 **1** 封，正确抽出
     seller=杭州创客家投资管理有限公司 / amount=3500.00 /
     date=2026-09-24 / no=26332000008261110741。
   ⇒ **检测器没问题，邮箱里也确实有发票**。
3. 逐条件复现 `extractInvoiceCandidates` 的判定链：
   - scope：5 个账户全部 `enabled && user_id != ''` → 5 个，正常；
   - **24h 窗口内只有 2 封邮件**，那张发票在窗口之外。

## 根因

`extractInvoiceCandidates` 硬编码 `rep.StartedAt-86400`（24 小时）。
定时任务每天跑一次，24h 窗口意味着历史邮件、上次同步失败期间积压的邮件、
延迟入库的邮件，**任何一次没赶上就永久漏掉**，且没有补偿机制。
于是「流水线 → 采集 → A4 网格导出 → 飞书推送 → 共享台账」整条链路
看起来都实现了，实际对真实数据从不触发。

这与本文件里已修过的 `RemindersScanned/Unclassified` 是同一类问题：
**可观测性缺口让「功能是否失灵」没法判断**——报告里 invoices 全 0 时，
「扫了 0 封候选」和「这批邮件里没有发票」长得一模一样。

## 修复（`46d9e779`）

把「envelope 判定窗口」与「拉原文窗口」解耦：

| | 值 | 为什么 |
|---|---|---|
| `invoiceCandidateLookbackDays` | 90 | envelope 判定只跑主题+摘要正则，**不碰 IMAP**，放宽几乎不增加代价 |
| `invoiceCandidateScanLimit` | 2000 | 必须配套：24h + LIMIT 500 时 500 行会被最近邮件占满（ORDER BY date DESC），窗口再宽也够不到旧邮件。取 `ListEmailsSince` 硬上限，再大会被静默重置成 500 |
| `maxInvoiceBodyFetches` | 24（不变） | 真正贵的拉原文仍受预算限制，超出顺延下一轮 |

报告新增 `invoiceCandidatesScanned` / `Created` / `BodyFetchDeferred` 三个计数。

### 测试

新增 `invoice_candidate_lookback_test.go`（5 用例），夹具照抄真实邮件
em-10435 的 subject/snippet：

- 10 天前入库的发票必须被建档，且 seller/amount/date 抽对（**正向断言**）
- 非发票邮件不得被建档（防「放宽窗口」变成「放宽判定」）
- 幂等：跑两轮不产生第二条
- 护栏：窗口 > 1 天、扫描上限在 (500, 2000]
- 接线护栏：调用点必须真的用这两个常量

负控 2 路（实测转红后恢复）：退回 `rep.StartedAt-86400, 500`
→ 2 个集成用例 + 接线护栏共 3 个转红。

**接线护栏是被自己的负控逼出来的**：第一版只锁常量，实测把调用点改回
24h 时它照样绿——常量还是 90，缺陷却装回去了。补了锁调用点的那条。
那条新护栏第一版又误报：判据匹配到了注释里的 `rep.StartedAt-86400`
（记录旧行为的那段），且函数体截取越过了收尾花括号。改为
「先 stripGoComments + 截到列 0 的 `}`」之后才正确。

回归：`go build ./...` 0、`go vet` 0、`go test ./internal/email/ -count=1`
全包 ok 26.5s。

## 真实数据端到端（不重启服务）

用临时测试按新代码跑候选建档，再调服务端接口采集：

1. 候选建档 → `window=90d scanned=120 autoCreated=1`（修复前 scanned=2 created=0）
2. `POST /api/emails/invoices/harvest`（服务端带真实 IMAP 凭据）
   → `{"Processed":1,"Downloaded":1}`，1.1s（走同步时落盘的原文缓存）
3. 落盘 `其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf`
   157,615 字节，头部 `%PDF-1.7`
   —— **正是需求要求的 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`**
4. `POST /api/emails/invoices/export {grid:2}` → `count=1`

> 顺带修好的第二件事：10-01 那次导出的是 `…-2026-10-01.pdf`（下载当天），
> 现在是正确的 `…-2026-09-24.pdf`（票面开票日期）。原因是摘要修复
> （`064ce29`）之后「开票日期：2026-09-24」才终于可解析——
> **摘要缺陷是文件名缺陷的上游**，两者叠加才让文件名对。

## A4 网格导出：验证通过，**没有缺陷**

用真实发票复制 1/4/5/9/10 份 × grid 2x2/3x3 实测
（`api.ReadAndValidate` + `PageDict` 读页面尺寸）：

| 输入 | 2x2 | 3x3 |
|---|---|---|
| 1 | 1 页 | 1 页 |
| 4 | 1 页 | 1 页 |
| 5 | 2 页 | 1 页 |
| 9 | 3 页 | 1 页 |
| 10 | 3 页 | 2 页 |

页数均 = ceil(n / grid²)，且每一页都是 **A4 竖版 595.28 × 841.89 pt
= 210 × 297 mm**，满足「按 A4 规范排版、打印后可直接剪裁」。

### 一个差点报成缺陷的误判（值得记）

我先用「扫 PDF 原始字节找 `/MediaBox`」的方式测，得到的尺寸是
595.2756 × 396.8504——既不是 A4 也不是 A4 的一半，看着就是缺陷。
实际上那是输出 PDF 里**内嵌的源发票 Form XObject 自带的 MediaBox**
（源发票本身是 210 × 140 mm 的宽幅票面），不是输出页。

**教训**：byte-scan 读 PDF 结构，在「页面里嵌了别页」的场景会读到内嵌对象。
测 PDF 几何必须走解析器。否则会把「实现正确」写成「发现新缺陷」。

## 仍未验证的链路

- **飞书推送 / 共享台账**：缺 `POCKET_FEISHU_APP_ID` / `APP_SECRET` /
  `INVOICE_CHAT_ID`（应用需开通「查看、评论、编辑和管理电子表格」，
  否则报 `1310213`）
- **重要邮件提醒**：~~`remindersSent=0` 的根因是 `importance` 恒空~~
  **（2026-10-02 05:4x 更正：这个结论是错的，见
  [correction-important-reminder-works-without-kxmemory.md](./correction-important-reminder-works-without-kxmemory.md)）**
  实际上 `importance` 由本地规则引擎（`fetcher.go:771`）写入、与 kxmemory 无关，
  真实库里 28 封 `importance='high'`、24 封已提醒，`GET /api/notifications`
  能读到 `[email/email.important]` 通知。**重要邮件提醒是通的**。
  kxmemory 缺的是 AI 摘要 / 建议 / 每日总结。
- **清垃圾真实 MOVE**：默认 `SpamDryRun=true` 预演模式（只判定不移动）。
  需先看判定结果再决定是否关掉预演
- **发票多来源**：本次只跑通了「正文 XML 链接/附件」这一类；
  「正文 PDF 链接」「多次重试才能下载到」尚未在真实数据上验证
