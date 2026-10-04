# round43 —— 采信侧没有闸门：营销横幅至今仍能当发票凭证入库

日期：2026-10-04 12:46 → 13:2x（本机时区 +08:00）
范围：24 小时修正任务的审计与完善
基线：`6d3a9893`（开工时本地 main，与 origin/main 同一个 commit，0/0）

---

## 一、结论 / 根因

### 1.1 分支与工作区盘点：**没有未合并的有效变更，也没有僵尸分支**

| 引用 | 位置 | 相对 main | 判定 |
|---|---|---|---|
| 本地 `main` | 主工作区 | 与 `origin/main` 同为 `6d3a9893` | 干净，无本地改动 |
| `C:/workspace/openpocket-wt-i18n2` | detached HEAD `6d3a9893` | 0 独有提交 | 工作区只有 8 个**未跟踪**的 `pocketd-qpfix*.exe` / `*.log` |
| 远端 | — | `git for-each-ref refs/remotes/` 只有 `origin/main` | 无遗留分支 |

24 小时内 215 个提交（`git log --since="2026-10-03 12:46"`），**全部已在 main 上**。
⇒ 本轮没有需要抢救或逐文件合并的分支。11 个 stash 一律未动（`stash@{0}` 起，
最早 2026-10-01），删除它们不在本轮授权范围内。

### 1.2 主干基线

| 命令 | 结果 |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./... -count=1`（**不设 DSN**） | **54** ok / 0 FAIL |
| `go test ./... -count=1`（**设 DSN**） | **54** ok / 0 FAIL，但 `internal/email` 从 19s → **172s** |

包总数 72：有测试的 54（全部 ok），无测试文件的 18。
54 这个数与 round42 记的一致。**（我第一遍口头报过「56」，那是数错了，
以本表为准。）**

**最后一行是本轮的一条方法论发现**：不设 `POCKET_TEST_POSTGRES_DSN` 时，
大量 PG 支撑的用例**静默 skip**（`newWorkspaceTestStore` 直接 `t.Skip`）。
本轮新写的两条端到端用例最初就是这样「绿」的——它们根本没跑。
设上 DSN（`postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable`，
取自 `scripts/start-pocketd-pg.ps1:40`）后同一批用例**立刻转红**。
⇒ **判据「通过」之前先确认它不是 skip。**
（`internal/server` 同样从 23s → 61s，PG 隔离护栏那一块也是靠 DSN 才真跑的。）

### 1.3 本轮找到并修掉的真缺陷（2 个，都由真实产物坐实）

#### 缺陷 A（P0）：采信侧只问「这是不是图片」，横幅一路通行到财务合计

`1da030ab` 修的是**候选收集侧**（`reHTMLSrcs` + `extractInvoiceURLs` 的 inline
集合），把 `<img src=>` 的地址排除出下载候选。**采信侧一行没动**，判据至今是纯魔数：

```go
invoice_harvest.go:370  附件：isPDFBytes(att.Data) || isImageBytes(att.Data)
invoice_harvest.go:385  链接：dlErr == nil && (isPDFBytes(data) || isImageBytes(data))
invoice_harvest.go:679  建档门槛 HasInvoiceAttachment 同款
```

`isImageBytes` = 「magic 是 JPEG/PNG/WEBP」。横幅对这个问题**答 true**。
于是横幅从**任何不是 `src=`** 的路进来都照样被存成发票凭证：

- `<a href="https://cdn.x.com/banner.jpg">`
  （`1da030ab` 自己的反向保护第 1 条恰恰**要求** href 必须仍被收走——收候选是对的，
  缺的是收下来之后的采信闸门）
- 正文裸 URL
- 直接作为 MIME 附件带进来的图片（内联 `cid:` 图同样会被解成附件）

**坐实证据（不是推演）**。`data/email-invoices/ws_user-admin/` 里
2026-10-04 08:00 那轮真跑留下了两行：

```
其他-系统服务-6071.00-2026-09-15-26332000007943899111.jpg   21429 字节
其他-系统服务-283.20-2026-09-15-26112000003895678291.jpg   21429 字节
```

- 两个文件 **SHA256 完全相同**（`9CED44F438A7998…`）——同一份字节不可能是
  两笔不同发票的凭证；
- 打开第一个：一张百望平台宣传横幅，印着「**用心服务 贴心用户**」，
  实测尺寸 **572 × 140**（长宽比 **4.09**）；
- 台账里这两行是 `downloaded` / **已核验**，
  金额 6071.00 + 283.20 = **6354.20 CNY**，
  占当轮 CNY 合计 10392.21 的 **61.1%**。

⇒ 交给财务的是两张标语，还各配一个不同的金额与发票号。

**修法**：新增 `imagePlausibleAsVoucher`（`invoice_file.go`），在**两处落盘点**
各加一道闸，并按「横幅」单列 `last_error`。
判据只用几何、不用 OCR——`572×140` 与 A4（竖 0.707 / 横 1.414）差着一个数量级，
这个差距不需要识别文字，且判据必须能在**采信当场**跑完。
阈值 `bannerAspectRatio = 2.5`，标定依据就是上面那两个实测值。

#### 缺陷 B（P1）：文件名里的日期是**编的**，且取名不确定

`InvoiceFileName` 在 `inv.InvoiceDate == ""` 时填 `time.Now().Format("2006-01-02")`，
也就是**下载当天**。真实产物：

```
通信-X-8.00-2026-10-04.pdf          ← 日期段是下载日
invoices-summary-20261004-080022.md
  | 通信 | X | 8.00 USD |  |  | downloaded | 已核验 |    ← 台账「日期」列是空的
```

同一份数据，文件名里有个像模像样的日期，汇总单里明说没有。**文件名那份更像真的。**
与 round37 §35（金额=信用额度、日期=到期还款日）同类：
**一个看起来权威的错值，比留空危险得多。**

顺带一个非确定性缺陷：填当天日期意味着**同一张票隔天重试会得到另一个文件名**，
而 `pickFreeInvoicePath` 是按「目标名 + 内容相同」去重的，名字变了目标就不存在
→ 写出第二份副本，同一张票在目录里出现两次。

**修法**：改成显式占位 `未知日期`（与既有 `未知单位` 兜底同一套约定），
`time.Now()` 依赖一并消失。

### 1.4 审计过但**判定不是缺陷**的（记下来，免得下一轮重复查）

| 位置 | 为什么看着像问题 | 为什么不改 |
|---|---|---|
| 台账里 `中国工商银行 58000.00 / 2026-10-25` 那一行 | 金额与日期都是错值 | round37 §35 已查实、§46.8 已列出，**处置需单独授权**（作废还是标注是业务判断）。代码侧 `admitDebtNotice` 准入门已就位，**存量行不会自己消失** |
| `HasInvoiceAttachment`（建档门槛）也用 `isImageBytes` | 与缺陷 A 同款判据 | **刻意不动**。这一层决定「邮件是否建档」，拒掉的后果是发票**根本不建档**，比「行留在 pending」更难被看见；而落盘点已经有闸门了。可见的失败优于不可见的失败 |
| `isImageBytes` 本身 | 它就是缺陷的根 | **不能改**。`ExtractInvoiceThumb` / `DetectInvoiceMedia` 还在用它，列表页缩略图场景下横幅**本来就该**能显示。闸门加在 isImageBytes 上会把缩略图一起弄坏 |
| `classifyInvoiceKind` / `xmlinvoice.go:83` / `spam.go:248` 仍用裸 `strings.Contains` | 与 round42 §1.4 同一批 | round42 已查实判定不是缺陷，本轮无新证据，**维持原判** |

---

## 二、改动文件与关键行为

| 文件 | 关键行为 |
|---|---|
| `backend/internal/email/invoice_file.go` | 新增 `bannerAspectRatio = 2.5`、`imagePlausibleAsVoucher`（解码配置取宽高，长边/短边 ≥ 2.5 即拒；**解码不出尺寸则放行**）、`voucherRejectionReason`（把实测宽高与长宽比写进 `last_error`）。**`isImageBytes` 未改** |
| `backend/internal/email/invoice_harvest.go` | ① 附件分支与链接分支各加一道 `imagePlausibleAsVoucher`，横幅 `continue` 而不是 `saveInvoiceFile`；② 新增 `bannerErrs`，被拒原因与 `linkErrs` 分开，在末尾 `markRetry` 里**单列**一句「取到的图片不是发票凭证（横幅/装饰图）：…」，不再被笼统的 `no usable pdf/xml found` 盖掉；③ `InvoiceFileName` 的日期兜底 `time.Now()` → `未知日期` |
| `backend/internal/email/invoice_banner_voucher_admission_test.go` | **新增** 4 条（见 §3.3） |
| `backend/internal/email/invoice_filename_fabricated_date_test.go` | **新增** 3 条（见 §3.3） |
| `backend/internal/email/invoice_seller_label_test.go` | **改了一条既有护栏**，见 §2.1 |

### 2.1 我改了一条既有护栏（必须单独说明，不能混在改动列表里）

改缺陷 B 之后全量跑出**一条真回归**：

```
--- FAIL: TestExtractInvoice_HeaderRowTableRecoversSellerFromNextLine
    文件名 "其他-杭州某某科技有限公司-1280.00-未知日期-25332000000123456789.pdf" 只有 5 段，
    不符合 {费用类型}-{对方单位}-{金额}-{日期}[-{发票号}] 的段结构
```

根因值得单独记：`assertNoLabelSegment` 断言「日期占 3 段（年-月-日）」，
因为日期自带两个连字符。**这条断言其实隐含依赖了「发票日期一定存在」**——
而它以前之所以总是存在，正是因为那个 `time.Now()` 编造。
换成诚实的 `未知日期`（占 1 段）之后，段数从 6 变 5。

⇒ **我的改动没有破坏一个既有契约；它让一个一直靠「编造值」撑着的断言露了底。**

处置：给 `assertNoLabelSegment` 增加一个分支，**只多认一种合法形态，不放松任何既有保证**：

- 列头段逐段检查（`isInvoiceLabelWord`）——**位置与逻辑完全未动**，仍在最前面跑；
- 金额形态（`^\d+\.\d{2}$`）——**提到分支之前，两条路径都要过**；
- 段数为 4 或 5（占位形态）；
- 日期已知时那三段的 `年-月-日` 校验——**一字未改**。

**负控**：把新增分支的匹配串改成永不命中 → 该测试**转红**
（`只有 5 段…`），证明这条分支是**承重的**，不是把断言改成永真。

**一条如实记的观察**：把 `isInvoiceLabelWord(seg)` 整个短路掉，
现有用例**没有任何一条转红**。原因是这些夹具里 `reSeller` 本来就取对了，
那条检查只在 `reSeller` **回归**成列头时才会触发——
而 `reSeller` 本身在同一个测试第 191 行有直接断言兜着。
所以它是纵深防御，不是当前唯一防线；**我没有单独证明它有牙齿**，
不把它算进「已验证的负控」。


**为什么横幅原因要与链接失败分开记**：两者处置完全不同。链接失败值得重试；
「邮件里只有一张横幅」重试多少次都不会变好。混在一句里，运维会一直重试。

---

## 三、测试命令与结果

### 3.1 缺陷复现（**先红**，且红得与生产一致）

判据写完后**故意不接线**，`POCKET_TEST_POSTGRES_DSN` 设上后跑：

```
--- FAIL: TestHarvestOne_BannerAttachmentIsNotArchivedAsVoucher
    [email/invoice-harvest] saved 其他-系统服务-6071.00-2026-09-15.jpg (invoice=inv-bnr source=attachment)
    harvestOne = "downloaded"，横幅被当成了发票凭证。
--- FAIL: TestHarvestOne_BannerBehindHrefIsNotArchivedAsVoucher
    [email/invoice-harvest] saved 其他-系统服务-283.20-2026-09-15.jpg (invoice=inv-hrf source=pdf-url)
```

⇒ 复现出的文件名与 `data/email-invoices/ws_user-admin/` 里的真产物**逐字一致**，
两条路径各对一条（`source=attachment` / `source=pdf-url`）。
这不是「构造的用例」，是同一条生产路径。

### 3.2 负控对照（三次变异，全部**实测转红**，不是「我以为会红」）

| 变异 | 期望转红 | 实测 |
|---|---|---|
| ① 横幅闸门未接线（函数在、调用点无） | 两条 e2e | ✅ 2 条 FAIL（§3.1） |
| ② `bannerAspectRatio` 2.5 → **100.0**（闸门形同虚设） | 拒绝侧 | ✅ `RejectsObservedBannerGeometry` + 两条 e2e 共 **3 条** FAIL |
| ③ `bannerAspectRatio` 2.5 → **0.5**（收得太紧） | **反向保护** | ✅ `AcceptsDocumentGeometry` FAIL，报出 A4 竖/横、手机竖/横、方形、1×1 全部被误拒 |
| ④ `未知日期` → `time.Now()`（把缺陷放回去） | 文件名两条 | ✅ 2 条 FAIL，且打出 `文件名 "通信-X-8.00-2026-10-04.pdf" 含今天（2026-10-04）的日期` |
| ⑤ §2.1 新增的占位分支改成永不命中 | 那条既有护栏 | ✅ `TestExtractInvoice_HeaderRowTableRecoversSellerFromNextLine` FAIL（`只有 5 段…`） |


**②③ 是双向的**：只写「横幅被拒」的判据，一个把所有图片一律拒掉的实现照样全绿。
变异 ③ 就是为了证明反向保护真的有牙齿。

还原一律用**显式反向替换**，不用 `git checkout`（变异一旦入索引，
`git checkout --` 取回的就是变异版）；还原后 `git diff --stat` 复核只剩预期增量。

### 3.3 新增护栏

| 护栏 | 钉什么 |
|---|---|
| `TestImagePlausibleAsVoucher_RejectsObservedBannerGeometry` | 572×140（真实观测值）、750×200（注释里那个 URL 的真实比例）、600×100、竖版窄条 120×572 |
| `TestImagePlausibleAsVoucher_AcceptsDocumentGeometry` | A4 竖 1240×1754 / A4 横 / 手机竖 / 手机横 / 方形 / **1×1 最小 PNG**（既有护栏依赖的夹具） |
| `TestHarvestOne_BannerAttachmentIsNotArchivedAsVoucher` | 附件路：不得 `downloaded`、不得落盘、**必须留 `last_error`** |
| `TestHarvestOne_BannerBehindHrefIsNotArchivedAsVoucher` | `href` 路（`1da030ab` 留下的缺口），**带非空洞前提断言**：该 href 必须真的进了候选集合，否则「没试过」也会让判据变绿 |
| `TestInvoiceFileName_NoFabricatedDateWhenInvoiceDateEmpty` | 日期未知时不得出现**任何** `YYYY-MM-DD` 形态（不只钉 `2026-10-04` 这一个值） |
| `TestInvoiceFileName_IsDeterministicWhenInvoiceDateEmpty` | 连取 5 次一致 + 不含今天日期 |
| `TestInvoiceFileName_KeepsRealInvoiceDate` | 反向保护：日期已知时原样使用，斜杠仍换连字符 |

### 3.4 全量

```
cd backend && go build ./...                      → exit 0
cd backend && go vet ./...                        → exit 0
cd backend && go test ./... -count=1              → 54 ok / 0 FAIL（设 DSN）
node scripts/check-gofmt.mjs                      → exit 0（真债 0）
node scripts/check-smart-quotes.mjs               → exit 0
node scripts/check-blankline-bloat.mjs            → exit 0（>35% 且 >=80 行：0）
node scripts/check-pg-schema-hardcoded.mjs        → exit 0
node scripts/check-main-overlap.mjs               → exit 0
```

> `gofmt` 门禁第一次跑是**红的**：真债 1，正是本轮新增的
> `invoice_banner_voucher_admission_test.go`（结构体字段对齐）。
> 按门禁自己的提示跑了两遍 `gofmt -w` 才收敛到 0。
> 另注：`gofmt -l` 原样输出 853 个文件里 **852 个是 CRLF 伪债**，
> 真债要看门禁归一化后的那个数字，不要看 `gofmt -l` 的行数。

---

## 四、遗留风险（如实记，不藏）

1. **台账里那两行假发票仍然在，仍然标着「已核验」。**
   本轮修的是**代码**，不碰存量数据。`其他-系统服务-6071.00-…jpg` 与
   `…-283.20-…jpg` 两个文件、以及它们在汇总单里 6354.20 CNY 的贡献，
   要作废还是标注，**需单独授权**。在那之前，任何一份新出的汇总单里
   这 61.1% 都还在。
2. **【需要你知情】凭证文件名格式变了。** 日期未知时，日期段从
   `<下载当天>` 变成 `未知日期`：
   `通信-X-8.00-2026-10-04.pdf` → `通信-X-8.00-未知日期.pdf`。
   这是**交付物格式**的改动，不只是内部实现。如果下游有按
   `-YYYY-MM-DD` 解析文件名的脚本或导入模板，需要一并调整。
   已知日期的文件名**完全不变**。若你认为格式不能动，
   告诉我，我把这一处回退成 `time.Now()` 并只保留确定性那部分讨论——
   但那样「文件名日期是编的」这条就会继续存在。
3. **缺陷 B 的取舍是「形状」换「诚实」。** 需求原文的格式是
   `{费用类型}-{对方单位}-{金额}-{日期}.pdf`，而日期**有时根本解析不出来**
   （`通信-X-8.00-2026-10-04.pdf` 就是证据）。三种选择：
   填当天日期（形状对、值假）、显式占位（形状变、值真）、或留空
   （`…-8.00-.pdf`，形状变且易被误读成解析失败）。本轮选了第二种。
   **这是一个业务判断，如果你更看重格式稳定，请推翻我。**
4. **`58000.00` 那一行也还在**（round37 §35/§46.8 的结论未变，同样待授权）。
   所以当前 `invoices-summary-*.md` 里的 12 行，至少 3 行是假的。
5. **`imagePlausibleAsVoucher` 是几何启发式，不是内容识别。**
   4.09 对 2.5 的余量很大，但**没有 OCR 兜底**：一张
   2.4:1 的横幅会被放行。一张横置拍摄的发票（长宽比 ≈ 1.4）不会被误杀，
   这条我有把握；「什么样的真票会超过 2.5」我没有真样本可证。
6. **webp 仍有洞**：`image.DecodeConfig` 没有 stdlib webp 解码器，
   解码失败一律**放行**（fail-open）。这是有意的取舍——
   宁可放过一张 webp 横幅，也不要因为「测不出尺寸」把真票判死。
   要堵需要引第三方解码器。
7. **没有最小像素尺寸下限。** 既有护栏的夹具是一张 **1×1** 最小 PNG，
   而我手上**没有任何一张真实拍照发票样本**可以标定下限。
   「没有证据就不改」——加了会打红一条既有护栏，且那个阈值是编的。
8. **本轮没有真机 / 真数据重跑 08:00 流水线。**
   结论止于「代码不再把横幅存成凭证」+「在真实数据目录里确认了问题存在」，
   **没有**证明「下一轮 08:00 跑出来的汇总单是干净的」——
   因为那还取决于那两行存量数据被授权作废。
9. **`C:/workspace/openpocket-wt-i18n2` 仍未处理**：detached、无独有提交，
   但有 8 个未跟踪的 `pocketd-qpfix*.exe` / `*.log`。
   删 worktree 会连带不可恢复地丢掉它们，**本轮不擅自删**（同 round42）。
10. **11 个 stash 一律未动**（最早 2026-10-01）。清理它们不在本轮授权内。
11. **并发写入风险非零**：开工时 `git status --porcelain` 干净、
    `origin/main` 与本地同为 `6d3a9893`；提交前会再 `git fetch` 复查。


---

## 五、下一轮提示词

```
接着 openpocket round43 做三件事，全部要对照证据，不要凭声明：

1. 【要授权才能做，但结论已经备好】把台账里那 3 行假数据处置掉：
   - `其他-系统服务-6071.00-…jpg` 与 `…-283.20-…jpg`
     （两张 SHA256 相同的百望宣传横幅，6071.00 + 283.20 = 6354.20 CNY）
   - `中国工商银行 58000.00 / 2026-10-25`（round37 §35 已查实，金额是信用额度、
     日期是到期还款日）
   问我要「作废」还是「保留并标注」，然后真跑一轮定时流水线，
   **打开新产出的 invoices-summary-*.md 逐行核对**（不要只看测试绿）。

2. 量化 round42 遗留的那个问题（本轮仍未做）：
   回真实库统计 90 天窗口内「主题或摘要含 `Invoice:` / `Receipt:` / `Invoice#`
   形态，且最终未建档」的邮件条数，区分「本来就不是发票」与「被词边界漏掉的真发票」。
   只报数量与可核查的邮件清单，不要报「大概」。

3. 补一张**真实**拍照发票样本，然后回头评估缺陷 A 的遗留风险 3 与 5：
   - 拿真票标定 `bannerAspectRatio`（现在的 2.5 只有横幅一个数据点支撑，
     A4 的 0.707/1.414 是算出来的不是量出来的）；
   - 有真样本之后才好决定要不要加最小尺寸下限（现在不敢加，
     因为既有护栏的夹具是 1×1）。

纪律沿用 round43：判据「通过」之前先确认它不是 skip
（`POCKET_TEST_POSTGRES_DSN` 不设时大量 PG 用例静默跳过，
本轮新写的两条 e2e 一开始就是这么「绿」的）；变异做双向
（放松过头与收得太紧都要试）；还原用显式反向替换，
`git checkout --` 在变异入索引后会取回变异版。
```
