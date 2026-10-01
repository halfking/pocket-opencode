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
	// Currency 是从 XML 里读到的币种。
	//
	// 2026-10-01 补。此前本结构没有这个槽位，labelMatch 也不认
	// <Currency>/<币种>，于是 XML 附件里写明的币种在解析阶段直接蒸发。
	// 后果不是「少个字段」而是**错账**：currencyOrDefault("") 返回 "CNY"，
	// ledger 又按币种分组汇总，于是 100.00 USD 的 XML 发票被当成
	// 100.00 CNY 计入合计——跨币种直接相加，这正是 14d3bd2 在 PDF 路径上
	// 修掉的同一个问题，在 XML 路径上原样存在。真实数据没暴露是因为
	// 现有 7 张发票全是 CNY 且都走 PDF 附件路径。
	Currency string
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
	case containsAny(n, "currency", "币种", "货币", "货币代码"):
		return "currency"
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
			// nodeText 而非 deepText：父节点（如 <Seller>）要下钻到「名称」叶子，
			// 否则会把名称和税号拼在一起。
			applyXMLField(fields, label, strings.TrimSpace(nodeText(n)))
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
	case "currency":
		if f.Currency == "" {
			// 认得出来的币种才落。认不出来就留空——**不能**兜底成 CNY，
			// 那等于把未知币种当人民币，正是本次要修的错账。
			// 留空时 mergeXMLFields 不覆盖 inv.Currency，行为与修复前
			// 完全一致（不会更糟），且 Why/LastError 侧能看出是未识别。
			up := strings.ToUpper(strings.TrimSpace(text))
			switch up {
			case "CNY", "RMB", "¥", "￥", "元", "人民币":
				f.Currency = "CNY"
			case "USD", "$", "美元":
				f.Currency = "USD"
			case "EUR", "€", "欧元":
				f.Currency = "EUR"
			case "GBP", "£", "英镑":
				f.Currency = "GBP"
			case "HKD", "港币":
				f.Currency = "HKD"
			case "JPY", "日元":
				f.Currency = "JPY"
			}
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
	if inv.Seller == "" {
		inv.Seller = f.Seller
	}
	if inv.Title == "" {
		inv.Title = f.BuyerTitle
	}
	if f.Category != "" && f.Category != "其他" {
		inv.Category = f.Category
	}
	// 币种与其它字段不同：**不**走「只在空时补」的规则。
	//
	// 主题里解析出的币种不可靠（信封里根本没有币种信息，那里只可能来自
	// 邮件正文模板），而 XML 附件里的 <Currency> 是开票方写死的权威值。
	// 两者冲突时以 XML 为准；XML 没写才保持原值。
	if f.Currency != "" {
		inv.Currency = f.Currency
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
// （如 <Seller><Name>供应商甲</Name></Seller>），必须下钻才拿得到文本。
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

// leafNameKeys 是「名称类」叶子的标签。真实数电票把销售方拆成
//
//	<Seller><销售方名称>腾讯…</销售方名称><销售方纳税人识别号>9144…</销售方纳税人识别号></Seller>
//
// 无条件 deepText 会把名称和税号拼成
// 「腾讯科技（深圳）有限公司9144030071526726XG」——那不是任何一方的名字，
// 直接进 {费用类型}-{对方单位}-{金额}-{日期}.pdf 就会产出一个畸形文件名。
// 所以父节点命中时优先下钻到「名称」叶子，只有找不到才退回 deepText。
var leafNameKeys = []string{"名称", "name"}

// nodeText 取一个节点作为字段值时的文本：优先返回其「名称」子叶子的文本。
func nodeText(n xmlNode) string {
	if t := findNameLeaf(n); t != "" {
		return t
	}
	return deepText(n)
}

// findNameLeaf 广度优先找第一个「名称」类叶子节点的文本。
func findNameLeaf(n xmlNode) string {
	queue := []xmlNode{n}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range cur.Children {
			tag := strings.ToLower(strings.TrimSpace(c.XMLName.Local))
			isName := false
			for _, k := range leafNameKeys {
				if k != "" && strings.Contains(tag, k) {
					isName = true
					break
				}
			}
			if isName {
				if txt := strings.TrimSpace(deepText(c)); txt != "" {
					return txt
				}
				continue // 名称节点本身没文本，继续往下找
			}
			queue = append(queue, c)
		}
	}
	return ""
}
