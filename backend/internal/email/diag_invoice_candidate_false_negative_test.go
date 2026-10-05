package email

// diag_invoice_candidate_false_negative_test.go — **只读**诊断：
// 量化 round42 遗留的那个问题——「词边界漏掉真发票」在真实库里到底值多少封。
//
// ## 要回答的问题（round42 §遗留、round44 接力）
//
// 回真实库统计窗口内「主题或摘要含 `Invoice:` / `Receipt:` / `Invoice#` 形态，
// 且最终未建档」的邮件条数，并区分两类：
//
//	(a) 本来就不是发票（AWS 付款方式失败提醒、GitHub 付款回执……）
//	(b) 被词边界漏掉的真发票
//
// ## 口径：为什么要同时跑「旧实现」和「新实现」
//
// 只跑现行 `InvoiceCandidate` 只能回答「现在还漏不漏」，回答不了
// 「round42 那条词边界缺陷的爆炸半径有多大」——因为修复已经在 main 上。
//
// 所以这里**并行**跑三组，逐封对齐：
//
//	old  = e54d797d 的词边界（URL 标点全算「词内」）——**重建**实现
//	new  = 现行 invoiceKeywordHit / InvoiceCandidate（**生产函数**，直接调）
//	arch = 该 email_id 在 email_invoices 里有没有行（真实库事实）
//
// 关键的一格是 `new && !old && !arch`：现行放行、旧实现漏判、而它**至今没建档**。
// 这说明挡住它的**不是**词边界，而是下游别的环节（准入门、正文预算、附件形态……）。
// 把这一格和「old&&new 都命中却仍未建档」分开，才能说清漏判到底卡在哪一层。
//
// ## ⚠ old 实现是**重建**的，必须自证
//
// e54d797d 的实现已随 round42 被替换掉，不在本仓任何文件里。本文件按
// `git show e54d797d:backend/internal/email/invoice.go` 的原文重建：
//
//	const keywordBoundaryClass = `^|[^0-9A-Za-z_\-./:?&=+#@%]`
//	regexp.MustCompile(`(?i)(`+keywordBoundaryClass+`)`+kw+`($|[^0-9A-Za-z_\-./:?&=+#@%])`)
//
// 重建的东西不能自证就等于没证据。所以本测试**先**跑一遍 round42
// 记在 `invoice.go` 注释里的 5 个已知旧返回值（全部应为 false），
// 对不上就直接 t.Fatalf —— 语料统计在后，一致性检查在前。
//
// ## 语料不进仓库
//
// 走环境变量 POCKET_DIAG_CANDIDATE_CORPUS（psql 导出的 TSV），
// 测试本身**不连数据库**。列：id / date / archived / subject / snippet / ai_summary，
// 换行与回车已在导出时折成空格，所以按行解析是安全的。
//
//	go test ./internal/email/ -run DiagInvoiceCandidateFalseNegative -v \
//	  （先设 POCKET_DIAG_CANDIDATE_CORPUS=...）

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// oldKeywordBoundaryClass 是 e54d797d 的词边界字符类，原文照抄。
//
// 与现行 asciiTokenWordChar/ASCIIURLPunct 的差别就是全部争议所在：
// 旧版把 `- . / : ? & = # @ + %` **全部**当成「词内」，
// 于是 `Invoice:` 的冒号算「紧邻词字符」⇒ 整个词被判成别人的子串。
const oldKeywordBoundaryClass = `^|[^0-9A-Za-z_\-./:?&=+#@%]`

var oldKeywordRegexes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(invoiceKeywordASCII))
	for _, kw := range invoiceKeywordASCII {
		out = append(out, regexp.MustCompile(
			`(?i)(`+oldKeywordBoundaryClass+`)`+regexp.QuoteMeta(kw)+`($|[^0-9A-Za-z_\-./:?&=+#@%])`))
	}
	return out
}()

// oldInvoiceKeywordHit 是 e54d797d 的 invoiceKeywordHit 重建版。
//
// 中文关键词表与负向短语**直接复用现行的**——round42 只动了英文词边界那一层，
// 复用现行的才不会把「中文侧」也一起变成旧行为。
func oldInvoiceKeywordHit(text string) bool {
	t := strings.ToLower(text)
	for _, kw := range []string{
		"发票", "电子发票", "增值税", "开票", "票据", "收据",
		"账单", "对账单", "订单确认", "支付成功", "扣款",
	} {
		if strings.Contains(t, kw) {
			return true
		}
	}
	hit := false
	for _, re := range oldKeywordRegexes {
		if re.MatchString(t) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	for _, re := range invoiceKeywordASCIINegPhrases {
		if re.MatchString(t) {
			return false
		}
	}
	return true
}

// asciiVoucherForm 是本轮要数的那个形态：`Invoice:` / `Receipt:` / `Invoice#`。
//
// 刻意**只**取 `[:：#]` 三种分隔符，不用裸 `invoice`：
// 裸词会把 `[QQ Wallet] Electronic Invoice Issuance Notice` 这类
// 「主题里出现 invoice 但形态完全不同」的邮件也拖进来，
// 而 round42 的缺陷只发生在**词紧邻 URL 标点**的形态上。
// 口径放宽一倍，报出来的数就没法对照 round42 的结论了。
var asciiVoucherForm = regexp.MustCompile(`(?i)(invoice|receipt)\s*[:#：]`)

// asciiBareForm 是**宽口径**对照：只要出现 invoice/receipt 裸词就算。
//
// 为什么要两套口径：窄口径实测在真实库里命中 0 封（psql 侧独立核对过），
// 那么「窄口径 0 封」这件事本身无法区分「这个缺陷没造成损失」和
// 「这个形态压根没人这么写」。宽口径给出同一批邮件的**实际**判定结果，
// 才谈得上有意义的损失评估。两套口径的差别必须在报告里说清，不能只报好看的那个。
var asciiBareForm = regexp.MustCompile(`(?i)(invoice|receipt)`)

type candRow struct {
	id       string
	date     int64
	archived bool
	subject  string
	snippet  string
	aiSum    string
}

func TestDiagInvoiceCandidateFalseNegative(t *testing.T) {
	// ── 第 0 关：闸门。没显式打开就 skip，**不是** Fatal ──
	//
	// 这一条是实测踩出来的：第一版没有闸门，只在后面检查语料路径，
	// 于是全量 `go test ./...`（不设该变量）时它在 0.00s 直接 Fatal，
	// 把一个「本轮没打开的只读诊断」变成整包红。
	// ⇒ 闸门缺失（未打开）= skip；闸门打开但语料没给 = Fatal。两者必须分开。
	if os.Getenv("POCKET_DIAG_CANDIDATE") != "1" {
		t.Skip("set POCKET_DIAG_CANDIDATE=1 (+ POCKET_DIAG_CANDIDATE_CORPUS=path to the psql-exported TSV) to run (read-only)")
	}

	// ── 第 1 关：重建的旧实现先自证，对不上就不许谈语料 ──
	//
	// 这 5 个期望值来自 round42 写进 `invoice.go` 注释里的**修复前实测**：
	// 全部应为 false（=被词边界漏判）。任何一条对不上，
	// 说明我重建错了（改错了字符类、或误动了中文侧），后面的统计全部作废。
	oldKnown := []struct {
		text string
		want bool // e54d797d 实测返回值
	}{
		{"Invoice: ACME Corp", false},
		{"Your invoice: https://x.example/inv.pdf", false},
		{"Receipt: #2662-4636-8457", false},
		{"Invoice#INV-2026-0001", false},
		{"Invoice/Receipt for September", false},
		// 对照：空格分隔的形态两代实现都应命中，防止重建把词边界收得比 e54d797d 更紧。
		{"Your invoice is ready", true},
		{"we have your receipt", true},
	}
	for _, c := range oldKnown {
		if got := oldInvoiceKeywordHit(c.text); got != c.want {
			t.Fatalf("重建的旧实现在 %q 上返回 %v，与 e54d797d 实测的 %v 不符 —— "+
				"重建错了，下面所有统计都不成立", c.text, got, c.want)
		}
	}
	// 新实现对同一批的真实发票形态必须全部命中（round42 修的就是这个）。
	newKnown := []string{
		"Invoice: ACME Corp", "Your invoice: https://x.example/inv.pdf",
		"Receipt: #2662-4636-8457", "Invoice#INV-2026-0001", "Invoice/Receipt for September",
	}
	for _, s := range newKnown {
		if !invoiceKeywordHit(s) {
			t.Errorf("现行 invoiceKeywordHit 在 %q 上漏判 —— round42 的修复似乎被回退了", s)
		}
	}

	path := os.Getenv("POCKET_DIAG_CANDIDATE_CORPUS")
	if path == "" {
		t.Fatal("POCKET_DIAG_CANDIDATE_CORPUS 未设置（本诊断无缺省值）——" +
			"没有语料会静默统计 0 封然后「结论：无漏判」")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	var rows []candRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 6 {
			continue
		}
		var ts int64
		if _, err := fmt.Sscanf(parts[1], "%d", &ts); err != nil {
			continue
		}
		rows = append(rows, candRow{
			id:       parts[0],
			date:     ts,
			archived: parts[2] == "1",
			subject:  parts[3],
			snippet:  parts[4],
			aiSum:    parts[5],
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// 0 行 ⇒ 必须报红。「语料是空的」和「一封都没漏」输出上完全一样。
	if len(rows) == 0 {
		t.Fatalf("语料 %s 解析出 0 行 —— 统计不成立", path)
	}

	// 实际窗口。**不假设**它是 90 天：邮件库可能只覆盖更短的一段，
	// 拿 32 天的数据说「90 天窗口」是本仓库反复踩过的措辞陷阱。
	minD, maxD := rows[0].date, rows[0].date
	for _, r := range rows {
		if r.date < minD {
			minD = r.date
		}
		if r.date > maxD {
			maxD = r.date
		}
	}
	spanDays := int((maxD-minD)/86400) + 1
	t.Logf("语料 %d 封，实际窗口 %s → %s（%d 天），已建档 %d 封",
		len(rows), ts(minD), ts(maxD), spanDays, countArchived(rows))
	if spanDays < 90 {
		t.Logf("⚠ 实际窗口只有 %d 天，**不足 90 天**。下面的数是这 %d 天的全部，不是 90 天的抽样。", spanDays, spanDays)
	}

	// 两套口径各跑一遍同一段统计——写成函数就是为了避免「窄口径用一套代码、
	// 宽口径手写另一套」导致两边口径悄悄分叉（本轮已经踩过一次 psql 侧与
	// Go 侧口径不一致）。
	reportForm(t, rows, asciiVoucherForm, "窄口径：`Invoice:` / `Receipt:` / `Invoice#` 形态")
	reportForm(t, rows, asciiBareForm, "宽口径：裸词 invoice / receipt")
}

// candHit 是一封候选邮件的判定结果。
//
// 四列各自对应流水线里**真实存在的一道闸门**，不是同一件事的四种说法：
//
//	kwNew / kwOld : 关键词层（现行词边界 vs e54d797d 词边界）
//	admit         : admitDebtNotice —— 债务通知形态（对账单/还款提醒）额外要求真发票语义
//	extract       : ExtractInvoice(e, "") 的最终 hit，即**建档硬门槛**是否通过
//	archived      : 真实库里 email_invoices 有没有行（最终事实）
//
// 把 admit / extract 单独列出来，是因为「候选命中却没建档」有两种完全不同的原因：
// 挡在关键词之后（词边界问题，round42 的范围），还是挡在金额/发票号硬门槛之后
// （根本不是词边界的问题）。不分开就会把后者算到前者头上。
type candHit struct {
	r        candRow
	kwNew    bool
	kwOld    bool
	admit    bool
	extract  bool
	formInAI bool
}

// stoppedBy 给出这封邮件到底卡在哪一道闸门。
func (c candHit) stoppedBy() string {
	if c.extract {
		return "ExtractInvoice 已命中 ⇒ 应当建档；没建档要往**建档之后**查（幂等/写库/后续步骤）"
	}
	if !c.admit {
		return "admitDebtNotice 拒收（债务通知形态，无真发票语义）"
	}
	if !c.kwNew {
		return "invoiceKeywordHit 不命中"
	}
	return "ExtractInvoice 硬门槛未过（抽不到金额且抽不到发票号，且本轮无附件证据）"
}

// reportForm 对给定口径跑四格统计 + 未建档逐封明细，并返回「仅旧命中」的数量。
//
// 返回值供调用方做硬断言（现行实现不得比 e54d797d 更紧）。
func reportForm(t *testing.T, rows []candRow, form *regexp.Regexp, label string) int {
	t.Helper()
	var cands []candHit
	for _, r := range rows {
		subjectSnippet := r.subject + "\n" + r.snippet
		if !form.MatchString(subjectSnippet) {
			continue
		}
		e := Email{ID: r.id, Subject: r.subject, Snippet: r.snippet}
		_, extractHit := ExtractInvoice(e, "")
		cands = append(cands, candHit{
			r:        r,
			kwNew:    InvoiceCandidate(e),
			kwOld:    oldInvoiceKeywordHit(subjectSnippet),
			admit:    admitDebtNotice(subjectSnippet, false),
			extract:  extractHit,
			formInAI: form.MatchString(r.aiSum),
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].r.date > cands[j].r.date })

	t.Logf("")
	t.Logf("=== %s：命中 %d 封（占语料 %.2f%%）===",
		label, len(cands), 100*float64(len(cands))/float64(len(rows)))

	// 四格表：new/old 两个判定 × archived 这个事实。
	var nArchived, nNotArchNewOld, nNotArchNewOnly, nNotArchOldOnly, nNotArchNeither, onlyAI int
	for _, c := range cands {
		switch {
		case c.r.archived:
			nArchived++
		case c.kwNew && c.kwOld:
			nNotArchNewOld++
		case c.kwNew && !c.kwOld:
			nNotArchNewOnly++
		case !c.kwNew && c.kwOld:
			nNotArchOldOnly++
		default:
			nNotArchNeither++
		}
		if c.formInAI && !form.MatchString(c.r.subject+"\n"+c.r.snippet) {
			onlyAI++
		}
	}
	t.Logf("  已建档                                  : %d", nArchived)
	t.Logf("  未建档 且 新旧都命中（卡在下游，不在词边界）: %d", nNotArchNewOld)
	t.Logf("  未建档 且 仅新命中（**round42 修复救回来的**）: %d", nNotArchNewOnly)
	t.Logf("  未建档 且 仅旧命中（新实现反而漏判 = 回归）  : %d", nNotArchOldOnly)
	t.Logf("  未建档 且 新旧都不命中（本来就不是发票候选）: %d", nNotArchNeither)
	t.Logf("  （另有 %d 封的该形态只出现在 ai_summary，主题/摘要里没有）", onlyAI)

	t.Logf("")
	t.Logf("--- 逐封明细（仅未建档）---")
	n := 0
	for _, c := range cands {
		if c.r.archived {
			continue
		}
		n++
		t.Logf("  [%s] %s", ts(c.r.date), c.r.id)
		t.Logf("      subject : %s", trunc(c.r.subject, 110))
		t.Logf("      snippet : %s", trunc(c.r.snippet, 110))
		t.Logf("      kwNew=%v kwOld=%v admit=%v extract=%v", c.kwNew, c.kwOld, c.admit, c.extract)
		t.Logf("      ⇒ 卡在：%s", c.stoppedBy())
	}
	if n == 0 {
		t.Logf("  (无)")
	}

	// 硬断言：不得出现「仅旧命中」——那是现行实现相对 e54d797d 的**回归**。
	if nNotArchOldOnly > 0 {
		t.Errorf("[%s] 有 %d 封邮件「旧实现命中、现行实现不命中」：现行词边界比 e54d797d 更紧，属回归。",
			label, nNotArchOldOnly)
	}
	return nNotArchOldOnly
}

func countArchived(rows []candRow) int {
	n := 0
	for _, r := range rows {
		if r.archived {
			n++
		}
	}
	return n
}

func ts(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02")
}
