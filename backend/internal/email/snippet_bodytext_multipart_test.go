package email

// multipart 邮件的 BODY[TEXT]：boundary 落在**第二行**时必须仍能拆 part。
//
// ## 这条样本的形态来自哪里
//
// 2026-10-04 真库 + 真 IMAP（diag_empty_snippet_locus_test.go，只读）。
// 真库有 32 行 snippet 永久为空（kimmy.huang@163.com 31 + feikemanager 1），
// 主体全是正常业务邮件：GitHub 通知、GitLab MR 讨论、Basecamp 邀请。
//
// 诊断把判据打到子判据一级后（diag_empty_snippet_locus_test.go 的 logMIMEHit）：
//
//	  reMIMEHeaderToken 命中 "Content-Type:" @46，上上下文：
//	    "  ------=_Part_21554049_1801402642.1790152695491  Content-Type: …"
//
// 那个 `  ` 一度被我读成「boundary 行带两个空格」，并据此写了
// 「容忍前导空白」的修法 —— **那是错的**。`t.Logf` 输出多行字符串时，
// Go 的 testing 包会给**后续行加缩进**，那 2 个空格是渲染出来的，
// 不是数据里的字符。改用 %q 逐行打印（保留 \n）之后，真实形态是：
//
//	首[0] ""
//	首[1] "------=_Part_21554049_1801402642.1790152695491"
//	首[2] "Content-Type: text/html; charset=\"UTF-8\""
//	尾[-2] "------=_Part_21554049_1801402642.1790152695491--"
//	尾[-1] ""
//
// 另一种 boundary 同形态：`----==_mimepart_6ab9d1503b4e7_e511d8145119`
//
// ⇒ **首行是空行，boundary 在第二行**。
//
// ## 缺陷
//
// startsWithBoundaryLine 要求「首行就是 boundary」⇒ 判否 ⇒ mimeParts
// 返回 nil ⇒ snippetFromMIMEParts 返回 "" ⇒ 流程掉到第 2 步，而第 2 步的
// containsMIMESource 又**正确地**认出这是 MIME 源码（它确实是）⇒
// 末尾 return ""。两道闸都没错，错的是第一道闸认不出 boundary 在第二行。
//
// 代价不是一次性显示空白：store.go 的
// `CASE WHEN EXCLUDED.snippet <> ''` 让空串=不覆盖，于是这 32 行一旦
// 落过空摘要就**永久空白**，重跑多少次 backfill 都填不上。
//
// ## 这条夹具为什么带 %q 逐行断言
//
// 夹具本身必须先自证「我造的形态 = 实测形态」，否则护栏会在自造的输入上
// 空转 —— 这条文件已经因此返工过一次（见上面 `  ` 的那段）。
// TestSynthMultipartFixtureMatchesMeasuredShape 就是这个自证。
//
// 负控：把 mimeParts 里「started 之前跳过空行」删掉，本文件必须转红。

import (
	"strings"
	"testing"
)

// synthMultipartBodyText 造一份 BODY[TEXT] 形态的 multipart 分片。
//
// ⚠️ 三个曾经写错、每个都会让护栏在自造形态上空转的点：
//
//  1. boundary 行**不含冒号**，内层头在**独立的下一行**。
//     `isBoundaryLine` 要求 `!Contains(line, ":")`。
//  2. 头部分**不参与 QP 编码**（MIME 头是 7bit ASCII，只有 body 才编码）。
//     整体 qpEncodeBody 会把换行变成 `=0D=0A`，boundary 行随之消失。
//  3. boundary 前面有一个**空行**（实测形态），不是前导空白。
func synthMultipartBodyText(leadBlankLines int, boundary string) []byte {
	html := strings.Join([]string{
		`<!DOCTYPE html><html><body><table>`,
		`<tr><td>占位正文甲</td></tr>`,
		`<tr><td>占位正文乙</td></tr>`,
		`</table></body></html>`,
	}, "\n")
	lines := make([]string, 0, leadBlankLines+6)
	for i := 0; i < leadBlankLines; i++ {
		lines = append(lines, ``)
	}
	lines = append(lines,
		boundary,
		`Content-Type: text/html; charset="UTF-8"`,
		`Content-Transfer-Encoding: quoted-printable`,
		``,
		qpEncodeBody(html),
		boundary+`--`,
		``,
	)
	return []byte(strings.Join(lines, "\n"))
}

// 两种实测 boundary：163 的 _Part_ 形态与 QQ 企业邮的 _mimepart_ 形态。
const (
	diagBoundaryPart     = `------=_Part_21554049_1801402642.1790152695491`
	diagBoundaryMimePart = `----==_mimepart_6ab9d1503b4e7_e511d8145119`
)

func TestSnippetDerive_BodyTextBoundaryOnSecondLine(t *testing.T) {
	cases := []struct {
		name     string
		blanks   int
		boundary string
	}{
		{"无前导空行", 0, diagBoundaryPart},
		{"一个前导空行", 1, diagBoundaryPart},
		{"两个前导空行", 2, diagBoundaryPart},
		{"mimepart 形态 + 空行", 1, diagBoundaryMimePart},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := synthMultipartBodyText(tc.blanks, tc.boundary)
			got := DeriveSnippet(raw, 500)

			parts := mimeParts(raw)
			if len(parts) == 0 {
				t.Fatalf("前导空行=%d：mimeParts 拆出 0 个 part。\n"+
					"前 4 行原始形态：%q", tc.blanks, diagHeadLines(string(raw), 4))
			}
			for i, p := range parts {
				msg, perr := ParseMIMEMessage(p)
				if perr != nil {
					t.Fatalf("part[%d] %d 字节 解析失败：%v（首 90：%q）",
						i, len(p), perr, headOf(string(p), 90))
				}
				if containsMIMESource(msg.HTMLBody) || containsMIMESource(msg.TextBody) {
					t.Fatalf("part[%d] 的正文里还带着 MIME 源码 —— 说明结束 boundary "+
						"没被识别、被并进 part 了。TextBody=%d HTMLBody=%d 首 90：%q",
						i, len(msg.TextBody), len(msg.HTMLBody),
						headOf(msg.TextBody+msg.HTMLBody, 90))
				}
			}
			if got == "" {
				t.Fatalf("前导空行=%d：DeriveSnippet 返回空串（%d 字节输入）。\n"+
					"注意这不是「安全降级」：空串在 store.go 里=不覆盖，该行会永久空白。",
					tc.blanks, len(raw))
			}
			if !strings.Contains(got, "占位正文") {
				t.Fatalf("前导空行=%d：摘要里没有正文（%d 字符）：%q",
					tc.blanks, len([]rune(got)), clipRunes(got, 160))
			}
			t.Logf("空行=%d %s → %d 字符：%s",
				tc.blanks, diagShort(tc.boundary), len([]rune(got)), clipRunes(got, 60))
		})
	}
}

// TestSynthMultipartFixtureMatchesMeasuredShape 是**判据的判据**：
// 夹具的首行必须是空行、boundary 必须落在第二/三行，且 boundary 不带
// 前导空白。任何一条不符，本文件的所有用例都在自造的形态上空转。
func TestSynthMultipartFixtureMatchesMeasuredShape(t *testing.T) {
	raw := string(synthMultipartBodyText(1, diagBoundaryPart))
	lines := strings.Split(raw, "\n")
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("夹具首行不是空行：%q —— 实测形态是首行为空", lines[0])
	}
	if lines[1] != diagBoundaryPart {
		t.Fatalf("夹具第二行不是实测 boundary：%q（应为 %q）", lines[1], diagBoundaryPart)
	}
	if lines[2] != `Content-Type: text/html; charset="UTF-8"` {
		t.Fatalf("夹具第三行不是实测的内层头：%q", lines[2])
	}
	if strings.HasPrefix(lines[1], " ") || strings.HasPrefix(lines[1], "\t") {
		t.Fatal("夹具 boundary 行带前导空白 —— 实测没有，那是 t.Logf 的渲染缩进")
	}
	// 结束 boundary 必须落在倒数第二行且以 -- 结尾。
	tail := strings.TrimRight(raw, "\n")
	endLine := tail[strings.LastIndex(tail, "\n")+1:]
	if endLine != diagBoundaryPart+"--" {
		t.Fatalf("夹具结束 boundary 不对：%q", endLine)
	}
	// 输入必须被 isUnsanitized 判为脏，否则护栏会在「输入没污染」的空态上变绿。
	if !isUnsanitized(raw) {
		t.Fatal("夹具未被 isUnsanitized 判为脏 —— 护栏会在无污染输入上变绿")
	}
	t.Logf("夹具形态自证通过：%s", diagHeadLines(raw, 4))
}

// 负控：正文里缩进过的 markdown 水平线**不能**被当成 boundary 切碎。
// 只跳空行、不放宽 isBoundaryLine 是这个约束的前提。
func TestMimeParts_DoesNotSplitIndentedMarkdownRule(t *testing.T) {
	raw := []byte("\n" +
		"------=_Part_1_1\n" +
		"Content-Type: text/plain; charset=\"UTF-8\"\n" +
		"\n" +
		"第一段\n" +
		"  ---\n" +
		"第二段\n")
	parts := mimeParts(raw)
	if len(parts) != 1 {
		t.Fatalf("缩进的 `  ---` 被当成了 boundary，拆出 %d 个 part（应为 1）", len(parts))
	}
	got := string(parts[0])
	if !strings.Contains(got, "第一段") || !strings.Contains(got, "第二段") {
		t.Fatalf("part 内容被切碎了：%q", clipRunes(got, 120))
	}
}

// 负控：首部空行之后若**不是** boundary，就必须判「不是分片」，
// 不能因为「前面有空行」就把它当分片起点。
func TestStartsWithBoundaryLine_RejectsNonBoundaryAfterBlank(t *testing.T) {
	for _, s := range []string{
		"\n\n第一段正文\n第二段正文\n",
		"\n",
		"",
	} {
		if startsWithBoundaryLine(s) {
			t.Fatalf("startsWithBoundaryLine(%q) = true，应当为 false", s)
		}
	}
}

// 负控：boundary 之后的空行属于正文，**不能**被吞掉（吞掉会让 part 变形）。
func TestMimeParts_KeepsBlankLinesAfterBoundary(t *testing.T) {
	raw := []byte("\n------=_Part_1_1\nContent-Type: text/plain; charset=\"UTF-8\"\n\n" +
		"甲\n\n乙\n------=_Part_1_1--\n")
	parts := mimeParts(raw)
	if len(parts) != 1 {
		t.Fatalf("拆出 %d 个 part（应为 1）", len(parts))
	}
	if !strings.Contains(string(parts[0]), "甲\n\n乙") {
		t.Fatalf("part 里的空行被吞了：%q", clipRunes(string(parts[0]), 160))
	}
}

// diagHeadLines 取前 n 行，用 %q 渲染（保留 \n 的可见性）。
// 刻意不把换行 replaceAll 掉 —— 那正是把「首行空、boundary 在第二行」
// 读成「同一行」的原因。
func diagHeadLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ⏎ ")
}

func diagShort(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}
