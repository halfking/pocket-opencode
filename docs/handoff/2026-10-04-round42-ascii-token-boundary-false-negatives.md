# round42 —— 词边界收紧过头：e54d797d 修掉假阳性的同时造出了一批真发票的假阴性

日期：2026-10-04 09:46 → 10:20（本机时区 +08:00）
范围：24 小时修正任务的审计与完善
基线：`533ef548`（本轮开工时本地 main）→ 快进到 `ac16921d`（origin/main）

---

## 一、结论 / 根因

### 1.1 分支与工作区盘点结论：**没有需要抢救的分支**

24 小时内本地只有两个引用，逐个查完的结论是**都已在 main 里，没有独有提交**：

| 引用 | 位置 | 相对 main | 判定 |
|---|---|---|---|
| `docs/round37-section10` | worktree `.wt-build` | `main..branch` = 0，`branch..main` = 0 | 与 origin/main **同一个 commit** `ac16921d`，无独有内容 → 已删除 |
| `C:/workspace/openpocket-wt-i18n2` | detached HEAD `ed22db11` | `main..ed22db11` = 0 | 无独有提交；工作区只有 8 个未跟踪的 `pocketd-qpfix*.exe` / `*.log` 临时产物 |
| `origin` 远端 | — | `git ls-remote --heads` 只有 `refs/heads/main` | 远端无遗留分支 |

⇒ **本轮没有「未合并的有效变更」需要逐文件合并**，也没有需要删除的僵尸分支（唯一那个
零差异分支已删）。这条结论是实测的，不是推测：`git rev-list --count main..<ref>` 对
两个引用都返回 0。

### 1.2 主干基线

- `go build ./...` → exit 0
- `go vet ./...` → exit 0
- `go test ./... -count=1` → **54 ok / 0 FAIL**（改动前后同一数字，见 §3.2）
- 门禁 `check-gofmt` / `check-smart-quotes` / `check-blankline-bloat` /
  `check-pg-schema-hardcoded` / `check-main-overlap` → 全部 exit 0

### 1.3 本轮找到并修掉的真缺陷（2 个，都是 24 小时内引入的）

#### 缺陷 A（P1）：词边界把正常标点当成「词内」，真发票主题**漏判**

`e54d797d`（发票候选判定）与 `193ed70c`（费用类型判定）把「词边界」实现成
**一个**字符类，把 URL 标点 `- . / : ? & = # @ + %` 与字母数字**一起**算进「词内」。
本轮逐条实测（修复前的真实返回值）：

```
invoiceKeywordHit("Invoice: ACME Corp")                = false   ← 漏判
invoiceKeywordHit("Your invoice: https://…/inv.pdf")   = false   ← 漏判
invoiceKeywordHit("Receipt: #2662-4636-8457")          = false   ← 漏判
invoiceKeywordHit("Invoice#INV-2026-0001")             = false   ← 漏判
invoiceKeywordHit("Invoice/Receipt for September")     = false   ← 漏判

classifyInvoiceCategory("AWS: 您的账单")                = "其他"  ← 原为「通信」
classifyInvoiceCategory("Stripe receipt for SaaS. Thanks!") = "其他" ← 原为「通信」
classifyInvoiceCategory("invoice from a hotel.com partner") = "其他" ← 原为「住宿」
```

`Invoice:` 是英文发票邮件**最常见**的主题形态。⇒ 上一轮把 24 格
`maxInvoiceBodyFetches` 里的假阳性换成了**真发票永远进不了候选队列**，两个方向的
代价同量级。

**为什么上一轮的判据没抓到**：`invoice_keyword_wordboundary_test.go` 的反向保护
（`TestInvoiceKeywordHit_StillAcceptsRealInvoiceSemantics`）只用了**空格分隔**的
形态（`"Your invoice is ready for download"`），**恰好绕开了所有出问题的标点**。
⇒ 教训不是「要写反向保护」，而是「**反向保护必须覆盖修复动过的那一维**」。
本轮补的用例里，`asciiTokenPunctuationAdjacentMustHit` 前 5 条就是修复前的实测输出。

**为什么费用类型侧也有**：`categoryTokenBoundary` 的注释写着「与 `keywordBoundaryClass`
**刻意写成同一个**：同一个缺陷模式要在所有入口堵住，两处判据就不能各写各的」。
本意对，做法错 —— **复制同一段有缺陷的判据，等于把同一个假阴性也复制了一份**。
⇒ 「不能各写各的」要落到**共用一个函数**上，不是共用一个字符类。

#### 缺陷 B（P2）：`reHTMLSrcs` 注释声称覆盖 `background`，正则只匹配 `src`

`1da030ab` 修「内联图片被当成发票文件下载」的注释写的是
「匹配内联资源属性（img/src、**background** 等）」，而正则是 `\bsrc\s*=`。
于是 `<div background="https://cdn.x.com/mail/bg?w=750&h=200">` 里的营销横幅
**仍被 `reBareURLs` 捞进候选** —— 即该提交声称堵掉的同一个泄漏，从 `background=`
这条路又漏回来，而**注释与代码不一致，读者无从察觉**。

实测（修复前）：`extractInvoiceURLs('<div background="https://cdn.baiwang.com/mail/bg?w=750&h=200&t=abc">x</div>')`
返回 `["https://cdn.baiwang.com/mail/bg?w=750&h=200&t=abc"]`；修复后返回 `nil`。

### 1.4 审计过但**判定不是缺陷**的（记下来，免得下一轮重复查）

| 位置 | 为什么看着像同一类 | 为什么不改 |
|---|---|---|
| `invoice.go:652-658` `classifyInvoiceKind` | 对 `"special vat"` / `"e-invoice"` / `"receipt"` 仍用裸 `strings.Contains` | 这三个词**都是多字符、语义特异**的，不存在 `vat`→`activation`、`aws`→`amazonaws` 那种短词碰撞；且输入已过 `admitDebtNotice`/附件/发票号准入。**没有证据就不改** —— 判别式是「词的字符数与特异度」，不是「有没有用 Contains」 |
| `xmlinvoice.go:83` `containsAny` | `strings.Contains` 逐键匹配 | 输入是 **XML 标签名**（`platenumber`/`invoicedate`…），不是自由正文，不存在 URL 子串碰撞 |
| `spam.go:248` | 关键词表裸匹配 | 表是人工策展的短语，不是词边界门禁；本轮无证据表明有误判 |

### 1.5 上一轮**已核实为真、确认无需再动**的（省下一轮重复劳动）

- `reLabeledEnglishDate`（`565388a2`）第 4 层**确实是纯增量**：
  `parseInvoiceDateAt("开票日期：2026年09月15日 Paid September 17, 2026")` = `"2026-09-15"`
  （中文层胜出，未被英文层改写）；无效日期 `"Paid February 30, 2026"` = `""`（未静默进位）。
- `reSellerFromIssuerPhrase`（`3b78da06`）语序与位置都对：`[\p{Han}…]{2,40}?` 非贪婪
  在真实样本上取到的是 `浙江智谱新篇科技有限公司`，不是「尊敬的 杭州开轩… 用户」。
- `invoiceKeywordASCIINegPhrases`（`billing cycle|period`）与词边界确实是**两类**问题，
  分开处理是对的：前者是「词边界正确命中了但语义无关」，后者是「子串巧合」。

---

## 二、改动文件与关键行为

| 文件 | 关键行为 |
|---|---|
| `backend/internal/email/invoice.go` | 新增 `asciiTokenWordChar` / `asciiTokenURLPunct` 两个常量 + `asciiTokenRegex` / `asciiTokenHit` 两个函数。**两级判定**：① 紧邻词字符（字母/数字/下划线）⇒ 否决（`vat` 撞 `activation`）；② **两侧**都是 URL 标点 ⇒ 判为 URL 路径段，也否决（`/billing/`）；**单侧标点不再被误伤**（`Invoice:`、`hotel.com`）。`invoiceKeywordHit` 与 `hasCategoryToken` 改为共用 `asciiTokenHit`。删除 `keywordBoundaryClass` / `categoryTokenBoundary` 两个常量 |
| `backend/internal/email/invoice_harvest.go` | `reHTMLSrcs` 由 `\bsrc\s*=` 扩为 `\b(?:src\|background\|poster)\s*=`。`\b` 开头顺带覆盖 `data-src=`。注释与代码从此一致 |
| `backend/internal/email/invoice_ascii_token_boundary_overreach_test.go` | **新增** 4 条护栏（详见 §3.3） |

**为什么是「两侧」而不是「任一侧」**：任一侧就等于把缺陷 A 原样留着。
为什么不是「两侧都不是」：`Invoice:` 的冒号、`hotel.com` 的点都只出现一次，
构不成「这个词只存在于 URL 里」的证据。

---

## 三、测试命令与结果

### 3.1 逐条实测（修复前 → 修复后）

```
invoiceKeywordHit:
  "Invoice: ACME Corp"              false → true
  "Your invoice: https://…/inv.pdf" false → true
  "Receipt: #2662-4636-8457"        false → true
  "Invoice#INV-2026-0001"           false → true
  "Invoice/Receipt for September"   false → true
  "see https://console.aws.amazon.com/billing/home"   仍 false ✓（原修复未失效）
  "Click to activate your account now"                仍 false ✓
  "This is a private repository"                      仍 false ✓
  "open https://x.io/invoice/download now"            仍 false ✓

classifyInvoiceCategory:
  "AWS: 您的账单"                     其他 → 通信
  "Stripe receipt for SaaS. Thanks!"  其他 → 通信
  "invoice from a hotel.com partner"   其他 → 住宿
  "https://stripe-images.s3.amazonaws.com/logo.png"   仍 其他 ✓

extractInvoiceURLs:
  <div background="…/bg?w=750&t=abc">   [1 条] → nil
  <img src="…/banner?w=750&t=abc">      nil  → nil（未回归）
  同体内真下载链接                       仍被收进 ✓
```

### 3.2 全量

```
cd backend && go build ./...            → exit 0
cd backend && go vet ./...              → exit 0
cd backend && go test ./... -count=1    → 54 ok / 0 FAIL   （改动前基线同为 54 ok / 0 FAIL）
node scripts/check-gofmt.mjs            → exit 0（真债 0）
node scripts/check-smart-quotes.mjs     → exit 0
node scripts/check-blankline-bloat.mjs  → exit 0（964 文件，>35% 且 >=80 行：0）
```

### 3.3 新增护栏 + **负控对照**（绿灯不算数，必须证明它抓得住回归）

| 护栏 | 钉什么 |
|---|---|
| `TestASCIITokenHit_PunctuationAdjacentKeywordsStillHit` | 紧邻单个标点的真发票形态必须命中（缺陷 A 的回归证据本身） |
| `TestASCIITokenHit_RejectsSubstringsAndURLSegments` | 上一轮修掉的假阳性**不许**被放回来（防放松过头） |
| `TestASCIITokenHit_TwoSidedURLRule` | 直接钉「两侧」这条规则本身，含 `{"/billing now": true, "x/billing/y": false}` 最小对照 |
| `TestClassifyInvoiceCategory_PunctuationAdjacentTokens` | 同一缺陷的**第二个入口** |
| `TestExtractInvoiceURLs_IgnoresInlineResourceAttributes` | `src`/`background`/`poster`/`data-src` 都不收；**且**同体内的真下载链接必须仍被收（防止判据靠「一律返回 nil」蒙混） |

**负控实测**（不是「我以为它会红」）：

- 变异 1：`asciiTokenHit` 里 `leftURL && rightURL` → `leftURL || rightURL`
  → `PunctuationAdjacent` / `TwoSidedURLRule` / `Category_PunctuationAdjacent` **三条转红** ✓
- 变异 2：`reHTMLSrcs` 退回 `\bsrc\s*=`
  → `IgnoresInlineResourceAttributes` 的 `background` / `poster` 两个子用例**转红** ✓
- 还原用**显式反向替换**，不用 `git checkout`；还原后在 git 对象层核对：
  `git grep --cached 'leftURL || rightURL'` 无输出，
  `git grep --cached 'leftURL && rightURL'` 与 `'src|background|poster'` 均有命中。

**我自己的一个期望值写错过，如实记**：负控写用例时把 `{"a/billing"}` 期望成
`false`，实测 `true`。查下来是**我的期望错了、代码是对的** —— 该串右侧是文本结尾
（零宽边界），单侧判不出路径段。已改成 `true` 并在用例里注明这是限制。
⇒ 判据红时先分清「实现错」还是「期望错」，别直接改实现去迎合断言。

---

## 四、遗留风险（如实记，不藏）

1. **本轮只验了函数层 + 包层，没有真机/真数据端到端重跑 08:00 流水线。**
   缺陷 A 的影响面（有多少真发票曾被 `Invoice:` 形态漏掉）**没有量化** ——
   要量化得回真实库统计 90 天窗口里「主题含 `Invoice:`/`Receipt:` 且未建档」的邮件数。
   本轮只证明了「现在不再漏判」，**没有**证明「过去漏了多少」。
2. **`asciiTokenHit` 的单侧限制**：命中词位于文本**结尾**且左侧是 URL 标点时
   （`a/billing`）判为命中。真实邮件正文里这种情况罕见，但**不是零**。
3. **缺陷 B 的 `background`/`poster` 覆盖面未经真实语料验证**：我只证明了
   「正则现在匹配这两个属性」，**没有**统计真实邮件里 `background=` 的实际出现率。
   若真实语料里根本没有这种写法，这段扩展是预防性的。
4. **`invoice_ascii_token_boundary_overreach_test.go` 未登记进任何豁免表** ——
   经查 `pg_test_isolation_guard_test.go` 的规则只对**打开 PG 连接**的文件生效，
   本文件不碰 PG，因此不需要登记（不是漏登记）。
5. **`C:/workspace/openpocket-wt-i18n2` 未删除**：detached HEAD、无独有提交，
   但工作区有 8 个**未跟踪**的 `pocketd-qpfix*.exe` / `*.log`。
   删 worktree 会连带不可恢复地丢掉这些文件，**本轮不擅自删，留待授权**。
6. **并发写入风险已排除但非零**：开工时连查两次 `git status --porcelain`（间隔 20s）
   并 `git fetch` 确认 `origin/main` 停在 `ac16921d` 未动，两个 worktree 均干净。
   提交前会再 `git fetch` 复查一次。
7. `docs/round37-section10` 分支已删；`.wt-build` worktree 保留（它现在检出的是
   已删除分支的 detached 状态，可随时 `git worktree remove` 回收，本轮未动）。

---

## 五、下一轮提示词

```
接着 openpocket round42 做三件事，全部要对照证据，不要凭声明：

1. 量化缺陷 A 的历史影响面（这是 round42 明确没做的）：
   回真实库统计 90 天窗口内「主题或摘要含 `Invoice:` / `Receipt:` / `Invoice#`
   形态，且最终未建档」的邮件条数，区分「本来就不是发票」与「被词边界漏掉的真发票」。
   只报数量与可核查的邮件清单，不要报「大概」。

2. 跑一次真实数据端到端：重建二进制 → 08:00 定时流水线 → 打开新产物核对
   费用类型与开票方。round42 只验到函数层与包层，**没有**验过真数据。

3. 决定 `C:/workspace/openpocket-wt-i18n2` 的去留：它有 8 个未跟踪的
   pocketd-qpfix*.exe / *.log。需要我先列出这些文件的大小与时间戳再删，
   还是直接 `git worktree remove --force` 回收。

纪律沿用 round42：每条结论标注是「实测」还是「假设」；判据要做负控对照
（改坏实现确认转红）并用 git 对象层核对还原；门禁在提交前跑全量而不是只跑被改的包。
```
