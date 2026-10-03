package email

// snippet 的真实语料回归：QP 编码的 HTML 在 BACKFILL 的取件形态下不得泄漏源码。
//
// ## 这条样本的形态来自哪里
//
// 2026-10-04 真库实测（diag_real_fetch_snippet_stages_test.go，真 schema +
// 真 IMAP）。backfill 走 fetcher.go:519 fetchSnippetOnConnected，取的是
//
//	UID FETCH BODY[TEXT]   （PartSpecifierText，Peek，**不带头**）
//
// 于�� raw 是这样一段（实测 8399 字节）：
//
//	<!DOCTYPE html>=0D=0A<html lang=3D\"en\">=0D=0A  <head>=0D=0A    <meta charset=3D=  \"UTF-8\" />=0D=0A …
//
// 注意两个容易被当成「同一形态」的细节，它们其实不同：
//  1. **软换行被编码成了 `=0D=0A`**，不是裸的 `=\r\n`。QP 的软换行只出现在
//     行尾且以字面 `=` 开头；这里是正文内容里的 CRLF 被正常转义。
//  2. `charset=3D=  ` —— `=3D` 后面跟**两个空格**。这是被实体化/压平过的
//     软换行残留（原本是 `=\r\n` 之类），QP reader 遇到就报 invalid escape。
//
// ## 泄漏机制（改动前）
//
// DeriveSnippet 第 2 步的顺序是「先 looksLikeHTML，后整体 QP 解码」：
//
//	s = normalizeWhitespace(raw)          // 压平后的 QP 源码
//	if looksLikeHTML(s) { h := htmlToText(s) ... }   // ← 对 QP 源码剥标签
//	...
//	if !looksLikeMIMEStructure(raw) { decodeWholeQuotedPrintable(raw) }  // ← 永远到不了
//
// looksLikeHTML(s) 对 `<!DOCTYPE html>=0D=0A<html …` 判 true，于是
// htmlToText 剥掉标签，剩下**全是 `=0D=0A` 残留**；这坨东西里没有
// `Content-Type:` / `boundary=`，`!containsMIMESource(h)` 为真，于是被
// truncateRunes 到 500 runes 当摘要返回。真库 25 条脏 snippet 全部产出于此。
//
// ## 为什么夹具是程序化生成的
//
// 最初想直接把真库导出的原文钉成 testdata —— 那是 25 条**真实企业邮件**
// （含发票金额、开票日期、购方名称、百望的发票链接 token）。诊断期用它是合理的，
// 提交进仓库不是。所以这里按上面量到的形态**程序化合成**：
//   · 形态（QP 化的 UTF-8 HTML、`=0D=0A` 换行、`=XX` 与裸空格混排）来自实测
//   · 内容（公司名、金额、日期）全部是编造的占位符
//
// 这样判据对形态敏感、对具体内容不敏感，且不会把业务数据带进历史。
// ⇒ 这条护栏不需要任何 tsv / testdata 夹具文件，在 CI 上永远会跑。
//
// 负控：把 DeriveSnippet 里新增的那一腿（HTML 分支之前的整体 QP 解码）
// 短路掉，本文件必须转红。

import (
	"strconv"
	"strings"
	"testing"
)

// isUnsanitized 判断一段文本是不是「用户会看到编码源码」的形态。
//
// ## 它必须独立于被测代码
//
// 刻意**不**调用 containsMIMESource / reQPEscape / looksLikeReadableText ——
// 那些是被测代码自己的判据，拿它们验证被测代码等于自证。
//
// ## 为什么按**密度**判，不按「出现任意一个 =XX」
//
// 最初写成「出现任意一个高位 `=XX` 就算脏」，结果把
//
//	https://pis.baiwang.com/smkp-vue/previewInvoiceAllEle?param=994386497A3EC72F12692CBDDCC42
//
// 判成泄漏 —— 那是 URL 查询参数，一长串**连续**十六进制，外面没有任何转义邻居。
// 区别在形态：
//   · QP 残留  = 大量 `=XX` 散布，彼此只隔空格/换行/其它转义
//   · URL 参数 = 一长串连续十六进制，外面没有转义邻居
//
// 真库里就踩到过：一个 `=994` 命中宽判据，害我差点把一条**本来就干净**的
// 发票通知当成泄漏报上去。所以判据要求「短窗口内多个高位 `=XX`」。
const diagUnsanitizedWindow = 200
const diagUnsanitizedMinEscapes = 3

func isUnsanitized(s string) bool {
	if s == "" {
		return false
	}
	up := strings.ToUpper(s)
	if strings.Contains(up, "CONTENT-TYPE") || strings.Contains(up, "BOUNDARY=") ||
		strings.Contains(up, "MULTIPART/") {
		return true
	}
	for start := 0; start < len(s); start += diagUnsanitizedWindow / 2 {
		end := start + diagUnsanitizedWindow
		if end > len(s) {
			end = len(s)
		}
		seg := s[start:end]
		n := 0
		for i := 0; i+2 < len(seg); i++ {
			if seg[i] != '=' || !diagIsHex(seg[i+1]) || !diagIsHex(seg[i+2]) {
				continue
			}
			if v, err := strconv.ParseUint(seg[i+1:i+3], 16, 8); err == nil && v >= 0x80 {
				n++
			}
		}
		if n >= diagUnsanitizedMinEscapes {
			return true
		}
	}
	return false
}

func diagIsHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// headOf 取前 n 字节并把换行压成空格，供 t.Logf 打印单行摘要。
func headOf(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// qpEncodeBody 把纯文本按 quoted-printable 编码：**只**把 `=` 与所有高位字节
// 转成 =XX，CRLF 转成 =0D=0A。
//
// 刻意**不做**行宽断行，也不发 `=\r\n` 软换行 —— 实测样本里两者都不存在：
// 每一行的结束都是被转义过的 `=0D=0A`，`=` 软换行只出现在行尾且是字面 `=`。
// 加一个不存在的形态进去，护栏就会在「合成的形态 ≠ 实测形态」上空转。
// `<`、`"` 等 ASCII 原样保留（实测里它们也没被转义）。
func qpEncodeBody(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\r' && i+1 < len(s) && s[i+1] == '\n':
			b.WriteString("=0D=0A")
			i++
		case c == '\n':
			b.WriteString("=0D=0A")
		case c == '=':
			b.WriteString("=3D")
		case c >= 0x80:
			const hex = "0123456789ABCDEF"
			b.WriteByte('=')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// 合成正文：HTML 形态 + 大量空行（空行是 `=0D=0A` 连发的来源）+ **中文正文**。
//
// 中文不是装饰：QP 把高位字节编成 `=XX`，只有正文含非 ASCII 时输出里才会
// 出现 `=E4=B8=80` 这类高位转义串。判据 isUnsanitized 按「200 字符窗口内
// ≥3 个高位 =XX」的**密度**判定（这样才不把 URL 查询参数那种一长串连续
// 十六进制误判成泄漏），纯 ASCII 正文根本产生不出高位转义 —— 夹具用 ASCII
// 的话，护栏会在「输入压根没有污染」的空态上变绿。
//
// `charset=` 后面跟两个空格的位置，见 synthBodyText 的说明。
const synthHTMLBody = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset=  "UTF-8" />
  <title>占位标题</title>
</head>
<body>
<table>




<tr><td>占位单元格甲</td></tr>




<tr><td>占位单元格乙</td></tr>





</table>
</body>
</html>
`

// Body[TEXT] 形态的报文：没有 MIME 头，第一行就是 HTML。
//
// ⚠️ `charset=3D=  ` 这个形态**不能用原始 HTML 表达**，只能在编码之后插入。
// 它是**二次处理**的产物：QP 编码器在超长行尾插软换行 `=\r\n`，中间层把它
// 压平成 `= `（`=` 保留、换行变空格），于是编码结果里出现「已编码的 `=3D`
// 后面紧跟一个字面 `=` 和两个空格」。QP 规范不允许字面 `=`，所以任何单次
// 编码器都生成不出它 —— 想用原始形态表达就一定会变成 `=3D3D=  `，形态失真，
// 护栏随之在自造的输入上空转。
//
// 同理不该加行宽断行：实测样本里没有 `=\r\n` 软换行，每行都结束于被转义的
// `=0D=0A`。
func synthBodyText() []byte {
	enc := qpEncodeBody(synthHTMLBody)
	// 实测原文：`…<meta charset=3D=  "UTF-8" />…`（两个空格）
	return []byte(strings.Replace(enc, `charset=3D`, `charset=3D=  `, 1))
}

// 主护栏：DeriveSnippet 在 BODY[TEXT] 形态下必须给出可读正文，不能吐 QP 源码。
func TestSnippetDerive_BodyTextQPHTMLIsNotReturned(t *testing.T) {
	raw := synthBodyText()
	got := DeriveSnippet(raw, 500)

	if got == "" {
		// 空串是安全的一侧，但这里必须给出正文：QP 能解开且解出来是 HTML，
		// 空串就说明新加的那一腿没走到（或者又被 decodeWholeQuotedPrintable
		// 的 MIME 结构闸拦下了 —— 那样是另一个缺陷，不该混在这条里）。
		t.Fatalf("DeriveSnippet 对可解的 QP HTML 返回空串（%d 字节输入）——"+
			"要么新腿没走到，要么被结构闸误拦。输入前 120 字节：%q",
			len(raw), headOf(string(raw), 120))
	}
	if isUnsanitized(got) {
		t.Fatalf("DeriveSnippet 吐出了 QP 源码（%d 字符）：\n%s",
			len([]rune(got)), clipRunes(got, 160))
	}
	if !strings.Contains(got, "占位单元格") {
		t.Fatalf("摘要里没有正文内容（%d 字符）：%q", len([]rune(got)), clipRunes(got, 160))
	}
	t.Logf("输出 %d 字符：%s", len([]rune(got)), clipRunes(got, 100))
}

// 夹具自身的负控：合成的输入必须真的含被测特征，否则上面那条会「因为测不到
// 任何东西而绿」。这是判据的判据。
func TestSynthBodyTextCarriesQPHMarkers(t *testing.T) {
	raw := string(synthBodyText())
	if !strings.HasPrefix(raw, "<!DOCTYPE html>=0D=0A") {
		t.Fatalf("合成输入开头与实测形态不符：%q（实测是 `<!DOCTYPE html>=0D=0A<html lang=3D\"en\">…`）",
			headOf(raw, 60))
	}
	if !strings.Contains(raw, "=0D=0A") {
		t.Fatal("合成输入里没有 =0D=0A —— 换行形态没造出来")
	}
	if !strings.Contains(raw, "=3D=  ") {
		t.Fatal("合成输入里没有 =3D=  的压平软换行残留 ——「解码失败」的前提没造出来")
	}
	// 判据的判据：这份输入必须被 isUnsanitized 判为脏。
	if !isUnsanitized(raw) {
		t.Fatal("合成输入未被 isUnsanitized 判为脏 —— 护栏会在「输入根本没有污染」的空态上变绿")
	}
	t.Logf("输入 %d 字节，前 100：%q", len(raw), headOf(raw, 100))
}
