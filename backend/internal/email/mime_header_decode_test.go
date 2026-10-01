package email

// mime_header_decode_test.go — RFC 2047 头字段解码（BUG-AP 的回归）。
//
// 实测：企业微信邮箱 imap.exmail.qq.com 收信成功，但列表里 5 封邮件的主题
// 全部是 `=?GBK?B?...?=` 原文。根因是 fetcher 直接存了 IMAP ENVELOPE 的原始
// Subject（go-imap 不解码），而 mime.WordDecoder 不挂 CharsetReader 时不认 GBK。
//
// 这里钉三层：
//  1. GBK / UTF-8 / Q 编码三种编码字都能解；
//  2. 解不动时**原样返回**（不能变成空串——空主题会让列表与关键词匹配一起废掉）；
//  3. 解码后的主题能真正命中发票关键词（否则修解码没有业务价值）。

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// gbkEncodedWord 造一个 `=?GBK?B?<base64>?=` 编码字。
func gbkEncodedWord(t *testing.T, s string) string {
	t.Helper()
	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", s, err)
	}
	return "=?GBK?B?" + base64.StdEncoding.EncodeToString(enc) + "?="
}

func TestDecodeMIMEWord_GBKBase64(t *testing.T) {
	got := decodeMIMEWord(gbkEncodedWord(t, "增值税电子发票已开具"))
	if got != "增值税电子发票已开具" {
		t.Fatalf("GBK encoded-word not decoded: %q", got)
	}
}

func TestDecodeMIMEWord_UTF8Base64AndQuotedPrintable(t *testing.T) {
	b64 := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte("月度对账单")) + "?="
	if got := decodeMIMEWord(b64); got != "月度对账单" {
		t.Fatalf("UTF-8 B not decoded: %q", got)
	}
	// Q 编码的 GBK。注意 hex 转义里放的是**GBK 字节**：
	// 发票 = B7 EE B7 A8。用 UTF-8 字节(=E5=8F=91=E7=A5=A8)冒充 GBK 会解出
	// 注意下面这串「鍙戠エ」是**故意的**：它正是「用 UTF-8 字节冒充 GBK」时
	// 解出来的经典乱码（U+94B5 U+6220 U+6D5C）。这个用例是反向夹具——证明
	// 编码搞错时长什么样，别把它当成乱码 bug 顺手改掉。
	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("发票"))
	if err != nil {
		t.Fatal(err)
	}
	q := "=?GBK?Q?"
	for _, b := range enc {
		q += "=" + strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	q += "?="
	if got := decodeMIMEWord(q); got != "发票" {
		t.Fatalf("GBK Q not decoded: %q (input %s)", got, q)
	}
}

func TestDecodeMIMEWord_MixedPlainAndEncoded(t *testing.T) {
	in := "Re: " + gbkEncodedWord(t, "报销单")
	if got := decodeMIMEWord(in); !strings.Contains(got, "报销单") {
		t.Fatalf("mixed subject not decoded: %q", got)
	}
}

func TestDecodeMIMEWord_UndecodableStaysIntact(t *testing.T) {
	// 空串与纯 ASCII 原样返回（不做无谓改写）。
	for _, in := range []string{"", "plain ascii subject"} {
		if got := decodeMIMEWord(in); got != in {
			t.Fatalf("decodeMIMEWord(%q) = %q, want unchanged", in, got)
		}
	}
	// 未知字符集：RFC 2047 规定按 ISO-8859-1 处理，我们的 CharsetReader 原样返回，
	// 于是拿到可读文本——**不能**退回 `=?X-...?=aGVsbG8=?=` 这种原文。
	if got := decodeMIMEWord("=?X-UNKNOWN-CHARSET?B?aGVsbG8=?="); strings.Contains(got, "=?") {
		t.Fatalf("unknown charset left an encoded-word in place: %q", got)
	}
	// 截断/畸形的编码字：解不了就整体保留原文，不能返回空串
	// （空主题会让列表与关键词匹配一起废掉）。
	for _, in := range []string{"=?GBK?B?not-base64!!?=", "=?GBK?B?"} {
		if got := decodeMIMEWord(in); got == "" {
			t.Fatalf("malformed encoded-word %q decoded to empty", in)
		}
	}
}

// 业务价值：解码后的中文主题必须能命中发票关键词。
func TestDecodedSubject_HitsInvoiceKeywords(t *testing.T) {
	e := Email{Subject: decodeMIMEWord(gbkEncodedWord(t, "增值税电子普通发票已开具，请查收"))}
	if !InvoiceCandidate(e) {
		t.Fatalf("decoded subject still misses invoice keywords: %q", e.Subject)
	}
}

// 真实观测到的样子：不解码时关键词必然落空（钉住「为什么这算 bug」）。
func TestRawEncodedWordSubject_MissesKeywords(t *testing.T) {
	raw := gbkEncodedWord(t, "增值税电子普通发票已开具，请查收")
	if InvoiceCandidate(Email{Subject: raw}) {
		t.Skip("keyword list unexpectedly matches the raw encoded-word")
	}
}

// 编码字里带 GB2312 声明也要能解（163/QQ 企业邮常见）。
func TestDecodeMIMEWord_GB2312Label(t *testing.T) {
	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("合同审批通知"))
	if err != nil {
		t.Fatal(err)
	}
	in := "=?GB2312?B?" + base64.StdEncoding.EncodeToString(enc) + "?="
	if got := decodeMIMEWord(in); got != "合同审批通知" {
		t.Fatalf("GB2312 label not decoded: %q", got)
	}
}

// 确认 CharsetReader 真的被挂上（防止有人改回 new(mime.WordDecoder)）。
func TestDecodeMIMEWord_CharsetReaderIsWired(t *testing.T) {
	word := gbkEncodedWord(t, "测试")
	out := decodeMIMEWord(word)
	if out == word || bytes.Contains([]byte(out), []byte("=?")) {
		t.Fatalf("charset reader not applied: %q", out)
	}
}

