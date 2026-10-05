package email

// invoice_human_mark_export_test.go — 钉住「人工标注能传导到交付物」。
//
// ## 要防的具体事故（2026-10-04 round44 查实，2026-10-05 修）
//
// 授权处置是「保留并标注」：两行营销横幅的 last_error 被写成
// `【人工标注】非发票凭证（营销横幅）：…`。台账里标注成立，**交付物里不存在**——
// `last_error` 根本不是导出列。于是新产出的 invoices-summary-*.md/.csv 里，
// 那两行仍以「downloaded / 已核验」的身份占着 CNY 合计的 61.1%。
//
// 关键教训（round44 §1d）：只验库内回读**不算数**，必须打开真实产物逐行核对。
// 所以本组判据全部走真的 WriteInvoiceSummaryDocs 并**读回文件内容**断言，
// 不去断言中间变量。
//
// ## 判据分三层
//
// ① 分类解析（纯函数）：词表命中/不命中、采集追加段不影响分类、非人工值不误判。
// ② 口径：被人工声明的行**不计入合计**，且核验列如实标出。
// ③ **产物**：打开真的 .md/.csv 逐列核对——表头有「备注」、标注原文在备注里、
//    合计不含被剔除的行、抬头说清「共 N 张（计入合计 M 张）」。
//
// ## 方向
//
// 把 InvoiceHumanMarkClass 改成恒返回 ""，②③ 会一起转红
// （合计重新含横幅、备注列空、核验列回到「已核验」）——负控实测过。

import (
	"os"
	"strings"
	"testing"
)

// bannerMark / statementMark 是**真实标注原文**的形状（与
// diag_mark_false_ledger_rows_test.go 的 composeMarkForInvoice 同形），
// 不是随手编的短串：解析器必须经得起「分类后面跟（子类）：理由」这种真形态。
const (
	bannerMark = "【人工标注】非发票凭证（营销横幅）：落盘件为 572×140（长宽比 4.09）平台宣传图，" +
		"且与另一横幅行字节 SHA256 相同（9ced44f4…），不可能是两笔不同发票的凭证。" +
		"本行金额与发票号系邮件正文解析所得，与图片内容无关。2026-10-04 人工复核。"
	statementMark = "【人工标注】非发票（信用卡对账单）：金额 58000.00 为**信用额度**、" +
		"日期 2026-10-25 为**到期还款日**，均非开票金额/开票日期。2026-10-04 人工复核。"
)

// markedInvoice 造一行「机械判据完全看不出问题」的人工标注行：
// status=downloaded 且 FilePath 非空 ⇒ 改动前它必然「已核验」并计入合计。
func markedInvoice(no string, amount float64, mark string) Invoice {
	return Invoice{
		Category: "其他", Seller: "某平台", Amount: amount, Currency: "CNY",
		InvoiceNo: no, InvoiceDate: "2026-10-01", Status: "downloaded",
		FileName: "其他-某平台-" + no + ".png", FilePath: "email-invoices/ws/" + no + ".png",
		Subject: "【活动】平台周年庆横幅", LastError: mark,
	}
}

func TestInvoiceHumanMarkClass_ParsesRealMarkShape(t *testing.T) {
	cases := []struct {
		name string
		last string
		want string
	}{
		{"营销横幅：非发票凭证", bannerMark, "非发票凭证"},
		{"对账单：非发票", statementMark, "非发票"},
		{
			"采集侧追加了本轮原因，分类仍取第一段",
			statementMark + " | 本轮采集：发票链接未能取到 PDF 文件：http://x",
			"非发票",
		},
		{
			"无人工标注的普通采集失败：不是人工声明",
			"no usable pdf/xml found in message", "",
		},
		{"空 last_error", "", ""},
		{
			// 关键负控：句子里含「非发票」三个字，但**分类词**不是它。
			// 用子串匹配就会把这一行剔出合计——合算错时没人会发现。
			"自由文本里提到非发票，但不是分类声明",
			"【人工标注】待补：非发票抬头缺失，需要销售方重开", "",
		},
		{
			"前缀之外的一切内容都不算人工标注",
			"人工标注：非发票凭证（横幅）", "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InvoiceHumanMarkClass(Invoice{LastError: c.last}); got != c.want {
				t.Errorf("InvoiceHumanMarkClass = %q，want %q（last_error=%q）", got, c.want, c.last)
			}
		})
	}
}

// TestInvoiceHumanMarkChangesTotal_AndSaysSo 钉住口径与标签同时改。
//
// 判据不分别断言「不计入」和「标签」，因为两者由同一个函数驱动，
// 分开写会允许「不计入但标签仍写已核验」这种最坏的组合溜过去：
// 那正是 ledger.go 注释里记的「标着已核验却不计入合计」。
func TestInvoiceHumanMarkChangesTotal_AndSaysSo(t *testing.T) {
	banner := markedInvoice("A1", 58000, bannerMark)
	stmt := markedInvoice("A2", 1000, statementMark)
	real := Invoice{
		Category: "交通", Seller: "某某出行", Amount: 3500, Currency: "CNY",
		InvoiceNo: "B1", Status: "downloaded", FilePath: "email-invoices/ws/B1.pdf",
	}

	if InvoiceCountsTowardTotal(banner) {
		t.Error("被人工声明为非发票凭证的行仍计入合计 —— 标注只在台账里生效的口径缺陷没修掉")
	}
	if InvoiceCountsTowardTotal(stmt) {
		t.Error("被人工声明为非发票的行仍计入合计")
	}
	if !InvoiceCountsTowardTotal(real) {
		t.Error("普通已下载行被误剔出合计 —— 判据过宽会把真票剔掉")
	}
	if got, want := InvoiceVerifiedLabel(banner), "非发票凭证·不计入合计"; got != want {
		t.Errorf("核验标签 = %q，want %q", got, want)
	}
	if got, want := InvoiceVerifiedLabel(stmt), "非发票·不计入合计"; got != want {
		t.Errorf("核验标签 = %q，want %q", got, want)
	}
	if got, want := InvoiceVerifiedLabel(real), "已核验"; got != want {
		t.Errorf("普通行核验标签 = %q，want %q", got, want)
	}
	// 标签与口径必须一致：凡是说「不计入合计」的行，判据必须真的不计入。
	for _, inv := range []Invoice{banner, stmt, real} {
		label := InvoiceVerifiedLabel(inv)
		excluded := !InvoiceCountsTowardTotal(inv)
		if strings.HasSuffix(label, "·不计入合计") != excluded {
			t.Errorf("标签(%q)与口径(计入=%v)不一致：核验列说一套、合计用另一套", label, !excluded)
		}
	}
}

// TestWriteInvoiceSummaryDocs_HumanMarkReachesDeliverable 是本组的核心：
// **打开真实产物**逐列核对，而不是断言中间变量。
//
// 三张票：真票 3,500 + 两行被标注的（58,000 + 1,000）。
// 合计必须是 3,500——若标注只在台账里生效，这里会是 62,500。
func TestWriteInvoiceSummaryDocs_HumanMarkReachesDeliverable(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		{Category: "交通", Seller: "某某出行", Amount: 3500, Currency: "CNY",
			InvoiceNo: "B1", InvoiceDate: "2026-09-28", Status: "downloaded",
			FileName: "交通-某某出行-3500.00.pdf", FilePath: "email-invoices/ws/B1.pdf",
			Subject: "行程单"},
		markedInvoice("A1", 58000, bannerMark),
		markedInvoice("A2", 1000, statementMark),
	}
	csvPath, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}

	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	mdText := string(md)

	// ① 抬头：共 3 张、计入 1 张、合计 3,500。
	if !strings.Contains(mdText, "共 3 张（计入合计 1 张）") {
		t.Errorf("抬头没有如实说明覆盖范围。实际抬头行：\n%s", firstLineOfText(mdText))
	}
	if !strings.Contains(mdText, "合计金额 **3500.00**") {
		t.Errorf("合计不是 3500.00 —— 被人工声明的两行仍进了合计。抬头行：\n%s", firstLineOfText(mdText))
	}

	// ② 核验列：两行被标注的必须标成「…·不计入合计」。
	for _, want := range []string{"非发票凭证·不计入合计", "非发票·不计入合计"} {
		if !strings.Contains(mdText, want) {
			t.Errorf("Markdown 里没有 %q —— 交付物上看不出这一行被人判定为非发票。\n%s", want, mdText)
		}
	}

	// ③ 备注列：表头 + 标注原文。
	if !strings.Contains(mdText, "| 备注 |") {
		t.Errorf("Markdown 表头没有「备注」列。\n%s", mdText)
	}
	// 备注要能看到「为什么」，而不只是「被判为非发票」——核验列已经说了后者。
	for _, want := range []string{"营销横幅", "572×140", "信用额度"} {
		if !strings.Contains(mdText, want) {
			t.Errorf("Markdown 备注里没有 %q —— 只标了结论没带理由，财务无法自行复核。\n%s", want, mdText)
		}
	}

	// ④ CSV：同样要能看到，且列宽一致（备注是**追加**列，不能挤动既有位置）。
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	csvText := strings.TrimPrefix(string(raw), "\ufeff")
	lines := strings.Split(strings.TrimSpace(csvText), "\n")
	header := strings.Split(lines[0], ",")
	if len(header) != 11 {
		t.Fatalf("CSV 表头 %d 列，want 11：%v", len(header), header)
	}
	if header[10] != "备注" {
		t.Errorf("CSV 末列=%q，want 备注", header[10])
	}
	for i, ln := range lines[1:] {
		if n := len(strings.Split(ln, ",")); n != len(header) {
			t.Errorf("CSV 第 %d 行 %d 格，表头 %d 格：%s", i+2, n, len(header), ln)
		}
	}
	if !strings.Contains(csvText, "非发票凭证·不计入合计") {
		t.Errorf("CSV 里没有核验标签，交付物（CSV 是给 Excel 的那份）仍看不出剔除。\n%s", csvText)
	}
	if !strings.Contains(csvText, "营销横幅") {
		t.Errorf("CSV 备注列没有标注理由。\n%s", csvText)
	}
	// 合计行：3500.00。
	//
	// 注意断言的是**合计行**而不是整个文件：被剔除的行仍要列出它的金额
	// （ledger.go 的原则「不计入合计 ≠ 从列表消失」——删掉就再也看不见
	// 「有一笔 58,000 需要人确认」）。所以「文件里出现 58000.00」是对的，
	// 「合计行里有它」才是缺陷。第一版把断言写成全文不含，那是把
	// 「剔除」误当成「隐藏」，会让正确行为被判红。
	totalLine := ""
	for _, ln := range lines {
		if strings.HasPrefix(ln, "合计") {
			totalLine = ln
		}
	}
	if totalLine == "" {
		t.Fatalf("CSV 里没有合计行：\n%s", csvText)
	}
	if !strings.Contains(totalLine, "3500.00") {
		t.Errorf("合计行缺 3500.00：%s", totalLine)
	}
	for _, bad := range []string{"58000.00", "62500.00", "58500.00"} {
		if strings.Contains(totalLine, bad) {
			t.Errorf("合计行里出现 %s —— 被人工声明的行仍进了合计：%s", bad, totalLine)
		}
	}
	// 反向：被剔除的行**必须还在明细里**（金额可见、状态可见、理由可见）。
	if !strings.Contains(csvText, "58000.00") {
		t.Errorf("被剔除的行从明细里消失了 —— 用户再也看不到「有一笔 58,000 待确认」。\n%s", csvText)
	}
}

// 飞书台账是另一条消费路径：同一份数据、同一套列。
// 只改本地 CSV/MD 会让两侧列宽不一致（既有判据钉着它们必须一致）。
func TestLedgerRows_HumanMarkReachesLedger(t *testing.T) {
	rows, totals := LedgerRows([]Invoice{
		markedInvoice("A1", 58000, bannerMark),
		{Status: "downloaded", FilePath: "b", Amount: 3500, Currency: "CNY"},
	})
	if len(totals) != 1 || totals[0].Amount != 3500 || totals[0].Count != 1 {
		t.Fatalf("台账合计 = %+v，want CNY 3500 / 1 张（被标注的 58,000 不该计入）", totals)
	}
	if got := rows[1][7]; got != "非发票凭证·不计入合计" {
		t.Errorf("台账核验列 = %v，want 非发票凭证·不计入合计", got)
	}
	if note, _ := rows[1][10].(string); !strings.Contains(note, "营销横幅") {
		t.Errorf("台账备注列 = %q，want 含「营销横幅」的标注理由", note)
	}
}

// firstLineOfText 只取抬头那一行（报错时把上下文缩到一行）。
// 名字带 Text：本包已有一个 firstLineOf([]byte)，同名会编译失败。
func firstLineOfText(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
