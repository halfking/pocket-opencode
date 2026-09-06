package email

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Invoice 是从「账单类」邮件中提取出的结构化发票/账单记录。
//
// 数据来源：邮件主题 + 摘要 + （可选）缓存的正文文本。由规则引擎
// ExtractInvoice 提取（kxmemory 未配置也可用），每封邮件至多一条记录，
// 以 email_id 幂等 upsert。
//
// 隐私约定：只提取结构化字段（发票号/金额/日期/销售方），不落原文；
// 邮件正文仍走既有 AES-GCM 加密缓存（body_path），本表不存任何正文。
type Invoice struct {
	ID          string  `json:"id"`
	EmailID     string  `json:"emailId"`
	AccountID   string  `json:"accountId"`
	WorkspaceID string  `json:"workspaceId,omitempty"`
	UserID      string  `json:"userId,omitempty"`
	Kind        string  `json:"kind"`                // e-invoice | paper | receipt | bill（电子发票/纸质/收据/账单）
	Category    string  `json:"category"`            // 餐饮 | 交通 | 办公 | 住宿 | 通信 | 其他
	Title       string  `json:"title"`               // 发票抬头
	Seller      string  `json:"seller"`              // 销售方
	Amount      float64 `json:"amount"`              // 价税合计
	Currency    string  `json:"currency,omitempty"`  // CNY/USD…（默认 CNY）
	InvoiceNo   string  `json:"invoiceNo,omitempty"` // 发票号码
	InvoiceDate string  `json:"invoiceDate,omitempty"`
	Subject     string  `json:"subject"`     // 来源邮件主题（便于回溯）
	Status      string  `json:"status"`      // new | filed（待整理 | 已归档）
	ExtractedBy string  `json:"extractedBy"` // rule | llm
	CreatedAt   int64   `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// invoice 金额单位符号 → 币种
var invoiceCurrencySymbols = map[string]string{
	"¥": "CNY", "￥": "CNY", "RMB": "CNY", "人民币": "CNY",
	"$": "USD", "US$": "USD",
	"€": "EUR", "£": "GBP", "₩": "KRW", "¥JPY": "JPY",
}

var (
	reInvoiceNo   = regexp.MustCompile(`(?:发票号码|发票号|票据号码|Invoice\s*(?:No\.?|Number)?|Bill\s*No\.?)[:：\s]*([A-Za-z0-9\-]{8,32})`)
	reInvoiceDate = regexp.MustCompile(`(?:开票日期|日期|Date)[:：\s]*(\d{4}[-/年.]\d{1,2}[-/月.]\d{1,2})日?`)
	// 价税合计优先，其次 合计/总额/Amount；金额允许千分位
	reAmountTotal = regexp.MustCompile(`(?:价税合计|合计金额|合计|总额|Amount\s*(?:Due|Total)?)[:：（(]?(?:小写[)）]?)?[:：\s]*[¥￥$€£]?\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)`)
	reAnyAmount   = regexp.MustCompile(`[¥￥]\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)`)
	reSeller      = regexp.MustCompile(`(?:销售方名称|销售方|开票方|商户名称|商户|Merchant|Seller)[:：\s]*([^\s,，;；。]{2,40})`)
	reTitle       = regexp.MustCompile(`(?:发票抬头|抬头|购买方名称|购买方)[:：\s]*([^\s,，;；。]{2,60})`)
)

// invoiceKeywordHit 判断文本是否像发票/账单邮件（主题或正文关键词）。
func invoiceKeywordHit(text string) bool {
	t := strings.ToLower(text)
	for _, kw := range []string{
		"发票", "电子发票", "增值税", "开票", "票据", "收据",
		"invoice", "receipt", "vat", "e-invoice", "billing",
		"账单", "对账单", "订单确认", "支付成功", "扣款",
	} {
		if strings.Contains(t, kw) {
			return true
		}
	}
	return false
}

// InvoiceCandidate 判断邮件主题+摘要是否命中发票/账单关键词。
// 供自动提取链路决定是否值得读缓存正文做二次提取（正文 IO/解密较贵，只对候选做）。
func InvoiceCandidate(e Email) bool {
	return invoiceKeywordHit(e.Subject + "\n" + e.Snippet)
}

// classifyInvoiceKind 按关键词判断票据种类。
func classifyInvoiceKind(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "增值税专用"), strings.Contains(t, "special vat"):
		return "vat-special"
	case strings.Contains(t, "电子发票"), strings.Contains(t, "e-invoice"), strings.Contains(t, "增值税电子"):
		return "e-invoice"
	case strings.Contains(t, "纸质"):
		return "paper"
	case strings.Contains(t, "收据"), strings.Contains(t, "receipt"):
		return "receipt"
	default:
		return "bill"
	}
}

// classifyInvoiceCategory 按销售方/主题/正文关键词推断消费类目（对齐 finance 的类目习惯）。
func classifyInvoiceCategory(parts ...string) string {
	t := strings.ToLower(strings.Join(parts, " "))
	switch {
	case strings.Contains(t, "餐"), strings.Contains(t, "美团"), strings.Contains(t, "饿了么"), strings.Contains(t, "肯德基"), strings.Contains(t, "麦当劳"), strings.Contains(t, "咖啡"), strings.Contains(t, "restaurant"):
		return "餐饮"
	case strings.Contains(t, "滴滴"), strings.Contains(t, "出行"), strings.Contains(t, "航空"), strings.Contains(t, "铁路"), strings.Contains(t, "12306"), strings.Contains(t, "出租车"), strings.Contains(t, "加油"), strings.Contains(t, "交通"):
		return "交通"
	case strings.Contains(t, "酒店"), strings.Contains(t, "住宿"), strings.Contains(t, "民宿"), strings.Contains(t, "hotel"):
		return "住宿"
	case strings.Contains(t, "话费"), strings.Contains(t, "移动"), strings.Contains(t, "联通"), strings.Contains(t, "电信"), strings.Contains(t, "宽带"), strings.Contains(t, "腾讯"), strings.Contains(t, "阿里云"), strings.Contains(t, "aws"), strings.Contains(t, "azure"), strings.Contains(t, "软件"), strings.Contains(t, "saas"), strings.Contains(t, "订阅"):
		return "通信"
	case strings.Contains(t, "办公"), strings.Contains(t, "文具"), strings.Contains(t, "打印"), strings.Contains(t, "京东"), strings.Contains(t, "淘宝"), strings.Contains(t, "天猫"), strings.Contains(t, "办公用品"):
		return "办公"
	default:
		return "其他"
	}
}

func normalizeInvoiceAmount(raw string) float64 {
	raw = strings.ReplaceAll(raw, ",", "")
	amt, err := strconv.ParseFloat(raw, 64)
	if err != nil || amt <= 0 {
		return 0
	}
	return amt
}

func normalizeInvoiceDate(raw string) string {
	// 2026年09月05日 / 2026-09-05 / 2026/9/5 → 2026-09-05
	r := strings.NewReplacer("年", "-", "月", "-", "日", "", ".", "-", "/", "-")
	s := r.Replace(raw)
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// ExtractInvoice 用规则从邮件中提取发票信息。第二个返回值表示是否命中。
//
// bodyText 为可选的已解密正文文本（server 层负责读缓存并解密）；空时只用
// 主题 + 摘要。规则优先级：价税合计 > 任一 ¥ 金额（取最大）。
func ExtractInvoice(e Email, bodyText string) (*Invoice, bool) {
	subject := e.Subject
	snippet := e.Snippet
	joined := subject + "\n" + snippet
	if bodyText != "" {
		// 正文只取前 4KB 参与匹配，避免大正文拖慢正则
		if len(bodyText) > 4096 {
			bodyText = bodyText[:4096]
		}
		joined = subject + "\n" + snippet + "\n" + bodyText
	}
	if !invoiceKeywordHit(joined) {
		return nil, false
	}

	inv := &Invoice{
		EmailID:   e.ID,
		AccountID: e.AccountID,
		Subject:   subject,
		Kind:      classifyInvoiceKind(joined),
		Category:  classifyInvoiceCategory(subject, snippet, e.FromName, e.FromAddress),
		Currency:  "CNY",
		// 提取产物初始为待整理；留空会违反 email_invoices 的 status check 约束
		//（UpsertInvoice 直接透传该列），导致每次提取落库必然失败。
		Status: "new",
	}

	if m := reInvoiceNo.FindStringSubmatch(joined); m != nil {
		inv.InvoiceNo = strings.TrimSpace(m[1])
	}
	if m := reInvoiceDate.FindStringSubmatch(joined); m != nil {
		inv.InvoiceDate = normalizeInvoiceDate(m[1])
	}
	if m := reSeller.FindStringSubmatch(joined); m != nil {
		inv.Seller = strings.TrimSpace(m[1])
	}
	if inv.Seller == "" {
		// 销售方常见在发件人域名/名称里（如 billing@didichuxing.com）
		inv.Seller = strings.TrimSpace(e.FromName)
		if inv.Seller == "" {
			inv.Seller = e.FromAddress
		}
	}
	if m := reTitle.FindStringSubmatch(joined); m != nil {
		inv.Title = strings.TrimSpace(m[1])
	}

	if m := reAmountTotal.FindStringSubmatch(joined); m != nil {
		inv.Amount = normalizeInvoiceAmount(m[1])
	}
	if inv.Amount == 0 {
		// 兜底：取文本里最大的 ¥ 金额
		best := 0.0
		for _, m := range reAnyAmount.FindAllStringSubmatch(joined, -1) {
			if v := normalizeInvoiceAmount(m[1]); v > best {
				best = v
			}
		}
		inv.Amount = best
	}

	// 没有金额也没有发票号 → 命中关键词但不是可归档票据（如营销邮件）
	if inv.Amount == 0 && inv.InvoiceNo == "" {
		return nil, false
	}
	inv.ExtractedBy = "rule"
	return inv, true
}
