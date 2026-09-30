package email

import (
	"regexp"
	"strings"
)

// DeriveSnippet 从 IMAP 抓回来的原始字节里取出「人能读的一行摘要」。
//
// ## 为什么需要它
//
// 2026-10-01 真机审计在 /notifications 上抓到：source=email 的通知正文是
// 整段原始 MIME。实测样本（真机 DOM 里读出来的 textContent）：
//
//	--part_8057f3aacb3e5508e18aca6f084c07d2ff86ddb6114108806b53e4b7f3822e6a
//	Content-...
//
// 另一批则是「您的额度即将用尽…<br/>充值链接：<a href='…'>…</a>」——
// 字面的 HTML 标签。
//
// 根因是 IMAP 路径直接 `snippet = string(bs.Bytes)` 再 `snippet[:500]`：
// 既不解析 MIME，也不剥 HTML。通知侧 `Body: e.Snippet` 于是把原始 MIME
// 原样透出到用户界面。
//
// ## 三件事一起修
//
//  1. MIME 解析优先（复用 ParseMIMEMessage，text/plain 优先）。
//  2. 只有 HTML 时剥标签 —— 标签、实体、脚本样式一并去掉，
//     发票链接之类的 href 文本会被保留（<a href=X>Y</a> → Y）。
//  3. **按 rune 截断而不是按字节**。原来的 snippet[:500] 是字节切片，
//     会在多字节字符中间劈开，产生用户可见的乱码（U+FFFD）。
//
// 任何一步都失败时返回空串——宁可摘要为空，也不要把 MIME 头转储给用户看。
// ��里没有"退回原文"这条兜底，那正是这个缺陷本身。
func DeriveSnippet(raw []byte, maxRunes int) string {
	if len(raw) == 0 {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = 500
	}

	// 1) 正常情况：整封 MIME 能解析出正文
	if msg, err := ParseMIMEMessage(raw); err == nil {
		if t := normalizeWhitespace(msg.TextBody); t != "" {
			return truncateRunes(t, maxRunes)
		}
		if h := htmlToText(msg.HTMLBody); h != "" {
			return truncateRunes(h, maxRunes)
		}
		// 解析成功但没有正文：常见于只拉到了 header 的 <partial> fetch。
		// 下面再试一次「纯文本」路径，但**不**再退回原始字节。
	}

	// 2) 抓回来的不是完整 MIME（部分服务器 BODY[TEXT] 直接给正文），
	//    或者只有一段裸 HTML。这时**先判断是不是 HTML**，别让它原样透出。
	if s := normalizeWhitespace(string(raw)); s != "" {
		if looksLikeHTML(s) {
			if h := htmlToText(s); h != "" {
				return truncateRunes(h, maxRunes)
			}
			return ""
		}
		// 纯文本：确认它不是 MIME 源码再当正文用
		if !looksLikeMIMEStructure(s) {
			return truncateRunes(s, maxRunes)
		}
	}

	// 3) 到这里说明内容确实是 MIME 源码（只拉到 header、或解析失败）。
	//    宁可返回空串，也不把 Content-Type / boundary 转储给用户看 ——
	//    「退回原文」正是这个缺陷本身。
	return ""
}

// looksLikeHTML 粗判一段文本是不是 HTML：出现成对尖括号标签即算。
func looksLikeHTML(s string) bool {
	return reTag.MatchString(s)
}

var (
	// 整块 script/style 的内容连标签一起删。
	// 注意：Go 用 RE2，不支持反向引用，所以 script 与 style 各写一条。
	reScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	reStyle  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	// 块级标签换成换行/空格，避免 <p>a</p><p>b</p> 粘成 "ab"
	reBreakTag = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/tr|/li|/h[1-6]|/table)\s*/?\s*>`)
	// 其余标签直接去掉，保留标签之间的文字
	reTag = regexp.MustCompile(`(?s)<[^>]*>`)
	// 常见实体；顺序要紧：&amp; 必须最后解，否则 &amp;lt; 会被二次解码
	entityReplacer = strings.NewReplacer(
		"&nbsp;", " ", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'",
		"&amp;", "&",
	)
	// 「头字段行」与「MIME 边界行」——出现这些就说明这是 MIME 源码不是正文
	reMIMEHeaderLine = regexp.MustCompile(`(?m)^(Content-Type|Content-Transfer-Encoding|Content-Disposition|MIME-Version|Content-ID)\s*:`)
	reBoundaryLine   = regexp.MustCompile(`(?m)^--[^\s-].*$`)
)

// looksLikeMIME 只看第一行是否就是一个邮件头（最典型的整封原文特征）。
func looksLikeMIME(s string) bool {
	head := s
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < 200 {
		head = s[:i]
	}
	return strings.Contains(head, ":")
}

// looksLikeMIMEStructure 在 looksLikeMIME 之外还认正文中间的 MIME 结构行。
func looksLikeMIMEStructure(s string) bool {
	return looksLikeMIME(s) || reMIMEHeaderLine.MatchString(s) || reBoundaryLine.MatchString(s)
}

// htmlToText 把 HTML 正文压成纯文本：去标签、解实体、压空白。
func htmlToText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reBreakTag.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = entityReplacer.Replace(s)
	return normalizeWhitespace(s)
}

// normalizeWhitespace 压掉多余空白但保留换行语义（换行仍归一成单空格，
// 摘要是一行，不需要保留原文段落结构）。
func normalizeWhitespace(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// 截断直接复用 invoice_pdf.go 里的 truncateRunes（同样按 rune，不劈开多字节字符）。
// 原实现是字节切片 snippet[:500]，中文邮件会在第 500 字节处产生半个字符。
