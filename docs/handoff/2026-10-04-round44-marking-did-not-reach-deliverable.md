# round44 —— 3 行假数据按授权处置完毕，但**标注没有传导到交付物**；词边界漏判实测为 0；横幅阈值拿到第 3 个数据点

日期：2026-10-04 13:52 → 16:0x（本机时区 +08:00）
范围：round43 §四 遗留提示词的三件事
基线：`e7e70584`（开工时本地 main，与 origin/main 同一个 commit）

---

## 一、结论先行

| # | 事项 | 结论 | 证据强度 |
|---|---|---|---|
| 1a | 台账 3 行假数据 | 已按授权「**保留并标注**」处置完毕，status 一行未动，备份表可回滚 | 强（库内回读 + psql 独立复核） |
| 1b | 真跑一轮定时流水线 | 完成（200 / 9.5s / 982 候选 / 3 张进采集） | 强 |
| 1c | 新汇总单逐行核对 | **12/12 行全部与台账对上**，列结构合法 | 强（机器比对，非肉眼） |
| 1d | ⚠ 标注是否改善了交付物 | **没有。** 汇总单里横幅仍占 CNY 合计 **61.1%**、仍标「已核验」 | 强 |
| 2 | 词边界漏判量化 | 窄口径 **0 封**；宽口径 8 封（6 已建档 / 2 未建档），**被词边界漏掉的真发票 = 0 封** | 强（psql 独立复核 + 旧实现自证） |
| 3 | 横幅阈值标定 | 实测 10 个真实票面，长宽比 **1.294–1.500**；2.5 的余量从「单点」变成「两侧都有实测」 | 中（**仅 PDF，非拍照**） |
| 3b | 最小像素尺寸下限 | **仍然不加**，且本轮给出了「为什么加不了」的硬理由 | 强 |

### 1d 必须先说，因为它是本轮最容易被读反的一条

授权是「保留并标注」。执行结果：

- 台账 `last_error` 里 3 行都带上了 `【人工标注】` 前缀，**保住了**；
- 但 `last_error` **根本不是导出列**。新产出的
  `invoices-summary-20261004-154538.md` 与 `.csv` 里：
  - 不含「人工标注」字样（实测 `includes('人工标注') === false`）；
  - 不含 `last_error` 字段（实测正则不匹配）；
  - 两行横幅仍以 `downloaded / 已核验` 出现，合计 **6354.20 CNY = 61.1%**。

⇒ **财务看到的汇总单与处置前一模一样。** 标注的价值是「台账里看得见」，
不是「交付物里不再误导」。这一点在提问时已经提示过（「汇总单口径仍需另加
『已标注』过滤，否则 61.1% 的假金额仍会被财务读到」），本轮按授权执行，
但必须把结果如实摆在这里，不能让「已处置」被读成「汇总单已干净」。

**要让汇总单不计入，需要改导出口径（过滤带 `invoiceHumanMarkPrefix` 的行）。
本轮没做、也没授权。** 这是下一轮的第一顺位。

---

## 二、三件事的详细经过

### 2.1 任务一：3 行假数据处置

#### 授权与执行口径

用户在「作废 / 保留并标注」里选了**保留并标注**。执行严格照此：
**不删任何行、不改任何 status**，只改写 `last_error`。

#### 先只读，再写

新增 `backend/internal/email/diag_mark_false_ledger_rows_test.go`，四道闸：
`POCKET_DIAG_ANNOTATE=1` / `POCKET_REAL_MAIL_DSN`（无缺省值）/
`POCKET_REAL_MAIL_SCHEMA`（无缺省值）/ `POCKET_DIAG_ANNOTATE_EXEC=1`。
只读阶段由**数据库强制**（连接带 `default_transaction_read_only=on`），
并用「故意 `CREATE TEMP TABLE`，它必须失败」自证——不靠「代码里没写 UPDATE」。

选行用**三个精确取值**（两个发票号 + `seller='中国工商银行' AND amount=58000.00
AND invoice_no=''`），并硬断言行数**恰好 3**。0 行与 4 行都 `t.Fatalf`：
前者说明数据被别人改过，后者说明口径写宽了——**往一行真发票上贴「非发票」
标注，比不标坏得多**。

执行阶段单事务：先 `CREATE TABLE email_invoices_markbak_20261004_154000 AS SELECT ...`
（回读确认 3 行）→ UPDATE（逐行断言 `RowsAffected()==1`）→ 同事务内回读比对 →
不一致就回滚。

#### 写完的实测结果

```
inv_1790903383222583800_1  中国工商银行  58000.00  pending     attempts 2→3  len 110→353  标注在位置 0
inv_1791072004989035300_2  系统服务      6071.00  downloaded  attempts 1     len 133      标注在开头
inv_1791072004990035300_3  系统服务       283.20  downloaded  attempts 1     len 133      标注在开头
```

### 2.2 ⚠ 为了让「保留并标注」成立，改了一行生产行为（**必须单独看**）

**这是本轮唯一的生产代码改动，且是处置方案的前置条件，不是顺手改的。**

问题（实测，不是推演）：`markRetry` 原本是**整段覆盖**
（`inv.LastError = msg`，`invoice_harvest.go:470`）。而 harvest 每轮处理
`status IN ('new','pending')`、`MaxInvoiceAttempts = 8`。
工行那行恰好是 `status=pending, attempts=2` ⇒ **下一轮采集失败就会把人工标注
原样抹掉**，换回一句笼统的「发票链接未能取到 PDF 文件」。

⇒ 人工判断被机器的例行失败静默覆盖，**比不标还糟**。
横幅那两行是 `status=downloaded`，不在 harvest 的选择集里，覆盖不到。

改法（`invoice_harvest.go`）：

```go
const invoiceHumanMarkPrefix = "【人工标注】"

func composeHarvestRetryMessage(prior, msg string) string {
    if strings.HasPrefix(prior, invoiceHumanMarkPrefix) {
        return prior + " | 本轮采集：" + msg
    }
    return msg
}

func (h *InvoiceHarvester) markRetry(...) string {
    msg = composeHarvestRetryMessage(inv.LastError, msg)
    ...
}
```

**为什么抽成纯函数**（这一步是被判据逼出来的，不是洁癖）：第一版判据复刻了
拼接逻辑，然后我把生产条件改成 `false && strings.HasPrefix(...)`
（短路成恒假 = 保护失效）——**判据全绿**。复刻版没碰生产代码，文本匹配那条
也没红（字面文本还在）。这正是本仓注释里记着的
「判据匹配注释里的字面文本，等于给退化开了后门」。
提成纯函数后判据打的是**生产代码本身**。

**真机验证**：重跑流水线后工行行 `attempts` 2→3（**确实被重试了**），
`last_error` 110→353 且 `【人工标注】` 仍在**位置 0**，本轮原因被追加在后面。
没有这处改动，这行现在已经被覆盖了。

#### 护栏与双向变异

`backend/internal/email/invoice_human_mark_test.go`，两层：

| 层 | 判据 | 钉什么 |
|---|---|---|
| 函数 | `TestComposeHarvestRetryMessage_PreservesHumanMark` | 标注+失败 ⇒ 标注在**且**本轮原因在；无标注 ⇒ 逐字不变；连续两轮 ⇒ 不叠第二个前缀 |
| 接线 | `TestMarkRetry_CallsComposeBeforeAssign` | markRetry 真的调它、参数是 `inv.LastError`、且在 `inv.LastError = msg` **之前** |

双向变异（都是**实测转红**，还原用显式反向替换）：

| 变异 | 期望 | 实测 |
|---|---|---|
| `strings.HasPrefix(...)` → `false && strings.HasPrefix(...)`（拆保护） | 转红 | ✅ 子测试 1、3 FAIL（"人工标注被采集失败覆盖了"） |
| 条件改成 `if prior != ""`（放过头，无标注也保留） | 转红 | ✅ 子测试 2 FAIL（实际 `"发票链接未能取到 PDF 文件：x \| 本轮采集：no usable pdf/xml found..."`） |

还原复核：`git grep MUTATION` 无命中（exit 1）＋
`git grep "const bannerAspectRatio = 2.5"` 命中 ＋ `git diff --stat` 只剩预期增量。

### 2.3 ⚠⚠ 我改了一条**既有护栏**，必须单独说明（不能混在改动列表里）

**改的文件**：`backend/internal/server/pg_test_isolation_guard_test.go`
**改的方式**：只**新增两条登记**，没有改任何判据逻辑、没有删改已有条目。

#### 为什么必须改

护栏规则 2 要求：任何打开 PG 连接的测试，要么把 `search_path` 钉到自建的
`*_test_` schema，要么登记进豁免表 `pgSafeWithoutIsolation`。
我新写的 `diag_mark_false_ledger_rows_test.go` 属于后者——它**必须**指向生产
schema（隔离库里没有那 3 行假数据，指向隔离库会输出「没有待标注行」的假结论）。

不登记的结果是实测的：`go test ./internal/server/` 直接红，报
`打开了 PostgreSQL 连接但没有把 search_path 钉到自建的 *_test_ schema`。

#### 两条登记分别登记了什么

| 表 | 登记内容 | 理由要点 |
|---|---|---|
| `pgSafeWithoutIsolation`（规则 2 出口） | 指向生产 schema 是**目的**，不能自建 `_test_` schema | 四道闸、无缺省值 DSN/schema、`current_schema()` 读回校验、选行硬断言恰好 3 行、单事务备份+回读 |
| `pgAllowlistedWrites`（规则 4） | 写语句只有 1 条 `UPDATE email_invoices SET last_error`（`WHERE id=$1`）+ 同事务的 `CREATE TABLE` 备份 | **不删行、不改 status**；`.Exec(` 共 2 处（CREATE TEMP TABLE 自证 + CREATE TABLE 备份），UPDATE 走 `tx.Exec` |

理由里刻意写清了「**不删行、不改 status**」这一条与 `diag_purge_injected_invoices_test.go`
（会真删行）的差别——两条同族登记混成一样就等于没登记。

#### 登记的负控（实测，不是「我以为它有用」）

把 `pgAllowlistedWrites` 那条的 key 改名成
`diag_mark_false_ledger_rows_test.go_DISABLED_FOR_NEGCTRL` 后重跑，
**实测转红**，报出两条：

```
: 登记在 pgAllowlistedWrites 里，但**不在** pgSafeWithoutIsolation 里。
: 登记在 pgAllowlistedWrites 里，但仓库中已找不到这个测试文件。
```

⇒ 两条登记都是**承重**的，不是把护栏改成永真。
还原用显式反向替换；还原后 `git grep 'NEGCTRL|DISABLED_FOR'` 只剩
`diag_last_leak_test.go:67` 一处**别人早就有的**命中（2026-10-03 的另一个负控），
我的负控标记已清干净；两条登记分别在第 351、681 行。

#### 护栏自己指出的一条局限（如实记）

```
（登记粒度限制）internal/email/diag_mark_false_ledger_rows_test.go 已在 pgAllowlistedWrites 内，本护栏不区分它写了几条
```

即：本护栏只认「登记/未登记」，不核对登记理由里写的条数与代码实际写语句数
是否一致。理由写得再细，也不是被机器验证过的。**下一轮若要收口，
应给这条加一个「登记条数 == 实测条数」的核对。**

### 2.4 ⚠ 我自己新写的诊断有一个**真 bug**，被全量跑抓出来了

`TestDiagInvoiceCandidateFalseNegative` 第一版**没有闸门**，只在后面检查语料路径
是否存在。全量 `go test ./...`（不设该变量）时它在 0.00s 直接
`t.Fatal`，把一个「本轮没打开的只读诊断」变成整包红：

```
--- FAIL: TestDiagInvoiceCandidateFalseNegative (0.00s)
    POCKET_DIAG_CANDIDATE_CORPUS 未设置（本诊断无缺省值）——没有语料会静默统计 0 封然后「结论：无漏判」
```

单独跑它时不会暴露，因为当时两个环境变量都给了。**是全量跑抓到的。**

修法：加显式闸门 `POCKET_DIAG_CANDIDATE=1`，未打开 ⇒ `t.Skip`；
闸门打开但语料没给 ⇒ 仍 `t.Fatal`。两者必须分开——「没打开」与
「打开了但配置错了」是两种状态，混成一个 `Fatal` 就会让未启用的诊断污染全量。



### 2.5 真跑流水线 + 逐行核对

启动实例：`logs\pocketd-pg.exe`（本轮自建），env 全部显式给
（`POCKET_DATA_DIR=C:\workspace\openpocket\data`、`POCKET_PG_SCHEMA=opencode_pocket`、
`POCKET_DEV_AUTH=true`、`POCKET_AUTH_PASS=<仅本次本地值>`）。
启动自检三项全部核对：`Postgres pool initialized (schema="opencode_pocket")`、
`email_master.key` 路径正确、**0 条 `decrypt credential` 错误**。

`POST /api/email/pipeline/run` → **200 / 9.5s**：

```
accountsSynced          = 5
invoiceCandidatesScanned= 982
invoices = {Processed:3, Downloaded:0, Pending:3, Failed:0, Skipped:0}
errors   = ["invoice raw body fetch: 2 未建档（服务端已无此消息 2、… 本轮被预算顺延 0）…"]
```

新产物：`data/email-invoices/exports/ws_user-admin/invoices-summary-20261004-154538.md`
（1562 字节）+ 同名 `.csv`。

**逐行核对是机器做的**，不是肉眼：解析 MD 表格 12 行，逐行按
`seller|amount|currency|invoice_no|date|status` 与库内 12 行做集合匹配，
并校验每行 7 列、核验列取值 ∈ {已核验, 未核验}。

```
汇总单数据行 = 12   台账行 = 12
✅ 逐行核对通过：12 行全部在台账中找到对应，列结构与取值合法
```

**顺带核实了 round43 的一处修复在真机上生效**：本轮 MD 表头最后一列是
`核验`（round43 §2.1 之前是错写成 `文件`），且该列内容确实是 已核验/未核验。

#### 两个口径必须分开（我自己第一版就踩了）

第一次出报告时我打的是「台账 CNY 合计 68416.21」，而汇总单上写的是 10392.21。
同一句「合计」两个数，差值来自 **pending 行**，不是标注造成的。已修诊断，
现在两个口径并排打：

| 口径 | 值 | 说明 |
|---|---:|---|
| ① 全表 | CNY 68416.21 | 全部 12 行，含 pending/未核验 |
| ② 汇总单（`downloaded` + 有文件） | CNY 10392.21 | 与 `invoices-summary-*.md` **逐字相同** |
| ② 里被标注的 | CNY 6354.20 | 横幅两行（工行那行是 pending，本就不在 ② 里） |
| ② 扣除被标注行 | **CNY 4038.01** | 财务若剔除假数据该看到的数 |

### 2.6 任务二：词边界漏判量化

新增 `backend/internal/email/diag_invoice_candidate_false_negative_test.go`。
语料从真实库 psql 导出到 `logs/zz-invoice-candidate-corpus.tsv`（**不进仓库**）：
982 行 / 982 个唯一 id / 12 已建档。

**实际窗口是 2026-09-03 → 2026-10-04，32 天，不是 90 天。**
邮件库只装了这么多，所以下面所有数字是这 32 天的**全部**，不是 90 天的抽样。
诊断里对此有显式告警，拿 32 天说 90 天是本仓反复踩过的措辞陷阱。

#### 关键设计：旧实现必须自证

`e54d797d` 的实现已被 round42 替换掉，不在本仓任何文件里。本诊断**重建**了它
（原文 `keywordBoundaryClass = ^|[^0-9A-Za-z_\-./:?&=+#@%]`），并在读语料**之前**
先跑 round42 记在注释里的 7 个已知返回值做一致性检查：5 个真实发票形态应为
false（旧实现漏判），2 个空格分隔形态应为 true。对不上直接 `t.Fatalf`——
重建错了，后面所有统计作废。这一关本轮**通过**。

#### 结果

**窄口径（`Invoice:` / `Receipt:` / `Invoice#`）：0 封。**

用 psql 在真实库独立复核（不只信测试）：

```
 all_alive | subj_form | snip_form | ai_form | subj_bare | snip_bare
       982 |         0 |         0 |       0 |         5 |         7
```

**宽口径（裸词 invoice/receipt）：8 封**

| 格 | 数量 |
|---|---:|
| 已建档 | 6 |
| 未建档 · 新旧实现都命中（卡在下游） | 2 |
| 未建档 · **仅新命中（round42 修复救回来的）** | **0** |
| 未建档 · 仅旧命中（= 回归） | 0 |
| 未建档 · 新旧都不命中 | 0 |

⇒ **「被词边界漏掉的真发票」= 0 封。** round42 那条词边界缺陷在这个邮箱里
**零 casualties**。

⚠ 但这个 0 要读对：它**不能**证明该缺陷普遍无害。它只说明这个邮箱里
**压根没人写 `Invoice:` 这种形态**（窄口径 0）。「形态不存在」与
「缺陷不伤人」是两件事，不能混。

#### 两封未建档邮件的真实闸门（这才是可执行的部分）

诊断把流水线里真实存在的每一道闸门都跑了一遍，而不是停在「候选命中」：

| 邮件 | 主题 | kwNew | kwOld | admit | extract | 卡在 |
|---|---|---|---|---|---|---|
| `em-10444-…-2` (2026-09-29) | 所需操作：AWS 账户提示 | true | true | **false** | false | `admitDebtNotice` 拒收（债务通知形态，无真发票语义）——**判对**，它本来就不是发票 |
| `em-1298894461-…-5` (2026-09-03) | [GitHub] Payment Receipt for halfking | true | true | true | **false** | `ExtractInvoice` 硬门槛：抽不到金额且抽不到发票号 |

第二封值得单独说：它**是一张真实的付款回执**（GitHub 真实扣款），却没建档，
而**与词边界无关**（新旧实现都命中）。库内证据：该邮件
`snippet` 长度为 **0**、`has_attachments=false`、`raw_body_gone_streak=1`
⇒ IMAP 原文取不回来，envelope 阶段没有金额也没有发票号，硬门槛永远过不去。
回看窗口是 90 天、上限 2000 行，所以它**在**扫描范围内。

⇒ 这是下一个值得查的真实缺口，但**本轮没有验证**「拉原文预算是否把它挤掉」——
我没拿到那一轮的流水线报告，不猜。

### 2.7 任务三：横幅阈值标定

新增 `backend/internal/email/diag_real_voucher_geometry_test.go`。
走 pdfcpu 的页面尺寸接口（**不**用正则扫字节——`diag_real_exports_test.go`
早就踩过「先命中内嵌 Form 自带的 /MediaBox」这个坑）。

对 12 个真实落盘 PDF 逐页测量，**10 页可解析**：

```
交通-浙江沪杭甬…-5.61…pdf#p1     594.96 × 841.92   1.415
交通-浙江高速公路…-19.00…pdf#p1   594.96 × 841.92   1.415
其他-Tencent-…-126.00…pdf#p1     595.00 × 842.00   1.415
其他-Tencent-…-328.50…pdf#p1     595.00 × 842.00   1.415
其他-Tencent-…-58.90…pdf#p1      595.00 × 842.00   1.415
其他-云服务开票中心-1280.00…pdf#p1  595.28 × 841.89   1.414
其他-云服务开票中心-发票抬头-1280.00…pdf#p1  595.28 × 841.89   1.414
其他-杭州创客家…-3500.00-2026-09-24.pdf#p1  595.28 × 396.85   1.500
其他-杭州创客家…-3500.00-2026-10-01.pdf#p1  595.28 × 396.85   1.500
通信-X-8.00-2026-10-04.pdf#p1   612.00 × 792.00   1.294
其他-财务部-0.00-2026-09-30.pdf   PARSE-FAIL: PANIC
其他-财务部-0.00-2026-10-01.pdf   PARSE-FAIL: PANIC

真实电子票面长宽比：min=1.294  max=1.500
```

阈值定位：

```
bannerAspectRatio 阈值  = 2.50
真票实测最大长宽比       = 1.500
余量（阈值 - 真票最大）  = 1.000
横幅实测长宽比           = 4.086
余量（横幅 - 阈值）      = 1.586
```

**一个此前没被记录的事实**：`杭州创客家` 那张票是 595.28×396.85，长宽比
**1.500**，**超过 A4 横的 1.414**。round43 记的「A4 竖 0.707 / A4 横 1.414」
是按纸张规格算的，低估了真实上限。现在 1.500 是量出来的。

双向变异（实测转红）：

| 变异 | 实测 |
|---|---|
| `bannerAspectRatio` 2.5 → **1.0**（收得太紧） | ✅ FAIL：`真实电子票里出现了长宽比 1.500 ≥ 阈值 1.00 的页面` |
| `bannerAspectRatio` 2.5 → **100.0**（放松过头） | ✅ FAIL：`横幅实测长宽比 4.086 ≤ 阈值 100.00：横幅不会被拒` |

#### 结论：`bannerAspectRatio` 维持 2.5，最小像素下限**仍然不加**

- 2.5 现在两侧都有实测支撑（真票 1.500 / 横幅 4.086），不再是「横幅一个数据点」；
- 但拿 PDF 数据去改一个**面向照片**的阈值属于用错介质过拟合，所以不动；
- **像素下限本轮确认「加不了」，且给出了硬理由**：
  MediaBox 的单位是 **pt（1/72 英寸），不是像素**。同一张 A4 渲染成 72dpi
  是 595×841，300dpi 是 2480×3507，**都是同一张票**。
  ⇒ 由本诊断推不出任何像素阈值。要标定必须有**拍照/扫描**样本。

#### ⚠ 一条被证伪的说法（不要外传）

我第一版在诊断注释里写「pdfcpu 在**真实电子发票**上 panic」。**这是错的。**
实测：那 2 个 panic 的文件是 `其他-财务部-0.00-*.pdf`，各 **69 字节**、
SHA256 相同（`cfa3181c1ee36e8b…`）——是退化件，正是 round37 那批自注入数据，
**不是真实票面**。

而且这**不是新的生产风险**：`pdfHasPages`（`invoice_file.go:139`）与
`pdfPageCountSafe`（`export_pdf.go:279`）**都已有 recover**，且两处注释早就
记录了这件事。本轮只是让「量不到 2 个文件」，没有新风险。

---

## 三、门禁

```
cd backend && go build ./...                     → exit 0
cd backend && go vet ./...                       → exit 0
cd backend && go test ./... -count=1（设 DSN）    → **0 FAIL**（详见下方确认）
node scripts/check-gofmt.mjs                     → exit 0（真债 0）
node scripts/check-smart-quotes.mjs              → exit 0
node scripts/check-blankline-bloat.mjs           → exit 0（970 个 .go/.sql，>35% 且 >=80 行：0）
node scripts/check-pg-schema-hardcoded.mjs       → exit 0
node scripts/check-main-overlap.mjs              → exit 0（重叠 0）
```

### 「0 FAIL」不是 skip 出来的 —— 用耗时自证

```
ok  backend/internal/email       149.181s
ok  backend/internal/server       62.103s
ok  backend/internal/repohygiene  58.205s
```

round43 记录过同一批包的对照：`internal/email` **不设 DSN 时 19s、设 DSN 时 172s**。
本轮 149s 落在「设了 DSN」那一侧，`internal/server` 62s 同理
（round43 记录 unset→23s / set→61s）。
⇒ 这些 PG 支撑的用例**真的执行了**，不是静默 skip。

**本轮在这上面栽过一次，并且是被全量跑抓到的**：
`TestDiagInvoiceCandidateFalseNegative` 缺闸门，不设变量时 0.00s 直接 Fatal
（见 §2.4）。单独跑它不会暴露，因为当时变量都给了。
⇒ 「单跑绿」不等于「全量绿」，也不等于「不是 skip」，三件事要分开验。

`gofmt` 门禁第一次跑是**红的**，真债 1，正是本轮新增的
`diag_real_voucher_geometry_test.go`。按门禁自己的提示跑了两遍
`gofmt -w` 才收敛到 0（单遍不够，本机 CRLF）。

> **不设 DSN 时的 skip 陷阱本轮再次生效**：单独跑
> `go test -run 'HumanMark|MarkRetry'` 时，既有的
> `TestMarkRetry_ConvergesToFailedAfterMaxAttempts` /
> `_SuccessPathIsTerminal` / `_StatusMachine` 三条**全部 SKIP**
> （`POCKET_TEST_POSTGRES_DSN not set`）。它们恰好是 markRetry 的行为判据。
> ⇒ 全量必须设 DSN，见 §4。

---

## 三点五、提交与推送的实况（2026-10-04 19:08 → 19:2x）

### 并发会话抢先 merge + push，我的提交是被它带上远端的

```
19:08   开工提交：fetch 后发现 origin/main 比本地**新 15 个提交**
        （并发会话的 maestro / UI / gates / styles 工作）
19:09   逐文件核对重叠：我的 7 个路径远端**一个都没动**（无冲突风险）
19:10:27 git commit → 560f4a78（7 files, 1554 insertions, 纯新增）
19:10:33 **别人**在这个 worktree 跑了 `git pull origin main` → 049bca53
19:10   我自己再跑 git merge origin/main → "Already up to date"
19:11+  origin/main 已经就是 049bca53 ⇒ 并发会话**连 push 一起做了**
```

reflog 是唯一能看清这件事的证据（`git status` 连读两次都「干净」，
`git log HEAD..origin/main` 在提交后突然从 15 变 0）：

```
049bca53 HEAD@{2026-10-04 19:10:33}: pull origin main: Merge made by the 'ort' strategy.
560f4a78 HEAD@{2026-10-04 19:10:27}: commit: fix(email): 人工标注会被采集流程整段覆盖…
```

⇒ **我没有执行 push，提交是经由并发会话那次 pull 的 push 进的远端。**
已核实两件事：`560f4a78` 是 `origin/main` 的祖先；5 个关键文件的 blob 哈希
本地与远端**逐个一致**（内容没有被改写）。

⇒ **教训**：`git fetch` 之后 `HEAD..origin/main` 计数为 0
**不等于**「我合并过了」，也可能是**别人刚替我合并并推送了**。
这两种情况在 `git status` 和 `git log` 上长得一模一样，
**只有 reflog 能分辨**——前者是「我做的」，后者是「别人做的」。

### ⚠ main 现在是**红的**，但不是本轮造成的

`go test ./internal/server/ -run TestPGTestsNeverTargetTheProductionSchema` 报：

```
internal/flashcards/seed_pg_test.go: 测试读取了 POCKET_POSTGRES_DSN（服务自己的生产连接串）1 处。
```

**归属核实（三步，都有证据）**：

| 步骤 | 结果 |
|---|---|
| `git log -- backend/internal/flashcards/seed_pg_test.go` | 引入提交是 `11481002 feat(ui,shell): z-index 阶梯…`——**并发会话的**，不在我的 `560f4a78` 里 |
| 该文件在 `87249317`（合并前的远端头）上已存在且已读该 env | 是 |
| **负控**：在 `87249317` 上实跑该护栏 | **同样红，报同一句话** |

⇒ **合并前 main 就已经是红的**，这次 merge 只是把它带过来，没有引入新问题。
该文件注释写着「POCKET_TEST_POSTGRES_DSN takes precedence, POCKET_POSTGRES_DSN
is the fallback」——测试回落去读**生产连接串**，正是这条护栏要拦的事。

**本轮没有动它**：修法有两条且都涉及别人的设计判断（删掉 fallback，
还是在 allowlist 登记并写明理由），不在「提交并推送」的授权内。**下一轮处理。**

### 其余门禁现状（合并后全量跑）

- `go build ./...` / `go vet ./...` → exit 0
- `go test ./...`（设 DSN）→ **1 处 FAIL**，即上面那条 flashcards 护栏；
  `internal/email` 包本身 ok
- node 门禁：19 个 `check-*.mjs` 里 **14 绿 5 红**。逐个查清归属：
  - `check-device-token.mjs` → 需要命令行参数（`<dumpfile> <port…>`），是工具不是门禁
  - `check-fts-triggers-device.mjs` → 需要 `lobster` 库连接
  - `check-marketplace-contract.mjs` → 需要 127.0.0.1:18101 上起隔离后端
  - `check-dev-pass-sourcing.mjs` / `check-fixed-cdp-ports.mjs` → 命中全在
    `scripts/verify-*.mjs`（android 设备探针），**负控确认在 `87249317` 上同样红**
- CI 实际只跑 `backend.yml` 的 check-smart-quotes 与
  `frontend/scripts/run-gates.mjs --ci`，上面这 5 个**都不在 CI 路径上**。

## 四、遗留风险（如实记，不藏）

1. **⚠ 最重要：标注没有传导到汇总单。** 横幅两行仍以 `downloaded / 已核验`
   出现在 `invoices-summary-*.md` 与 `.csv` 里，仍占 CNY 合计 **61.1%**。
   `last_error` 不是导出列。**要让汇总单不计入，必须改导出口径**（过滤带
   `invoiceHumanMarkPrefix` 的行），本轮未授权、未做。这是下一轮第一顺位。
2. **`【人工标注】` 只是一个字符串前缀，没有任何代码消费它。**
   除了 `composeHarvestRetryMessage` 认它以免被覆盖，导出、统计、A4 排版
   全都不看它。⇒ 它现在只是「台账里的一句人话」，不是数据约束。
3. **工行那行只是被标注，没有被纠正。** 它仍会**每轮**被重试
   （本轮 attempts 2→3，`MaxInvoiceAttempts=8`），8 轮后会转 `failed`。
   也就是说一个已经确认「不是发票」的行，还要靠 8 次失败才安静下来。
   **更省事的做法是让准入失败的行不进采集选择集**，但那是行为变更，未授权。
4. **`发票-X-8.00-2026-10-04.pdf` 这个文件名还在。** 那是 2026-10-04 03:xx
   跑的旧轮次留下的，**本轮 build 之前**就在磁盘上。`通信-X-8.00-未知日期.pdf`
   只有**重新下载**才会产生；因为 `status=downloaded` 幂等跳过，本轮没有重新落盘。
   ⇒ round43 §遗留 2 描述的格式变更，**至今没有任何真实产物证明它**。
5. **第 3 项的标定只覆盖 PDF 路径。** 拍照/扫描件的长宽比分布完全未知。
   拿到真样本之前，`bannerAspectRatio` 的照片侧余量仍是未验证的。
6. **GitHub 付款回执仍未建档**（§2.6）。已定位到硬门槛与 `raw_body_gone_streak=1`，
   但**没有**验证它是否被 24 封/轮的拉原文预算挤掉（没拿到那轮报告）。
7. **本轮只跑了一轮手动流水线**，没验证 08:00 定时那条路由。
8. **`C:/workspace/openpocket-wt-i18n2` 仍未处理**：detached、无独有提交，
   且它的 `pocketd-main.exe` **正在运行**（PID 117108，端口 **18099**，自 12:41 起），
   并持有到同一个 PG 的空闲连接。
   ⇒ 本仓库**同时有两个 pocketd 指向同一个库**。定时流水线有
   `pg_try_advisory_lock`（锁键带 schema）兜底，但**手动触发那条路径不受锁保护**。
   我没动它（删进程/删 worktree 超出本轮授权）。**建议下一轮处理。**
10. **本轮自起的 `pocketd-pg.exe`（8088）已停掉。** 理由：它带着
   `POCKET_DEV_AUTH=true` + 一个我在本次会话里用明文设过的口令，
   且监听 `:8088`（**不是**只绑 127.0.0.1）——那是我自己制造的一处暴露，
   验证做完就没理由留着。复现只需：
   `go build -o ..\logs\pocketd-pg.exe ./cmd/pocketd` 然后
   `scripts\start-pocketd-pg.ps1`（该脚本会显式钉 dataDir / schema，
   但**不设** dev 旁路口令；要手动触发流水线需自行补 `POCKET_AUTH_PASS`）。
   ⚠ 顺带记下一个本轮踩到的点：dev 旁路的环境变量是 **`POCKET_AUTH_PASS`**，
   不是 `POCKET_DEV_AUTH_PASS`；后者会被静默忽略，日志只打一行
   `WARN: POCKET_AUTH_PASS not set; dev auth bypass disabled.`，登录直接 401。
11. **11 个 stash 一律未动**（最早 2026-10-01）。清理不在本轮授权内。
12. **语料文件 `logs/zz-*.tsv` 是临时产物**，在 `logs/`（已 gitignore），未入库。

---

## 五、下一轮提示词

```
接着 openpocket round44 做四件事，全部要对照证据，不要凭声明：

1. 【最高优先】标注没有传导到交付物。`invoices-summary-*.md` 与 `.csv` 里
   横幅两行仍占 CNY 合计 61.1%、仍标「已核验」，因为 `last_error` 不是导出列。
   - 问我要不要改导出口径（过滤带 `【人工标注】` 前缀的行，或加一列「备注」）；
   - 改完必须**真跑一轮流水线**并**打开新的 invoices-summary-*.md 逐行核对**
     （本轮的机器比对脚本思路可复用：解析表格 → 按
     seller|amount|currency|invoice_no|date|status 与库内做集合匹配）；
   - 注意两个口径必须分开：全表 CNY 68416.21 vs 汇总单口径 CNY 10392.21。

2. 查 GitHub 付款回执（`em-1298894461-acct-1790870162079171800-5`）：
   真实扣款回执但未建档，已定位到 ExtractInvoice 硬门槛（snippet 长度 0、
   无附件、raw_body_gone_streak=1）。**本轮没能验证**它是否被
   `maxInvoiceBodyFetches=24` 的每轮预算挤掉——需要拿到那轮的流水线报告
   （`InvoiceBodyFetchDeferred` 字段）才能下结论，不要猜。

3. 补**真实拍照发票**样本。本轮用 9 张真实电子发票 PDF 做了保守标定：
   票面长宽比实测 1.294–1.500（其中杭州创客家那张 1.500 超过 A4 横 1.414），
   横幅 4.086，阈值 2.5 两侧都有实测余量了。**但 PDF 量不出像素**——
   MediaBox 单位是 pt，同一张 A4 渲染 72dpi 是 595×841、300dpi 是 2480×3507。
   ⇒ 最小尺寸下限仍然没有标定依据，本轮确认不加。拿到照片样本后才好决定。

4. 处理 `C:/workspace/openpocket-wt-i18n2`：它的 `pocketd-main.exe` 正在跑
   （端口 18099），和主实例指向同一个 PG 库。定时流水线有 advisory lock
   兜底，但**手动触发不受锁保护**。另外本轮新起的 `pocketd-pg.exe`（8088）
   是否保留也要定。

纪律沿用：判据「通过」之前先确认它不是 skip（本轮又一次看到
TestMarkRetry_* 三条在不设 DSN 时静默跳过）；变异做双向；还原用显式反向替换，
`git checkout --` 在变异入索引后会取回变异版；改既有护栏必须在 handoff 里
单独说明。

⚠ 开工前先看 §三点五：main 上有一条**别人带进来的红**
（`internal/flashcards/seed_pg_test.go` 读生产 DSN），负控确认它在本轮之前
就已经是红的。不要把它算到 round44 头上，也不要因为「不是我弄的」就不管。
```

---

## 六、提交与推送记录

| 时间 | 事件 |
|---|---|
| 19:10:27 | `git commit` → `560f4a78`（7 files, 1554 insertions, 纯新增） |
| 19:10:33 | 并发会话 `git pull origin main` → 合并提交 `049bca53` |
| 19:11+ | 并发会话 push；`origin/main = 049bca53`，本地领先 0 |

工作树与两个 worktree 均已复核干净；`logs/.round44-commit-msg.txt` 等临时
产物**未入库**（`logs/` 已 gitignore）。
