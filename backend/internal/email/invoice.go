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
	EmailDate int64 `json:"emailDate,omitempty"`
	// sellerIsFallback 标记 Seller 来自**信封兜底**（发件人名称/地址），
	// 而不是从邮件正文或发票 XML 里解析出来的。
	//
	// 不落 email_invoices 列（同 EmailDate）。它只回答一个问题：
	// 「这个单位名是**开票方**，还是只是邮件经过了谁的服务器？」
	//
	// 2026-10-03 真实数据实测：ExtractInvoiceLoose 在正文没写「销售方」时把
	// inv.Seller 兜底成发件地址（noreply@toll.example），而 mergeXMLFields
	// 只在「为空时补」⇒ 发票 XML 里权威的
	// 「浙江沪杭甬高速公路股份有限公司」**永远进不来**，规范文件名退化成
	// `其他-noreply@toll.example-5.61-2026-09-14-….pdf`——
	// 需求原文 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 里的「对方单位」
	// 那一段直接是错的，交给财务时看不出是谁开的票。
	//
	// 只给**发件人兜底**打标；「来自XX的发票」这类从主题解析出的单位名
	// 是真证据，不打标，不许被 XML 覆盖。
	sellerIsFallback bool
	Subject          string `json:"subject"`     // 来源邮件主题（便于回溯）
	Status           string `json:"status"`      // new | pending | downloaded | failed | filed
	ExtractedBy      string `json:"extractedBy"` // rule | llm
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`

	// —— 发票文件采集（Harvest 流水线维护）——
	// FileName 落盘文件名，基础格式 {费用类型}-{对方单位}-{金额}-{日期}.pdf。
	// 实际可能带两段后缀（都由 saveInvoiceFile 决定，不要在这里反推）：
	//   · `[-{发票号}]`——有发票号时加，防不同发票同名（见 InvoiceFileName）；
	//   · `[-N]`——**目标名已被内容不同的另一张票占用**时加序号，
	//     防止 os.Rename 静默覆盖掉别人的凭证（见 pickFreeInvoicePath）。
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
	reInvoiceNo   = regexp.MustCompile(`(?i)(?:发票号码|发票号|票据号码|Invoice\s*(?:No\.?|Number)?|Bill\s*No\.?)[:：\s]*([A-Za-z0-9\-]{7,31}[0-9][A-Za-z0-9\-]*)`)
	reInvoiceDate = regexp.MustCompile(`(?:开票日期|发票日期|开票时间|日期|Date)[:：\s]*(\d{4}[-/年.]\d{1,2}[-/月.]\d{1,2}|\d{8})日?`)
	reLooseCNDate = regexp.MustCompile(`(\d{4}年\d{1,2}月\d{1,2})日?`)
	// 账单/对账单的出具日。必须**单列一层**，且刻意不含「账单周期」——
	// 工行那封里是「账单周期 2026年09月01日—2026年09月30日 对账单生成日
	// 2026年09月30日」：周期起始日不是出具日，只靠「跳过未来日期」会取到
	// 2026-09-01，仍然是错的（只比 2026-10-25 好一点，仍然错）。
	// reInvoiceDate 也抓不到它：那里的标签是「日期」二字，「生成日」不含。
	reStatementDate = regexp.MustCompile(`(?i)(?:对账单生成日|账单生成日|对账日期|账单日期|出账日|statement\s*date)\s*[:：]?\s*(\d{4}[-/年.]\d{1,2}[-/月.]\d{1,2}|\d{8})日?`)
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
	//
	// reHTMLTagRun 允许「标签与金额之间夹一段 HTML 标签」。这不是为了迁就某个
	// 供应商的怪写法，而是**真实票面**的常态（2026-10-03 从加密原文缓存里
	// 解出来的原文，不是手写夹具）：
	//
	//	…张发票，发票金额共计<span style='color: #FF9100;'>19</span>元。
	//
	// 纯文本部分里「共计」与「19」之间隔着标签。上一轮把它当成「加个『共计』
	// 就好」，实测**加了也不匹配**（handoff §7.4.4）——因为分隔符集合里
	// 仍然容不下 `<span …>`。有了 reHTMLTagRun 才真正匹配得上。
	//
	// 长度上限 200 是防呆：不允许正则跨越整篇 HTML 去「找」一个远处的数字，
	// 那种跨越正是把对账单里的某个数误当成发票金额的来源。
	reHTMLTagRun  = `(?:<[^>\n]{0,200}>\s*)*`
	reAmountTotal = regexp.MustCompile(`(?i)(?:价税合计|合计金额|合计|总额|金额|amount|Amount\s*(?:Due|Total)?)[:：（(]?(?:小写[)）]?|共计)?[:：\s]*` + reHTMLTagRun + `(` + reCurrency + `)([0-9][0-9,]*(?:\.[0-9]{1,2})?)\s*(?:元|[圆])?`)
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
	reSeller = regexp.MustCompile(`(?i)(?:销售方名称|销售方|开票方|商户名称|商户|Merchant(?:\s*Name)?|Seller(?:\s*Name)?)[:：\s]*([^\s:：,，;；。]{2,40}(?:[^\S\r\n]+[^\s:：,，;；。]{2,40}){0,5})`)
	// 「您收到来自XX的发票」——中文发票邮件最常见的形态，主题里就有对方单位。
	// 不抽的话销售方会退化成发件地址，规范文件名变成
	// 「其他-noreply@<发件域名>-3500.00-….pdf」，对账时看不出是谁开的票。
	reSellerFromSubject = regexp.MustCompile(`(?:来自|由)\s*([^,，;；。]{2,40}?)(?:开具|开具的|提供|提供的|的)?\s*(?:电子)?(?:发票|账单|收据|票据)`)
	reTitle             = regexp.MustCompile(`(?:发票抬头|抬头|购买方名称|购买方)[:：\s]*([^\s,，;；。]{2,60})`)
)

// invoiceLabelWords 是电子发票邮件里**当列头用的标签词**。
//
// 为什么需要它（2026-10-02 实测）：reSeller 的值规则是「标签后取 1–6 个 ≥2 字的词」，
// 它挡不住**紧接着的另一个标签**。月度批量开票邮件是 HTML 表格，拍平成文本后
// 单元格之间是换行：
//
//	销售方
//	发票抬头
//	发票号码
//	开票日期
//	价税合计
//
// reSeller 匹配「销售方」后吃掉换行，把「发票抬头」当成了销售方名字。
// 后果直击需求「发票文件格式：{费用类型}-{对方单位}-{金额}-{日期}.pdf」：
// 文件名多出一段变成 5 段，而第 5 段「发票抬头」是**买方**的列头，
// 根本不是对方单位，对账时认不出人。
//
// **别拿磁盘上那个文件当证据**（2026-10-04 更正）：这里原先写着
// 「真实产物：`其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf`」
// ——**那是错的**。该文件解压后正文是 `VAT E-INVOICE (IMAP fixture)`，
// 是 `gen_fixture_invoice_test.go` 的夹具泄漏进了真实数据目录；
// 只读查过台账 7 行，没有任何 5 段名。而且 (云服务开票中心, 1280.00,
// 2026-09-28) 这组三元组在 invoice_retry_test.go /
// invoice_sources_e2e_test.go / invoice_attachment_harvest_wiring_test.go
// 里全是夹具常量。
//
// ⇒ 本条描述的是**代码路径的形态**（喂进拍平的表头就会产出 5 段名），
// 不是「生产上已经产出过」的观测。目前**缺一份真实的生产样本**。
//
// 本文件 reSeller 上方的注释早就写着「值要取**冒号/空格之后第一个非标签词**」——
// 那是**意图**，实现里从来没有这个判断。这里补上。
//
// 词表与本文件其它正则的词典对齐（reSeller / reTitle / reAmountTotal /
// reInvoiceNo / reInvoiceDate 的中文键 + 常见电子发票列头）。加词的标准是
// 「它确实是某张发票的列头」，不是「它看起来不像公司名」——公司名里出现
// 「金额」「日期」的概率低，但列头表里没有的词一律别加，宁可漏判。
var invoiceLabelWords = map[string]bool{
	// 销售方侧
	"销售方名称": true, "销售方": true, "销方名称": true, "销方": true,
	"开票方": true, "开票单位": true, "商户名称": true, "商户": true,
	"商家": true, "卖方": true, "供应商": true, "供货方": true,
	// 购买方侧
	"发票抬头": true, "抬头": true, "购买方名称": true, "购买方": true,
	"购方名称": true, "购方": true, "买方": true,
	// 单据标识与日期
	"发票号码": true, "发票号": true, "票据号码": true, "号码": true,
	"开票日期": true, "发票日期": true, "开票时间": true, "日期": true,
	// 金额侧
	"价税合计": true, "价税合计金额": true, "合计金额": true, "合计": true,
	"总金额": true, "总额": true, "金额": true, "小写": true, "大写": true,
	"不含税金额": true, "税额": true, "税率": true, "单价": true, "数量": true,
	// 登记与联系方式
	"纳税人识别号": true, "统一社会信用代码": true, "税号": true,
	"单位地址": true, "地址": true, "开户行": true, "开户银行": true,
	"银行账号": true, "账号": true, "电话": true, "备注": true,
	"序号": true, "类型": true, "名称": true, "规格": true,
	// 英文列头
	"seller": true, "sellername": true, "merchant": true, "buyer": true,
	"buyername": true, "invoice": true, "invoiceno": true, "invoicenumber": true,
	"amount": true, "total": true, "totalamount": true, "date": true,
	"name": true, "number": true, "taxid": true, "address": true,
	"quantity": true, "unitprice": true, "remark": true, "type": true,
}

// sellerTokenTrim 去掉词两端的标点，正则抓到的值常带「：」「,」这类残留。
var sellerTokenTrim = " \t:：,，;；。、()（）[]【】<>《》\"'"

// isInvoiceLabelWord 判断一个词是不是发票邮件的列头标签（大小写不敏感）。
func isInvoiceLabelWord(w string) bool {
	return invoiceLabelWords[strings.ToLower(strings.Trim(w, sellerTokenTrim))]
}

// isNumericToken 判断一个词是不是纯数字（金额/票号片段）。
func isNumericToken(tok string) bool {
	s := strings.NewReplacer(",", "", "¥", "", "￥", "").Replace(tok)
	if s == "" {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// cleanSellerValue 把 reSeller 抓到的原始值收敛成「对方单位」。
//
// 规则：跳过开头的标签词，从**第一个非标签词**开始取，遇到下一个标签词或数字
// 就停（公司名里不会夹着「发票号码」这种列头，也不会以金额结尾）。取不出、
// 或取到的只是数字，说明这次匹配抓到的是表头而不是单位名，返回 ok=false，
// 让调用方继续走主题规则与发件人兜底。
func cleanSellerValue(raw string) (string, bool) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
	})
	var kept []string
	for _, f := range fields {
		tok := strings.Trim(f, sellerTokenTrim)
		// 数字是段落终止符，不是单位名：既挡住「跳过头部标签后剩下金额」
		// （销售方\n发票抬头\n价税合计：1280.00），也挡住金额被粘在单位名后面
		// （…\n云服务开票中心\n1280.00）。
		if isNumericToken(tok) || reDateLike.MatchString(tok) {
			break
		}
		if isInvoiceLabelWord(f) {
			// 还没取到正主的标签直接跳过；取到之后再遇标签就是段落结束。
			if len(kept) == 0 {
				continue
			}
			break
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return "", false
	}
	out := strings.Join(kept, " ")
	if len([]rune(out)) < 2 {
		return "", false
	}
	return out, true
}

// reDateLike 识别「纯日期 / 纯票号」形态的词。
//
// 它和 isNumericToken 一样是**终止符**而不是候选：表头行形态下销售方那一列的
// 后面紧跟着的就是「开票日期 2026-09-28」这类行，ParseFloat 认不出
// "2026-09-28"，没有这道闸它会被当成单位名写进文件名。
var reDateLike = regexp.MustCompile(`^\d{4}[-/.年]\d{1,2}(?:[-/.月]\d{1,2})?日?$`)

// invoiceSellerSuffix 是中文单位名常见的**结尾**。
//
// 判据用「有没有企业后缀」而不是「有几个汉字」：字数这道闸形同虚设——
// 「详见附件」正好 4 个汉字，直接被当成了对方单位（实测踩过）。
// 对方单位几乎必然带一个这样的结尾，散文则不会。
// 只收两字以上的后缀，单字（「社」「厂」「店」）在散文里太常见。
var invoiceSellerSuffix = []string{
	"公司", "中心", "集团", "股份", "有限", "银行", "事务所", "研究院",
	"科技", "商贸", "实业", "物业", "医院", "学校", "工作室", "分公司",
}

// looksLikeEntityName 判断一个字符串像不像「对方单位」。
//
// 中文走企业后缀；英文走「≥4 个字母且 ≥2 个词」（Tencent Cloud Computing Co Ltd）。
//
// 代价：短品牌名（如「美团」「腾讯」）在这条路上会被放行到发件人兜底。
// 这道闸只作用于**跨行兜底**，不碰主路径，代价仅是「退一步」而不是「取错值」。
func looksLikeEntityName(s string) bool {
	for _, suf := range invoiceSellerSuffix {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	letters, words := 0, 0
	inWord := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
			if !inWord {
				words++
				inWord = true
			}
			continue
		}
		inWord = false
	}
	return letters >= 4 && words >= 2
}

// startsWithLabelField 判断整行是不是「标签：值」形态。
//
// 跨行兜底逐行看候选，而表格行常常是**黏**在一起的：
// 「价税合计：1280.00」没有空格，cleanSellerValue 看到的是单个词
// 「价税合计：1280.00」，跳过标签词那步根本触发不了（它按空白切词），
// 整行会被当成单位名。这类行要在这里挡掉，且**不计入**尝试次数——
// 它是别的字段，不是候选单位名。
func startsWithLabelField(ln string) bool {
	low := strings.ToLower(ln)
	for w := range invoiceLabelWords {
		lw := strings.ToLower(w)
		if !strings.HasPrefix(low, lw) {
			continue
		}
		rest := ln[len(w):]
		if rest == "" {
			return true
		}
		// rest[0] 是字节，直接和 rune 字面量比较会编译报错（'：' 溢出 byte），
		// 所以用前缀判断而不是下标。
		for _, sep := range []string{":", "：", "=", " ", "\t", "　"} {
			if strings.HasPrefix(rest, sep) {
				return true
			}
		}
	}
	return false
}

// sellerFromFollowingLines 在紧邻取值「只剩列头」时，往后找真正的单位名。
//
// 场景（实测复现的形态）：批量开票邮件的表格拍平后先出**一整行表头**，
// 值在后面的行里：
//
//	销售方
//	发票抬头
//	发票号码
//	开票日期
//	价税合计
//	杭州某某科技有限公司
//	1280.00
//
// reSeller 的值分隔符是 [^\S\r\n]+——刻意不含换行，否则会一路吞掉整段散文
// （见 reSeller 上方注释）。代价就是它只能抓到紧邻的「发票抬头」这个列头，
// 抓不到下一行的单位名，于是规范文件名里的「对方单位」只能退化成发件人名称。
// 这个函数补上那一步。
//
// 四道闸，防止它变成散文收割机：
//  1. 只在紧邻取值已经失败时启用（正常路径完全不受影响）；
//  2. 表头行里的其它列头直接跳过、且**不计入**尝试次数；
//  3. 每个候选都要过 cleanSellerValue（拒列头、拒纯数字、拒日期、拒 <2 字碎片）
//     与 looksLikeEntityName（至少 2 个汉字或 3 个字母）；
//  4. 最多试 3 个非列头行。
func sellerFromFollowingLines(joined string, after int) string {
	if after < 0 || after >= len(joined) {
		return ""
	}
	tried := 0
	for _, ln := range strings.Split(joined[after:], "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// 表头行里的其它列头、以及「标签：值」形态的整行，都不是候选单位名，
		// 跳过且不计次数——它们是别的字段。
		if isInvoiceLabelWord(ln) || startsWithLabelField(ln) {
			continue
		}
		if s, ok := cleanSellerValue(ln); ok && looksLikeEntityName(s) {
			return s
		}
		if tried++; tried >= 3 {
			break
		}
	}
	return ""
}

// invoiceKeywordHit 判断文本是否像发票/账单邮件（主题或正文关键词）。
// invoiceKeywordASCII 是必须**按词边界**匹配的英文关键词。
//
// 为什么单独一列：这些词在英文里是普通业务词，子串巧合极多
// （2026-10-04 真实库逐封实测，971 封未建档邮件里的命中情况）：
//
//	billing ← "this billing cycle"（GitHub 套餐周期）
//	         ← console.aws.amazon.com/billing/home（AWS 控制台 URL）
//	vat     ← "activation" / "activate" / "private" 里的子串
//	         ← NVIDIA GTC 会议邀请这类营销邮件
//
// 那一批邮件一封都不是发票，却会占掉 maxInvoiceBodyFetches=24/轮 的拉原文预算，
// 把真发票挤到下一轮。
//
// 中文关键词**不需要**词边界：汉字没有「词内含子词」这回事，
// 「发票」两个字连续出现就是发票语义。中文那侧原样保留 Contains 行为。
var invoiceKeywordASCII = []string{"invoice", "receipt", "vat", "e-invoice", "billing"}

// invoiceKeywordASCIIRegexes 是上面每个英文关键词的**预编译**词边界正则。
//
// 为什么预编译：invoiceKeywordHit 在流水线的候选扫描里对**每封邮件**调用一次
// （实测 90 天窗口 978 封），每次调用现场 MustCompile 五个正则等于把常量开销
// 乘以邮件数。判据 invoice_keyword_wordboundary_test.go 只验行为不验性能，
// 但这个开销是能避免的，就不留下。
//
// 边界两侧不得是：字母 / 数字 / 下划线，也不得是 URL 与标点里常见的形态
// （. - _ / : ? & = # @ + %）。把 URL 分隔符也算进「词内」是刻意的：
// `console.aws.amazon.com/billing/home` 里的 billing 是**路径段**，语义是
// 「账单页面的地址」而不是「这是一张账单」；只挡字母数字的话 `/billing/`
// 照样命中。
const keywordBoundaryClass = `^|[^0-9A-Za-z_\-./:?&=+#@%]`

// invoiceKeywordASCIINegPhrases 是「含发票词但**不是**发票语义」的英文短语。
//
// 与词边界是**两类不同**的问题，混为一谈就会修错：
//
//	· 词边界解决的是 vat 撞 activation、billing 撞 URL 路径段 —— 子串巧合；
//	· 这里解决的是 billing 撞 "this billing cycle"（服务计费周期）—— 词边界
//	  **正确**命中了一个确实存在、但语义无关的词。
//
// 真实库实测：GitHub Actions 分钟耗尽提醒（"You have used 100% so far this
// billing cycle"）会被放行，一封都不是发票，白占一格拉原文预算。
//
// 短语**内部**的空格必须能匹配，所以用 \b 包裹整体而不是逐词加边界类。
var invoiceKeywordASCIINegPhrases = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbilling\s+(cycle|period)\b`),
}

var invoiceKeywordASCIIRegexes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(invoiceKeywordASCII))
	for _, kw := range invoiceKeywordASCII {
		out = append(out, regexp.MustCompile(
			`(?i)(`+keywordBoundaryClass+`)`+regexp.QuoteMeta(kw)+`($|[^0-9A-Za-z_\-./:?&=+#@%])`))
	}
	return out
}()

// invoiceKeywordHit 判断一段文本（通常是 subject + snippet）是否含发票语义。
func invoiceKeywordHit(text string) bool {
	t := strings.ToLower(text)
	for _, kw := range []string{
		// 中文关键词：不需要词边界（汉字无「词内含子词」）。
		"发票", "电子发票", "增值税", "开票", "票据", "收据",
		"账单", "对账单", "订单确认", "支付成功", "扣款",
	} {
		if strings.Contains(t, kw) {
			return true
		}
	}
	// 英文关键词：按预编译的词边界正则逐个匹配，见 invoiceKeywordASCII 的注释。
	hit := false
	for _, re := range invoiceKeywordASCIIRegexes {
		if re.MatchString(t) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	// 命中了英文词，再排除「含该词但语义无关」的短语（见上面负向短语的注释）。
	for _, re := range invoiceKeywordASCIINegPhrases {
		if re.MatchString(t) {
			return false
		}
	}
	return true
}

// InvoiceCandidate 判断邮件主题+摘要是否命中发票/账单关键词。
// 供自动提取链路决定是否值得读缓存正文做二次提取（正文 IO/解密较贵，只对候选做）。
func InvoiceCandidate(e Email) bool {
	return invoiceKeywordHit(e.Subject + "\n" + e.Snippet)
}

// reDebtNoticeShape 识别「债务通知」形态：信用卡/银行的对账单、还款提醒。
//
// 这类邮件在 invoiceKeywordHit 里必然放行——关键词表本来就含
// 「账单」「对账单」「扣款」「支付成功」。而它们**不是发票**：
// 2026-10-02 真实库实例（diag_real_invoice_extract_test.go 记录）：
//
//	inv_1790903383222583800_1  amount=58000.00  invoice_date=2026-10-25
//	主题=中国工商银行客户对账单(ICBC Peony Card Bank Statement)
//
// 58000 是原文里的**信用额度**（由 invoice.go 的「兜底取全文最大值」选中），
// 10-25 是**贷记卡到期还款日**。一笔根本没发生的 5.8 万元支出进了台账。
var reDebtNoticeShape = regexp.MustCompile(`(?i)(对账单|账单周期|还款日|应还款|最低还款|信用额度|授信额度|信用卡|贷记卡|借记卡|account\s+statement|statement\s+of\s+account|billing\s+statement|credit\s*card|amount\s+due)`)

// reTaxNo 识别开票方税号。开票方必须披露税号，没有它基本可断定不是发票。
var reTaxNo = regexp.MustCompile(`(?i)(纳税人识别号|统一社会信用代码|销售方纳税人识别号|税\s*号|tax\s*id|VAT\s*(?:No|Number))`)

// admitDebtNotice 判断一封「债务通知形态」的邮件是否仍应进发票台账。
//
// 规则：债务通知必须**另外**带至少一个真实发票语义信号——
// 发票号、税号、或发票类附件——否则不建档。
//
// ## 为什么附件算一个信号
//
// 真实账单邮件的形态是「主题写月度对账单、金额只印在附件 PDF 里」
// （见 ExtractInvoiceLoose 的注释）。那种邮件正文里本来就没有发票号，
// 但它也不是发票；反过来，若它确实带着**发票类**附件（PDF/图片/XML），
// 说明对方是当凭证发的，仍应建档交给采集器。判据保持与
// ExtractInvoiceLoose 的 hasInvoiceAttachment 同源。
//
// ## 风险已用真实数据量化
//
// 收紧前准入门放行 7 封、最终建档 2 封（1 真 + 1 幽灵）。被放行但在
// invoice.go 门槛处丢弃的 5 封逐个复核（2026-10-02）：
//
//	Xiaomi MiMo API 开放平台扣款成功通知   交易通知    非发票
//	所需操作：AWS 账户提示                 发票词      非发票（操作提醒）
//	Amazon Web Services Account Alert     对账单词    非发票（告警）
//	AWS 账户提醒                           对账单词    非发票（提醒）
//	来自 Apple 西湖商务团队的问候          发票词      非发票（商务拓展信）
//
// 5 封里 **0 封是真发票**，收紧的误杀在当前真实数据上为 0。
func admitDebtNotice(joined string, hasInvoiceAttachment bool) bool {
	if !reDebtNoticeShape.MatchString(joined) {
		return true // 不是债务通知，行为不变
	}
	// 带真实发票语义才放行。
	return hasInvoiceAttachment || reInvoiceNo.MatchString(joined) || reTaxNo.MatchString(joined)
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
//
// ## 为什么必须跳过「未来的日期」
//
// 2026-10-02 真实库实测（diag_real_invoice_extract_test.go 记录的那条）：
// 工商银行信用卡对账单被抽成发票，invoice_date 落成 **2026-10-25**——
// 那是原文里的「贷记卡到期还款日」，比当天晚 23 天。同一封邮件里其实带着
// 正确的「对账单生成日 2026年09月30日」，但 `reLooseCNDate` 只取**第一个**
// 中文年月日，于是先撞上了还款日。
//
// 发票不可能在未来开具，「到期还款日」是债务通知的字段而不是凭证的日期。
// 这一点与「对账单要不要算进台账」那个产品决定无关：无论算不算，
// 未来日期都不是开票日期。所以这里直接跳过它并继续往后找。
//
// 留 1 天宽限：服务器时区与邮件出具地可能跨日，临界日不至于被误杀。
func ParseInvoiceDate(text string) string { return parseInvoiceDateAt(text, time.Now()) }

func parseInvoiceDateAt(text string, now time.Time) string {
	// 三层优先级，每层都跳过未来日期并继续往后找：
	//   1) 发票标签（开票日期 / 发票日期 / 日期 / Date）
	//   2) 账单出具日（对账单生成日 / 账单日期 / 出账日 …）
	//   3) 裸中文年月日
	// 顺序是承重的：真发票邮件有「开票日期」就该以它为准；对账单没有「开票日期」，
	// 只能落到第 2 层，否则会取到「账单周期」的起始日或「到期还款日」。
	for _, re := range []*regexp.Regexp{reInvoiceDate, reStatementDate, reLooseCNDate} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if d := normalizeInvoiceDate(m[1]); d != "" && !isFutureInvoiceDate(d, now) {
				return d
			}
		}
	}
	return ""
}

// isFutureInvoiceDate 判断 YYYY-MM-DD 是否比 now 晚了超过 1 天。
func isFutureInvoiceDate(d string, now time.Time) bool {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return false // 解析不了就当它不是未来日期，交给调用方按原样处理
	}
	cut := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, 2)
	return t.After(cut)
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
	// 债务通知形态（信用卡/银行对账单、还款提醒）额外要求真实发票语义。
	// 详见 admitDebtNotice 的注释——那是 2026-10-02 真实库里那笔
	// 「amount=58000（其实是信用额度）」幽灵发票的根因。
	if !admitDebtNotice(joined, hasInvoiceAttachment) {
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
	if loc := reSeller.FindStringSubmatchIndex(joined); loc != nil {
		// loc[2]:loc[3] 是捕获组，即对方单位的候选值。
		//
		// reSeller 只保证「标签后 1–6 个词」，不保证那些词不是**另一个标签**
		// （月度批量开票邮件的表格拍平后，「销售方」后面紧跟的就是「发票抬头」
		// 这个列头）。先按 cleanSellerValue 收敛；收敛不出单位名再往下一行找
		// ——表头行形态下真正的单位名在下一行。两条都拿不到才落到主题/发件人兜底。
		if s, ok := cleanSellerValue(joined[loc[2]:loc[3]]); ok {
			inv.Seller = s
		} else {
			inv.Seller = sellerFromFollowingLines(joined, loc[1])
		}
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
		// 打标：这是**路由痕迹**（邮件经过了谁的服务器），不是开票方。
		// 发票 XML 里的 SellerName 是权威值，必须能覆盖它——
		// 见 SellerIsFallback 注释与 mergeXMLFields。
		inv.sellerIsFallback = inv.Seller != ""
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
