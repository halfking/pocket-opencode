package email

// xmlinvoice_test.go — 需求 3「原邮件中有 XML 数据格式（可解析后重新渲染）」。
//
// 2026-10-01 实测发现 ParseInvoiceXML **零测试覆盖**：全仓唯一提到 XML 的
// 测试文件（invoice_font_test.go）里并没有 ParseInvoiceXML / mergeXMLFields
// 的调用。也就是说这条需求路径从来没被验证过——解析器可能早就坏了而没人知道。
//
// 这里按国内**真实**的全电发票（数电票）结构写夹具，不自造简化格式：
//  1. 中文标签嵌套结构（<发票><发票号码>…）
//  2. 英文字段清单（InvoiceNo / TotalAmount / SellerName）
//  3. 属性形式（AmountTotal="126.00"）——解析器显式支持这条分支
//  4. UTF-8 BOM（Windows 工具导出常见）
//  5. 命名空间前缀（xsi:Invoice）
//
// 负控对照见文件末尾注释：改 labelMatch 的词典会让对应用例转红。

import (
	"strings"
	"testing"
)

// 真实数电票结构：中文标签 + 嵌套卖方/购方 + 价税合计小计。
const realChineseXML = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice>
  <InvoiceHeader>
    <发票号码>24312000000012345678</发票号码>
    <开票日期>2026年09月28日</开票日期>
  </InvoiceHeader>
  <Seller>
    <销售方名称>腾讯科技（深圳）有限公司</销售方名称>
    <销售方纳税人识别号>9144030071526726XG</销售方纳税人识别号>
  </Seller>
  <Buyer>
    <购买方名称>开玄科技（深圳）有限公司</购买方名称>
  </Buyer>
  <Item>
    <项目名称>云服务器租赁费</项目名称>
  </Item>
  <Total>
    <价税合计>￥126.00</价税合计>
  </Total>
</Invoice>`

// 英文 Key/Value 清单形式（部分服务商导出）。
const englishKeyValueXML = `<?xml version="1.0" encoding="UTF-8"?>
<InvoiceData>
  <InvoiceNo>INV-TEST-0001</InvoiceNo>
  <InvoiceDate>2026-09-30</InvoiceDate>
  <SellerName>Alibaba Cloud Computing Ltd</SellerName>
  <TotalAmount>328.50</TotalAmount>
  <ItemName>OSS Storage</ItemName>
</InvoiceData>`

// 属性形式：解析器 xmlinvoice.go:80-84 显式支持。
const attributeStyleXML = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice AmountTotal="454.50" InvoiceNo="24312000000099887766" InvoiceDate="2026-10-01" SellerName="Tencent Cloud"/>`

func TestParseInvoiceXML_RealChineseFullEInvoice(t *testing.T) {
	f := ParseInvoiceXML([]byte(realChineseXML))
	if f == nil {
		t.Fatal("real chinese e-invoice XML must parse, got nil")
	}
	if f.InvoiceNo != "24312000000012345678" {
		t.Errorf("InvoiceNo = %q, want 24312000000012345678", f.InvoiceNo)
	}
	// 价税合计 ￥126.00 -> 126.00（stripCurrency 去掉 ¥ 和千分位）
	if f.Amount != 126.00 {
		t.Errorf("Amount = %v, want 126.00", f.Amount)
	}
	// 卖方必须取「名称」那一层，而不是整个 <Seller> 的聚合文本
	if f.Seller != "腾讯科技（深圳）有限公司" {
		t.Errorf("Seller = %q, want 腾讯科技（深圳）有限公司", f.Seller)
	}
	if f.BuyerTitle != "开玄科技（深圳）有限公司" {
		t.Errorf("BuyerTitle = %q, want 开玄科技（深圳）有限公司", f.BuyerTitle)
	}
	// 开票日期归一化成 ISO
	if f.InvoiceDate != "2026-09-28" {
		t.Errorf("InvoiceDate = %q, want 2026-09-28", f.InvoiceDate)
	}
}

func TestParseInvoiceXML_EnglishKeyValue(t *testing.T) {
	f := ParseInvoiceXML([]byte(englishKeyValueXML))
	if f == nil {
		t.Fatal("english key/value XML must parse, got nil")
	}
	if f.InvoiceNo != "INV-TEST-0001" {
		t.Errorf("InvoiceNo = %q, want INV-TEST-0001", f.InvoiceNo)
	}
	if f.Amount != 328.50 {
		t.Errorf("Amount = %v, want 328.50", f.Amount)
	}
	if f.Seller != "Alibaba Cloud Computing Ltd" {
		t.Errorf("Seller = %q", f.Seller)
	}
}

// 属性形式：这条分支容易在重构中被悄悄删掉。
func TestParseInvoiceXML_AttributeStyle(t *testing.T) {
	f := ParseInvoiceXML([]byte(attributeStyleXML))
	if f == nil {
		t.Fatal("attribute-style XML must parse, got nil")
	}
	if f.InvoiceNo != "24312000000099887766" {
		t.Errorf("InvoiceNo = %q", f.InvoiceNo)
	}
	if f.Amount != 454.50 {
		t.Errorf("Amount = %v, want 454.50", f.Amount)
	}
	if f.Seller != "Tencent Cloud" {
		t.Errorf("Seller = %q, want Tencent Cloud", f.Seller)
	}
}

// Windows 工具导出的 XML 常带 BOM，解析前必须剥掉，否则 xml.Unmarshal 直接失败。
func TestParseInvoiceXML_StripsUTF8BOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(englishKeyValueXML)...)
	f := ParseInvoiceXML(withBOM)
	if f == nil {
		t.Fatal("XML with UTF-8 BOM must still parse")
	}
	if f.InvoiceNo != "INV-TEST-0001" {
		t.Errorf("InvoiceNo = %q", f.InvoiceNo)
	}
}

// 命名空间前缀：标签的 Local 名才是匹配依据，带前缀不应影响。
func TestParseInvoiceXML_HandlesNamespacePrefix(t *testing.T) {
	ns := `<?xml version="1.0" encoding="UTF-8"?>
<xsi:Invoice xmlns:xsi="http://x">
  <xsi:InvoiceNo>24312000000055443322</xsi:InvoiceNo>
  <xsi:TotalAmount>99.90</xsi:TotalAmount>
</xsi:Invoice>`
	f := ParseInvoiceXML([]byte(ns))
	if f == nil {
		t.Fatal("namespace-prefixed XML must parse")
	}
	if f.InvoiceNo != "24312000000055443322" {
		t.Errorf("InvoiceNo = %q", f.InvoiceNo)
	}
}

// 无关 XML 必须返回 nil，让调用方走重试/人工路径，而不是造出一堆空字段。
func TestParseInvoiceXML_UnrelatedXMLReturnsNil(t *testing.T) {
	cases := []struct{ name, body string }{
		{"empty", ""},
		{"malformed", "<Invoice><InvoiceNo>123"},
		{"unrelated", "<Order><Item>widget</Item><Qty>3</Qty></Order>"},
		{"only seller, no no/amount", "<Invoice><销售方名称>某公司</销售方名称></Invoice>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseInvoiceXML([]byte(tc.body)); got != nil {
				t.Fatalf("expected nil, got %+v", got)
			}
		})
	}
}

// mergeXMLFields 的契约：只在原字段为空时补全，绝不覆盖邮件主题已提取的值。
// 这条是「XML 补全 vs 主题提取」的优先级边界，改错了会静默丢数据。
func TestMergeXMLFields_OnlyFillsEmptyFields(t *testing.T) {
	inv := &Invoice{
		InvoiceNo: "FROM-SUBJECT",
		Amount:    42.00,
		Seller:    "SUBJECT-SELLER",
	}
	mergeXMLFields(inv, &XMLInvoiceFields{
		InvoiceNo:   "FROM-XML",
		Amount:      999.00,
		Seller:      "XML-SELLER",
		InvoiceDate: "2026-10-01",
		BuyerTitle:  "XML-BUYER",
		Category:    "其他",
	})
	if inv.InvoiceNo != "FROM-SUBJECT" {
		t.Errorf("subject-derived InvoiceNo must win, got %q", inv.InvoiceNo)
	}
	if inv.Amount != 42.00 {
		t.Errorf("subject-derived Amount must win, got %v", inv.Amount)
	}
	if inv.Seller != "SUBJECT-SELLER" {
		t.Errorf("subject-derived Seller must win, got %q", inv.Seller)
	}
	// 空字段才补（Invoice.Title 是「发票抬头」，即购方）
	if inv.Title != "XML-BUYER" {
		t.Errorf("empty Title should be filled from XML, got %q", inv.Title)
	}
	if inv.InvoiceDate == "" {
		t.Error("empty InvoiceDate should be filled from XML")
	}
}

// 金额清洗：¥/￥/元/千分位/RMB/CNY 都要能剥掉。
func TestParseInvoiceXML_AmountCleaning(t *testing.T) {
	cases := []struct {
		xml  string
		want float64
	}{
		{`<Invoice><InvoiceNo>A1</InvoiceNo><价税合计>￥1,234.56</价税合计></Invoice>`, 1234.56},
		{`<Invoice><InvoiceNo>A1</InvoiceNo><价税合计>1234.56元</价税合计></Invoice>`, 1234.56},
		{`<Invoice><InvoiceNo>A1</InvoiceNo><价税合计>RMB 88.00</价税合计></Invoice>`, 88.00},
		{`<Invoice><InvoiceNo>A1</InvoiceNo><价税合计>CNY 77.70</价税合计></Invoice>`, 77.70},
	}
	for _, tc := range cases {
		f := ParseInvoiceXML([]byte(tc.xml))
		if f == nil {
			t.Fatalf("xml %q must parse", tc.xml)
		}
		if f.Amount != tc.want {
			t.Errorf("xml %q: Amount = %v, want %v", tc.xml, f.Amount, tc.want)
		}
	}
}

// 发票号可能带星号掩码（部分平台脱敏），applyXMLField 会 Trim 掉。
func TestParseInvoiceXML_TrimsMaskingAsterisks(t *testing.T) {
	f := ParseInvoiceXML([]byte(`<Invoice><发票号码>**243120000000112233**</发票号码><价税合计>10.00</价税合计></Invoice>`))
	if f == nil {
		t.Fatal("masked invoice number must still parse")
	}
	if strings.Contains(f.InvoiceNo, "*") {
		t.Errorf("masking asterisks must be trimmed, got %q", f.InvoiceNo)
	}
	if f.InvoiceNo != "243120000000112233" {
		t.Errorf("InvoiceNo = %q, want 243120000000112233", f.InvoiceNo)
	}
}

// labelMatch 的词典边界：裸 "date"/"number"/"title" 这类宽泛词是**子串**匹配，
// 会命中意料之外的标签名。这条把当前行为钉死，让改动必须显式。
func TestLabelMatch_WideSubstringBehaviour(t *testing.T) {
	cases := []struct{ name, want string }{
		{"发票号码", "no"},
		{"InvoiceNo", "no"},
		{"开票日期", "date"},
		{"价税合计", "amount"},
		{"TotalAmount", "amount"},
		{"销售方名称", "seller"},
		{"Seller", "seller"},
		{"购买方名称", "buyer"},
		{"项目名称", "item"},
		{"Unrelated", ""},
	}
	for _, tc := range cases {
		if got := labelMatch(tc.name); got != tc.want {
			t.Errorf("labelMatch(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
