package email

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf8"
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

	// 1) 正常情况：整封 MIME 能解析出正文。
	//
	//    BODY[TEXT] 对 multipart 邮件返回的是**正文部分**，它总是以一行
	//    MIME boundary 打头（`------=_Part_…`）。那行没有冒号，
	//    mail.ReadMessage 会直接报 "malformed header line"，于是本来
	//    完全可解析的 text/html + quoted-printable 邮件被判成「解析失败」，
	//    摘要退化成空串 —— 2026-10-02 模拟器上 120 封里 83 封摘要显示成
	//    原始 MIME 就是这么来的。所以这里多试一个候选：剥掉开头的
	//    boundary 行再解析一次。只在剥完真的能解析时才采用，误伤不了
	//    真正的纯文本正文。
	for _, cand := range mimeCandidates(raw) {
		msg, err := ParseMIMEMessage(cand)
		if err != nil {
			continue
		}
		if t := normalizeWhitespace(msg.TextBody); t != "" {
			return truncateRunes(t, maxRunes)
		}
		if h := htmlToText(msg.HTMLBody); h != "" {
			return truncateRunes(h, maxRunes)
		}
		// 解析成功但没有正文：常见于只拉到了 header 的 <partial> fetch。
		// 下面再试一次「纯文本」路径，但**不**再退回原始字节。
		break
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
		// 纯文本：确认它不是 MIME 源码再当正文用。
		//
		// MIME 判定用的是**保留换行的原始文本**而不是上面压平过的 s ——
		// 判据里的 ^Content-Type: / ^--boundary 都是逐行正则，压平之后
		// 整段变成一行，行首锚点全部失效，判据形同虚设。
		//
		// 判据成立（即确实不是 MIME 源码）之后，还有两次整体解码尝试。
		// 两者都针对同一个现实：部分服务器的 <partial> 从**正文**起取，
		// 头被整个切掉，于是既没有 Content-Transfer-Encoding 也没有
		// Content-Type，MIME 判据一条都不命中，编码原文会被直接放行。
		// 2026-10-02 模拟器上两种都真实出现过：
		//   · base64：`PHN0eWxlPgogICAgLmVsbC13IHsK…`
		//   · quoted-printable：`=20 =E7=94=A8 ChatGPT Images=EF=BC=8C…`
		// 解得开且解出来像文本就用它（等于把正文找回来了），解不开才落到
		// 纯文本分支。
		//
		// 顺序不能反：解码必须放在 MIME 判据**之后**。整段 MIME 里同样含
		// 大量 QP 转义，先解码的话会把 Content-* 头和 boundary 一起「解」
		// 出来，输出变成「boundary + 头 + 正文」的混合体 —— 那比原来的
		// 整段转储更难读。
		//
		// 两个整体解码都喂**原文**而不是压平过的 s：压平会把 QP 的软换行
		// `=\n` 变成 `= `，那是非法转义，QP reader 直接报错。
		if !looksLikeMIMEStructure(string(raw)) {
			if dec, ok := decodeWholeQuotedPrintable(string(raw)); ok {
				if t := normalizeWhitespace(dec); t != "" {
					return truncateRunes(t, maxRunes)
				}
			}
			if dec, ok := decodeWholeBase64(s); ok {
				if h := htmlToText(string(dec)); h != "" {
					return truncateRunes(h, maxRunes)
				}
				if t := normalizeWhitespace(string(dec)); t != "" {
					return truncateRunes(t, maxRunes)
				}
			}
			return truncateRunes(stripTrailingBoundary(s), maxRunes)
		}
	}

	// 3) 到这里说明内容确实是 MIME 源码（只拉到 header、或解析失败）。
	//    宁可返回空串，也不把 Content-Type / boundary 转储给用户看 ——
	//    「退回原文」正是这个缺陷本身。
	return ""
}

// mimeCandidates 给出解析 MIME 时的候选输入：原样，以及「剥掉开头一行
// MIME boundary」之后的版本。
//
// BODY[TEXT] 对 multipart 邮件返回的是正文部分，首行恒为 boundary
// （`------=_Part_…` / `--_000_…`）。boundary 行没有冒号，
// mail.ReadMessage 会在第一行就报 malformed header line，于是后面
// 明明是完整可解析的 Content-Type/Content-Transfer-Encoding + 正文
// 全都读不到，摘要只剩空串。
//
// 判据刻意保守：只有首行以 `--` 开头**且**不含冒号才剥。正文第一行
// 恰好是 `--` 开头的话，剥完 ParseMIMEMessage 会失败，候选自动作废。
func mimeCandidates(raw []byte) [][]byte {
	out := [][]byte{raw}
	rest, ok := dropLeadingBoundaryLine(raw)
	if ok {
		out = append(out, rest)
	}
	return out
}

func dropLeadingBoundaryLine(raw []byte) ([]byte, bool) {
	s := string(raw)
	nl := strings.IndexAny(s, "\r\n")
	var first, rest string
	if nl < 0 {
		first, rest = s, ""
	} else {
		first = s[:nl]
		rest = s[nl+1:]
		if strings.HasPrefix(rest, "\n") {
			rest = rest[1:]
		}
	}
	first = strings.TrimRight(first, "\r")
	if !strings.HasPrefix(first, "--") || strings.Contains(first, ":") || rest == "" {
		return nil, false
	}
	return []byte(rest), true
}

// decodeWholeQuotedPrintable 尝试把整段文本当作 quoted-printable 载荷解开。
//
// 与 decodeWholeBase64 同一场景：<partial> 从正文起取，头被切掉，
// 剩下的仍是 QP 编码的字节。没有 Content-Transfer-Encoding 可依据，
// looksLikeMIMEStructure 判不出来，QP 原文会被当正文透给用户。
//
// **容错**解码，不走 mime/quotedprintable.Reader：真实邮件的 QP 正文里
// 常常混着 HTML 实体（`&zwnj;`）和被实体化过的软换行（`&=` + 空格），
// 标准 reader 遇到 `&=` 就报 invalid escape 整段放弃，于是
// decodeWholeQuotedPrintable 返回 false、摘要退化成空串、旧值被保留，
// 脏数据永远修不掉。2026-10-02 真库实测正是这一类。
// 这里只把 `=XX` 换成对应字节、把 `=` + 换行当软换行删掉，其余原样保留。
func decodeWholeQuotedPrintable(s string) (string, bool) {
	if len(s) < 12 {
		return "", false
	}
	body := strings.ReplaceAll(s, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	var out strings.Builder
	out.Grow(len(body))
	escapes := 0
	for i := 0; i < len(body); {
		if body[i] != '=' {
			out.WriteByte(body[i])
			i++
			continue
		}
		// 软换行：`=` 后紧跟换行
		if i+1 < len(body) && body[i+1] == '\n' {
			escapes++
			i += 2
			continue
		}
		// 转义：`=` + 两个十六进制
		if i+2 < len(body) {
			if v, ok := hexPair(body[i+1], body[i+2]); ok {
				out.WriteByte(v)
				escapes++
				i += 3
				continue
			}
		}
		out.WriteByte('=')
		i++
	}
	if escapes < 3 {
		return "", false
	}
	dec := out.String()
	if !utf8.ValidString(dec) || !looksLikeReadableText([]byte(dec)) {
		return "", false
	}
	return dec, true
}

func hexPair(a, b byte) (byte, bool) {
	hi, ok1 := hexVal(a)
	lo, ok2 := hexVal(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return hi<<4 | lo, true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// stripTrailingBoundary 去掉粘在正文末尾的 MIME 结束边界。
//
// BODY[TEXT] 的最后一个 part 后面会跟一行 `------=_Part_xxx…--`，
// 压平空白后它和正文连成同一句，用户会看到
// 「极客时间 点击这里取消订阅 ------=_Part_172449_2115296577.1789970890196--」。
// 只削末尾那个独立 token，前面必须是行首或空白，且 token 至少 4 个连字符，
// 这样「见附件 --」这种正常收尾不会被误伤。
func stripTrailingBoundary(s string) string {
	return reBoundaryTail.ReplaceAllString(s, "")
}

// decodeWholeBase64 尝试把整段文本当作一个 base64 载荷解开。
//
// 只在「解得开 + 解出来是像样的 UTF-8 文本」时才算成功，避免把
// 「ABCDEF」这种恰好由 base64 字符组成的正常短句误伤。判据：
//
//	· 去掉所有空白后长度 ≥ 32 且是 4 的倍数（太短的一律不试）；
//	· 全部字符落在 base64 字母表内；
//	· 解出来必须是合法 UTF-8，且含有「读起来像正文」的信号
//	  （HTML 标签、中文、或至少一个非字母数字的可见字符）。
func decodeWholeBase64(s string) ([]byte, bool) {
	// 门槛一：原文里出现中日韩文字就一定不是 base64 载荷，直接不试。
	// 这条同时挡住了「把正常中文正文误当编码解开」的风险。
	if len(s) < 40 {
		return nil, false
	}
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return nil, false
		}
	}
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
	if len(compact) < 32 || len(compact)%4 != 0 {
		return nil, false
	}
	for _, r := range compact {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			r == '+' || r == '/' || r == '=') {
			return nil, false
		}
	}
	dec, err := base64.StdEncoding.DecodeString(compact)
	if err != nil || len(dec) == 0 {
		return nil, false
	}
	if !utf8.Valid(dec) {
		return nil, false
	}
	if !looksLikeReadableText(dec) {
		return nil, false
	}
	return dec, true
}

// looksLikeReadableText 判断一段字节解出来之后像不像「人读的正文」：
// 有 HTML 标签、有中日韩文字，或者至少有一个非字母数字的可见字符。
// 纯随机二进制解码出来的结果通常一条都不满足。
func looksLikeReadableText(b []byte) bool {
	if reTag.Match(b) {
		return true
	}
	s := string(b)
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			continue
		}
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return true
		}
	}
	return false
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
	// RFC 5322 的 field-name：可打印 ASCII，去掉冒号，且不能以空格开头。
	reMIMEFieldName = regexp.MustCompile(`^[!-9;-~]+:[ \t]`)
	// quoted-printable 的 =XX 转义。`=?utf-8?B?` 这类 MIME 编码字里的 `=?`
	// 和 `=ut` 都不匹配，天然被排除。
	reQPEscape = regexp.MustCompile(`=[0-9A-Fa-f]{2}`)
	// 粘在正文末尾的 MIME 结束边界（`------=_Part_…--`）。要求前面是行首或
	// 空白、至少 4 个连字符，避免误伤「见附件 --」这种正常收尾。
	reBoundaryTail = regexp.MustCompile(`(?:^|\s)-{4,}[A-Za-z0-9=+._/-]*-{2,}\s*$`)
)

// looksLikeMIME 只看第一行是否就是一个邮件头（最典型的整封原文特征）。
//
// 判据必须是「**头字段名** + 冒号」，不能是「含冒号」。原实现是
// strings.Contains(head, ":")，而 DeriveSnippet 里传进来的文本已经被
// normalizeWhitespace 压成了一行，于是「会议改到下午三点：记得带季报」
// 这种正常中文正文、乃至任何一句带冒号的英文句子，都会被判成 MIME 源码
// ——摘要直接返回空串。列表里大量邮件没有摘要，根因就在这里。
func looksLikeMIME(s string) bool {
	head := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		head = s[:i]
	}
	head = strings.TrimRight(head, "\r")
	return reMIMEFieldName.MatchString(head)
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
