# 邮箱配置部署到真机：一次性操作手册

目标需求：把 5 个真实邮箱的配置存到服务端（admin 名下），再同步到真机客户端本地库。
本文记录**已验证的步骤**与**仍然卡住的点**，避免下次重复排查。

## 1. 已完成并验证（2026-10-01）

### 1.1 服务端 SSOT：5 个账户，IMAP + SMTP 齐备

`POST/PUT /api/email/accounts`（admin 作用域）。部署脚本：

```bash
KAIXUAN_PW=... QQ_PW=... N163_FK_PW=... N163_FK1_PW=... N163_KH_PW=... \
  node scripts/deploy-email-accounts-to-device.mjs --base http://127.0.0.1:18099 --prune
```

实测 **13 PASS / 0 FAIL**，回读校验逐账户确认 IMAP 主机端口与 SMTP 均已落库。

> **本轮修掉的真缺口：SMTP 全部未配置。** 5 个账户原先只有 IMAP，
> `POST /api/email/accounts/{id}/test-smtp` 对每一个都返回 **400**。
> 原因是账户早于 `UpsertSMTPSettingsScoped` 的写入入口存在时创建，
> 之后没人补。目标里两个协议都给了参数，所以这是必须补的。

| 账户 | IMAP | SMTP |
|---|---|---|
| huangxutao@kxpms.cn | imap.exmail.qq.com:993 | smtp.exmail.qq.com:465 |
| 56551681@qq.com | imap.qq.com:993 | smtp.qq.com:465 |
| feikemanager@163.com | imap.163.com:993 | smtp.163.com:465 |
| feikemanager1@163.com | imap.163.com:993 | smtp.163.com:465 |
| kimmy.huang@163.com | imap.163.com:993 | smtp.163.com:465 |

**已删除**两个非目标账户（会随需求 8 同步进真机列表）：
`invoice-fixture@example.com`（IMAP 夹具）、`audit-poc@pocket-audit.test`（审计 PoC）。
它们的邮件/发票历史数据仍在库里，只是账户不再出现。

### 1.2 凭证有效性：真实 IMAP 登录 + 整轮同步

- 三个 163：`lastSyncedAt` 持续刷新，单账户 245~501ms。
- 企业微信 `huangxutao@kxpms.cn`：直连 TLS + LOGIN **698ms 返回 `a2 OK Success login ok`**，
  凭证无误。写入凭证后整轮同步从 **1m20s 降到 1.07s**。
- QQ `56551681@qq.com`：POP3 路径，440~549ms 稳定；发票已落库
  （`其他-杰赛云…-3500.00-2026-10-01.pdf`，`fileSource=pdf-url`），
  文件名符合目标的 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`。

整轮（5 账户）：`POST /api/emails/sync` → **2911ms**，`{"synced":5,"new":2}`，`failed` 为空。

### 1.3 需求 8 的 LWW 下发链路无回归

`node --test src/features/email/__tests__/account-sync.test.mjs` → **5/5 PASS**
（`account-lww.ts` 是生产与测试共用的纯模块，不是复制品）。

## 2. 关键环境事实（别再走错）

### 2.1 真机连的是 **18099**，不是 8088

- `frontend/.env.android-dev` 写的是 `http://192.168.31.20:8088`；
- 但真机 App 登录页显示的后端地址是 `http://127.0.0.1:18099`，
  `wt3/scripts/start-local-backend.ps1` 的注释也明确写了这一点。
- 18099 由 `powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1` 启动，
  **每次真机验证前先确认它在听**（脚本注释里记了一次「后端没了导致任务列表全空、
  看起来像功能坏了」的误判）。
- 18099 实例的邮箱 master key 在 `wt3/backend/data/email_master.key`（自动生成，跨重启稳定）。
  换 worktree 起实例会生成**另一把** key，症状是所有账户
  `decrypt credential: cipher: message authentication failed`。

### 2.2 adb

- adb 路径：`C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe`（不在 PATH）。
- 设备：`192.168.31.19:5555`。

### 2.3 发票文件下载/导出 404 与 400 的根因：`dataDir` 跟着进程 CWD 跑

**症状**：在 18099 实例上
- `GET /api/emails/invoices/{id}/file` → **404**
- `POST /api/emails/invoices/export` → **400 `no harvested invoice files in selection`**

**看着像功能坏了，其实不是。** `cmd/pocketd/main.go:65` 是
`dataDir := filepath.Dir(cfg.DBPath)` —— `dataDir` 由**相对**的 `DBPath` 派生，
进程 CWD 一变目录就跟着变。发票采集时写在主工作区 `C:\workspace\openpocket\data\`，
而 18099 实例的 dataDir 指向 `wt3\logs\pocketd-data`，`os.Stat` 自然找不到。
这与 handoff §7b.5 记的 master key 问题是**同一个根因**。

**证实方式**（不是靠推理）：用同一个二进制起一个 `POCKET_DB_PATH` 指向主工作区的
隔离实例（端口 18100），同一张发票立刻正常：

```
单张下载   157,615 bytes  magic=%PDF
A4 2x2     count=1 skipped=0  invoices-a4-2x2-20261001-181824.pdf  157,061 bytes %PDF
A4 3x3     count=1 skipped=0  invoices-a4-3x3-20261001-181824.pdf  157,059 bytes %PDF
```

> **顺带记一个我自己踩过并已排除的误判**：单张导出后我查 `/MediaBox` 看到
> `595 x 396`，而代码常量 `a4HeightPt = 841.89` 是 A4 竖版，于是判「页面被裁成横版」
> —— **这是错的**。原始发票本身就是 `595.2756 x 396.8504`（横向），
> 单张时 pdfcpu 不触发网格重排，保持原尺寸是正确的。
> 多张才排成 A4 竖版，`TestExportInvoiceGrid_2x2FitsOneA4Page` 8 个用例全绿可证。
> **教训：看到尺寸与常量不符，先查被测文件本身，别直接判缺陷。**

### 2.4 A4 导出的调用姿势（两个都踩过）

- `POST /api/emails/invoices/export` 的 `grid` 在 **body** 里（`{ids:[], grid:2|3}`），
  放 query 会 400；
- `ids` **必填**，省略同样 400（`ids required`）；
- 该端点返回 JSON（含 `url`），PDF 要再 `GET <url>` 下载，不是直接返回 PDF。

### 2.5 通知中心 53 条 email.important 里的「重复标题」不是重复提醒

我一度判定这是缺陷（同一封邮件被提醒多次），**已排除**。判据：

```
通知 53 条 / 唯一标题 46
「重要邮件：您的额度即将用尽」×4 → created_at 全部 = 04:04:12（同一秒）
「重要邮件：生产环境变更」×3   → 01:40:28, 01:40:28, 23:30:59（跨轮）
```

同一封邮件不可能在**同一秒内**被派发 4 次（`notifyImportant` 是 for 循环逐封发，
一封一次）。所以那 4 条是**主题相同的 4 封不同邮件**（信用卡日汇总、额度告警
这类周期性通知本就同主题）。跨轮的那 3 条更是分属不同时间的不同邮件。

去重逻辑本身也是对的，已核源码：
- `ListEmailsSince` 一次 query 同时取 `emails` 与 `notified_at`，两个切片**严格对齐**
  （`store_pipeline.go:64`），`splitReminderCandidates` 按下标配对成立；
- `MarkEmailsNotified(ids, now)` 在 `notifyImportant` 末尾统一回写。

> **教训**：看到「同一个标题出现 N 次」不要直接判重复推送。
> **先看 created_at 是否相同** —— 同秒 = 不同邮件，跨秒才可能是重复。

## 3. 腾讯系 IMAP 间歇性挂 80s（现象已定位到「只在腾讯系发生」，根因未定位）

**现象**：`POST /api/emails/sync` 或 scheduler 轮询时，**只有腾讯系两个账户**会挂满 80s：

```
[email/fetcher] imap login huangxutao@kxpms.cn failed: in response: cannot read tag:
    read tcp 192.168.31.20:53479->36.158.243.217:993: i/o timeout — trying POP3 fallback
[email/fetcher] huangxutao@kxpms.cn sync trace total 1m20.136s
```

**分布（18099 实例全量日志）**：kxpms 20 次、QQ 7 次、**三个 163 账户 0 次**。
挂死全部发生在**每小时的 `:59` 分**（18:03:59 / 18:07:59 / … / 18:42:59），
与 `pollLoop` 的 1 分钟 ticker 对齐。下一分钟往往立刻恢复正常（959ms / 513ms）。

**80s 的构成**：`imapIdleTimeout=60s` 打断挂住的读 + POP3 降级预算耗尽，
所以总时长 ≈ 60 + 20 = 80s。超时机制本身按设计工作。

### 已排除的五个方向（都做过对照，不是推理）

| 假设 | 实验 | 结果 |
|---|---|---|
| 凭证错 | 直连 TLS + `LOGIN` | 617ms `a1 OK Success login ok` ✅ 凭证有效 |
| 某个 IP 有问题 | 4 个 IP 逐个直连 LOGIN | 全部 608–701ms 成功，含挂死时用的 `36.158.243.217` ✅ |
| 服务端限流 | 连续 6 次快速 LOGIN | 全部 173–259ms 成功 ✅ |
| 并发压力 | 5 账户并发 LOGIN | 全部 47–681ms 成功 ✅ |
| 两个实例抢连接 | 停掉 18100 隔离实例后观察 4 分钟 | 仍挂（18:46:59 QQ 挂 80s，18:47:40 恢复 513ms）❌ 假设不成立 |

另外排除了进程侧：18099 进程 CPU 12.5s / 内存 60MB / 993 端口 Established 连接 **0 条**
（无 goroutine/连接泄漏）。

### 尚未定位的部分

**只在腾讯系发生，且直连完全正常** —— 说明差异在 go-imap 客户端与 pocketd 的
交互上，而非网络或账号。已排除的还有：`sendClientID`（失败点在 LOGIN 之前，
`ID` 在 LOGIN 之后才发）、`ListEmailsSince` 切片错位（两切片同 query 严格对齐）。

**建议的下一步**（需要能反复复现的环境）：
1. 给 `f.login` 加逐步打点（`Capability` / `Login` 分开计时），
   确认是卡在 TLS 后首个读，还是 LOGIN 应答本身；
2. 挂死时抓 `netstat -ano` 看该连接是否已 ESTABLISHED 且无在途数据；
3. 对照试验：把 `imapDialWithTimeout` 换成不带 `deadlineConn` 的裸连接，
   看是 `deadlineConn` 干扰了 go-imap 的读写时序，还是纯服务端行为。

> 影响面有限：80s 上界有生效（不会无限挂），下一分钟自动恢复，
> 5 个账户整轮同步通常 1.5–2.9s。**不阻塞部署，但会让偶发轮次变慢。**

## 4. 仍然卡住：adb 会话 offline（未完成）

**现象**：`adb connect 192.168.31.19:5555` 返回 `already connected`，
但 `adb devices` 始终是 `offline`；`ping` 通、`Test-NetConnection` 到 5555 也 **True**，
即 TCP 层活着、adbd 协议层不响应。`kill-server` / `start-server` / `disconnect` 后重连均无效。

**已排除**：本机 adb server 状态、端口连通性、同网段其它主机
（`192.168.31.29:5555` 存在但连接被拒，是另一台设备）。

**唯一解**：在手机上把「无线调试」关掉再打开（必要时重启手机），
让 adbd 重新监听并接受本机密钥。恢复后：

```bash
$adb='C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
& $adb connect 192.168.31.19:5555
& $adb devices                      # 期望 "device"，不是 "offline"
& $adb -s 192.168.31.19:5555 reverse tcp:18099 tcp:18099
```

**adb 不可用时的退路**：真机与本机在同一 WiFi（`192.168.31.19` / `192.168.31.20`），
`192.168.31.20:18099` 从局域网**实测可达**。App 侧改用
`http://192.168.31.20:18099` 即可直连后端，不需要 adb reverse。
但**装机、截图、UI 自动化仍必须 adb**，所以它终究要恢复。

一键脚本（设备 online 时直接走完连设备 → reverse → 装 APK → 校验包名与后端）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install-apk-to-device.ps1
```

## 5. 另外两项卡在**外部凭证**，不是实现缺失

这两项我已确认链路存在、缺的只是配置，因此**无法自行验证**，需要你提供。

### 5.1 飞书推送（目标：发票发到飞书 / 共享台账）

需要：

| 环境变量 | 用途 |
|---|---|
| `POCKET_FEISHU_APP_ID` | 飞书应用 |
| `POCKET_FEISHU_APP_SECRET` | 同上 |
| `POCKET_FEISHU_INVOICE_CHAT_ID` | 发票推送到哪个群 |
| `POCKET_FEISHU_INVOICE_FOLDER_TOKEN` | 可选：共享台账建在哪个云空间目录 |

本机**完全没有**这些凭证，已查：环境变量、`wt3/scripts/start-local-backend.ps1`、
以及 seed 脚本依赖的 `~/workspace/ai-native-tools/envs`（该 loader 路径在
Windows 下解析不到 `~/workspace`，也不存在）。

> 电子表格链路（建表/写单元格/读回）此前已在真实租户验证过（handoff §8 记
> `code=0`）。**未验证的只剩「发送消息到指定群」**，因为它需要 CHAT_ID。
> 应用还需开通「查看、评论、编辑和管理电子表格」权限，否则报 1310213。

### 5.2 重要邮件提醒的 AI 分类（目标：对其它重要邮件进行提醒）

提醒链路**本身是通的**：通知中心有 53 条 `email.important`，真实数据可查。
但触发条件是 `importance='high'`，而 `importance` 由 kxmemory 写入。启动日志明写：

```
INFO: POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled
Email scheduler started (fetch_enabled=true, kxmemory=false, ...)
```

本机没有 kxmemory 服务（查过：只有两个 `local-asr-server.py` 进程在 18900，
不是它）。所以需要 `POCKET_KXMEMORY_BASE_URL` 指向一个可用的 kxmemory 实例。

> 不配的后果可量化：最近一轮流水线 `remindersScanned=155`、
> `remindersUnclassified=5`、`remindersSent=0` —— 报告上的 0 分不清
> 「这批邮件确实不重要」和「邮件根本没被分类过」。这正是 handoff §7k 补
> `remindersUnclassified` 计数要解决的问题，现在它把缺口如实暴露出来了。

## 6. adb 恢复后要做的事

1. `adb reverse tcp:18099 tcp:18099`，确认 App 能登录。
2. 打开 App 的邮箱设置页，触发需求 8 的账户同步（登录后自动跑一次），
   确认列表里是 **5 个**真实邮箱、没有夹具账户。
3. 截图留证：这是本目标唯一还没拿到证据的一环。
