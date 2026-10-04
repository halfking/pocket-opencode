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
	// 结尾形态：`{费用类型}-{对方单位}-{金额}-{日期}[-{发票号}].pdf`
	//
	// 合并修订：原断言的正则是 `-\d+\.\d{2}-\d{4}-\d{2}-\d{2}\.pdf$`。它有两个问题，
	// 都不是「命名逻辑坏了」：
	//   1. 它把发票号段写死成**必需**。但 InvoiceFileName 的实现是「有则加」——
	//      发票号为空（采集早期 / XML 未解析出）时回到需求原文的四段式。
	//   2. 它自身就**恒不匹配**任何真实文件名：日期 `2026-09-28` 本身带连字符，
	//      `\d{4}-\d{2}-\d{2}` 能对上日期，但前面那个 `-\d{2}-` 要求「年」后面
	//      紧跟一个两位数再接横线，实际是 `2026-09-28-`（月是两位、日是两位，
	//      中间没有多余的一位）。于是一个恒假断言挡不住任何东西。
	//
	// 改按**段**判定：去掉 .pdf 后按 `-` 切分，段结构与实现里
	// `fmt.Sprintf("%s-%s-%s-%s", category, seller, amount, date)` 一一对应；
	// 日期占 3 段（年-月-日），末尾可选 1 段发票号。这样不依赖发票号是否解析出来，
	// 也不会被日期里的连字符绊住。
	//
	// ---- round43 增补：显式占位形态 ----
	//
	// 上面那条「日期占 3 段」其实**隐含依赖了发票日期一定存在**。而它以前
	// 之所以总是存在，是因为 `InvoiceFileName` 在日期为空时填 `time.Now()`
	// ——那个「日期」是**下载当天**，编的（真实产物 `通信-X-8.00-2026-10-04.pdf`
	// 的台账「日期」列是空的）。
	//
	// round43 把那个兜底换成显式占位 `未知日期`（占 1 段），本断言因此要认
	// 这一种形态。**只多认一种合法形态，不放松任何既有保证**：列头段检查照旧
	// 在最前面逐段跑，金额形态照旧要验，日期已知时那三段的 年-月-日 校验
	// 也照旧。
	segs := strings.Split(body, "-")
	if !regexp.MustCompile(`^\d+\.\d{2}$`).MatchString(segs[2]) {
		t.Errorf("文件名 %q 第 3 段 %q 不是金额形态（数字.两位小数）", name, segs[2])
	}
	if segs[3] == "未知日期" {
		// 显式占位：{类别}-{单位}-{金额}-未知日期[-发票号]
		if len(segs) != 4 && len(segs) != 5 {
			t.Errorf("文件名 %q 用了「未知日期」占位，段数应为 4 或 5，实际 %d: %v",
				name, len(segs), segs)
		}
		return
	}
	if len(segs) < 6 {
		t.Errorf("文件名 %q 只有 %d 段，不符合 {费用类型}-{对方单位}-{金额}-{日期}[-{发票号}] 的段结构: %v",
			name, len(segs), segs)
		return
	}
	ymd := segs[len(segs)-4 : len(segs)-1]
	if !regexp.MustCompile(`^\d{4}$`).MatchString(ymd[0]) ||
		!regexp.MustCompile(`^\d{2}$`).MatchString(ymd[1]) ||
		!regexp.MustCompile(`^\d{2}$`).MatchString(ymd[2]) {
		t.Errorf("文件名 %q 的日期三段应为 年-月-日，实际 %v", name, ymd)
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

// 表头行形态下真正的单位名在**下一行**：reSeller 的值分隔符不含换行，抓不到。
// 这是跨行兜底存在的理由，也是它的承重用例。
func TestExtractInvoice_HeaderRowTableRecoversSellerFromNextLine(t *testing.T) {
	body := "销售方\n发票抬头\n发票号码\n开票日期\n价税合计\n" +
		"杭州某某科技有限公司\n25332000000123456789\n2026-09-28\n1280.00\n"
	e := Email{
		ID: "em-cross", AccountID: "acct-1",
		Subject:  "9 月批量开票明细",
		Snippet:  "发票号码：25332000000123456789，价税合计 1280.00 元",
		FromName: "云服务开票中心",
	}
	inv, hit := ExtractInvoice(e, body)
	if !hit || inv == nil {
		t.Fatalf("带表格的发票邮件应被识别，实际 hit=%v inv=%v", hit, inv)
	}
	if inv.Seller != "杭州某某科技有限公司" {
		t.Errorf("销售方=%q，want 杭州某某科技有限公司（应跨过表头行取到下一行的单位名，而不是退化成发件人名）", inv.Seller)
	}
	assertNoLabelSegment(t, InvoiceFileName(inv))
}

// 兜底的三道闸各自都要能挡住东西，否则它就是个散文收割机。
func TestSellerFromFollowingLines_Gates(t *testing.T) {
	cases := []struct {
		name string
		rest string
		want string
	}{
		{
			// 闸 1：表头行里的其它列头被跳过且不计次数，才能走到真正的值。
			name: "跳过其余列头",
			rest: "\n发票号码\n开票日期\n价税合计\n杭州某某科技有限公司\n1280.00",
			want: "杭州某某科技有限公司",
		},
		{
			// 闸 2：日期不能当单位名（ParseFloat 认不出 2026-09-28）。
			name: "跳过日期与票号",
			rest: "\n2026-09-28\n25332000000123456789\n杭州某某科技有限公司",
			want: "杭州某某科技有限公司",
		},
		{
			// 闸 3：全是散文就别硬凑，退回空串让上层走发件人兜底。
			name: "散文不当单位名",
			rest: "\n如需\n您好\n详见附件",
			want: "",
		},
		{
			// 闸 4：最多试 3 个非列头行。三个散文候选都失败后，
			// 第 4 行那个像模像样的公司名**也不取**——宁可退回空串。
			name: "尝试次数上限后不再取",
			rest: "\n如需\n您好\n详见附件\n甲乙丙丁公司",
			want: "",
		},
		{
			// 「标签：值」黏成一行：整行不能被当成单位名，且不计次数。
			name: "跳过标签值黏行",
			rest: "\n价税合计：1280.00\n发票号码 25332000000123456789\n杭州某某科技有限公司",
			want: "杭州某某科技有限公司",
		},
		{
			// 承重的鉴别用例：销售方那一列后面紧跟的是**买方**行。
			// 「某某采购有限公司」以「公司」结尾，looksLikeEntityName 放行它，
			// 只有 startsWithLabelField（发票抬头 + 「：」）能挡住。
			// 没有这道闸，对方单位会被写成**采购方**——错得比退化成发件人还隐蔽。
			name: "买方行不得被当成销售方",
			rest: "\n发票抬头：某某采购有限公司\n杭州某某科技有限公司",
			want: "杭州某某科技有限公司",
		},
		{
			name: "英文单位名",
			rest: "\nSeller\nTencent Cloud Computing Co Ltd",
			want: "Tencent Cloud Computing Co Ltd",
		},
		{
			name: "没有可取的行",
			rest: "\n\n\n",
			want: "",
		},
	}
	for _, c := range cases {
		if got := sellerFromFollowingLines("销售方"+c.rest, len("销售方")); got != c.want {
			t.Errorf("%s: sellerFromFollowingLines = %q, want %q", c.name, got, c.want)
		}
	}
}

// 日期/票号在**主路径**上也必须被当终止符：表头行形态下紧接着销售方标签的
// 就是「开票日期」那一列。
func TestCleanSellerValue_RejectsDateAndID(t *testing.T) {
	for _, raw := range []string{"2026-09-28", "25332000000123456789", "2026年09月28日 开票日期"} {
		if s, ok := cleanSellerValue(raw); ok {
			t.Errorf("cleanSellerValue(%q) = (%q, true)，日期/票号不该被当成单位名", raw, s)
		}
	}
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
