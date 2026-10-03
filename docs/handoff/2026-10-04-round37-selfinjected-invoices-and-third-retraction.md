# round37 —— 第三次撤回：台账里 513.40 CNY 是我们自己注入的测试数据

> 上一轮（round36）我在这里写下了「推翻二」：3 张腾讯云发票不是我们伪造的，而是
> 「上游（QQ Wallet / 腾讯云开票）自己生成的摘要页，采集器忠实落盘」。
> **那句话是错的。** 本轮把它改成可核查的事实，并给出对应的口径修正。
> 保留原文而不删改，是为了让「错在哪」能被对照出来。

日期：2026-10-04 00:00–00:20
基线：`origin/main` = `82806c9f`
工作区：`C:\workspace\openpocket\.wt-build`（独立 worktree，未碰主工作区的并发改动）

---

## 一、第三次撤回：3 张「腾讯云发票」是自发自收的 E2E 注入

### 1.1 错在哪

round36 的结论链条是：

| 当时的推断 | 实际情况 |
|---|---|
| `file_source=attachment` ⇒ 附件来自邮件 | ✅ 对，但只说明**落盘**忠实 |
| 仓库里没有代码生成该文案 ⇒ 是上游生成的 | ❌ **不成立**：生成者不在仓库里，不代表生成者是腾讯云 |
| PDF 正文自称 `This is a system-generated e-invoice.` ⇒ 是上游的摘要页 | ❌ 这句话**谁都能写**，包括我们自己 |

我把「不是我们仓库的代码生成的」当成了「是供应商生成的」。这两者之间没有任何
逻辑关系 —— 那是**判据只覆盖了一种可能来源**的又一次实例（同「grep 不到 ≠ 不存在」
同族：判据只认「本仓库 vs 上游」二分，没考虑「本仓库之外的自造数据」）。

### 1.2 实测证据（全部可复算）

**证据 A —— 发件人就是收件邮箱本身。** 这是纯 DB 事实，不依赖任何文案字面量：

```
SELECT a.email_address AS mailbox, e.from_address, e.from_name, count(*)
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE lower(e.from_address) = lower(a.email_address)
 GROUP BY 1,2,3;

  mailbox         | from_address     | from_name                                          | n
-----------------+------------------+----------------------------------------------------+---
 56551681@qq.com | 56551681@qq.com  | [QQ Wallet] Electronic Invoice Issuance Notice      | 3   ← 台账里那 3 张
 56551681@qq.com | 56551681@qq.com  | [urgent-e2e] 数据库延迟告警                          | 1
 56551681@qq.com | 56551681@qq.com  | [urgent-e2e] 生产环境告警                            | 1
 huangxutao@kxpms.cn | huangxutao@kxpms.cn | 【开轩启圭】SMTP 配置测试邮件                   | 3
```

同一行里出现了 `[urgent-e2e]` —— 那是**本系统 E2E 测试**的邮件前缀。round27 /
round28 文档已记录过这个机制（「`[urgent-e2e]`，`em-pop3-` 前缀，来自真实邮箱
`56551681@qq.com`，2026-09-07 08:29/08:43 注入」）。
**这个真实邮箱本来就是 E2E 注入靶子**，那 3 封 `[QQ Wallet]` 是同一批次注入的。

**证据 B —— 时间线对得上同一场注入。**

| 邮件 | 发出时间（Asia/Shanghai） |
|---|---|
| `[urgent-e2e] 生产环境告警` | 2026-09-07 08:29 |
| `[urgent-e2e] 数据库延迟告警` | 2026-09-07 08:43 |
| `[QQ Wallet] …`（票号 …711，58.90） | **2026-09-07 08:18:31** |
| `[QQ Wallet] …`（票号 …698，126.00） | 2026-10-03 00:10:44 |
| `[QQ Wallet] …`（票号 …703，328.50） | 2026-10-03 00:10:44 |

第一封与两封 `[urgent-e2e]` 是**同一天早上**。

**证据 C —— 正文是英文。** 腾讯云/QQ 钱包开出的中文通知不会写
`Dear user, your electronic invoice has been issued.`；磁盘上那封邮件的
`snippet` 字段就是这句。

**证据 D —— 附件在结构上不可能是增值税电子普通发票票面。** 973 字节、
`/BaseFont /Helvetica`、`/Subtype /Type1`、正文只有 7 个 `Tj`：

```
/F1 16 Tf 72 770 Td (Electronic VAT Invoice \(Dianzi Piao\)) Tj
/F1 12 Tf 72 720 Td (Invoice No: 24317200000907012698) Tj
/F1 12 Tf 72 692 Td (Total Amount \(incl. tax\): CNY 126.00) Tj
/F1 12 Tf 72 664 Td (Seller: Tencent Cloud Computing Co Ltd) Tj
/F1 11 Tf 72 636 Td (Service: Cloud Server Monthly Subscription) Tj
/F1 11 Tf 72 614 Td (Date: 2026-09-07) Tj
/F1 10 Tf 72 570 Td (This is a system-generated e-invoice.) Tj
```

无内嵌 CJK 字体、无发票代码/密码区/校验码/税控信息。
对照同目录里**真票面**的形态：`其他-杭州创客家…-3500.00-….pdf` 157615 B，
含 7 个 `/FontFile2` + `/Type0` + `/Identity-H` CID 中文字体子集。

**证据 E —— 落盘链路本身是忠实的（这部分 round36 说对了）。**
`invoice_harvest.go:359` `saveInvoiceFile(ctx, inv, att.Data, "attachment")`
→ `invoice_harvest.go:545` `os.WriteFile(tmp, data, 0o600)`，字节原样；
`mime.go:533-539` 的 `att.Data` 来自 `decodePartBytes(part, CTE)`。
**采集器没有伪造任何字节 —— 它只是把上游（此处是我们自己）给的字节存了下来。**

### 1.3 口径修正

| 口径 | 金额（CNY） | 组成 |
|---|---:|---|
| round36 报的台账合计 | 4038.01 | 6 行 downloaded 全量 |
| **其中自注入测试数据** | **513.40** | 58.90 + 126.00 + 328.50 |
| **可交财务的真实凭证** | **3524.61** | 创客家 3500.00 + 通行费 19.00 + 5.61 |

3500.00 / 19.00 / 5.61 三张经查是真票面：来源邮件分别是
`dzfp@mail.171win.com`（亿企发票平台，主题含票号 26332000008261110741）与
`service@invoice.txffp.com`（浙江通行费电子发票，zip+XML），
**都是外部供应商地址**，不是自发自收。

> ⚠️ **本条只做定性，不做删除。** 剔除这 3 行是**写操作**，需用户显式授权；
> 我没有动库，也没有删磁盘文件。授权前请注意：删行会让 `downloaded` 从 6 变 3、
> 合计变 3524.61，而 round36 记的「A4 产物 20 个」是按当时 6 行导出的，
> 重新导出后数量会变。

---

## 二、新增判据 0：台账引用的文件「不是真票面」的三种形态

`backend/internal/email/diag_invoice_handoff_integrity_test.go` 原来只查
「台账有·磁盘无 / 磁盘有·台账无 / 字节重复 / 日期打架」四类。
这四类**都假定台账里的行是真凭证** —— 而 1.2 证明这个假定不成立。
`classifyOrphanPDF` 明明能区分夹具，但它只查**无主**文件，永远看不到有主的那几张。

### 2.1 判据拆成三条（因为它们各自会失效）

| 判据 | 依据 | 失效形态 |
|---|---|---|
| **0a 自发自收** | `from_address = email_accounts.email_address`（纯 DB 事实） | 只认地址相等；若用别的方式自注入（如 SMTP 改 From）就漏 |
| **0b 夹具/退化件** | 复用 `classifyOrphanPDF`（会解压 Flate 流） | 只认 `IMAP fixture` / `Fixture Cloud` / `Demo Co` 三种字样 |
| **0c 摘要页** | 正文含 `system-generated` | **只认这一种英文写法**，改文案即完全漏判 |

**0c 我明确降级为「佐证，不单独作结论」**，理由写在函数注释里：
第一版把它当判据时，它的危害不是漏判而是**恒亮** —— 只要命中就报，
读日志的人会以为「抓到证据了」，而真正硬的 0a 反被这条弱判据盖住。

### 2.2 自检：判据自报的数必须和肉眼可数的事实对得上

第一版汇总行写的是 `自发自收 3 + 夹具 0 + 退化件 0 + 摘要页 3 = 6 行`，
**这是错的**：0a 与 0c 命中的是同一批 3 个文件，被数了两遍。
读者照着 6 去目录里核对会核不出来，从此不再信这条诊断。
已改为按文件去重：

```
[diag] 判据 0：自发自收 3 行 / 夹具 0 / 退化件 0 / 摘要页 3；按文件去重后实际待定性 **3** 个
[⚠ 口径] downloaded 行合计 4038.01；剔除自发自收的 513.40 后 = 3524.61；其中 invoice_date 为空的 0 行
```

### 2.3 负控（证明 0a 不是恒暗）

把谓词改成 `strings.EqualFold(c.fromAddr, c.mailbox) && false`，同一台设备、同一份真实数据：

| | 自发自收 | 口径合计 | 0c 摘要页 |
|---|---:|---:|---:|
| 正常 | **3** | 剔除后 3524.61 | 3 |
| 负控（0a 关掉） | **0** | 回到 4038.01 | 3 |

⇒ 0a 确实由那个谓词驱动；0c 独立成立（它没跟着归零），
两者命中同一批 3 个文件，所以去重后仍是 3 —— 与 2.2 的自检一致。

### 2.4 运行方式

```powershell
$env:POCKET_DIAG_HANDOFF='1'
$env:POCKET_REAL_MAIL_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_REAL_MAIL_SCHEMA='opencode_pocket'
$env:POCKET_DIAG_INVOICE_DIR='C:\workspace\openpocket\data\email-invoices\ws_user-admin'
go test ./internal/email/ -run TestDiagInvoiceHandoffIntegrity -v -count=1
```

---

## 三、顺带发现并修掉的一个真隐患（**不是**我引入的）

跑 `TestPGTestsNeverTargetTheProductionSchema` 时报红。第一件事是判定
责任归属：**把本轮改动全部还原到 HEAD 再跑，仍然 FAIL** ⇒ 既有失败。
`git status` 显示 `internal/email/store_upsert_messageid_test.go` 是并发会话
23:44 新增的（扫描数从 528 变 531）。

### 3.1 它做了什么

```go
schema := os.Getenv("POCKET_DIAG_SCHEMA")
if schema == "" {
    schema = "opencode_pocket"     // ← 缺省就是生产库
}
cfg.ConnConfig.RuntimeParams["search_path"] = schema
...
cleanup := func() {
    _, _ = pool.Exec(ctx, `DELETE FROM emails WHERE message_id = $1`, msgID)
}
```

它**会真的写** `emails` 表（`InsertEmailIfNew` + `DELETE`），
而闸门只有 `POCKET_REAL_MAIL_DSN` 非空。
本仓跑只读真实库诊断时**本来就要**带 `POCKET_REAL_MAIL_DSN`
（`POCKET_DIAG_HANDOFF` 等十几个诊断都靠它）—— 两边一撞就是
**「谁带着真实 DSN 跑一次全量 `go test`，谁就在生产 `emails` 表插两行再删掉」**。

### 3.2 我做了什么 / 没做什么

**做了**：抽出 `upsertGuardSchema(t)`，缺省即 `t.Skip`、显式点名
`opencode_pocket` 即 `t.Fatalf`，并把它登记进 `pgAllowlistedWrites`
（它有写语句，按规则 4 不能进 `pgSafeWithoutIsolation`），理由写明
「缺省指向哪里、被哪几道开关挡住」。

**没做**：守卫的**规则 2 仍然判红**，因为该文件确实没有自建 `*_test_` schema。
修法要么让它自建隔离 schema（用 `pgscope_test.go` 的 `newScopedPool` +
跑迁移，会改变该测试「借真实 `email_accounts` 行解外键」的语义），
要么把理由写宽松让它变绿 —— 守卫自己的报错文本就写着
「**不要靠把 pgSafeWithoutIsolation 的理由写宽松来绕过——那是让冲突变沉默**」。
我选了不动：`internal/server` 目前**只有这一条红**，是真实信号，
留给该文件的作者收尾。

### 3.3 这不是新问题

`pgSafeWithoutIsolation` 里 20 多条登记理由都在反复写同一句话：
「写路径拒绝 schema 缺省值（缺省=生产库 `opencode_pocket`）」。
**本仓的默认失败模式就是「新写库路径把生产库当缺省值」**，这条登记是第 N 次。

---

## 四、验证记录

| 项 | 结果 |
|---|---|
| `node scripts/check-gofmt.mjs` | ✓ 全部符合（忽略行尾后真债 0） |
| 3 个改动文件 `bareLF` | 0 / 0 / 0（gofmt 会写回 LF，必须 gofmt 在前、CRLF 归一在后） |
| `go vet ./internal/email/` | 通过 |
| `go test ./internal/email/ -count=1` | **ok 14.262s** |
| `go test ./internal/server/ -count=1` | FAIL，**仅** `TestPGTestsNeverTargetTheProductionSchema`（3.2 的既有失败） |
| 诊断真实数据复跑 | 判据 0 报 3 行 / 513.40，口径 4038.01 → 3524.61 |

---

## 五、待用户拍板（本轮变化）

1. **【新增·优先】要不要把自注入的那 3 行从台账里剔除？** 合计 4038.01 → 3524.61。
   这是写操作，我没有授权也没有执行。剔除后 A4 产物需重新导出。
   **在剔除之前，这张台账不能直接交财务** —— 里面有 513.40 是假的。
2. **【作废】round36 的「3 张摘要页算不算凭证」** —— 问题本身错了。
   它们不是「算不算凭证」的问题，是「根本不是凭证」。
3. **无开票日期时文件名用采集当天还是收信日期**（不变，磁盘已有前科：
   创客家同一张票存在 `…-2026-09-24.pdf` 与 `…-2026-10-01.pdf` 两份字节相同的拷贝）。
4. **飞书四项凭据**（不变，仍阻塞，推送腿至今未在真实环境跑过）。

## 六、明早 08:00 之后的复核清单（不变，08:15 已排自动提醒）

台账行数 / step1.5 计数 / 幽灵行 / 日期兜底 / 死信列 + 本轮新增的
「判据 0 抓到的自发自收行数是否继续增长」——
**这一项是新的重点**：如果明早那轮又往这个邮箱注入了测试邮件，
自发自收的行数会从 3 往上走，而它们会**自动计入合计**。
