package email

// snippet_bodytext_parts_test.go — BODY[TEXT] 分片的回归护栏。
//
// ## 缺陷
//
// IMAP 对 multipart 邮件的 BODY[TEXT] 返回「去掉顶层头之后的整个 body
// 部分」：boundary 行 + 每个 part 的 Content-* 头 + 各自的编码正文，
// 多个 part 依次排列。生产路径（fetcher.go:543 的 fetchSnippetOnConnected、
// backfill.go:270 的 emailFromMessage）把这份**分片**原样喂给
// DeriveSnippet。
//
// 而 DeriveSnippet 原先的 mimeCandidates **只丢掉第一行 boundary**，
// 于是 ParseMIMEMessage 把 part1 的头当顶层头、把
// 「part1 正文 + part2 的头 + part2 的正文」当作一封邮件的 body。
// 后果：TextBody 里必然混进内层 part 的 Content-* 头 ⇒ containsMIMESource
// 判 true ⇒ 正确答案被丢弃 ⇒ 流程落到第 2 步的 HTML 分支，而**那条分支
// 当时没有任何 MIME 判据**，于是把整段分片压成一行原样返回。
//
// 2026-10-03 真机/真库实测：25 个真实样本在 BODY[TEXT] 出口上 **25/25
// 全部泄漏**，邮件列表里显示
//
//	----_NmP-…-Part_1 Content-Type: text/plain; … quoted-printable =E8=AE=A2…
//
// ## 两条护栏，缺一不可
//
//  1. TestDeriveSnippet_BodyTextFragmentYieldsRealBody
//     分片必须被按 part 拆开并取到**真正的正文**。只钉「不泄漏」是不够的
//     ——「一律返回空串」也能让护栏 1 变绿，而那是把缺陷换成「列表全空」。
//     这条钉住的是**内容**：必须出现「订阅到期提醒」这几个字。
//
//  2. TestDeriveSnippet_HtmlBranchRefusesMIMESource
//     钉住第 2 步 HTML 分支那道**新加的** MIME 判据。护栏 1 修的是
//     「按 part 拆」，护栏 2 钉的是「兜底出口不许裸奔」—— 两者是不同的
//     失效模式，只测其中一个都会留下另一个的洞。
//
// ## 负控
//
// 护栏 1 对**没有**按 part 拆分（删掉第 0 步）的代码必须转红。
// 护栏 2 对**没有**HTML 分支判据（回到 `if h != "" { return h }`）的
// 代码必须转红。两者都实测过，见提交说明。

import (
	"fmt"
	"strings"
	"testing"
)

// bodyTextFragmentFixture 是 2026-10-03 真库 em-1298896153（[FlatRouter]
// 订阅到期提醒）的 BODY[TEXT] 形态，字段与顺序照抄真实响应：
//
//	· 开头是 boundary 行；
//	· part1 是 text/plain + quoted-printable，正文里的软换行 `=\r\n` 保留；
//	· part2 是 text/html + quoted-printable；
//	· 末尾是结束 boundary。
//
// 软换行是这份 fixture 的关键：QP 正文按 76 列折行，没有 `=\r\n` 就不是
// 真实形态，而「先压平再解码」恰好是本缺陷的表象。
const bodyTextFragmentFixture = "----_NmP-04dc9fb738451150-Part_1\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"=E8=AE=A2=E9=98=85=E5=88=B0=E6=9C=9F=E6=8F=90=E9=86=92 =C2=A0 nick=EF=BC=8C=E6=82=A8=E5=A5=BD=EF=BC=9A=\r\n" +
	"=E6=82=A8=E7=9A=84 Default =E8=AE=A2=E9=98=85=E5=B0=86=E5=9C=A8 7 =E5=A4=A9=E5=90=8E=E5=88=B0=\r\n" +
	"=E6=9C=9F=E3=80=82 =E5=88=B0=E6=9C=9F=E6=97=B6=E9=97=B4=EF=BC=9A2026-10-10= 13:00 =E9=80=80=E8=AE=A2=\r\n" +
	"=E6=AD=B4=E7=B1=BB=E8=AE=A2=E9=98=85=E6=8F=90=E9=86=92\r\n" +
	"----_NmP-04dc9fb738451150-Part_2\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"<html><body><p>=E8=AE=A2=E9=98=85=E5=88=B0=E6=9C=9F=E6=8F=90=E9=86=92</p></body></html>\r\n" +
	"----_NmP-04dc9fb738451150-Part_1--\r\n"

// TestDeriveSnippet_BodyTextFragmentYieldsRealBody 钉住「取到真正文」。
func TestDeriveSnippet_BodyTextFragmentYieldsRealBody(t *testing.T) {
	got := DeriveSnippet([]byte(bodyTextFragmentFixture), 500)

	if containsMIMESource(got) {
		t.Fatalf("DeriveSnippet 把原始 MIME 转储当摘要返回了（%d 字符）：\n%s",
			len([]rune(got)), got)
	}
	// 内容判据：必须真的解出了 quoted-printable 的中文正文。
	//
	// 这条不能省。只断言「不含 Content-Type」的话，把 DeriveSnippet 改成
	// 无条件 `return ""` 也能过 —— 那是拿「列表全空」换「列表显示转储」，
	// 同样是缺陷，而且更隐蔽（没有报错，只是没有摘要）。
	if !strings.Contains(got, "订阅到期提醒") {
		t.Fatalf("摘要没有解出真实正文（%d 字符）：%q\n期望包含「订阅到期提醒」——"+
			"说明分片没被按 MIME part 拆开，或 quoted-printable 没解码。", len([]rune(got)), got)
	}
	if !strings.Contains(got, "您的 Default 订阅将在 7 天后到期") {
		t.Fatalf("摘要只拿到了正文开头，正文不完整：%q", got)
	}
	// 软换行必须被正确处理：`=\r\n` 折叠后不该留下裸 `=`。
	if strings.Contains(got, "=E8") || strings.Contains(got, "=\r") {
		t.Fatalf("摘要里仍有 quoted-printable 转义原文：%q", got)
	}
}

// TestDeriveSnippet_HtmlBranchRefusesMIMESource 钉住第 2 步 HTML 分支的
// MIME 判据 —— 那条分支在修复前**完全没有**判据，是本缺陷真正的出口。
//
// 这里的输入刻意构造成「含 HTML 标签、且含 MIME token」，
// 但**不以 boundary 行开头**（mimeParts 会直接返回 nil），
// 所以它只能走到第 2 步，专测那一条分支。
func TestDeriveSnippet_HtmlBranchRefusesMIMESource(t *testing.T) {
	raw := "<html><body><p>季度报表已生成</p></body></html>\r\n" +
		"------=_Part_9f2c1a Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"=E6=8A=A5=E8=A1=A8=E6=AD=A3=E6=96=87"
	got := DeriveSnippet([]byte(raw), 500)
	if containsMIMESource(got) {
		t.Fatalf("第 2 步 HTML 分支没有 MIME 判据，把转储放了出来：%q", got)
	}
}

// TestDeriveSnippet_CompleteMessageUnaffected 钉住「新增的第 0 步对
// 完整报文零影响」。
//
// 这一步（按 part 拆分）必须只作用于 BODY[TEXT] 分片。若它对普通完整
// 报文也生效，就会把「首行是 boundary 行的合法正文」误切，
// 或者更糟：把一段正文切成多段后只取第一段。
func TestDeriveSnippet_CompleteMessageUnaffected(t *testing.T) {
	const body = "季度报表已生成，请查详情，账号见邮件签名。"
	full := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: 季度报表\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		qpEncodeWithSoftBreaks(body)
	got := DeriveSnippet([]byte(full), 500)
	if !strings.Contains(got, body) {
		t.Fatalf("完整报文被新增的按-part 拆分路径影响了：%q", got)
	}
	if containsMIMESource(got) {
		t.Fatalf("完整报文路径泄漏了 MIME 源码：%q", got)
	}
	// mimeParts 对完整报文必须明确返回 nil（首行不是 boundary 行）。
	if parts := mimeParts([]byte(full)); len(parts) != 0 {
		t.Fatalf("mimeParts 对完整报文返回了 %d 个 part，应当为 0", len(parts))
	}
}

// TestMimeParts_SplitsRealShape 钉住拆分本身：3 个 part、顺序不变、
// 每个 part 都带自己的 Content-* 头（能直接喂 ParseMIMEMessage）。
func TestMimeParts_SplitsRealShape(t *testing.T) {
	parts := mimeParts([]byte(bodyTextFragmentFixture))
	if len(parts) != 2 {
		t.Fatalf("期望 2 个 part（part1 + part2，结束 boundary 不算 part），得到 %d", len(parts))
	}
	if !strings.HasPrefix(string(parts[0]), "Content-Type: text/plain") {
		t.Fatalf("part1 应以自己的 Content-Type 开头，实际：%q", firstLineOf(parts[0]))
	}
	if !strings.HasPrefix(string(parts[1]), "Content-Type: text/html") {
		t.Fatalf("part2 应以自己的 Content-Type 开头，实际：%q", firstLineOf(parts[1]))
	}
	// part1 里不得含 part2 的头 —— 那正是旧实现泄漏的形态。
	if strings.Contains(string(parts[0]), "text/html") {
		t.Fatalf("part1 里混进了 part2 的头：%q", string(parts[0]))
	}
}

// qpEncodeWithSoftBreaks 把一段文本编码成 quoted-printable，并按 20 个
// 转义一组的节奏插入 `=\r\n` 软换行。
//
// ## 为什么测试夹具要**生成**而不是手写
//
// 本文件第一版的完整报文夹具是手写十六进制的，两处都算错了：
//
//	=E6=8A=A5  是「报」不是「季」   → 断言「季度报表已生成」永远不成立
//	=E6=AC=BE  是「账」  =E6=80=80 是 U+8080，不是「号」
//
// 症状是「测试红了」，很容易误判成实现坏了而去改生产代码。
// 手写转义把**夹具的算术错误**伪装成**被测逻辑的缺陷**——这类自造的
// 假阳性比没有测试更费时间。改成生成，夹具就不可能算错。
func qpEncodeWithSoftBreaks(s string) string {
	const perLine = 20
	var out strings.Builder
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x21 && c <= 0x7E && c != '=' {
			out.WriteByte(c)
			n++
		} else {
			fmt.Fprintf(&out, "=%02X", c)
			n += 3
		}
		if n >= perLine*3 && i < len(s)-1 {
			out.WriteString("=\r\n")
			n = 0
		}
	}
	out.WriteString("\r\n")
	return out.String()
}
