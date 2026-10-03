# 更正记录（三则）：定时流水线是通的；「重要提醒靠规则引擎」是错的归因；「没有任何写入路径」证据不成立

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：更正记录 + 一处提示文案修复

---

## 更正 1：每日定时流水线**不是**死代码

上一轮会话给出的口头结论是「`SetPipelineRunner` 全仓从无调用点，
`POCKET_EMAIL_PIPELINE_HOUR` 无人消费，每日 8 点不会自动触发，只能手工
`POST /api/email/pipeline/run`」。**这个结论是错的**，错因是只读了
`cmd/pocketd/main.go:457-490`（scheduler 构造那一段）就下了全称判断。

实际接线在同一个文件更靠后的位置：

```go
// cmd/pocketd/main.go:759-768
if emailScheduler != nil {
    emailScheduler.SetBroadcaster(srv.WSHub())
    // 每日定时流水线（收信→清垃圾→重要提醒→发票采集→飞书推送/共享汇总）
    // 这一句是「每天定时处理」成立的前提——漏掉它则清垃圾/发票采集/飞书推送
    // 只能手动 POST /api/email/pipeline/run 触发。
    emailScheduler.SetPipelineRunner(srv, cfg.EmailPipelineHour)
}
```

运行中的 pocketd 启动日志是硬证据：

```
[email/scheduler] daily pipeline runner injected (hour=8)
[email/scheduler] pipeline scheduled at 2026-10-02T08:00:00+08:00
```

`pipelineLoop`（`scheduler.go:684`）实现完整：注入时钟 `nextTimeAt` 排期、
`30*time.Minute` 超时、触发后立刻排下一天。`server.Server` 自身实现
`email.PipelineRunner`（`server_email_pipeline.go:222`），按
`EmailExecutionMode` 决定本进程执行还是委托远端。

测试也早已存在：`internal/email/scheduler_pipeline_test.go` 4 例，覆盖
「Start 前注入」「Start 后注入（cmd/pocketd 的真实顺序）」「hour<0 关闭」
「重复注入只起一个 loop」，均已提交（`88cb5a27`）。

**教训（与本仓库既往同类）**：判定「X 没有调用点」时，
`grep <标识符>` 一次只覆盖了人正在读的那一段文件。全仓结论必须由
**独立的全仓检索**得出，不能由「我读的那段没有」推出。

### 补证：2026-10-02 08:00 到点**真的跑了一次**（不只是排期成功）

上面那两行只证明「排上了」。`2026-09-30-email-pipeline-verify.md:753` 记着
「定时流水线到点执行没有等过一次真实 06:00」，这条缺口在 2026-10-02 补上了 ——
运行进程（`pocketd-18099-20261002-013559.err.log`）里 08:00 的完整一轮：

```
08:00:00 [email/pipeline] step 1/5 sync 5 account(s) (t+2ms)
08:00:00 step1 sync feikemanager1@163.com new=0 in 318ms
08:00:00 step1 sync 56551681@qq.com   new=0 in 523ms
08:00:00 step1 sync kimmy.huang@163.com new=1 in 363ms
08:00:00 step1 sync feikemanager@163.com new=1 in 346ms
08:00:01 step1 sync huangxutao@kxpms.cn new=0 in 1.125s
08:00:01 [email/pipeline] step 1.5/5 invoice candidates (t+1.126s)
08:00:01 step1.5 scanned=1 rawBodyFetches=0 fetchFailed=0 autoCreated=0
08:00:01 [email/pipeline] step 2/5 spam clean (dryRun=true) (t+1.133s)
08:00:01 spam dry-run: 0 mail(s) would be moved, 0 near-miss
08:00:01 [email/pipeline] step 3/5 important reminders (t+1.135s)
08:00:01 [email/pipeline] step 4/5 invoice harvest (t+1.135s)
08:00:01 [email/pipeline] step 5/5 push+ledger over 1 scope(s) (t+1.136s)
08:00:01 [email/pipeline] done synced=5 new=2 spam=0(+0 local) reminders=0 \
          inv={Processed:0 Downloaded:0 Pending:0 Failed:0 Skipped:0} feishu=0/0 errors=0
08:00:01 [email/scheduler] pipeline scheduled at 2026-10-03T08:00:00+08:00
```

判定（按三种情况分档，不混为一谈）：**5 个步骤全部执行 + 有 `done` 行 + 带
`reminders=`/`inv={}` 计数 ⇒ 到点触发成立**；`errors=0`；触发后立刻把下一次
排到 10-03 08:00。触发时刻 `t+2ms`，没有任何人手点也能对上整点，且跑完立刻
自排下一天 —— 这三点合起来排除「其实是有人手工 POST 的」。

这一轮的 `reminders=0` **不能**当成「importance 无来源」的证据。理由见下面
「更正 3」。

一个必须写下来的反直觉点：**`new=2` 是虚报**。库里自 10-01 23:56:52 起一行新
邮件都没有（全量 120 行的 `created_at` 落在 23:56:38~23:56:52 的 14 秒内，是
一次批量导入）。成因是 `InsertEmail` 对已存在的 id 走 `ON CONFLICT DO UPDATE`
并返回 nil，fetcher 无条件 `saved++`，重复同步被算成新邮件。已由 `03885ba3`
修掉（`InsertEmailIfNew` + xmax 判据）。也就是说**这一行日志的 `new` 在修复前
不可信**，`synced=5` / `errors=0` / 步骤齐全这几项才可信。

---

## 更正 2：「重要邮件提醒靠本地规则引擎」——机制对，但真实库里这条路没通

`231448a5` 的结论是：旧记录说「缺 kxmemory ⇒ importance 恒为空 ⇒ 需求
结构性失效」是错的，因为**本地规则引擎**（`fetcher.go:771`
`rules.ActionMarkImportant`）会写 `importance=high`，与 kxmemory 无关。

**机制描述正确，但由此得出的「真实数据上重要提醒是通的」不成立。**
规则引擎要生效，前提是该账户配了 `email_accounts.rules`。

真实库实测（2026-10-02，只读诊断，跑完已删）：

```
=== email_accounts.rules ===
huangxutao@kxpms.cn     rules=<NULL>
feikemanager1@163.com   rules=<NULL>
56551681@qq.com         rules=<NULL>
feikemanager@163.com    rules=<NULL>
kimmy.huang@163.com     rules=<NULL>
```

`rules.ParseRules("")` 直接 `return nil, nil`（`engine.go:117-121`），
`fetcher.go:756` 的守卫是 `len(rulesParsed) > 0` ⇒ **规则路径一次都不会执行**。

### importance 在生产里只有两个写入点，两个都被挡住

> ⚠️ 本节的结论已被**更正 3** 推翻或收窄：「两个都被挡住」只在 01:43:44 那一刻成立。
> 两个写入点的**枚举**仍然正确（规则引擎 + classify），但 classify 那条在 03:41 之后
> 是通的。下面保留原文以便对照。

| 写入点 | 条件 | 当前部署 |
|---|---|---|
| `fetcher.go:771` 规则引擎 | `acc.Rules` 非空且命中 | 5 个账户 rules 全 NULL ⇒ 不执行 |
| `classify_run.go:68` → `SetClassificationScoped` | kxmemory 或 LLM provider 可用 | 日志逐封报 `llmbff: no provider configured` ⇒ 失败 |

第三条路已排除：`InsertEmail`（`store.go:531`）确实插 `e.Importance`，
但 `fetcher.go` 构造 `em` 时并不给 `Importance` 赋值（`740-750` 的字面量
没有这个字段），它只由上面的规则分支写入。没有邮件头→importance 的映射。

启动日志同时确认：`POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled`。

**所以：当前部署下没有任何生产代码路径能写 `importance`。**

### 那 35 封 high 是哪来的

真实库分布：

```
importance=high     35 封   date 2026-09-30..2026-09-30
importance=medium   15 封   date 2026-09-30..2026-10-01
importance=low       2 封   date 2026-09-30..2026-09-30
importance=（空）    68 封   date 2026-09-05..2026-09-30
```

按上面两条被挡住的路径，这些行**不可能由运行中的后端写出**。
旁证：`231448a5` 当时记的分布是 28/14/1/77，现在是 35/15/2/68 ——
在两个写入点都被堵死的情况下数字仍在变，说明这些行是**带外写入**的
（最可能是前一轮为验证提醒投递链路而构造的数据）。本文不推断是谁写的。

### 仍然成立的部分

**提醒的投递链路是真的通了**，这一条不因上面的更正而动摇：

```
2026/10/02 05:38:32 [email/pipeline] step 1/5 sync 5 account(s)
2026/10/02 05:38:33 [email/pipeline] reminders sent: [[halfking/Trendaradar] Run failed: ... 您的额度即将用尽 ...]
2026/10/02 05:38:33 [email/pipeline] done synced=5 new=2 spam=0(+0 local) reminders=24 ...
```

`GET /api/notifications` 返回 `[email/email.important]` 通知。
即 `importance='high'` → 流水线扫描 → 派发通知中心 → API 可读，这条链完整。

**但要分清两件事**：

- 「投递链路可用」= 成立，已实测 24 条。
- 「稳态下会有新提醒」= **不成立**。没有 rules、没有分类器 ⇒ 新邮件
  永远拿不到 `importance` ⇒ 提醒只会停在已注入的那批数据上。

需求「对其它重要邮件进行提醒」要真正落地，得二选一（或都做）：

1. 给账户配 `rules`（含 `mark-important`），走本地规则引擎，不需要 AI；
2. 配 `POCKET_KXMEMORY_BASE_URL` 或接上 LLM provider，走 AI 分类。

这是**配置缺口，不是代码缺陷**——两条代码路径本身都通。

### 顺带修的一处：提示文案把排查方向带偏

`notifyImportant` 原来打印：

> `47/47 封邮件 importance 为空 —— 未被 AI 分类过，不会进入重要提醒（检查 POCKET_KXMEMORY_BASE_URL）`

在 rules 全 NULL 的真实环境里，这句话会让人跑去配 kxmemory，而真正缺的是
账户规则。已改为同时点名两条写入路径（`reminderUnclassifiedHint`），
并抽成纯函数 + AST 接线护栏（`reminder_unclassified_hint_test.go`，3 例）。

**只改日志文案，不改任何生产行为。**

---

## 更正 3：更正 2 的否定结论（「没有任何生产代码路径能写 importance」）证据不成立

更正 2 写的是「**所以：当前部署下没有任何生产代码路径能写 `importance`**」，
支撑证据是「日志逐封报 `llmbff: no provider configured` ⇒ 失败」。

**那条证据只覆盖 01:43:44 一个时刻，之后就不成立了。** 同一份日志：

```
01:43:44  [email/classify] …: llmbff: no provider configured      × 856160 行
03:41:29  POST /api/emails/classify - 200 (37.4s)
05:13:12  [email/classify] …: llm-gateway chat 429: rate_limit_exceeded
03:58:50 / 05:13  [email/classify] …: llm-gateway chat: context canceled
```

（`context canceled` 出现在 03:58 与 05:13 附近；`429` 一直延续到 07:58。）

到 05:13 已经不是「no provider configured」，而是实打实地打到了
`https://llm.kxpms.cn/v1/chat/completions` —— **provider 是配上的，网关在应答**。
429 是限流，不是「路径不通」：它恰恰证明请求进了网关。

### 关键的一点：分类器**成功时不打日志**

`classifyViaGateway`（`server_email_classify_gateway.go:174-189`）只在两处打日志：
解析失败（`unparseable gateway output`）和出错。而成功分支是
`SetClassificationScoped(...)` 之后直接 `return out, nil`，**一行日志都不打**。

所以「日志里 `[email/classify]` 只有失败行」**推不出「从没成功过」**——
这是把「没记录」当「没发生」。统计口径也印证：
`[email/classify]` 共 856,324 行，`no provider configured` 856,160、
`429` 117、`context canceled` 43，合计 856,320，**没有任何一行是成功日志**
（因为根本不存在成功日志这个类别）。剩下 4 行未归类，样本看也是
`context canceled` 同一族（见 L856746 起），不影响结论。

### 日志里能直接看到的「importance 从空变非空」

```
03:32:59  step 3/5 important reminders
          47/47 封邮件 importance 为空 —— 未被 AI 分类过…        ← 扫到的 47 封全是空
05:38:33  step 3/5 important reminders
          reminders sent: [[halfking/Trendaradar] Run failed: …]  ← 没有「为空」那行了
05:38:33  done … reminders=24
```

`reminderUnclassifiedHint` 只在 `unclassified > 0` 时打印（`pipeline.go:785-787`）。
05:38 那轮**没有**这行 ⇒ 该轮 `unclassified == 0` ⇒ 同一批邮件的 `importance`
在 03:32→05:38 之间被写进去了。这中间 `POST /api/emails/classify` 成功返回了 5 次
（03:41、03:58、04:12、05:13、05:28）。

写 `importance` 的生产路径只有两条（更正 2 自己列的）：规则引擎（rules 全 NULL，
`ParseRules("")` 直接 `return nil`，永不执行）与 classify 路径。前者被排除，
**所以写进去的就是 classify 路径**。

### 库侧交叉验证（只读，2026-10-02 08:1x）

```sql
-- 24 条提醒对应的行，notified_at 全部是同一个时间戳
select coalesce(importance,'(null)') imp, count(*),
       to_timestamp(min(notified_at))::timestamp(0) first_notified
from emails where notified_at > 0 group by 1;
--  high | 24 | 2026-10-02 05:38:33     ← 一次 run 写完，24/24 全 high
```

这 24 行的 `ai_summary` 是逐封不同的中文摘要（`GitHub项目Trendaradar的…工作流运行失败。`），
`suggested_action` 24/24 非空，`category` 2 种 —— 形态与
`parseGatewayClassification` 产出一致（`Importance` 缺失时兜底 `medium`，
见 `server_email_classify_gateway.go:90-96`）。

### 因此更正 2 里这两句要改口

| 原文 | 改为 |
|---|---|
| 「日志逐封报 `llmbff: no provider configured` ⇒ 失败」 | 只在 01:43:44 成立；03:41 起 classify 已连上 `llm.kxpms.cn` |
| 「**没有任何生产代码路径能写 importance**」 | **无证据**。日志无法证明（成功不打日志），且 03:32→05:38 的空→非空转换指向 classify 路径 |
| 「稳态下不会有新提醒」 | **未证伪也未证实**，取决于网关是否还配着、是否还在限流 |

需求「对其它重要邮件进行提醒」当前**可能是通的**（经 LLM 网关分类），
只是被 429 限流和「成功不打日志」这两件事同时挡住了观测。
**要判它通不通，不能读日志，得查库**：
`select coalesce(importance,'(null)'), count(*) from emails group by 1;`
并连续两次采样看数字是否在涨。

### 一处我没能对上、如实记下来

03:32:59 那轮 `ListEmailsSince(now-2d)` 扫到 47 行且全为空。但我现在用同一条
SQL（`store_pipeline.go:69`：`date >= $1 AND COALESCE(deleted_at,0)=0`）
对同一个窗口跑，只回 4 行（3 medium + 1 low）。`deleted_at` 全为 0，
总行数仍是 120。这两件事对不上，我没有找到解释，**不编**。
它不影响上面「空→非空」这个结论（那是从日志前后两轮读出来的），
但意味着「当前库 = 03:32 那个库」这个前提**没有被我证实**。

### 另一处部署状态：跑着的二进制比源码旧

- `pocketd.exe` 构建于 `2026-10-02 01:35:53`，进程 `01:36:00` 启动；
- `backend/internal/email/pipeline.go` 的 mtime 是 `2026-10-02 07:14:14`。

对得上：日志 03:32:59 打的是**旧文案**（「未被 AI 分类过，不会进入重要提醒
（检查 POCKET_KXMEMORY_BASE_URL）」），而当前源码 `pipeline.go:823-828` 打的是
点名两条路径的新文案。**即上面「顺带修的提示文案」在运行进程里还没生效。**
这不影响定时触发的结论（那部分是更早就在二进制里的接线），但任何关于新文案的
说法目前都没有运行时证据。

---

## 给用户的两条数据卫生提醒

1. ~~真实库里有 52 行 `importance` 是当前生产代码写不出来的带外数据~~
   —— **这条按更正 3 撤回**：「写不出来」这个前提没被证实。能确认的只有：
   这 120 行的 `created_at` 全部落在 `2026-10-01 23:56:38~23:56:52` 的 14 秒内
   （一次批量导入），其中 24 行 `notified_at = 2026-10-02 05:38:33`（同一轮跑出来的）。
   要判断通知中心展示的是不是演示数据，得先弄清那 14 秒是谁写的。
2. `email_accounts.rules` 全空这件事本身没有任何告警——流水线只在
   「有邮件 importance 为空」时才说一句话，而那句话原本指错了方向。
3. **分类成功不打日志**这件事本身就是个可观测性缺陷：一条功能是否在工作，
   现在只能靠查库或看 `reminders` 计数反推。这次就是因为它，差一点把
   「功能通着」误判成「功能没接线」。
