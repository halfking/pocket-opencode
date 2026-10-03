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
// 这里没有"退回原文"这条兜底，那正是这个缺陷本身。
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
		// 两条正文都要过 containsMIMESource。
		//
		// 判据跑在**未压平**的 TextBody / HTMLBody 上，不是压平后的 t / h。
		//
		// 这一点是本轮踩过的坑，代价是三条「不许泄漏」的护栏全绿而函数
		// 在实泄：containsMIMESource 的行锚点判据（^Content-Type: / ^--…）
		// 逐行匹配，压平之后整段变成一行，锚点全部失效；而压平后的那份
		// 恰恰是**真正会被返回给用户**的字符串。判据必须跑在它成立的那个
		// 输入上——顺带 token 判据（reMIMEHeaderToken / reBoundaryToken）
		// 与位置无关，压平后仍然有效，两者缺一不可。
		if t := normalizeWhitespace(msg.TextBody); t != "" && !containsMIMESource(msg.TextBody) {
			return truncateRunes(stripTrailingBoundary(t), maxRunes)
		}
		if h := htmlToText(msg.HTMLBody); h != "" && !containsMIMESource(msg.HTMLBody) {
			return truncateRunes(h, maxRunes)
		}
		// 解析成功但正文本身就是 MIME 源码（TextBody 聚合进了内层报文原文），
		// 或者压根没有正文（只拉到 header 的 <partial> fetch）。
		// 两种都**不能**退回原始字节——那正是 2026-10-03 真机上
		// 邮件列表 5/5 封 snippet 直接显示 `------=_Part_… Content-Type: …`
		// 的成因。继续往下走第 2 步的纯文本/HTML 判定。
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
			// ⚠️ 这里**不能**无条件退回原文。looksLikeMIMEStructure 用的是逐行锚点
			// （^Content-Type: / ^--boundary），压平之后整段变成一行、锚点全部失效
			// —— 本文件上方注释已经写明「判据形同虚设」。真实语料上确实漏判：
			// 2026-10-03 真机（Redmi 2411DRN47C）em-10462-acct-…-2 的 snippet
			// 就是这样进来的（boundary + Content-* 头 + 未解码的 quoted-printable
			// 正文，压平成一行），实测 looksLikeMIMEStructure=false 而
			// containsMIMESource=true。
			//
			// 而 SnippetFromParsed 的文档（见本文件下方）点名「DeriveSnippet 里已经
			// 写明『这里没有退回原文这条兜底』」—— 那句话在修之前是**不成立**的，
			// 这一行就是那条兜底。
			//
			// 判据要**先剥尾部 boundary 再判**：正常正文后面跟着一行 boundary
			// （`snippet_partial_fetch_test.go` 的 "text-with-trailing-boundary" 用例）
			// 是合法形态，剥掉之后剩下的就是干净正文，不该被一起拒掉。
			// 判保守方向：正文里恰好提到 "Content-Type:" 的邮件会从「显示原文」变成
			// 「显示空」。空是安全的一侧，泄漏不是。
			trimmed := stripTrailingBoundary(s)
			if !containsMIMESource(trimmed) {
				return truncateRunes(trimmed, maxRunes)
			}
		}
	}

	// 3) 到这里说明内容确实是 MIME 源码（只拉到 header、或解析失败）。
	//    宁可返回空串，也不把 Content-Type / boundary 转储给用户看 ——
	//    「退回原文」正是这个缺陷本身。
	return ""
}

// SnippetFromParsed 从已解析的邮件里取一行可展示的摘要。
//
// ## 为什么要单独一个函数
//
// 2026-10-03 真机实测（Redmi 2411DRN47C / Android 14）：邮件列表的
// snippet 直接显示 MIME 源码，5/5 封全中，开头就是
//
//	------=_Part_8505717_93977514.1790821420306
//	Content-Type: text/html; charset=utf-8
//
// 根因不在解析器，而在出口：fetcher.go 当时写的是
//
//	em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)
//
// TextBody 是**所有 text/plain 部件的聚合**（mime.go 里 `out.TextBody += body`）。
// 对 `multipart/mixed` 里嵌一整封内层报文原文的形态（企业网关转发的常见形态），
// 被聚合进来的就是那封内层报文——boundary 行和 Content-* 头一起进了摘要。
//
// DeriveSnippet 里已经写明「这里没有『退回原文』这条兜底，那正是这个缺陷本身」，
// 但它只用在 HTMLBody 的回退分支上，主路径绕过了它。本函数把那条不变量
// 收到**所有**取摘要的出口都必须经过的地方。
//
// ## 判据为什么跑在未压平的原文上
//
// 第一版这里写的是
//
//	if t := normalizeWhitespace(msg.TextBody); t != "" && !looksLikeMIMEStructure(t)
//
// 两处都错，且互相掩护：
//
//	· looksLikeMIMEStructure 的行锚点在压平后全部失效（t 变成一行）；
//	· 就算锚点没失效，t 才是**真正被返回**的那份字符串，判据跑在别的
//	  字符串上，绿灯不指向用户看到的东西。
//
// 结果是本函数对着实泄的输入返回 PASS，护栏 3/3 全绿。修法见
// containsMIMESource 的注释。
func SnippetFromParsed(msg *ParsedMessage, maxRunes int) string {
	if msg == nil {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = 500
	}
	// text/plain 优先（已解码，无需再剥标签）。
	if t := normalizeWhitespace(msg.TextBody); t != "" && !containsMIMESource(msg.TextBody) {
		return truncateRunes(stripTrailingBoundary(t), maxRunes)
	}
	// 只有 HTML 时剥标签。判据同样跑在未压平的 HTMLBody 上。
	if h := htmlToText(msg.HTMLBody); h != "" && !containsMIMESource(msg.HTMLBody) {
		return truncateRunes(h, maxRunes)
	}
	// 两条都不可用：宁可返回空串，也不把 MIME 源码转储给用户看。
	return ""
}

// containsMIMESource 判断一段文本里是否混进了 MIME 源码。
//
// ## 为什么不能只用 looksLikeMIMEStructure
//
// looksLikeMIMEStructure 的两条主力判据都是**逐行正则**（`^Content-Type: …`、
// `^--…`），只在**保留换行的原文**上成立。而摘要出口返回给用户的字符串
// 绝大多数已经被 normalizeWhitespace 压成一行——
//
//	------=_Part_8505717_… Content-Type: text/html; charset=utf-8 <html>…
//
// 压平之后行首锚点全部失效，判据形同虚设。2026-10-03 本轮实测：派生函数
// 明明在整段转储 MIME 源码，三条「不许泄漏」的护栏却全绿——检测器只会
// 找行首，于是对着一个单行字符串永远找不到 `^Content-Type:`。
//
// ## 两层判据，缺一不可
//
//  1. 行锚点判据（looksLikeMIMEStructure）：保留换行的原文上最准，保留；
//     它还能识别「开头第一行就是一个头字段」（looksLikeMIME）。
//  2. token 判据（reMIMEHeaderToken / reBoundaryToken）：与位置无关，
//     压平之后仍然有效。
//
// ## token 判据的误伤控制
//
// 边界 token 只认 `--` 后面紧跟 `=` / `_` / `-` 或 `part_` / `Part_` 的形态
// （`------=_Part_…`、`--_000_10f7b8d35f184af`、`--part_8057f3aacb…` 都是真机
// 实测形态）。不写成宽松的 `--[A-Za-z]{6,}`：那会把正文里的
// 「COVID-19--related」「见附件 --」一并判成 MIME 源码，摘要直接清空，
// 那是用一个缺陷换另一个缺陷。
//
// 头字段 token 允许大小写不敏感（QP 解码后可能残留大写形态），但要求
// 前面不是字母数字（`\b`），避免把「参见 MyContent-Type 规范」误判。
//
// 宁可误伤也不要漏：这条函数保护的是**用户可见界面**，
// 一次误判的代价是一封邮件没有摘要，一次漏判的代价是整段 MIME 源码
// 铺在列表页上。
func containsMIMESource(s string) bool {
	if s == "" {
		return false
	}
	return looksLikeMIMEStructure(s) ||
		reMIMEHeaderToken.MatchString(s) ||
		reBoundaryToken.MatchString(s)
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
	// 与位置无关的 token 判据：给「已被 normalizeWhitespace 压成一行」的
	// 摘要兜底。行锚点（reMIMEHeaderLine / reBoundaryLine）在压平后失效，
	// 而压平后的那份恰恰是要返回给用户的那一份。详见 containsMIMESource。
	//
	// 边界 token 刻意收紧：只认 `--` 后紧跟 `=` / `_` 或 `part_` / `Part_`。
	// 真机实测的三种形态都覆盖到了：------=_Part_… / --_000_10f7b8d35f184af /
	// --part_8057f3aacb3e5508e…；而「COVID-19--related」「见附件 --」不会误伤。
	//
	// **裸 `-` 已从字符类里去掉**（原为 `[=_-]`）。这不是推测，是全库 180 条
	// 真实摘要的实测（diag_boundary_tighten_candidates_test.go，带门控跑）：
	//
	//	现状 `--(?:[=_-]|[Pp]art[_-])`   命中 13 条
	//	本式 `--(?:[=_]|[Pp]art[_-])`    命中 11 条
	//
	// 少掉的那 2 条逐条核过都不是 MIME：工行对账单的
	// `---人民币(本位币)---`（@591）与 newsletter 的 22 连字符分割线（@353）。
	// 保留的 11 条**全部**是真 `------=_Part_…` boundary 泄漏，含
	// em-1669791317 的整段 MIME 源码（带 Content-Type/Content-Transfer-Encoding）。
	//
	// 关键：真机那三种形态在 `--` 后面分别是 `=` / `_` / `p`，**没有一个靠
	// 裸 `-`**。而裸 `-` 正是 `---` 分隔线的来源，且它命中的后果不是「多挡
	// 一点」——mime.go:645-648 会把整条正文丢弃，邮件列表显示空白（需求 7）。
	reBoundaryToken = regexp.MustCompile(`--(?:[=_]|[Pp]art[_-])`)
	// 头字段 token：`\b` 保证前面不是字母数字，避免「参见 MyContent-Type 规范」误判。
	reMIMEHeaderToken = regexp.MustCompile(`(?i)\b(?:Content-Type|Content-Transfer-Encoding|Content-Disposition|Content-ID|MIME-Version)\s*:`)
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
