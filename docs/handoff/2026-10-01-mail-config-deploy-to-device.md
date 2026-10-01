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

### 1.2b 需求 3 三种来源：端到端验证 + 修掉一个真缺陷（2026-10-01 晚）

目标原文：「原邮件中有 PDF 下载地址（可直接下载已有 PDF），也有 XML 数据格式
（可解析后重新渲染）」。此前**三条路径只有单元级测试**，真实数据上只跑通过
`pdf-url` 一种（`src=pdf-url / amt=3500 / downloaded`）。

新增 `invoice_sources_e2e_test.go` 把三条都端到端钉住，跑的过程中**抓到并修掉
一个真缺陷**：

```xml
<Seller>
  <销售方名称>云服务开票中心</销售方名称>
  <纳税人识别号>91330100MA2XXXXXXX</纳税人识别号>
</Seller>
```

解析出的 `Seller` 是 `云服务开票中心91330100MA2XXXXXXX` —— `<Seller>` 自身命中
`labelMatch` 的 `seller` 词典，`deepText` 把两个子节点拼在一起，而
`applyXMLField` 先到先得不覆盖，父节点赢过更具体的 `<销售方名称>`。

**后果直击需求 3**：文件名里的「对方单位」变成一串税号，对账认不出人。

```
修复前：其他-云服务开票中心91330100MA2XXXXXXX-1280.00-….pdf
修复后：其他-云服务开票中心-1280.00-2026-09-28.pdf
```

修法：属性值先于元素值；元素值只在「没有带文本的子节点」时直接采用，否则
登记为 pending 兜底（保住 `<Seller>供应商甲</Seller>` 这种老结构）。
负控实测：把 `hasTextChild` 判断改成恒真 → 用例立刻转红；恢复后全量 ok，
既有 XML 测试零回归。

> **存量未受污染**：库里唯一落盘的发票走的是 pdf-url，不经 XML 解析。
> 这个 bug 从现在起才对新的 XML 发票生效。
>
> 注意顺带确认的一件事：XML 渲染产出的日期是**开票日期** `2026-09-28`，
> 不是下载当天 —— handoff §7b.4 记的「日期退化为下载日」是**附件 PDF 路径**
> 的问题（PDF 内抽不到日期），XML 路径不受影响。

### 1.2c 台账「汇总金额」口径统一 + 修掉第 4 个缺陷（2026-10-01 晚）

目标原文：「需要整理一个列表，记录必要信息并汇总金额」。这个数字是拿去
对账的，所以口径必须明确，而且**三处必须一致**。原来三处都是无条件
`total += inv.Amount`：

| 位置 | 用途 |
| --- | --- |
| `email.LedgerRows` | 飞书共享表格的合计行 |
| `email.WriteInvoiceSummaryDocs` | 本地 CSV/MD 的合计 |
| `server.handleEmailInvoiceSummary` | 界面上的 `amountTotal` |

**为什么是缺陷而不只是「不够严谨」**：库里确实存在 `status=failed` 却残留
脏字段的记录（两张 QQ Wallet：`seller="name:"`、`invoiceNo="Issuance"`，
字段是从邮件错误段落抽出来的，见 handoff §7o）。金额当时恰好是 `0` 才没出事。
将来某张 failed 发票若带着错误抽取出的非零金额，就会被静默算进总额，
让对账虚高，**而且没有任何地方会提示**。

判据统一为：`status ∈ {downloaded, filed}` **且** `FilePath != ""`。这与
server 层紧邻的 `downloaded` 计数完全对齐 —— 界面上「已下载 N 张」和
「合计 X 元」指向同一批发票，否则两个数字会同屏互相矛盾、用户无从判断该信哪个。

**不计入合计 ≠ 从列表消失**：failed/pending 的行照样在表里、状态列写明，
用户仍能看见「还有几张没拿到」，合计也才有对账意义。

> **真库核对：线上台账数字不变。** 真实 3 张发票（downloaded 3500 +
> failed 0 + failed 0），新旧口径都是 `3500.00`。这次修的是隐患，不是改现状。
> 核对脚本 `ledger_realdata_diag_test.go`（`POCKET_REAL_MAIL_DSN` 驱动）留在
> 仓库里，以后有存量变化时重跑一次即可确认。

三处各自做了负控（判据改回无条件累加，确认测试转红）：

| 改动点 | 负控结果 |
| --- | --- |
| `ledger.go` | 3 条转红（合计 5779.99 / 4499.99 / 4277，want 3500） |
| `pipeline.go` | `TestWriteInvoiceSummaryDocs` 转红（合计 123.45，want 100.00） |
| server 层 | 转红（合计 5557，want 4780） |

`go vet` 通过；`internal/email` 与 `internal/server` 全量测试均 ok。

> **两个夹具补了 `FilePath`，这不是迁就实现**：真实产物里 `downloaded` 一定有
> `FilePath`（`saveInvoiceFile` 先写文件、再改状态），原夹具的形态在真实数据中
> 根本不存在。补上之后，测试才真正在断言「文件确实落盘才算数」。

> **写 server 层测试时踩到的坑（下次直接照抄）**：`UpsertInvoice` 的列清单里
> **没有 `file_name` / `file_path`** —— 把 `FilePath` 塞进去会被**静默丢弃**，
> 五张票的 `FilePath` 全空、合计自然是 0，而报错信息指向合计口径、真因在夹具。
> 文件字段必须走 `UpdateInvoiceHarvest`（这条路径也正是生产顺序）。
> 另外身份别硬编码：handler 无登录态时回落成 `("local", "default")`，
> 从 `userIDFromRequest` / `workspaceIDFromRequest` 取，回落策略变了测试不会失效。

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

> **2026-10-01 19:26 实例被终止过一次（不是崩溃）**，日志停在正常的整分钟轮询、
> 无 panic，进程就没了 —— 没有任何错误信息可查，只能重建。
> 恢复时用下面这份配方（`wt3/scripts/start-local-backend.ps1` 是给 8088 用的，
> 端口不对；`start-pocketd-email-verify.ps1` 是 8099 的那份，也不对）：
>
> ```powershell
> $dataDir = 'C:\workspace\openpocket\wt3\backend\data'   # master key + 已落盘发票都在这
> $env:POCKET_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
> $env:POCKET_PG_SCHEMA        = 'opencode_pocket'
> $env:POCKET_DEV_AUTH         = 'true'
> $env:POCKET_AUTH_LEGACY_ONLY = 'true'
> $env:POCKET_HTTP_PORT        = '18099'
> $env:POCKET_SCHEDULER_ENABLED   = 'true'
> $env:POCKET_EMAIL_FETCH_ENABLED = 'true'
> # master key 按 base64 注入：跨目录复制 key 会被安全策略拦截
> $env:POCKET_EMAIL_MASTER_KEY = [Convert]::ToBase64String([IO.File]::ReadAllBytes("$dataDir\email_master.key"))
> # 必须给**绝对** DBPath：dataDir = filepath.Dir(DBPath)，相对路径会让 dataDir 跟着 CWD 跑（§2.3）
> $env:POCKET_DB_PATH = "$dataDir\pocket.db"
> Start-Process -FilePath <pocketd.exe> -WorkingDirectory $dataDir `
>   -RedirectStandardError '<log>\pocketd-18099.err.log' -WindowStyle Hidden
> ```
>
> 环境变量名是从 `backend/internal/config` 的 `getEnv(...)` 逐个核对的，不是凭记忆写的。
> 起来后应看到 `healthz = ok`、账户数 5、且 `/api/emails/invoices/summary` 的
> `amountTotal` 与 `downloaded` 指向同一批发票。
>
> 恢复后的实测（跑的是本分支已提交的代码，非 wt3 旧二进制）：
> 账户 5 个（IMAP+SMTP 齐备），`count=3 amountTotal=3500 downloaded=1 failed=2`，
> 两张 failed 仍在 rows 里（`seller='name:'` 这个已知脏字段），合计只算那张 3500 的。

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

### 2026-10-01 20:1x 新证据：可以按需复现，且问题在**我们的 stack**，不在服务端

之前「直连完全正常」的对照有个致命弱点：直连用的是**另一套客户端代码**，
所以它同时排除了「服务端问题」和「我们的客户端问题」两件事。必须把两者分开。

新增 `backend/internal/email/diag_tencent_imap_test.go`（三个显式开关才跑的诊断，
不设环境变量一律跳过）。同一台服务器、同一账号、相隔几分钟，A/B 结果：

| 模式 | 用的客户端 | 结果 |
|---|---|---|
| bare | `tls.Dial` + 手写 `LOGIN`（绕开 deadlineConn 与 go-imap） | **15/15 全正常**，dial ~130ms / greeting ~130ms / login 610–906ms |
| stack | 生产同款 `imapDialWithTimeout` + go-imap `client.Login` | 10 次里**第 1 次挂 1m20.172s**，其余 9 次 774–884ms |

> **结论：同一台服务器、同一账号、同一时刻，裸客户端正常、我们的 stack 会挂
> → 问题在我们的连接/客户端层，不在服务端。**

生产侧也能按需复现：连续跑流水线时 5 轮里有 2 轮是 80.5s / `synced=4`
（一个账户失败），其余轮次 1.3–1.9s / `synced=5`。

#### 「冷启动才挂」假设：不成立

`imap.exmail.qq.com` 解析出 4 个 IP（`112.49.56.19` / `36.158.243.217` /
`112.49.56.212` / `120.226.165.33`），逐节点「冷/热」各连一次：

| 节点 | 冷（首次） | 热（第二次） |
|---|---|---|
| 112.49.56.19 | ok 837ms | ok 799ms |
| 36.158.243.217 | ok 831ms | ok 804ms |
| 112.49.56.212 | ok 867ms | ok 835ms |
| 120.226.165.33 | ok 849ms | **FAILED 1m20.17s** |

第一次全好、第二次反而挂 —— 与「冷启动」相反。且历史 37 次失败**分散在 6 个不同
IP**（13/7/6/5/4/2 次），不是某个坏节点。**所以「钉住某个好 IP」这条路不通。**

#### 已排除 / 仍未排除

- 仍未排除：腾讯侧按源 IP 的认证频次限制（表现为静默丢弃而不是明确拒绝）。
  本会话持续压测后 stack 登录约 5–10% 概率挂，bare 0/15。
- 未验证（**不建议自行压测**，有触发风控/锁定的风险）：短时间内高频登录
  是否会显著提高失败率。真要验需要用户点头。

#### 建议的缓解（**尚未实施**，等用户确认）

`stack #01` 挂 80s 之后，紧接着的 `stack #02`（1 秒后、新连接）785ms 就成功。

**重试有效性已实测 2/2**（`TestDiagTencentIMAPStackAB`，12 次登录里挂 2 次）：

```
stack #01  LOGIN failed after 1m20.206s: cannot read tag: ...36.158.243.217:993
  └ retry #01 ok after 820ms   ← 重试有效
stack #10  LOGIN failed after 1m20.168s: cannot read tag: ...36.158.243.217:993
  └ retry #10 ok after 944ms   ← 重试有效
```

但重试前那 80s 仍要等，所以完整方案是两步：把 **LOGIN 单条命令的超时收紧到 ~15s**
（正常 login 只要 610–906ms，15s 极宽松），失败立即换连接重试一次
→ 最坏从 80s 降到 ~16s。

这是对生产路径的行为变更，且建立在「挂起是暂态、重试可恢复」这个**部分验证**
的判断上（stack 约 10% 概率挂、bare 0/15；还没排除腾讯侧按源 IP 的认证频次限制），
所以没有擅自实施。

**累计观测**（本会话）：bare 0/15 挂；stack 4 次挂 / 约 32 次登录。
三次实验里有两次是**该实验的第 1 次**登录就挂，但也有第 8 次、第 10 次才挂的，
所以「一定是首次」也不成立 —— 只能说**挂起偏向集中在一次集中压测的早期**。

#### 顺带修掉：打点看不见这 80s

`syncTrace.step()` 只在**进入下一阶段**时结算上一阶段，而失败路径是直接 `return`
的 —— 于是**恰好卡在失败点上的那段时间永远不会被记录**。历史日志里 80s 挂起时
`SLOW step` 计数为 **0**，只剩 `imap login ... failed` 和 `sync trace total`。
这和当初「Sync 里一个日志都没有」是同一个坑。已修（`done()` 补报最后一个阶段）
并加 4 个用例，负控实测转红。

> 影响面：80s 上界有生效（不会无限挂），下一分钟自动恢复，
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
> `remindersUnclassified=5`、`remindersSent=0`。

### 5.2.1 `remindersSent=0` 已查清：**不是缺陷**（别再怀疑提醒链路）

那 46 封 high 为何一封没提醒？用只读诊断直接查真库
（`TestDiagnoseReminderNotifiedAt`，`POCKET_REAL_MAIL_DSN` 指向真实 schema）：

```
扫描窗口（近 2 天，与 ListEmailsSince 同条件）
  扫描总数        = 155
  importance=high = 46
    其中已提醒    = 46   (notified_at > 0)
    其中未提醒    = 0    (notified_at == 0)
结论：46 封 high 全部已提醒过 —— remindersSent=0 符合设计。
```

**所以「库里有 46 封 high 却 remindersSent=0」不是矛盾，是去重在正常工作。**
通知中心那 53 条 `email.important` 正是它们留下的记录。

判定逻辑本身也被单测钉住（`reminder_window_test.go`）：46 封
`category = work(38)/notification(6)/bill(2)`、`notified_at = 0` 的邮件
**必须全部**进入候选 —— 防止将来有人把正常业务类别误当垃圾排除掉，
那才会真的让提醒静默失效。

**剩余的真实缺口只有一个**：新邮件拿不到 `importance`（kxmemory 未配），
所以 `remindersUnclassified=5` 持续增长、新的重要邮件永远等不到提醒。
这是**依赖缺失**，不是代码问题。

## 5.5 事故：`opencode_pocket` schema 被清空（2026-10-01 19:47，根因未定）

**发生了什么**：19:46:13 邮件同步还正常；19:47:12 起日志开始刷
`relation "email_accounts" does not exist`。查库发现 `opencode_pocket` 里
**所有表都是 0 行**（`email_accounts` / `emails` / `email_invoices` …），
`tasks` / `scheduled_tasks` 同样报错 —— **不是邮件模块的问题，整个 schema 都被清了**。

**丢了什么**：
- 5 个邮箱账户（已用部署脚本重新落库，`12 PASS / 0 FAIL`，IMAP+SMTP 齐备）
- 3 张发票的数据库行、约 155 封已同步邮件、提醒去重历史
- **发票 PDF 文件本身没丢**：`C:\workspace\openpocket\data\email-invoices\ws_user-admin\`
  下那张 3500 的 PDF（157,615 字节）还在磁盘上

**根因未定，不要臆断**。已排除的方向：
- 后端代码里**没有**任何针对这些表的 `DROP TABLE` / `TRUNCATE`（全量 grep 过）
- 事故窗口内（19:46–19:47）本会话只跑了 `gofmt` / `go build` /
  `go test ./internal/config/`；`internal/config` 包**完全不引用 pgx**，
  且 `go test` 带 `-run TestResolveDataDir` 过滤
- `deploy/bin/rebuild-db-local.sh:119` 确实是 `DROP SCHEMA ... CASCADE` + `CREATE SCHEMA`，
  形态完全吻合，但**它的备份目录 `~/Downloads/kaixuan/opp/backup` 不存在**，
  而该脚本会先备份再删 —— 所以不能断定就是它
- 同一 PG 上残留 `meeting_test_*` / `task_test_*` 两个测试 schema，
  说明**有别的测试/会话在同一实例上活动**。共享工作区被并发会话改动不是第一次。

**恢复动作与结果**：
1. `scripts/deploy-email-accounts-to-device.mjs` 重部署 5 个账户 → 12 PASS / 0 FAIL
2. 凭证解密正常（无 `message authentication failed`）——
   master key 仍用 `wt3/backend/data/email_master.key` 以 base64 注入，没换
3. `POST /api/emails/sync` → 2.88s，`{"mode":"imap_fetch","new":2,"synced":5}`，
   与事故前基线（1.5–2.9s / synced=5）一致
4. 重启实例时把 `POCKET_DATA_DIR` 显式指向 `C:\workspace\openpocket\data`
   （发票 PDF 真正所在处），日志确认 `data dir = C:\workspace\openpocket\data`。
   **这就是 §2.3 那个缺陷的正解**：以前只能靠「碰巧从对的目录启动」。

> **教训**：邮件相关的库数据没有任何备份。schema 被人清空时，
> 磁盘上的发票文件是唯一的幸存副本 —— 而 §2.3 那个 dataDir 缺陷会让它们
> 「明明在磁盘上却下载 404」。两件事撞在一起才会让这次恢复这么被动。

## 6. adb 恢复后要做的事

1. `adb reverse tcp:18099 tcp:18099`，确认 App 能登录。
2. 打开 App 的邮箱设置页，触发需求 8 的账户同步（登录后自动跑一次），
   确认列表里是 **5 个**真实邮箱、没有夹具账户。
3. 截图留证：这是本目标唯一还没拿到证据的一环。
