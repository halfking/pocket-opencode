package email

// invoice_summary_md_header_test.go — Markdown 汇总表的**表头必须与该列内容对得上**。
//
// ## 缺陷（2026-10-04 实测真实产物发现）
//
// 真实文件 `invoices-summary-20261003-161756.md` 的表头是：
//
//	| 费用类型 | 对方单位 | 金额 | 发票号 | 日期 | 状态 | 文件 |
//
// 而末列填的是 `已核验` / `未核验` —— 那是 `r[7]`=InvoiceVerifiedLabel。
// **文件名在 `r[8]`，从头到尾没进过 Markdown。** 读的人按「文件」去找文件名，
// 只会看到「已核验」三个字。CSV 侧是对的（「核验」与「文件名」两列分开）。
//
// 这是需求「整理一个列表，记录必要信息并汇总金额」的交付物本身出错，
// 而且是**要交给财务看的那一份**。
//
// ## 判据设计
//
// 三条，从「这一个 bug」泛化到「表头漂移」这一类：
//
//  1. 末列表头字面量必须是「核验」；
//  2. 每一数据行的末列取值必须落在 {已核验, 未核验} 这个**独立字面量集合**里；
//  3. 表头与每一数据行的**单元格数必须相等**（泛化护栏：以后有人加列/漏列会红）。
//
// 期望值是字面量，**不是**调 `InvoiceVerifiedLabel` 算出来的——否则改坏那个
// 函数会同时改掉期望值，用例在缺陷存在时照样绿。
//
// 鉴别夹具：第一张票**带 FileName**。若有人把末列改成写 `r[8]`（文件名），
// 第 2 条会立刻转红（`a.pdf` 不在字面量集合里），而不是靠表头名字蒙混过去。

import (
	"os"
	"strings"
	"testing"
)

// mdCells 拆一行 Markdown 表格：去掉首尾空片段，逐格 Trim。
func mdCells(line string) []string {
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return nil
	}
	parts = parts[1 : len(parts)-1]
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func TestInvoiceSummaryMarkdown_HeaderMatchesColumnContent(t *testing.T) {
	dir := t.TempDir()
	invs := []Invoice{
		// 带 FileName 的已下载票：鉴别夹具。
		{Category: "其他", Seller: "单位甲", Amount: 100, Currency: "CNY",
			InvoiceNo: "26332000008261110741", InvoiceDate: "2026-09-24",
			Status: "downloaded", FileName: "其他-单位甲-100.00-2026-09-24.pdf",
			FilePath: "email-invoices/ws/a.pdf", Subject: "发票已开具"},
		// 无文件、未下载：末列必须是「未核验」，不能是空。
		{Category: "其他", Seller: "单位乙", Amount: 58000, Currency: "CNY",
			InvoiceDate: "2026-10-25", Status: "pending", Subject: "对账单"},
	}
	_, mdPath, err := WriteInvoiceSummaryDocs(dir, "ws_user-admin", invs)
	if err != nil {
		t.Fatalf("WriteInvoiceSummaryDocs: %v", err)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}

	var header []string
	var dataRows [][]string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := mdCells(line)
		if len(cells) == 0 {
			continue
		}
		// 分隔行（全是 ---）跳过。
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		if header == nil {
			header = cells
			continue
		}
		dataRows = append(dataRows, cells)
	}
	if header == nil {
		t.Fatal("Markdown 里没有表格表头")
	}
	if len(dataRows) != len(invs) {
		t.Fatalf("数据行数=%d，want %d（每张票都要在明细里）", len(dataRows), len(invs))
	}

	// 1) 末列表头字面量。
	//
	// 2026-10-05：末列不再是「核验」——加列时末尾追加了「备注」（人工标注理由），
	// 所以核验列往前挪了一位。核验列**按位置**断言而不是「最后一列」：
	// 「末列」这种断言在每次加列时都要改，而它保护的其实是「表头最后一格
	// 说的是它那一格真正装的东西」——位置断言同样能保护，且不会在加列时
	// 变成一个必须跟着改的仪式。
	const (
		wantHeader       = "备注"
		wantVerifyHeader = "核验"
		verifyCol        = 6
	)
	if got := header[len(header)-1]; got != wantHeader {
		t.Errorf("末列表头=%q，want %q。这一列填的是 InvoiceHumanNote（人工标注理由）",
			got, wantHeader)
	}
	if got := header[verifyCol]; got != wantVerifyHeader {
		t.Errorf("第 %d 列表头=%q，want %q", verifyCol+1, got, wantVerifyHeader)
	}

	// 2) 核验列的取值必须落在独立字面量集合里。
	//
	// 集合含三种值：已核验 / 未核验 / 「<分类>·不计入合计」。第三种是人工
	// 声明非发票的行（2026-10-05 加）——这里用**前缀通配**而不是把两种分类
	// 都列成字面量：词表 invoiceHumanMarkExcludedClasses 增加第三种分类时，
	// 本判据不必跟着改；而它仍然能抓住「这一列装的不是核验状态」。
	wantLabels := map[string]bool{"已核验": true, "未核验": true}
	for _, row := range dataRows {
		got := row[verifyCol]
		marked := strings.HasSuffix(got, "·不计入合计")
		if !wantLabels[got] && !marked {
			t.Errorf("核验列取值=%q，不在 {已核验,未核验} 也不以「·不计入合计」结尾。"+
				"表头说核验、内容却不是核验状态（若这里填的是文件名，说明该加一列而不是改表头）：行=%v",
				got, row)
		}
	}

	// 3) 泛化护栏：表头与每行的单元格数必须一致。
	for i, row := range dataRows {
		if len(row) != len(header) {
			t.Errorf("第 %d 数据行单元格数=%d，表头=%d。Markdown 表格列数不一致"+
				"会让整张表在渲染时错位：%v", i, len(row), len(header), row)
		}
	}
}
