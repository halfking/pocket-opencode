package email

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// xmlinvoice.go — 电子发票 XML 附件解析。
//
// 全电发票（数电票）与各服务商导出的 XML 结构不统一：有的用 <Invoice/>
// 元素 + 中文子标签，有的用英文 Key/Value 清单，有的套 xsi 命名空间。
// 这里不做 schema 绑定，而是把整棵树拍平成 (路径, 文本) 对，再按中英文
// 标签词典匹配，把能对上的字段补全进 Invoice。解析不出来时返回 nil，
// 调用方走重试/人工路径。

// XMLInvoiceFields 是从 XML 里解析出的字段集（零值表示未解析到）。
type XMLInvoiceFields struct {
	InvoiceNo   string
	InvoiceDate string
	Amount      float64
	Seller      string
	BuyerTitle  string
	Category    string
}

// labelMatch 把 XML 元素名/键名映射到字段（中英文常见写法）。
func labelMatch(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case containsAny(n, "发票号码", "发票号", "invoiceno", "invoice_no", "invoicenumber", "number"):
		return "no"
	case containsAny(n, "开票日期", "发票日期", "invoicedate", "invoice_date", "issuedate", "date"):
		return "date"
	case containsAny(n, "价税合计", "合计金额", "总额", "totalamount", "amounttotal", "total_amount", "totaltaxamount", "amountintotal"):
		return "amount"
	case containsAny(n, "销售方名称", "销售方", "开票方", "sellername", "seller_name", "seller"):
		return "seller"
	case containsAny(n, "购买方名称", "发票抬头", "购买方", "buyername", "buyer_name", "buyer", "title"):
		return "buyer"
	case containsAny(n, "项目名称", "货物名称", "品名", "itemname", "item_name", "goodsname"):
		return "item"
	}
	return ""
}

func containsAny(s string, keys ...string) bool {
	// 与其它 containsAny 不同：这里键本身可能是中文（无大小写），直接子串匹配
	for _, k := range keys {
		if k == "" {
			continue
		}
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// ParseInvoiceXML 解析发票 XML。不识别或字段过少时返回 nil。
func ParseInvoiceXML(raw []byte) *XMLInvoiceFields {
	if len(raw) == 0 {
		return nil
	}
	// 去 UTF-8 BOM（Windows 工具导出的 XML 常见）
	raw = bytes.TrimPrefix(bytes.TrimSpace(raw), []byte{0xEF, 0xBB, 0xBF})
	var root xmlNode
	if err := xml.Unmarshal(raw, &root); err != nil {
		return nil
	}
	fields := &XMLInvoiceFields{}
	hits := 0
	var walk func(n xmlNode)
	walk = func(n xmlNode) {
		tag := n.XMLName.Local
		if label := labelMatch(tag); label != "" {
			applyXMLField(fields, label, strings.TrimSpace(deepText(n)))
		}
		// attribute 形式：<Item AmountTotal="123.00" .../>
		for _, attr := range n.Attrs {
			if label := labelMatch(attr.Name.Local); label != "" {
				applyXMLField(fields, label, strings.TrimSpace(attr.Value))
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	// 统计命中：金额或发票号至少拿到一个才算有效解析
	if fields.InvoiceNo != "" {
		hits++
	}
	if fields.Amount > 0 {
		hits++
	}
	if hits == 0 {
		return nil
	}
	return fields
}

// applyXMLField 把一个 (标签类别, 文本) 应用到字段集（先到先得，不覆盖）。
func applyXMLField(f *XMLInvoiceFields, label, text string) {
	if text == "" {
		return
	}
	switch label {
	case "no":
		if f.InvoiceNo == "" {
			f.InvoiceNo = strings.Trim(text, "*")
		}
	case "date":
		if f.InvoiceDate == "" {
			f.InvoiceDate = normalizeInvoiceDate(text)
		}
	case "amount":
		if f.Amount == 0 {
			f.Amount = normalizeInvoiceAmount(stripCurrency(text))
		}
	case "seller":
		if f.Seller == "" {
			f.Seller = text
		}
	case "buyer":
		if f.BuyerTitle == "" {
			f.BuyerTitle = text
		}
	case "item":
		if f.Category == "" {
			f.Category = classifyInvoiceCategory(text)
		}
	}
}

// stripCurrency 去金额里的货币符号与千分位。
func stripCurrency(s string) string {
	repl := strings.NewReplacer("¥", "", "￥", "", "元", "", ",", "", "RMB", "", "CNY", "", "$", "")
	return strings.TrimSpace(repl.Replace(s))
}

// mergeXMLFields 用解析结果补全发票记录（只在原字段为空/为零时覆盖，
// 保持邮件主题提取值的优先级）。
func mergeXMLFields(inv *Invoice, f *XMLInvoiceFields) {
	if inv.InvoiceNo == "" {
		inv.InvoiceNo = f.InvoiceNo
	}
	if inv.InvoiceDate == "" {
		inv.InvoiceDate = f.InvoiceDate
	}
	if inv.Amount == 0 {
		inv.Amount = f.Amount
	}
	if f.Seller != "" {
		inv.Seller = firstNonEmpty(f.Seller, inv.Seller)
	}
	if inv.Title == "" {
		inv.Title = f.BuyerTitle
	}
	if f.Category != "" && f.Category != "其他" {
		inv.Category = f.Category
	}
	// 金额/日期补全后 savePDF 会用 InvoiceFileName 重新生成规范文件名
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// xmlNode 是宽容解析用的通用 XML 节点：不绑定任何 schema，递归接收任意
// 嵌套与属性（XMLName 捕获标签名，",any,attr" 捕获全部属性）。
type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []xmlNode  `xml:",any"`
}

// deepText 聚合节点及其所有后代的 chardata。字段值常包在一层结构里
//（如 <Seller><Name>供应商甲</Name></Seller>），必须下钻才拿得到文本。
func deepText(n xmlNode) string {
	var b strings.Builder
	var rec func(x xmlNode)
	rec = func(x xmlNode) {
		b.WriteString(x.Text)
		for _, c := range x.Children {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}
