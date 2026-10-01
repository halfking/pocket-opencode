package email

import (
	"regexp"
	"sort"
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
	// EmailDate 是来源邮件的收到时间（Unix 秒）。列表按它倒排；
	// 不落 email_invoices 列，由 JOIN emails.date 填入。
	EmailDate int64  `json:"emailDate,omitempty"`
	Subject   string `json:"subject"` // 来源邮件主题（便于回溯）
	Status      string  `json:"status"`      // new | pending | downloaded | failed | filed
	ExtractedBy string  `json:"extractedBy"` // rule | llm
	CreatedAt   int64   `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`

	// —— 发票文件采集（Harvest 流水线维护）——
	// FileName 落盘文件名，格式 {费用类型}-{对方单位}-{金额}-{日期}.pdf。
	// FilePath 服务端磁盘相对路径（dataDir 下）。FileSource 标记来源：
	// attachment（邮件附件）/ pdf-url（正文链接直下）/ xml-render（XML 解析后重渲染）。
	// Attempts 记录下载尝试次数——部分发票平台要多次点击才能拿到文件，
	// 流水线每轮对 pending 的记录重试，超过上限转 failed。
	FileName     string `json:"fileName,omitempty"`
	FilePath     string `json:"filePath,omitempty"`
	FileSource   string `json:"fileSource,omitempty"`
	Attempts     int    `json:"attempts,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	ExportedAt   int64  `json:"exportedAt,omitempty"`   // 最近一次进入 A4 网格导出的时间
	FeishuSentAt int64  `json:"feishuSentAt,omitempty"` // 推送飞书成功时间；0 = 未推送
}

// invoice 金额单位符号 → 币种
var invoiceCurrencySymbols = map[string]string{
	"¥": "CNY", "￥": "CNY", "RMB": "CNY", "人民币": "CNY",
	"$": "USD", "US$": "USD",
	"€": "EUR", "£": "GBP", "₩": "KRW", "¥JPY": "JPY",
}

var (
	// 值必须**含数字**：真实发票号总是数字串或数字+字母混合
	// （中文 8/20 位纯数字、英文 "INV-TEST-0001" 这类带字母的）。
	// 纯字母单词必须排除——否则主题「[QQ Wallet] Electronic Invoice
	// Issuance Notice」里，`Invoice`（Number 可选）+ 空格 会把后面的
	// "Issuance"（8 个纯字母，刚过 {7,31} 长度门槛）当成发票号，
	// invoice_no="Issuance"、真实号码被丢（2026-10-01 真实数据）。
	// 用「含数字」而不是「数字打头」：后者会把 INV-TEST-0001 这类
	// 合规的字母前缀号码也误杀（invoice_realworld_test.go 覆盖）。
	reInvoiceNo = regexp.MustCompile(`(?i)(?:发票号码|发票号|票据号码|Invoice\s*(?:No\.?|Number)?|Bill\s*No\.?)[:：\s]*([A-Za-z0-9\-]{7,31}[0-9][A-Za-z0-9\-]*)`)
	reInvoiceDate = regexp.MustCompile(`(?:开票日期|发票日期|开票时间|日期|Date)[:：\s]*(\d{4}[-/年.]\d{1,2}[-/月.]\d{1,2}|\d{8})日?`)
	reLooseCNDate = regexp.MustCompile(`(\d{4}年\d{1,2}月\d{1,2})日?`)
	// 货币代码 / 符号前缀。
	//
	// 为什么加 ISO 4217 代码（2026-10-01 真实数据）：QQ Wallet 英文发票写
	// 「Total tax-inclusive amount: CNY 126.00」。旧正则只认 [¥￥$€£] 符号，
	// `CNY ` 不在白名单，数字整个没被捕获 ⇒ amount=0 ⇒ 规范文件名退化成
	// `其他-<单位>-0.00-<日期>.pdf`，对账时金额是空的。
	reCurrency = `[¥￥]?\s*(?:CNY|RMB|USD|EUR|GBP|HKD|JPY)?\s*|[$€£]\s*`
	// 价税合计优先，其次 合计/总额/金额/Amount；金额允许千分位与尾随「元」。
	//
	// 「金额」是实测补的：真发票邮件（QQ 邮箱，2026-09-30）主题写
	// 「…发票号码：<20位号码>，金额：3500.00元，请注意查收！」（标识符已合成化，
	// 全形态见 invoice_realworld_test.go），
	// 关键词表里没有「金额」⇒ amount=0 ⇒ 共享台账合计行直接少算这一张。
	// 为了不误伤散文（「您本月的金额已超出额度」这种后面不跟数字的句子），
	// 数值部分是必需的：没数字就不匹配。
	reAmountTotal = regexp.MustCompile(`(?i)(?:价税合计|合计金额|合计|总额|金额|amount|Amount\s*(?:Due|Total)?)[:：（(]?(?:小写[)）]?)?[:：\s]*(` + reCurrency + `)([0-9][0-9,]*(?:\.[0-9]{1,2})?)\s*(?:元|[圆])?`)
	reAnyAmount   = regexp.MustCompile(`(?i)([¥￥$€£])\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)|(?i)(CNY|RMB|USD|EUR|GBP|HKD|JPY)\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)`)
	// 销售方：标签里可能夹着 "name"/"名称" 之类的修饰词，值要取**冒号/空格
	// 之后第一个非标签词**。
	//
	// 真实数据：英文发票写「Seller name: Tencent Cloud Computing Co Ltd」。
	// 旧正则 `Seller)[:：\s]*([^\s,，;；。]{2,40})` 匹配 "Seller" 后吃空格，
	// 把标签词 "name:" 当成了销售方（seller="name:"），真正的公司名丢掉。
	// 这里显式允许 "Seller name"/"销售方名称" 这类复合标签。
	// 值可含空格（公司名 "Tencent Cloud Computing Co Ltd"），但**不得跨行**：
	// 用 [^\S\r\n]（非换行空白）而不是 \s——\s 含 \r\n，贪婪匹配会把
	// "Seller name: Tencent Cloud Computing Co Ltd" 后面那行
	// "Invoice details please see attachment (PDF)." 一起吞成销售方，
	// 规范文件名退化成 `其他-Tencent-…-Invoice-details-please-see-a-….pdf`。
	// 同时限制最多 6 个词，避免长句被当单位名。
	reSeller      = regexp.MustCompile(`(?i)(?:销售方名称|销售方|开票方|商户名称|商户|Merchant(?:\s*Name)?|Seller(?:\s*Name)?)[:：\s]*([^\s:：,，;；。]{2,40}(?:[^\S\r\n]+[^\s:：,，;；。]{2,40}){0,5})`)
	// 「您收到来自XX的发票」——中文发票邮件最常见的形态，主题里就有对方单位。
	// 不抽的话销售方会退化成发件地址，规范文件名变成
	// 「其他-noreply@<发件域名>-3500.00-….pdf」，对账时看不出是谁开的票。
	reSellerFromSubject = regexp.MustCompile(`(?:来自|由)\s*([^,，;；。]{2,40}?)(?:开具|开具的|提供|提供的|的)?\s*(?:电子)?(?:发票|账单|收据|票据)`)
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

// normalizeCurrency 把正则捕获到的币种标记归一成 ISO 4217 代码。
// mark 可能是符号（¥ ￥ $ € £）或代码（CNY RMB USD EUR GBP HKD JPY）。
// 空/无法识别时返回 fallback（默认 CNY）——宁可标成 CNY 也不要留空。
//
// 为什么需要它：提取器原先把 Currency 硬编码成 "CNY"（invoice.go:286），
// 而 reCurrency 其实**已经能识别** USD/EUR 等——识别结果被丢掉了。
// 后果是一张「Total tax-inclusive amount: USD 126.00」的外币发票会被标成
// CNY，汇总时 USD 与 CNY 直接相加，得到的合计是错的。
func normalizeCurrency(mark, fallback string) string {
	m := strings.ToUpper(strings.TrimSpace(mark))
	switch m {
	case "CNY", "RMB", "¥", "￥", "元":
		return "CNY"
	case "USD", "$":
		return "USD"
	case "EUR", "€":
		return "EUR"
	case "GBP", "£":
		return "GBP"
	case "HKD":
		return "HKD"
	case "JPY":
		return "JPY"
	}
	if fallback != "" {
		return fallback
	}
	return "CNY"
}

func normalizeInvoiceDate(raw string) string {
	// 2026年09月05日 / 2026-09-05 / 2026/9/5 / 20260901 → 2026-09-05
	r := strings.NewReplacer("年", "-", "月", "-", "日", "", ".", "-", "/", "-")
	s := strings.TrimSpace(r.Replace(raw))
	if len(s) == 8 && isDigits(s) {
		s = s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

func isDigits(s string) bool {
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return s != ""
}

// ParseInvoiceDate 从发票/邮件文本里抽出开票日期并归一化为 YYYY-MM-DD。
// 先匹配带「开票日期」等标签的写法，再兜底中文年月日。
func ParseInvoiceDate(text string) string {
	if m := reInvoiceDate.FindStringSubmatch(text); m != nil {
		return normalizeInvoiceDate(m[1])
	}
	if m := reLooseCNDate.FindStringSubmatch(text); m != nil {
		return normalizeInvoiceDate(m[1])
	}
	return ""
}

// ParseInvoiceDateFromBytes 扫描文件字节（PDF 未压缩文本 / 图片旁路无效）。
func ParseInvoiceDateFromBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > 64<<10 {
		raw = raw[:64<<10]
	}
	return ParseInvoiceDate(string(raw))
}

func invoiceReceivedKey(inv Invoice) int64 {
	if inv.EmailDate > 0 {
		return inv.EmailDate
	}
	return inv.CreatedAt
}

// SortInvoicesByReceived 按来源邮件收到时间倒排；没有 emailDate 时用 createdAt。
func SortInvoicesByReceived(invoices []Invoice) {
	sort.SliceStable(invoices, func(i, j int) bool {
		di, dj := invoiceReceivedKey(invoices[i]), invoiceReceivedKey(invoices[j])
		if di != dj {
			return di > dj
		}
		return invoices[i].CreatedAt > invoices[j].CreatedAt
	})
}

// ExtractInvoice 用规则从邮件中提取发票信息。第二个返回值表示是否命中。
//
// bodyText 为可选的已解密正文文本（server 层负责读缓存并解密）；空时只用
// 主题 + 摘要。规则优先级：价税合计 > 任一 ¥ 金额（取最大）。
func ExtractInvoice(e Email, bodyText string) (*Invoice, bool) {
	return ExtractInvoiceLoose(e, bodyText, false)
}

// ExtractInvoiceLoose 与 ExtractInvoice 相同，但多一个 hasInvoiceAttachment 开关：
// 命中关键词却抽不到金额/发票号时，若调用方确认这封邮件**带着**发票类附件
// （PDF/图片/XML），仍然建档，把金额与日期留给采集器从附件里补。
//
// 为什么需要这条：真实账单邮件的形态是「主题写 9 月度对账单、金额只印在
// 附件 PDF 里」。原来的硬门槛（金额和发票号都没有 → 直接丢弃）会在采集器
// 看到附件**之前**就把这封邮件扔掉，于是 harvestOne 的 PDF 附件分支永远
// 没机会跑——实测夹具邮件就是这样：正文只有一句「见附件」，流水线
// invoices.Processed=0，发票列表 0 条。
//
// 放宽只在有附件证据时生效，且不碰强门槛：没有附件的营销「账单提醒」邮件
// 依旧被丢弃，避免发票列表被垃圾邮件灌满。
func ExtractInvoiceLoose(e Email, bodyText string, hasInvoiceAttachment bool) (*Invoice, bool) {
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
	inv.InvoiceDate = ParseInvoiceDate(joined)
	if m := reSeller.FindStringSubmatch(joined); m != nil {
		inv.Seller = strings.TrimSpace(m[1])
	}
	if inv.Seller == "" {
		// 正文里没有「销售方：」时，主题里的「来自XX的发票」往往就是开票方。
		if m := reSellerFromSubject.FindStringSubmatch(subject); m != nil {
			inv.Seller = strings.TrimSpace(m[1])
		}
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
		// m[1] = 币种标记（¥/CNY/USD…），m[2] = 金额
		inv.Amount = normalizeInvoiceAmount(m[2])
		inv.Currency = normalizeCurrency(m[1], inv.Currency)
	}
	if inv.Amount == 0 {
		// 兜底：取文本里最大的金额。reAnyAmount 有两个分支：
		//   分支 1（符号）: m[1]=符号, m[2]=金额
		//   分支 2（ISO 码）: m[3]=代码, m[4]=金额
		best := 0.0
		var bestCur string
		for _, m := range reAnyAmount.FindAllStringSubmatch(joined, -1) {
			cur, num := m[1], m[2]
			if num == "" {
				cur, num = m[3], m[4]
			}
			if v := normalizeInvoiceAmount(num); v > best {
				best, bestCur = v, cur
			}
		}
		inv.Amount = best
		if bestCur != "" {
			inv.Currency = normalizeCurrency(bestCur, inv.Currency)
		}
	}

	// 没有金额也没有发票号：营销邮件伪命中。
	// 例外——邮件确实带着 PDF/图片/XML 附件时仍然建档，让采集器去附件里找。
	if inv.Amount == 0 && inv.InvoiceNo == "" && !hasInvoiceAttachment {
		return nil, false
	}
	inv.ExtractedBy = "rule"
	return inv, true
}
