package email

// body_invoice_link_test.go 的实现文件：bodyHasInvoiceLink（q3）。
//
// 「正文里的发票下载链接」也要算附件（需求 7 的 📎 标记）。这不是拍脑袋：
// 电子发票邮件（尤其阿里云/腾讯云/电子发票平台）大量把发票放在**下载链接**里，
// 邮件本身**没有**任何 MIME 附件。附件判定只看 BODYSTRUCTURE 时，这些邮件
// 的 has_attachments 恒为 false，📎 永不亮 —— 而它们恰恰是最该被一眼看见的那类。
//
// ## 为什么必须扫原始 MIME 而不是 snippet
//
// 见 fetcher.go 里 fetchSnippetOnConnected 的函数头：DeriveSnippet 会对 HTML
// 正文调 htmlToText，后者把标签整个删掉，href 里的 URL 随之消失。
// 实测（2026-10-04）：`<a href="https://inv.example.com/download/abc123.pdf">
// 点击下载发票</a>` 经 DeriveSnippet 后是 "点击下载发票"，URL 一个不剩。
//
// ## 为什么不用「有链接就算」
//
// extractInvoiceURLs 收集的是**所有**非跳过链接：营销邮件的「了解更多」
// （example.com/campaign?utm_source=...）也会命中。若据此置 has_attachments，
// 每一封带推广链接的邮件都会亮 📎，标记就失去意义。
//
// 所以这里用 scoreInvoiceURL 打分并要求**超过阈值**：口径与 Harvest 阶段
// 真正会去尝试下载的判据保持一致 —— 不是「看起来像附件」，而是「采集器会
// 把它当发票链接去抓」。

import "strings"

// invoiceLinkScoreThreshold 是「多个弱特征叠加」这条路径的分数线。
//
// scoreInvoiceURL 对「URL 以 .pdf 结尾」给 20 分，对每个命中的弱 hint 各给
// 10 分。20 分于是代表「.pdf 后缀」或「两个弱特征」——单靠一个泛化词
// （任何 URL 里的 "fp"）不够。
//
// 专属性词走 hasStrongInvoiceHint 那条路，不看分数（理由见下面 strongInvoiceHints）。
const invoiceLinkScoreThreshold = 20

// strongInvoiceHints 是「单独命中就足以认定这是发票链接」的完整词。
//
// ## 为什么不直接用 scoreInvoiceURL 的分数
//
// 最初的实现是 `scoreInvoiceURL(u) >= 20`。实测（2026-10-04）它会误杀两个
// **真实**的电子发票平台：
//
//	https://fapiao.example.cn/detail?id=7788   score=10
//	https://etax.example.gov.cn/print/556677   score=10
//
// 原因是 scoreInvoiceURL 对**每个**命中的 hint 一律给 10 分，不区分
// 「命中完整词」与「命中子串」：
//
//	inv 是子串 → fapiao / etax / invoice / inviter 全都只值 10
//
// 也就是说分数这个维度**不携带**「专属性」信息。要区分它们，得看命中的是
// 不是完整词（路径段或主机名的一部分），而不是看命中了几个。
//
// ## 为什么不把阈值降到 10
//
// 那会让 `inviter` / `invite` / `inventory` 这类含 "inv" 的普通链接全部
// 判成发票链接 —— 每一封带推广链接的营销邮件都会亮 📎，标记失去意义。
// 实测（2026-10-04）这就是降阈值后的样子：`https://example.com/inviter/join`
// score=10，与 fapiao 平台同分。
//
// ## 阈值仍然保留
//
// 下面 bodyHasInvoiceLink 是「OR 关系」：命中任一强特征词，**或**分数达标。
// 分数那条留给「多个弱特征叠加」与「.pdf 后缀」——它们不依赖专属性词。
var strongInvoiceHints = []string{"invoice", "fapiao", "etax", "fapiaoquery", "invoicecenter"}

// bodyHasInvoiceLink 判断一封邮件的**原始 MIME 字节**里有没有发票下载链接。
//
// 参数是 BODY[TEXT] 的原始字节，不是 snippet —— 见文件头。
func bodyHasInvoiceLink(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	// extractInvoiceURLs 走的是与 Harvest 阶段同一套正则与跳过规则，
	// 判据与执行方共用一份，不会出现「标记说有链接、采集器却没去抓」的不一致。
	for _, u := range extractInvoiceURLs(string(raw)) {
		if hasStrongInvoiceHint(u) || scoreInvoiceURL(u) >= invoiceLinkScoreThreshold {
			return true
		}
	}
	return false
}

// hasStrongInvoiceHint 判断 URL 是否命中一个**专属性**的发票平台词。
//
// 判据用「路径段 / 主机名里出现完整词」，而不是子串包含 —— 这正是
// strongInvoiceHints 与 invoiceLinkHints 的分工：后者是「排序用的弱特征」，
// 前者是「认定用的强特征」。
func hasStrongInvoiceHint(u string) bool {
	lu := strings.ToLower(u)
	for _, h := range strongInvoiceHints {
		if containsWholeWord(lu, h) {
			return true
		}
	}
	return false
}

// containsWholeWord 判断 h 是否作为「完整词」出现在 s 中。
//
// 词边界取 URL 里真实存在的分隔符：/ . - _ ? & = 与数字。这样
// fapiao.example.cn、/invoice/2026、invoice_id=8 都算命中，而
// inviter、inventory、invite 不算（"inv" 是 "inviter" 的前缀，不是独立词）。
func containsWholeWord(s, h string) bool {
	off := 0
	for {
		i := strings.Index(s[off:], h)
		if i < 0 {
			return false
		}
		start := off + i
		end := start + len(h)
		// 词边界的正确判据：h 前面的字符**不是**词内字符，且 h 后面的字符
		// **不是**词内字符。前面写成 isURLWordByte 是反的 —— 那等于要求
		// h 前面必须紧挨着一个字母数字（inviter 里的 inv 恰好像），导致
		// fapiao.example.cn（前面是 /）这类真平台一个都命中不了。
		if (start == 0 || !isURLWordByte(s[start-1])) &&
			(end == len(s) || !isURLWordByte(s[end])) {
			return true
		}
		off = start + 1
		if off >= len(s) {
			return false
		}
	}
}

// isURLWordByte 判断 c 是否是「词内字符」——即出现在里面说明它与相邻的
// 字母数字连成一体（inviter 里的 inv），而不是独立的一段。
func isURLWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return false
	}
}

// bodyHasInvoiceLinkFor 供测试用：把判据按「邮箱正文里出现这个词」的形式
// 暴露出来，避免测试去构造完整 MIME（那样测的是 MIME 解析器，不是本判据）。
//
// 只在 _test.go 里通过 var 引用，不导出。
var bodyHasInvoiceLinkFor = func(fragment string) bool {
	return bodyHasInvoiceLink([]byte(fragment))
}

// invoiceLinkScoreForTest 暴露打分函数给测试断言阈值口径。
var invoiceLinkScoreForTest = scoreInvoiceURL
