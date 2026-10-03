// snippet 的真实语料回归：压平后的 MIME 分片原文不得作为摘要返回。
//
// ## 这条样本是哪来的
//
// 2026-10-03 真机（Redmi 2411DRN47C）从「消息」tab 看到邮件预览直接显示
//
//	------=_Part_36252622_2120907832.1791025295913 Content-Type: text/html;charset=utf-8
//	Content-Transfer-Encoding: quoted-printable =E6=AD=A4=E9=82=AE=E4=BB=B6…
//
// 落到 opencode_pocket.emails.em-10462-acct-1790870162047413500-2（2026-10-03 19:01 同步），
// 下面 leakedSnippet 与库里那条**逐字一致**（含压平成一行的形态）。
//
// ## 根因（一行）
//
// DeriveSnippet 第 2 步的闸门是 `!looksLikeMIMEStructure(string(raw))`，
// 而 looksLikeMIMEStructure 的行锚点（^Content-Type: / ^--boundary）是**逐行正则**。
// snippet.go 自己的注释已经写明这一点：「压平之后整段变成一行，行首锚点全部失效，
// 判据形同虚设」。压平形态于是让闸门**漏判**为「不是 MIME」，一路走到第 2 步末尾的
//
//	return truncateRunes(stripTrailingBoundary(s), maxRunes)
//
// —— 那正是同文件 SnippetFromParsed 文档里点名不存在的「退回原文」兜底
// （snippet.go:158-160：「DeriveSnippet 里已经写明『这里没有退回原文这条兜底，
// 那正是这个缺陷本身』」）。**注释说没有，代码有。**
//
// 复现时三个判据的读数（这就是为什么它能一路绿灯）：
//
//	SnippetFromParsed(样本)      = ""      ← 正确拦住
//	DeriveSnippet(样本)          = 样本原文 ← 实泄
//	looksLikeMIMEStructure(样本) = false   ← 闸门漏判（行锚点在压平形态上失效）
//	containsMIMESource(样本)     = true    ← 位置无关的判据其实认得出来
//
// ## 修法方向
//
// containsMIMESource 本来就是为修「压平后判据失效」而写的（见它的注释），
// 但 DeriveSnippet 第 2 步没换用它。第 2 步末尾的兜底必须加同一道闸；
// 漏过去就落到第 3 步返回 ""，那才是本文件声明的正确行为（「宁可返回空串」）。
package email

import (
	"strings"
	"testing"
)

// 与 opencode_pocket.emails 里那条泄漏记录逐字一致（压平成一行）。
const leakedSnippet = `------=_Part_36252622_2120907832.1791025295913 Content-Type: text/html;charset=utf-8 Content-Transfer-Encoding: quoted-printable =E6=AD=A4=E9=82=AE=E4=BB=B6=E7=94=B1=E9=98= =BF=E9=87=8C=E4=BA=91=E5=8F=91=E9=80=81=EF=BC=8C=E8=AF=B7=E5=8F=BF=E7=9B=B4=E6= =8E=A5=E5=9B=9E=E5=A4=8D=EF=BC=8C=E8=B0=A2=E8=B0=A2=EF=BC=81`

func TestSnippetDerive_FlattenedPartIsNotReturned(t *testing.T) {
	got := DeriveSnippet([]byte(leakedSnippet), 500)
	if strings.TrimSpace(got) == "" {
		return // 正确：宁可空串也不转储 MIME
	}
	if containsMIMESource(got) {
		t.Fatalf("DeriveSnippet 把原始 MIME 转储当摘要返回了（%d 字符）：\n%s", len([]rune(got)), clipRunes(got, 160))
	}
	// 返回的不是 MIME 转储就算通过（万一将来有别的合理降级形态）。
	t.Logf("返回了非 MIME 内容（%d 字符）：%s", len([]rune(got)), clipRunes(got, 120))
}

// 同一条样本走 POP3 的主出口也必须为空 —— 那是 fetcher.go 的 SnippetFromParsed 路径。
// 这条现在就是绿的，写出来是为了**钉住它别退化**（它曾经是实泄的那条路径）。
func TestSnippetFromParsed_FlattenedPartReturnsEmpty(t *testing.T) {
	pm := &ParsedMessage{TextBody: leakedSnippet, HTMLBody: leakedSnippet}
	if got := SnippetFromParsed(pm, 500); strings.TrimSpace(got) != "" {
		t.Fatalf("SnippetFromParsed 应返回空串，却返回了 %d 字符：%s", len([]rune(got)), clipRunes(got, 160))
	}
}

// 闸门本身在这条样本上必须能识别出 MIME 结构，否则修法无从谈起。
// 这条是**判据的负控**：若哪天 looksLikeMIMEStructure 修好了，本条仍应绿
// （containsMIMESource 是更宽松的判据，含逐行与 token 两路）。
func TestGuard_RecognisesFlattenedPart(t *testing.T) {
	if !containsMIMESource(leakedSnippet) {
		t.Fatal("containsMIMESource 对真实泄漏样本返回 false —— 判据失明了，修法无效")
	}
	t.Logf("looksLikeMIMEStructure(压平样本) = %v（漏判，故必须由 containsMIMESource 兜底）",
		looksLikeMIMEStructure(leakedSnippet))
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
