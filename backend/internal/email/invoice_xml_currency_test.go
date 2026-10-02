package email

// invoice_xml_currency_test.go — XML 发票的币种被丢弃（财务正确性缺陷）。
//
// 缺陷：XMLInvoiceFields 没有 Currency 槽位，labelMatch 也不把
// <Currency>/<币种> 映射到任何标签，于是 XML 附件里写明的币种在
// ParseInvoiceXML 阶段直接蒸发，mergeXMLFields 后 inv.Currency 为空串。
//
// 为什么这不是小事：currencyOrDefault("") 返回 "CNY"，而 ledger.go 按
// 币种分组汇总。于是**一张 100.00 USD 的 XML 发票会被当成 100.00 CNY
// 计入合计**——账面上总额不变，但把两种货币直接相加本身就是错的
// （这正是 14d3bd2 修掉的那个问题的 XML 版本）。更隐蔽的是
// InvoiceFileName 也不带币种，两张同名同额不同币种的发票会撞名。
//
// 真实数据为什么没暴露：现有 7 张真实发票全是 CNY，且全部走 PDF 附件
// 路径（PDF 路径不经过 XML 解析）。
//
// 本文件先**证明缺陷存在**，再钉住修复后的行为。

import (
	"testing"
)

// 明确写明币种为 USD 的发票 XML。
const xmlInvoiceUSD = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice>
  <Seller><Name>Amazon Web Services</Name></Seller>
  <InvoiceNo>AWS-2026-0915-001</InvoiceNo>
  <InvoiceDate>2026-09-15</InvoiceDate>
  <价税合计>100.00</价税合计>
  <Currency>USD</Currency>
</Invoice>`

// XML 里的币种必须被保留，并进到发票记录里。
//
// 修复前：Currency="" -> currencyOrDefault 兜底成 CNY -> 100 USD 被当
// 100 CNY 计入合计。修复后归入 USD 分组，与 CNY 分开汇总。
func TestXMLInvoiceUSD_CurrencyPreserved(t *testing.T) {
	fields := ParseInvoiceXML([]byte(xmlInvoiceUSD))
	if fields == nil {
		t.Fatal("ParseInvoiceXML 返回 nil")
	}
	if fields.Currency != "USD" {
		t.Errorf("解析出的 Currency=%q, want USD", fields.Currency)
	}
	inv := &Invoice{Subject: "AWS 账单"}
	mergeXMLFields(inv, fields)

	if inv.Amount != 100.00 {
		t.Fatalf("Amount=%v, want 100.00", inv.Amount)
	}
	if inv.Currency != "USD" {
		t.Fatalf("合并后 Currency=%q, want USD —— 丢了它账本就会按 CNY 计（100 USD 变 100 CNY）", inv.Currency)
	}
	if got := currencyOrDefault(inv.Currency); got != "USD" {
		t.Errorf("账本按 %q 归组, want USD", got)
	}
}

// XML 是权威值：即使主题侧已解析出别的币种，也要以 XML 为准。
//
// 主题/正文里的币种来自邮件模板，不可靠；XML 附件是开票方写死的。
func TestXMLInvoiceCurrencyOverridesSubject(t *testing.T) {
	fields := ParseInvoiceXML([]byte(xmlInvoiceUSD))
	if fields == nil {
		t.Fatal("ParseInvoiceXML 返回 nil")
	}
	inv := &Invoice{Subject: "账单", Currency: "CNY"}
	mergeXMLFields(inv, fields)
	if inv.Currency != "USD" {
		t.Errorf("Currency=%q, want USD（XML 附件应覆盖主题侧推测值）", inv.Currency)
	}
}

// 账本层面：CNY 与 USD 必须分组，不能直接相加（14d3bd2 的语义在 XML 路径上同样成立）。
func TestXMLInvoiceMultiCurrencyGroupedInLedger(t *testing.T) {
	invs := []Invoice{
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 100, Currency: "USD", Seller: "AWS"},
		{Status: "downloaded", FilePath: "ledger-fixture.pdf", Amount: 454.50, Currency: "CNY", Seller: "腾讯"},
	}
	rows, totals := LedgerRows(invs)
	trs := totalRows(rows)
	if len(trs) != 2 {
		t.Fatalf("两种币种应产生 2 行合计，got %d: %v", len(trs), trs)
	}
	// 返回值也必须分组（2026-10-01 起不再返回跨币种的标量总额）
	if len(totals) != 2 {
		t.Fatalf("两种币种应产生 2 个 CurrencyTotal, got %+v", totals)
	}
	byCur := map[string]float64{}
	for _, r := range trs {
		byCur[r[3].(string)] = r[2].(float64)
	}
	if byCur["USD"] != 100.00 || byCur["CNY"] != 454.50 {
		t.Errorf("分组合计错误: %v", byCur)
	}
}

// 附带确认：规范文件名不带币种，两张同额不同币种的发票会撞名。
//
// 这不是本次要修的（需求原文的文件名格式里没有币种位），但要记录后果。
func TestXMLInvoiceFileNameCollisionAcrossCurrencies(t *testing.T) {
	a := &Invoice{Status: "downloaded", FilePath: "ledger-fixture.pdf", Category: "其他", Seller: "某供应商", Amount: 100, Currency: "CNY", InvoiceDate: "2026-09-15"}
	b := &Invoice{Status: "downloaded", FilePath: "ledger-fixture.pdf", Category: "其他", Seller: "某供应商", Amount: 100, Currency: "USD", InvoiceDate: "2026-09-15"}
	if InvoiceFileName(a) == InvoiceFileName(b) {
		t.Logf("确认：%s —— 需求原文的命名格式不含币种，两张不同币种的发票会撞名", InvoiceFileName(a))
	} else {
		t.Logf("文件名已含币种信息，不再撞名")
	}
}
