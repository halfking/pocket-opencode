package email

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// snippet_partial_fetch_test.go — 覆盖真实 IMAP `BODY[TEXT]` 的三种取回形态。
//
// 背景（2026-10-02 模拟器收件箱实测）：120 封邮件里 83 封的摘要显示成
// 原始 MIME 编码，用户看到的是 boundary 行、Content-* 头和
// quoted-printable / base64 串。
//
// snippet_test.go 里已有的用例用的是「头与正文之间有空行」的完整 MIME，
// 那一类 ParseMIMEMessage 能解析，走的是正常分支；而真实服务器返回的是
// **BODY[TEXT] 的正文部分**，首行恒为 boundary 开头，整封的头不在里面。
// 两种形态走的是完全不同的代码路径，所以已有用例覆盖不到线上这一类。
//
// 判据分两层，缺一不可：
//   1. 负向：输出不得含任何原始 MIME / 编码痕迹；
//   2. **正向**：必须真的把正文还原出来。
// 只写第 1 条是不够的 —— 去掉修复后函数会返回空串，负向断言照样通过，
// 护栏就成了永远绿的摆设（2026-10-02 负控实测：只查泄漏时打坏
// boundary 剥离，测试仍然全绿）。所以这里同时钉住「内容对不对」。

const (
	wantPlainBody  = "尊敬的客户：您昨日的账单已生成，请及时查收。"
	wantHTMLBody   = "<html><body><p>尊敬的用户，您的验证码是 <b>824193</b>，请勿告知他人。</p></body></html>"
	wantHTMLAsText = "尊敬的用户，您的验证码是 824193，请勿告知他人。"
)

var leakyMIMEArtifacts = []string{
	"Content-Type:", "Content-Transfer-Encoding", "=_Part_", "=E6=", "PHN0eWxlP", "--_000_",
}

func assertNoRawMIME(t *testing.T, label string, got string) {
	t.Helper()
	for _, bad := range leakyMIMEArtifacts {
		if strings.Contains(got, bad) {
			t.Errorf("%s: 原始 MIME 痕迹 %q 泄漏到摘要：%q", label, bad, got)
		}
	}
}

// qpEncode 按 RFC 2045 的形态做 quoted-printable 编码：非 ASCII 字节逐个
// 转成 =XX，每满 75 列用**软换行**（行尾补一个 `=`）折行，CRLF 行尾。
//
// 两个刻意的选择：
//
//	· 不用 mime/quotedprintable.Writer。它按 76 列折行且换行用 LF，和真实
//	  邮件不一致，2026-10-02 第一次写用例时就是被它带偏的。
//	· 折行必须用软换行而不是硬换行。硬换行在解码结果里代表原文的换行，
//	  落在多字节字符中间就会把 UTF-8 劈开；RFC 2045 也只允许在原文真的有
//	  换行处出现硬换行。
func qpEncode(s string) string {
	var sb strings.Builder
	col := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		var tok string
		if b == '=' || b < ' ' || b > '~' {
			tok = fmt.Sprintf("=%02X", b)
		} else {
			tok = string(rune(b))
		}
		if col+len(tok) > 74 {
			sb.WriteString("=\r\n")
			col = 0
		}
		sb.WriteString(tok)
		col += len(tok)
	}
	sb.WriteString("\r\n")
	return sb.String()
}

// 形态 1：multipart 的 text/plain + quoted-printable，正文部分以 boundary 开头。
// 剥掉首行 boundary 后就是一个完整可解析的 MIME 实体，摘要是可读正文。
func realQPBodyPart() string {
	return "------=_Part_8505717_93977514.1790821420306\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		qpEncode(wantPlainBody)
}

// 形态 2：text/plain + base64，同样只有正文部分。
// 声明 utf-8：国内邮箱常写 charset=gb2312 却发 UTF-8 字节，那是另一条
// 独立问题（decodeCharset 会按声明转码），不在本用例范围内。
func realBase64BodyPart() string {
	return "--_000_10f7b8d35f184af588cb482c8d4663c7xiaomicom_\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(wantPlainBody)), "\n", "\r\n") + "\r\n"
}

// 形态 3：头被整个切掉，只剩 base64 正文（部分服务器的 <partial> 从正文起）。
// 这时候没有任何 MIME 头可依据，base64 原文会被当正文透出。
func realBase64BodyOnly() string {
	b := base64.StdEncoding.EncodeToString([]byte(wantHTMLBody))
	// 真实邮件的 base64 正文每 76 字符换行
	var sb strings.Builder
	for i := 0; i < len(b); i += 76 {
		end := i + 76
		if end > len(b) {
			end = len(b)
		}
		sb.WriteString(b[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// 形态 4：quoted-printable 正文，头被整个切掉（只剩转义序列）。
// 2026-10-02 真库实测：`=20 =E7=94=A8 ChatGPT Images=EF=BC=8C…`
func realQPDecodedBodyOnly() string {
	return qpEncode("用 ChatGPT Images，8 张自拍照 = 电影《头号玩家》肖像照。")
}

// 形态 5：可读正文后面粘着 MIME 结束边界。
// 2026-10-02 真库实测：
//
//	「极客时间 点击这里取消订阅 ------=_Part_172449_2115296577.1789970890196--」
func realTextWithTrailingBoundary() string {
	return "极客时间 点击这里取消订阅\r\n------=_Part_172449_2115296577.1789970890196--"
}

func TestDeriveSnippet_PartialFetch_RecoversBody(t *testing.T) {
	for _, c := range []struct {
		name, raw, want string
	}{
		{"quoted-printable-body-part", realQPBodyPart(), wantPlainBody},
		{"base64-body-part", realBase64BodyPart(), wantPlainBody},
		{"base64-body-only", realBase64BodyOnly(), wantHTMLAsText},
		{"qp-body-only", realQPDecodedBodyOnly(), "用 ChatGPT Images，8 张自拍照 = 电影《头号玩家》肖像照。"},
		{"text-with-trailing-boundary", realTextWithTrailingBoundary(), "极客时间 点击这里取消订阅"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := DeriveSnippet([]byte(c.raw), 500)
			assertNoRawMIME(t, c.name, got)
			if got == "" {
				t.Fatalf("%s: 摘要为空，正文没被还原出来", c.name)
			}
			if got != c.want {
				t.Errorf("%s:\n got = %q\nwant = %q", c.name, got, c.want)
			}
		})
	}
}

// 只查「不泄漏」是不够的：去掉修复后函数返回空串也能通过负向断言。
// 这条用例把空串明确判为失败，防止护栏退化成永远绿。
func TestDeriveSnippet_PartialFetch_EmptyIsFailure(t *testing.T) {
	for _, c := range []struct{ name, raw string }{
		{"quoted-printable-body-part", realQPBodyPart()},
		{"base64-body-part", realBase64BodyPart()},
		{"base64-body-only", realBase64BodyOnly()},
		{"qp-body-only", realQPDecodedBodyOnly()},
		{"text-with-trailing-boundary", realTextWithTrailingBoundary()},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveSnippet([]byte(c.raw), 500); strings.TrimSpace(got) == "" {
				t.Fatalf("%s: 摘要退化成空串 —— 负向断言单独存在时这会静默通过", c.name)
			}
		})
	}
}

// 边界一：剥 boundary 那一步必须保守。正文首行恰好以 `--` 开头时不能被误剥。
func TestDeriveSnippet_DashLeadingTextBodyNotMangled(t *testing.T) {
	body := "-- 这是一条以双横线开头的正文，后面的内容必须一字不差地保留。"
	got := DeriveSnippet([]byte(body), 500)
	if got != body {
		t.Errorf("以 -- 开头的正文处理不对：\n got = %q\nwant = %q", got, body)
	}
}

// 边界二：正文里带冒号**不是** MIME 头。
//
// 原 looksLikeMIME 是 strings.Contains(head, ":")，而调用点传进来的文本
// 已经被 normalizeWhitespace 压成一行，于是任何含冒号的正文都被判成 MIME
// 源码、摘要返回空串。收件箱里大量邮件因此没有摘要。
func TestDeriveSnippet_ColonInPlainBodyIsNotMIME(t *testing.T) {
	for _, body := range []string{
		"Hi John, the meeting is at 3pm: bring the quarterly report.",
		"会议改到下午三点：记得带上季度的报表原件。",
		"提醒：您的额度即将用尽，请及时充值后再试。",
		// 正常收尾的两个连字符不能被当成 boundary 削掉
		"会议纪要见附件 --",
	} {
		if got := DeriveSnippet([]byte(body), 500); got != body {
			t.Errorf("含冒号的正文被误判成 MIME：\n got = %q\nwant = %q", got, body)
		}
	}
}
