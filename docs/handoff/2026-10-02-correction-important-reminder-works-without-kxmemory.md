# 更正：「重要邮件提醒」结构性失效 —— 记录错误，功能其实是通的

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**更正记录，无代码改动**（归因部分已被 2026-10-02 后续更正，见文末）

> **⚠️ 本文第 24-34 行的归因有误，2026-10-02 已更正。**
> 规则引擎确实是 importance 的写入路径之一，但它要生效必须该账户配了
> `email_accounts.rules`；真实库 5 个账户 rules **全为 NULL**，这条路径
> 一次都没执行过。「真实数据上重要提醒是通的」这个推论不成立。
> 仍然成立的是：提醒的**投递链路**已实测跑通（reminders=24）。
> 详见 `2026-10-02-correction-scheduled-pipeline-and-importance-provenance.md`。

对应需求「对其它重要邮件进行提醒」。

---

## 错在哪

旧记录（handoff 与 todo 列表）写的是：

> **重要邮件 AI 分类缺 kxmemory**：`POCKET_KXMEMORY_BASE_URL`。未设时
> `importance` 恒为空，`remindersSent` 永远是 0，**需求结构性无法工作**。

**这个结论是错的。** 它把「AI 分类缺配置」误当成「重要提醒无法工作」。

---

## 实测证据

### 1. `importance` 并不依赖 kxmemory

`fetcher.go:771`：

```go
case rules.ActionMarkImportant:
    em.Importance = "high"
```

这是**本地规则引擎**（`internal/email/rules`）的路径，与 kxmemory 完全无关。
AI 分类只是**另一条**写 `importance` 的路径（`SetClassification`）。

### 2. 真实库里的分布

```
importance 分布：
  (null/空) -> 77 封
  high      -> 28 封
  medium    -> 14 封
  low       ->  1 封

importance='high' = 28 封；已提醒(notified_at>0) = 24 封
```

**28 封已判重要，24 封已提醒。** 24 封几乎全部是 CI 失败告警邮件
（`[halfking/... Run failed: ...`），正是该被提醒的那类。

### 3. 提醒真的发出去了

`Notifier` 是 `notifycenterEmailNotifier`（`server_email_pipeline.go:198`），
派发到**站内通知中心**，不依赖 kxmemory 也不依赖飞书。

日志（`logs/pocketd-18099-20261002-013559.err.log`）：

```
[email/pipeline] reminders sent: [[halfking/Trendaradar] Run failed: Hot News Crawler - main (0457e22)
  [halfking/pocket-opencode] Run failed: frontend - main (914b567) ...
```

`GET /api/notifications` 实际返回：

```
[email/email.important] 重要邮件：[halfking/Trendaradar] Run failed: Hot News Crawler - main (0457e22)
[email/email.important] 重要邮件：[halfking/pocket-opencode] Run failed: frontend - main (914b567)
[email/email.important] 重要邮件：[halfking/pocket-opencode] Run failed: backend - main (914b567)
...
```

**端到端已验证：IMAP 收信 → 规则判定 importance=high → 流水线扫描 →
派发到通知中心 → API 可读。**

---

## 预演报告佐证

`POST /api/email/pipeline/run {"dryRunSpam":true}` 的返回里：

```json
"remindersSent": 24,
"remindersScanned": 37
```

`remindersUnclassified` 字段（`omitempty`）没出现 = **0**。也就是 37 封
进入提醒判定的邮件**全部已被分类过**，没有一封卡在「未分类」。

> 这条也说明旧记录里"`remindersUnclassified=47`"同样是当时那批数据的快照，
> 不是稳定的系统状态。

---

## kxmemory 到底影响什么

不是「重要提醒」，而是 **AI 增强的那部分**：

| 能力 | 缺 kxmemory 时 |
|---|---|
| 重要邮件提醒（站内通知） | **正常工作**（走本地规则引擎） |
| `category` 分类 | 规则可写部分正常；AI 分类的细分标签缺失 |
| `ai_summary` / `suggested_action` | **不产出**（这两个字段由 AI 写） |
| 发票候选判定 | 正常（`InvoiceCandidate` 是纯规则） |
| 清垃圾判定 | 正常（`LooksLikeSpam` 是纯规则） |
| 每日总结 | 依赖 kxmemory 生成，缺失则退化为无 AI 的版本 |

所以「待 kxmemory」这条待办的**内容要改**：它挡的是 AI 摘要/建议/每日总结，
不是重要邮件提醒。

---

## 复现命令

```bash
# 1) 通知中心里看实际派发
GET /api/notifications?limit=5

# 2) 流水线上报
POST /api/email/pipeline/run  {"dryRunSpam":true}
#    看 remindersSent / remindersScanned / remindersUnclassified
```

> PowerShell 控制台显示中文通知标题时会出现乱码（如 `ééæ¬é¢`），
> 那是终端编码问题，**接口返回的字节是对的**。不要据此误判为数据损坏。
