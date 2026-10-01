# 更正二则：定时流水线是通的；「重要提醒靠规则引擎」是错的归因

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

## 给用户的两条数据卫生提醒

1. 真实库里有 52 行 `importance` 是当前生产代码写不出来的带外数据，
   其中 24 行 `notified_at` 也被写过。通知中心现在展示的是**演示数据**。
   如果要拿它评估提醒效果，得先清掉再重配 rules 后重跑。
2. `email_accounts.rules` 全空这件事本身没有任何告警——流水线只在
   「有邮件 importance 为空」时才说一句话，而那句话原本指错了方向。
