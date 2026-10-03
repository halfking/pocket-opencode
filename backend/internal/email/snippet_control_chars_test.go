package email

// snippet_control_chars_test.go — 摘要不得含会**让整条 INSERT 失败**的字符。
//
// ## 缺陷
//
// quoted-printable 的 `=00` 与 base64 解码出的二进制都含 NUL。NUL 是合法
// UTF-8（`utf8.ValidString("a\x00b")` == true），所以
// decodeWholeQuotedPrintable / decodeWholeBase64 的「解出来像文本吗」判据
// 全部放行，DeriveSnippet 把 NUL 原样带进摘要。
//
// 而 PostgreSQL 的 `text` **不能存 NUL**：值在协议层被拒，报
// `ERROR: invalid byte sequence for encoding "UTF8": 0x00 (SQLSTATE 22021)`。
// 后果不是「摘要变空」，而是 `store.InsertEmail` 整条失败、**这封邮件
// 根本没有入库** —— 且没有补偿逻辑，是静默丢失。
//
// 实测：2026-10-03 22:02 的一次真库 backfill 出现 3 次该错误
// （em-10349 / em-10350 等），全部插入失败。
//
// ## 为什么判据钉在 DeriveSnippet 这个出口
//
// 该函数有 6 个 return 点。净化若只加在其中一个分支上，其余分支照旧漏。
// 修法是把净化收口到薄包装 `DeriveSnippet`，新增返回分支时不会漏。
// 所以这两条用例打的是**包装**而不是某个内部分支——这正是要钉住的东西。

import (
	"encoding/base64"
	"strings"
	"testing"
)

func snippetHasDisruptiveControl(s string) bool {
	return strings.ContainsFunc(s, isDisruptiveControl)
}

func assertSnippetIsStorable(t *testing.T, label string, s string) {
	t.Helper()
	if snippetHasDisruptiveControl(s) {
		t.Fatalf("%s：摘要含 C0 控制字符（含 NUL），落库必失败：%q", label, s)
	}
}

// TestDeriveSnippet_StripsNULFromBase64Part base64 形态。
func TestDeriveSnippet_StripsNULFromBase64Part(t *testing.T) {
	payload := "季度报表已生成\x00\x00尾部还带NUL"
	raw := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte(payload))
	got := DeriveSnippet([]byte(raw), 500)
	assertSnippetIsStorable(t, "base64 part", got)
	// 同时钉住「不是把整段清空」：净化要删 NUL，不是删正文。
	if !strings.Contains(got, "季度报表已生成") || !strings.Contains(got, "尾部还带NUL") {
		t.Fatalf("净化过头，正文被一起删了：%q", got)
	}
}

// TestDeriveSnippet_StripsNULFromQuotedPrintablePart QP 形态（=00 即 NUL）。
//
// 正文用 qpEncodeWithSoftBreaks **生成**，不手写十六进制。
// 这个文件之前的第一版手写了 `=E5=B0=BE` 想表示「度」，实际那是 U+30BE；
// 于是断言红，而实现是好的。手写转义会把**夹具的算术错误**伪装成
// **被测逻辑的缺陷**——这比没有测试更费时间。
func TestDeriveSnippet_StripsNULFromQuotedPrintablePart(t *testing.T) {
	qp := qpEncodeWithSoftBreaks("季报已生成\x00\x00尾部")
	raw := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + qp
	got := DeriveSnippet([]byte(raw), 500)
	assertSnippetIsStorable(t, "qp part", got)
	if !strings.Contains(got, "季报已生成") {
		t.Fatalf("净化过头，正文被一起删了：%q", got)
	}
	if !strings.Contains(got, "尾部") {
		t.Fatalf("净化过头，NUL 之后的正文被一起删了：%q", got)
	}
}

// TestDeriveSnippet_WholeBase64FallbackIsAlreadyStorable 覆盖**整段 base64
// 兜底**那条路（输入不带任何 Content-* 头，<partial> 从正文起取）。
//
// ## 这条用例**没有**被证明对净化层有牙齿 —— 别把它算进净化层的覆盖
//
// 负控实测（把 DeriveSnippet 的 sanitizeSnippet 调用摘掉）时，
// 本用例**仍然绿**，而同批的另外三条都转红。原因是这条路径本来就
// 不产 NUL：base64 解码结果先过 htmlToText（正则删标签）与
// normalizeWhitespace（strings.Fields 切分），NUL 在那一步就被当作
// 非空白字符丢弃了；纯文本形态则被 decodeWholeBase64 的「含 CJK 就不试」
// 门槛挡下，原样返回 base64 串 —— 同样不含 NUL。
//
// 所以它守的是「这条路径的可落库性」，**不是**净化层。
// 如实标注在这里，是为了免得以后有人拿「5 条用例」的数字去声称
// 净化层有 5 条护栏 —— 实际有牙齿的是 3 条。
func TestDeriveSnippet_WholeBase64FallbackIsAlreadyStorable(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("<html><body>hello\x00\x00world</body></html>"))
	got := DeriveSnippet([]byte(raw), 500)
	assertSnippetIsStorable(t, "whole-base64 fallback", got)
	// 这条路必须**解得开**并给出正文，否则本用例又在对着一个空串恒绿。
	if got == "" {
		t.Fatal("整段 base64 兜底返回空串 —— 本用例将退化为空转的断言")
	}
}

// TestDeriveSnippet_StripsNULFromMimeParts 覆盖 2026-10-03 新增的
// 「按 MIME part 拆分」路径：NUL 藏在 part 正文里，拆开后仍必须被清掉。
func TestDeriveSnippet_StripsNULFromMimeParts(t *testing.T) {
	b1 := base64.StdEncoding.EncodeToString([]byte("额度不足，请及时充值\x00\x00明细见附件"))
	b2 := base64.StdEncoding.EncodeToString([]byte("<html><body>额度不足</body></html>"))
	raw := "----_NmP-part-test-Part_1\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b1 + "\r\n" +
		"----_NmP-part-test-Part_2\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b2 + "\r\n" +
		"----_NmP-part-test-Part_1--\r\n"
	got := DeriveSnippet([]byte(raw), 500)
	assertSnippetIsStorable(t, "mime parts", got)
	if !strings.Contains(got, "额度不足") {
		t.Fatalf("净化过头，正文被一起删了：%q", got)
	}
}

// TestDeriveSnippet_KeepsUPlusFFFD 钉住「不要顺手把 U+FFFD 也清掉」。
//
// U+FFFD 是按 rune 截断的既有产物（multibyte 字符被劈开）。它能正常落库，
// 清掉等于把用户可见的乱码**藏起来**而不是修好。
func TestDeriveSnippet_KeepsUPlusFFFD(t *testing.T) {
	if got := sanitizeSnippet("前半�后半"); got != "前半�后半" {
		t.Fatalf("sanitizeSnippet 把 U+FFFD 也清掉了：%q", got)
	}
	// 而 NUL 必须被清。
	if got := sanitizeSnippet("前半\x00后半"); strings.ContainsRune(got, 0) {
		t.Fatalf("sanitizeSnippet 没清掉 NUL：%q", got)
	}
}
