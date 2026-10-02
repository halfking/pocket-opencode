package email

// invoice_seller_label_test.go — 销售方（对方单位）不得是**列头标签**。
//
// 缺陷背景（2026-10-02 实测）：`reSeller` 的取值规则是「标签后取 1–6 个 ≥2 字的词」，
// 它挡不住紧接着的**另一个标签**。月度批量开票邮件是 HTML 表格，拍平成文本后
// 单元格之间是换行，于是「销售方」后面紧跟的就是「发票抬头」这个列头：
//
//	body = "销售方\n发票抬头\n价税合计：1280.00"
//	  旧：reSeller -> "发票抬头"     fileName -> "其他-发票抬头-1280.00-<日期>.pdf"
//
// 后果直击需求「发票文件格式：{费用类型}-{对方单位}-{金额}-{日期}.pdf」：
// 文件名多出一段变成 5 段，而那一段是**买方**的列头，根本不是对方单位。
//
// 真实样本已随 2026-10-01 的 schema 重建丢失（DB 只剩 2 行，源邮件也不在），
// 所以这里的回归数据是**合成**的：形态取自实测复现时 reSeller 的真实输出，
// 断言钉在性质上——「销售方绝不能是列头词」——而不是钉在某一段具体文件名上。

import (
	"regexp"
	"strings"
	"testing"
)

// 缺陷签名断言：规范文件名里**不允许出现任何列头作为独立的一段**。
//
// 为什么不能按「`-` 分几段」来判 4 段格式：日期本身就带两个连字符
// （2026-09-28），一个完全正确的 `其他-云服务开票中心-1280.00-2026-09-28.pdf`
// 按 `-` 数会得到 6 段。我第一版就是这么写的，结果真发票被判成「6 段」。
// 那是断言写错，不是产物错。
//
// 缺陷形态（`其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf`）的真正特征
// 是**多出来的那一段恰好是列头**，所以直接按段查列头，既不误伤日期，也不
// 会被公司名里的连字符带偏。
func assertNoLabelSegment(t *testing.T, name string) {
	t.Helper()
	body := strings.TrimSuffix(name, ".pdf")
	for _, seg := range strings.Split(body, "-") {
		if isInvoiceLabelWord(seg) {
			t.Errorf("文件名 %q 里出现了列头段 %q —— 对方单位被写成了表头", name, seg)
		}
	}
	if !regexp.MustCompile(`-\d+\.\d{2}-\d{4}-\d{2}-\d{2}\.pdf$`).MatchString(name) {
		t.Errorf("文件名 %q 不符合 {费用类型}-{对方单位}-{金额}-{日期}.pdf 的结尾形态", name)
	}
}

// cleanSellerValue 的单元口径。三类值分别对应三种真实形态。
func TestCleanSellerValue(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{
			// 实测复现的那个形态：跳过头部标签后只剩金额 ⇒ 整次匹配无效。
			name: "只剩金额",
			raw:  "发票抬头 价税合计 1280.00",
			want: "", ok: false,
		},
		{
			name: "全是列头",
			raw:  "发票抬头 发票号码 开票日期 金额",
			want: "", ok: false,
		},
		{
			// 「财务部」不是列头（它是发件部门），但后面的列头必须截断。
			name: "正主后面遇列头即停",
			raw:  "财务部 发票号码 开票日期 金额",
			want: "财务部", ok: true,
		},
		{
			name: "正主后面遇金额即停",
			raw:  "云服务开票中心 1280.00",
			want: "云服务开票中心", ok: true,
		},
		{
			// 完整表格：多级列头全部跳过后才取到真正的单位名。
			name: "多级列头后取到单位名",
			raw:  "销售方 发票抬头 发票号码 开票日期 价税合计 云服务开票中心 1280.00",
			want: "云服务开票中心", ok: true,
		},
		{
			// 英文公司名允许含空格（回归旧行为，别被新校验误杀）。
			name: "英文公司名保留空格",
			raw:  "Tencent Cloud Computing Co Ltd",
			want: "Tencent Cloud Computing Co Ltd", ok: true,
		},
		{
			name: "标签后紧跟英文公司名",
			raw:  "name Tencent Cloud Computing Co Ltd",
			want: "Tencent Cloud Computing Co Ltd", ok: true,
		},
	}
	for _, c := range cases {
		got, ok := cleanSellerValue(c.raw)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: cleanSellerValue(%q) = (%q, %v), want (%q, %v)",
				c.name, c.raw, got, ok, c.want, c.ok)
		}
	}
}

// TestExtractInvoice_SellerIsNeverAColumnLabel 是承重的行为断言：
// 走完整的 ExtractInvoice，任何一列发票表头都不得成为销售方。
//
// 这里同时钉住文件名：4 段格式 {费用类型}-{对方单位}-{金额}-{日期}。
func TestExtractInvoice_SellerIsNeverAColumnLabel(t *testing.T) {
	// 各种批量开票邮件里出现过的列头。
	labels := []string{
		"发票抬头", "销售方", "开票方", "商户", "发票号码", "开票日期",
		"价税合计", "金额", "纳税人识别号", "单位地址", "开户行", "序号",
		"购买方", "销方名称", "合计", "总额", "小写", "电话", "备注",
	}
	for _, lab := range labels {
		if !isInvoiceLabelWord(lab) {
			t.Errorf("列头 %q 不在标签词表里——采集器会把它当成对方单位", lab)
		}
	}

	// 行式表格：值紧跟在自己的列标签后面。这是批量开票邮件最常见的形态。
	body := "发票号码 25332000000123456789\n" +
		"销售方 杭州某某科技有限公司\n" +
		"发票抬头 某某采购有限公司\n" +
		"开票日期 2026-09-28\n" +
		"价税合计 1280.00\n" +
		"纳税人识别号 91330100MA2XXXXXXX\n"

	e := Email{
		ID: "em-label", AccountID: "acct-1",
		Subject:  "9 月批量开票明细",
		Snippet:  "本月发票已开具，详见下表。",
		FromName: "云服务开票中心",
	}
	inv, hit := ExtractInvoice(e, body)
	if !hit || inv == nil {
		t.Fatalf("带完整表格的发票邮件应被识别，实际 hit=%v inv=%v", hit, inv)
	}
	if isInvoiceLabelWord(inv.Seller) {
		t.Fatalf("销售方被识别成列头 %q（body 的表头被当成了单位名）", inv.Seller)
	}
	if inv.Seller != "杭州某某科技有限公司" {
		t.Errorf("销售方=%q，want 杭州某某科技有限公司", inv.Seller)
	}
	assertNoLabelSegment(t, InvoiceFileName(inv))
}

// 缺陷形态：表格先出一整行**表头**，值行在后面。HTML 表格拍平成文本时单元格
// 之间是换行，于是「销售方」后面紧跟的是下一个列头「发票抬头」——
// 旧实现在这里产出 `其他-…-发票抬头-…-….pdf`（5 段）。
//
// 注意 reSeller 的值规则里分隔符用的是 [^\S\r\n]+（不含换行），所以真正的
// 值在下一行时**根本抓不到**——这不是本次能修的（要跨行取值）。能修的是：
// 抓到的那个列头**不得**被当成单位名，此时应落到发件人兜底。
func TestExtractInvoice_HeaderRowTableDoesNotYieldLabel(t *testing.T) {
	body := "销售方\n发票抬头\n发票号码\n开票日期\n价税合计\n" +
		"杭州某某科技有限公司\n25332000000123456789\n2026-09-28\n1280.00\n"
	e := Email{
		ID: "em-hdr", AccountID: "acct-1",
		Subject:  "9 月批量开票明细",
		// 摘要行带着发票号与金额：表头行形态下正文里号码与金额都**不挨着**
		// 自己的列标签，正则抽不到。没有它们，ExtractInvoice 会按「金额 0 且
		// 发票号空且无附件」判成营销邮件直接丢弃——那样就测不到销售方了。
		Snippet:  "发票号码：25332000000123456789，价税合计 1280.00 元",
		FromName: "云服务开票中心",
	}
	inv, hit := ExtractInvoice(e, body)
	if !hit || inv == nil {
		t.Fatalf("带表格的发票邮件应被识别，实际 hit=%v inv=%v", hit, inv)
	}
	if isInvoiceLabelWord(inv.Seller) {
		t.Fatalf("销售方=%q 是列头标签（这正是实测产物的 5 段文件名来源）", inv.Seller)
	}
	if inv.Seller != "云服务开票中心" {
		t.Errorf("销售方=%q，want 云服务开票中心（跨行取不到值时应回落到发件人名称）", inv.Seller)
	}
	assertNoLabelSegment(t, InvoiceFileName(inv))
}

// 收敛不出单位名时，必须落到发件人兜底，而不是把列头留在销售方字段里。
func TestExtractInvoice_FallsBackWhenOnlyLabelsFound(t *testing.T) {
	// 「销售方」后面全是列头，reSeller 抓到的值里没有任何可用的单位名。
	body := "销售方\n发票抬头\n价税合计：1280.00\n发票号码 25332000000123456789\n"
	e := Email{
		ID: "em-label2", AccountID: "acct-1",
		Subject:  "电子发票开具通知",
		Snippet:  "价税合计 1280.00 元",
		FromName: "云服务开票中心",
	}
	inv, hit := ExtractInvoice(e, body)
	if !hit || inv == nil {
		t.Fatalf("有发票号的邮件应被识别，实际 hit=%v inv=%v", hit, inv)
	}
	if isInvoiceLabelWord(inv.Seller) {
		t.Fatalf("销售方=%q 是列头标签，兜底没生效", inv.Seller)
	}
	if inv.Seller != "云服务开票中心" {
		t.Errorf("销售方=%q，want 云服务开票中心（应回落到发件人名称）", inv.Seller)
	}
	if inv.Amount <= 0 {
		t.Errorf("金额=%v，应从正文抽到 1280.00", inv.Amount)
	}
}
