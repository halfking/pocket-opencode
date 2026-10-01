# 清垃圾预演实测：规则在真实数据上从未触发，阈值远高于实际得分

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**结论已定，未改代码、未开真实 MOVE**

对应需求「清理广告与垃圾邮件，将它们移到垃圾邮件箱」。

---

## 怎么跑的

预演模式（**只判定、不 MOVE**，不发任何 IMAP 命令、不改本地分类）：

```http
POST /api/email/pipeline/run   {"dryRunSpam": true}
```

返回：

```json
{ "accountsSynced":5, "newEmails":2,
  "spamMoved":0, "spamLocalOnly":0,
  "remindersSent":24, "remindersScanned":37,
  "invoices":{"Processed":0,...}, "feishuPushed":0 }
```

`spamDryRun` / `spamDryRunSamples` / `spamNearMiss` 三个字段都是
`omitempty`，响应里没有 = 判定命中 0 封、近似 0 封。

---

## 实测数据

用真实库跑 `LooksLikeSpam`（直接调产品代码，不自己拼判定）：

```
库内 120 封，最新 2026-10-01，最旧 2026-09-05（最新邮件距今 0.8 天）

lookback=  7d  扫到  77 封  判垃圾 0  有分未判 0
lookback= 30d  扫到 120 封  判垃圾 0  有分未判 3
lookback= 90d  扫到 120 封  判垃圾 0  有分未判 3
lookback=180d  扫到 120 封  判垃圾 0  有分未判 3
```

分数分布（90 天窗口，120 封）：

```
   0 分 -> 117 封
  30 分 ->   3 封
```

3 封全部是同一个发件人 `InfoQChina@edm.infoq.com.cn` 的技术 newsletter：

| 分数 | 判定理由 | 主题（截断） |
|---|---|---|
| 30 | `营销词×1:订阅; 营销发件人特征:edm` | 阿里 Open Code Review 作者…QCon上海 |
| 30 | `营销词×1:订阅; 营销发件人特征:edm` | 月满中秋，9折赴约！QCon上海站… |
| 30 | `营销词×1:精选; 营销发件人特征:edm` | 唐杰披露智谱RSI最新进展… |

**阈值是 100 分**（`spam.go:151` `if score >= 100`）。实际最高分 30，
差 70 分。所以：

> 在当前这 120 封真实邮件上，清垃圾规则**一封都不会命中**。
> 开真实 MOVE 与不开，**结果完全一样**。

---

## 更正一条旧记录

旧 handoff 记的是「444 封真实邮件命中 0 封但 **17 封**拿到 30 分」。
**17 这个数字是错的**——当前实测 120 封里只有 **3 封**拿到 30 分。
（样本库不同，但"全库只有 newsletter 类拿到 30 分"这个结论一致。）

---

## 为什么不擅自开真实 MOVE

1. **当前开了也没用**：命中数为 0，MOVE 不会发生任何事。
2. **一旦放宽阈值就会真搬用户邮件**：3 封 30 分的是 InfoQ 的技术活动推广，
   属于「可订阅的技术 newsletter」，不是垃圾广告。把阈值降到 30 就等于
   静默丢弃这类邮件。
3. 真实 MOVE **不可逆**（要恢复得手工从垃圾箱搬回来）。

所以这属于**必须由你判断**的事：这几封 newsletter 你想留还是想丢？

---

## 如果你要开

```bash
# 部署时固定注入
POCKET_EMAIL_SPAM_DRYRUN=false
```

或单轮覆盖（不改配置）：

```http
POST /api/email/pipeline/run   {"dryRunSpam": false}
```

**开之前建议先做的事**：真机/模拟器上跑一次收件箱，看这 3 封 InfoQ 邮件
在列表里长什么样，确认它们不是你会需要的。

---

## 复现命令

```bash
cd C:\workspace\openpocket-wt-maildeploy\backend
$env:POCKET_REAL_MAIL_DSN='postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_DIAG_SCHEMA='opencode_pocket'
# 判定分布需临时诊断测试（验完已删）；只看流水线上报则用 HTTP 端点。
```
