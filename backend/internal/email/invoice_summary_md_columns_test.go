package email

// invoice_summary_md_columns_test.go — 泛化护栏：**每一列的取值必须与表头对得上**。
//
// ## 为什么需要它（第三十节那个判据的缺口）
//
// `TestInvoiceSummaryMarkdown_HeaderMatchesColumnContent` 只查**末列**：
// 表头字面量是「核验」、取值落在 {已核验, 未核验}、列数一致。
// 它能抓住 2026-10-02 那个具体缺陷，但抓不住**同一类的其它位置**。
//
// 2026-10-04 查清了那类缺陷的真正机制（handoff 44.5）：
// `bcd27ebd` 在**共享表头**的第 8 位插入了「核验」，
// 把「文件名」从 r[7] 挤到 r[8]；而 MD 侧是 `r[0]…r[7]` **按位置**取值，
// 且 MD 侧代码**一个字都没改**——于是 MD 的表头与内容自动对不上。
//
// 同理可推：若有人把新列插在**中间**（例如在「状态」前插一列），
// 末列仍可能侥幸通过（若新列值恰好是核验标签），
// 但**「金额」列会开始显示币种、「发票号」列会显示别的**——末列判据全部看不见。
//
// ## 本判据的做法
//
// 夹具给**每一列一个互不相同的哨兵值**，然后逐列断言 MD 单元格的取值。
// 任何位置的插入/重排都会让某列错位 ⇒ 转红。
//
// 期望值是**独立字面量**，不调任何被测函数算出来。
//
// ## 范围（如实说）
//
// 只覆盖 MD 明细表的这 7 列。**不覆盖**合计行、不覆盖 CSV 侧、
// 不覆盖表头字面量本身（那是第三十节判据的职责）。三条判据各管一段，
// 故意不合并——合并会让一处改动同时影响多个期望值。

import (
	"os"
	"strings"
	"testing"
)

// TestInvoiceSummaryMarkdown_EveryColumnCarriesItsOwnValue 逐列钉住。
func TestInvoiceSummaryMarkdown_EveryColumnCarriesItsOwnValue(t *testing.T) {
	dir := t.TempDir()

	// 哨兵：每一列一个**形态完全不同**的值，这样任何错位都不会巧合通过。
	// 金额 + 币种在 MD 里是合并成「金额」一格的（见 WriteInvoiceSummaryDocs），
	// 所以那一格的期望是 "777.00 CUR" 这种「数字 + 币种」的两段形态。
	const (
		sentinelCategory = "哨兵-费用类型"
		sentinelSeller   = "哨兵-对方单位"
		sentinelAmount   = "777.00"
		sentinelCurrency = "CUR"
		sentinelNo       = "SENTINEL-INVOICE-NO-0001"
		sentinelDate     = "2099-12-31"
		sentinelStatus   = "哨兵-状态"
	)
	invs := []Invoice{{
		Category: sentinelCategory,
		Seller:   sentinelSeller,
		Amount:   777,
		Currency: sentinelCurrency,
		// InvoiceNo 里含 "INVOICE" 是刻意的：若某次错位把这一列换成了
		// 邮件主题，"SENTINEL-INVOICE-NO-0001" 仍然只能是发票号自己。
		InvoiceNo:   sentinelNo,
		InvoiceDate: sentinelDate,
		Status:      sentinelStatus,
		FileName:    "哨兵-文件名-不应出现在MD.pdf",
		FilePath:    "email-invoices/ws/sentinel.pdf",
		Subject:     "哨兵-邮件主题-不应出现在MD",
	}}

	_, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}

	// 找明细数据行：7 格、且第 1 格是哨兵费用类型。
	var row []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := mdCells(line)
		if len(cells) > 0 && cells[0] == sentinelCategory {
			row = cells
			break
		}
	}
	if row == nil {
		t.Fatalf("明细表里找不到哨兵数据行；Markdown 内容：\n%s", string(data))
	}

	// 逐列期望（独立字面量）。顺序 = MD 表头的 7 列。
	// 末列「核验」的期望不能用 InvoiceVerifiedLabel 算（第三十节已记原因：
	// 改坏那个函数会同时改掉期望值）。这张票 status=哨兵-状态、
	// 既不是 downloaded 也不是 filed ⇒ 必然是「未核验」。
	want := []string{
		"费用类型", sentinelCategory,
		"对方单位", sentinelSeller,
		"金额", sentinelAmount + " " + sentinelCurrency,
		"发票号", sentinelNo,
		"日期", sentinelDate,
		"状态", sentinelStatus,
		"核验", "未核验",
		// 2026-10-05 加的末列。哨兵票没有人工标注 ⇒ 期望空串。
		// 这里刻意**不**用 InvoiceHumanNote 算期望（同上：不能用被测函数算期望）。
		"备注", "",
	}
	if len(row) != len(want)/2 {
		t.Fatalf("明细行 %d 格，表头 %d 列", len(row), len(want)/2)
	}
	for i := 0; i < len(want); i += 2 {
		name, exp := want[i], want[i+1]
		if got := row[i/2]; got != exp {
			t.Errorf("「%s」列取值=%q，want %q。\n"+
				"  这一列的错位说明**共享表头**在某处插入/重排了列，而 Markdown 侧"+
				"是按位置（r[n]）取值的——MD 代码可能一个字都没改，"+
				"但语义被别的消费方隔空改了。\n  实际整行：%v",
				name, got, exp, row)
		}
	}

	// 反向：这两个值**不该**出现在 MD 里（它们在 CSV 有、MD 无位置）。
	// 若哪天有人在 MD 里插了一列取到它们，本条会红并指出该同步表头。
	for _, forbidden := range []string{"哨兵-文件名-不应出现在MD", "哨兵-邮件主题-不应出现在MD"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("MD 里出现了 %q —— 说明某一列取到了文件名/来源邮件。"+
				"那**不是缺陷**（加列是好事），但表头必须同步加一列，否则又是表头与内容不符。", forbidden)
		}
	}
}

// TestWriteInvoiceSummaryDocs_SharedHeaderMatchesCSVWidth 钉住**共享表头与 CSV
// 数据行等宽**（负控暴露的缺口）。
//
// ## 负控实测（2026-10-04）
//
// 在共享表头「状态」前插入一列「负控新列」、**MD 侧一个字不改**，
// 上面两条判据**全部保持绿色**。
//
// 原因：共享表头与 `rows` 的数据是**两处独立的硬编码**——
//
//	header: "费用类型", "对方单位", …              ← 表头字面量
//	rows:   []string{ inv.Category, inv.Seller, … } ← 另一处字面量
//
// 只改表头而漏改 append，两边就不等宽。**Go 不会报错**：MD 按 `r[0]…r[7]`
// 取值，`[]string` 下标不越界，于是安静地取到**错位**的那一格。
//
// ## 为什么用 CSV 而不是 MD 判这件事
//
// CSV 是**完整**的共享结构（表头 + 每行 10 列都在），MD 只有前 7 列
// 且金额列是 r[2]与r[3] 合并而成。所以「共享表头 == 共享数据行」这件事
// 只能在 CSV 上验。
//
// 期望值是**独立字面量** `10`，不是 `len(header)`——否则有人把两边一起改窄，
// 判据跟着变，绿灯就毫无意义（这是第三十一节记的同一条纪律）。
func TestWriteInvoiceSummaryDocs_SharedHeaderMatchesCSVWidth(t *testing.T) {
	// 2026-10-05：10 → 11（末尾追加「备注」）。仍是**独立字面量**，不是 len(header)。
	const wantSharedWidth = 11 // 费用类型/对方单位/金额/币种/发票号/日期/状态/核验/文件名/来源邮件/备注

	dir := t.TempDir()
	invs := []Invoice{{
		Category: "其他", Seller: "单位丙", Amount: 42, Currency: "CNY",
		InvoiceNo: "26332000008261110741", InvoiceDate: "2026-09-24",
		Status: "downloaded", FileName: "b.pdf", FilePath: "email-invoices/ws/b.pdf",
		Subject: "主题丙",
	}}
	csvPath, _, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	// CSV 带 UTF-8 BOM（pipeline.go 的注释解释了为什么），先剥掉。
	text := strings.TrimPrefix(string(raw), "\ufeff")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		t.Fatalf("CSV 不足两行：%q", text)
	}
	header := strings.Split(lines[0], ",")
	if len(header) != wantSharedWidth {
		t.Errorf("CSV 共享表头 %d 列，want %d：%v\n"+
			"  表头与 rows 的数据是两处独立硬编码；只改一处会让另一处静默错位，"+
			"而 Go 不会报错（下标不越界）。负控实测：插入一列后产物全错但测试全绿。",
			len(header), wantSharedWidth, header)
	}
	// 表头第 8 列必须是「核验」、第 9 列「文件名」——44.5 记的那个位置语义。
	if len(header) >= 9 {
		if header[7] != "核验" {
			t.Errorf("共享表头第 8 列=%q，want 核验（位置 7，从 0 起）", header[7])
		}
		if header[8] != "文件名" {
			t.Errorf("共享表头第 9 列=%q，want 文件名（位置 8，从 0 起）", header[8])
		}
		// 2026-10-05 新增：末列必须是「备注」。它是**追加**列，所以
		// 既有位置的语义（8=文件名、7=核验）不能被挤动。
		if header[10] != "备注" {
			t.Errorf("共享表头第 11 列=%q，want 备注（位置 10，从 0 起）", header[10])
		}
	}
	// 数据行的列数也必须等于表头。
	for i, ln := range lines[1:] {
		// 合计行以「合计」开头，它有自己的一套形状，不参与本检查。
		if strings.HasPrefix(ln, "合计") {
			continue
		}
		if n := len(strings.Split(ln, ",")); n != len(header) {
			t.Errorf("CSV 第 %d 行 %d 格，表头 %d 格", i+2, n, len(header))
		}
	}
}
