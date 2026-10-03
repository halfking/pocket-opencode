package email

// diag_live_snippet_replay_test.go — 诊断：拿**真库里真实报文的原文**，
// 逐条重放生产取摘要管线，判断当前代码是否还会产出原始 MIME 转储。
//
// ## 为什么需要这个诊断
//
// 2026-10-03 晚，真库 emails 表里 snippet 含 MIME 结构的行有 18~34 条
// （口径不同）。但这批行的 updated_at **最新只到 19:01:43**，而当轮
// 声称「DeriveSnippet 已修好」的判断是在 21:13 之前的构建上做的 ——
// 「修复之后还有没有新泄漏」**从未被观测过**，而期间同步一直在跑、
// 泄漏行数从 10 涨到 18。
//
// 只看库里的存量无法区分两种相反结论：
//
//	A. 修复已生效，剩下的是历史脏数据（问题在「治存量」）；
//	B. 修复没生效，当前代码仍在产出脏数据（问题在 DeriveSnippet）。
//
// ## 这个诊断曾经自己骗了自己（重要）
//
// 第一版重放只喂了**完整 RFC 5322 报文**（正文缓存里存的那份），
// 于是报出「LEAK 0 / CLEAN 9」的乐观结论。而那 9 封的存量摘要**恰恰
// 是坏的**。原因是两个输入不是一回事：
//
//   · 正文缓存里的是 **BODY[] 完整报文**（带顶层头），ParseMIMEMessage
//     能正常拆出 text/plain，一切干净；
//   · 生产路径 fetcher.go:543 / backfill.go:270 喂给 DeriveSnippet 的
//     是 **BODY[TEXT]**：multipart 邮件的 BODY[TEXT] 是「去掉顶层头
//     之后的整个 body 部分」，boundary 行、各 part 的 Content-* 头、
//     quoted-printable 原文全在里面。
//
// 补上 BODY[TEXT] 这一路之后，同一批样本 **9/9 全部泄漏**。结论反转。
//
// **判据必须跑在真正会被返回给用户的那份输入上** —— 这条与本仓此前
// 在 containsMIMESource 上踩的坑是同一条，只是方向相反：那次是判据
// 跑错了对象（对压平后的串用行锚点），这次是我**根本没喂那个对象**。
//
// ## 本诊断回答什么
//
// 对每个样本打印三个出口：
//   · main       = SnippetFromParsed(ParseMIMEMessage(完整报文))  （fetcher.go:957）
//   · fallback   = 主出口为空时 DeriveSnippet(parsed.HTMLBody)     （fetcher.go:961）
//   · fromRaw    = DeriveSnippet(完整报文)
//   · BODY[TEXT] = DeriveSnippet(重建的分片)  ← **生产摘要真正吃的那一个**
//
// 统一用 containsMIMESource 判「是否还是 MIME 转储」，不用
// 「有没有 Content-Type:」—— 后者会把「正文可读、只粘了尾部 boundary」
// 这一类误报，而那一类与整段转储必须分开看。
//
// ## 门控
//
//	POCKET_DIAG_RAWBYTES_DUMP=1        复用现成的解密装置
//	POCKET_DIAG_RAWBYTES_OUT=<目录>     已解密的 .eml 所在目录
//
// 前置：先用 TestDiagRawBytesDump 把正文缓存解密到该目录。
// 只读：不连 PG、不连 IMAP、不写库。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDiagLiveSnippetReplay 重放真实报文，对比库里存量摘要与当前代码的产出。
func TestDiagLiveSnippetReplay(t *testing.T) {
	if os.Getenv("POCKET_DIAG_RAWBYTES_DUMP") != "1" {
		t.Skip("set POCKET_DIAG_RAWBYTES_DUMP=1 and POCKET_DIAG_RAWBYTES_OUT=<dir>")
	}
	dir := os.Getenv("POCKET_DIAG_RAWBYTES_OUT")
	if dir == "" {
		t.Fatal("POCKET_DIAG_RAWBYTES_OUT 未设置")
	}
	names := replaySamples(dir)
	leaky, clean := 0, 0
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n+".eml"))
		if err != nil {
			t.Logf("%s: 无原文样本（%v）", n, err)
			continue
		}
		parsed, perr := ParseMIMEMessage(raw)
		if perr != nil {
			t.Logf("%s: ParseMIMEMessage 失败：%v", n, perr)
			continue
		}
		main := SnippetFromParsed(parsed, 500)
		fallback := ""
		if strings.TrimSpace(main) == "" {
			fallback = DeriveSnippet([]byte(parsed.HTMLBody), 500)
		}
		fromRaw := DeriveSnippet(raw, 500)
		// ★ 生产摘要实际吃的输入，见文件头「这个诊断曾经自己骗了自己」。
		bodyText := DeriveSnippet(bodyTextSection(raw), 500)

		mainLeak := containsMIMESource(main)
		fbLeak := containsMIMESource(fallback)
		rawLeak := containsMIMESource(fromRaw)
		bodyTextLeak := containsMIMESource(bodyText)
		if mainLeak || fbLeak || rawLeak || bodyTextLeak {
			leaky++
		} else {
			clean++
		}
		verdict := "CLEAN"
		if mainLeak || fbLeak || rawLeak || bodyTextLeak {
			verdict = "LEAK "
		}
		t.Logf("%s %s main=[leak=%v] %q", verdict, n, mainLeak, clipRunes2(main, 90))
		if strings.TrimSpace(main) == "" {
			t.Logf("        fallback=[leak=%v] %q", fbLeak, clipRunes2(fallback, 90))
		}
		t.Logf("        fromRaw   =[leak=%v] %q", rawLeak, clipRunes2(fromRaw, 90))
		t.Logf("        BODY[TEXT]=[leak=%v] %q", bodyTextLeak, clipRunes2(bodyText, 90))
	}
	t.Logf("==== 当前代码重放结果：LEAK %d / CLEAN %d ====", leaky, clean)
}

// TestDiagBodyTextPipelineWalk 逐出口走一遍 BODY[TEXT] 分片，定位泄漏是
// 从 DeriveSnippet 的**哪一步**被放出来的。
func TestDiagBodyTextPipelineWalk(t *testing.T) {
	if os.Getenv("POCKET_DIAG_RAWBYTES_DUMP") != "1" {
		t.Skip("set POCKET_DIAG_RAWBYTES_DUMP=1 and POCKET_DIAG_RAWBYTES_OUT=<dir>")
	}
	dir := os.Getenv("POCKET_DIAG_RAWBYTES_OUT")
	raw, err := os.ReadFile(filepath.Join(dir, "em-1298896153-acct-1790870162079171800-5.eml"))
	if err != nil {
		t.Skipf("无样本：%v", err)
	}
	body := bodyTextSection(raw)
	t.Logf("BODY[TEXT] 分片长度 = %d", len(body))
	t.Logf("分片开头 = %q", clipRunes2(string(body), 160))

	// mimeCandidates 原有两条路（剥首行 boundary）在这里解出什么，
	// 用来证明「第 1 步拒收是对的、拒的却是正确答案」。
	cands := mimeCandidates(body)
	t.Logf("mimeCandidates 产出 %d 个候选", len(cands))
	for i, c := range cands {
		t.Logf("  候选[%d] 首行 = %q", i, clipRunes2(firstLineOf(c), 90))
		msg, perr := ParseMIMEMessage(c)
		if perr != nil {
			t.Logf("  候选[%d] ParseMIMEMessage 失败：%v", i, perr)
			continue
		}
		t.Logf("  候选[%d] 解析成功：TextBody(%d runes, leak=%v) HTMLBody(%d runes, leak=%v)",
			i, len([]rune(msg.TextBody)), containsMIMESource(msg.TextBody),
			len([]rune(msg.HTMLBody)), containsMIMESource(msg.HTMLBody))
		if containsMIMESource(msg.TextBody) {
			for j, line := range strings.Split(msg.TextBody, "\n") {
				if reMIMEHeaderLine.MatchString(line) || reBoundaryLine.MatchString(line) {
					t.Logf("        内层 MIME 证据 TextBody 行[%d] = %q", j, clipRunes2(line, 100))
				}
			}
		}
	}

	// 新增的按 part 拆分路径拿到的结果。
	t.Logf("mimeParts 拆出 %d 个 part", len(mimeParts(body)))
	t.Logf("snippetFromMIMEParts = %q", clipRunes2(snippetFromMIMEParts(body), 120))
	t.Logf("DeriveSnippet(分片) 最终 = %q", clipRunes2(DeriveSnippet(body, 500), 120))
}

// TestDiagWhichSubPredicate 回答：解析出来的 TextBody 上，
// containsMIMESource 到底是被**哪一条**子判据打红的。
//
// 已知结论：那次判据**没有误判** —— TextBody 里确实混进了内层 part 的
// Content-* 头。本诊断把「命中的是哪一条、命中了什么文本」固定下来，
// 免得以后有人再把它当成误判而去放宽判据（那会放走真泄漏）。
func TestDiagWhichSubPredicate(t *testing.T) {
	if os.Getenv("POCKET_DIAG_RAWBYTES_DUMP") != "1" {
		t.Skip("set POCKET_DIAG_RAWBYTES_DUMP=1 and POCKET_DIAG_RAWBYTES_OUT=<dir>")
	}
	dir := os.Getenv("POCKET_DIAG_RAWBYTES_OUT")
	raw, err := os.ReadFile(filepath.Join(dir, "em-1298896153-acct-1790870162079171800-5.eml"))
	if err != nil {
		t.Skipf("无样本：%v", err)
	}
	cands := mimeCandidates(bodyTextSection(raw))
	msg, perr := ParseMIMEMessage(cands[len(cands)-1])
	if perr != nil {
		t.Skipf("候选解析失败：%v", perr)
	}
	tb := msg.TextBody
	t.Logf("TextBody %d runes", len([]rune(tb)))
	t.Logf("  looksLikeMIME          = %v", looksLikeMIME(tb))
	t.Logf("  reMIMEHeaderLine 命中  = %v 片段 %q", reMIMEHeaderLine.MatchString(tb), reMIMEHeaderLine.FindString(tb))
	t.Logf("  reBoundaryLine   命中  = %v 片段 %q", reBoundaryLine.MatchString(tb), reBoundaryLine.FindString(tb))
	t.Logf("  reMIMEHeaderToken 命中 = %v 片段 %q", reMIMEHeaderToken.MatchString(tb), reMIMEHeaderToken.FindString(tb))
	t.Logf("  reBoundaryToken   命中 = %v 片段 %q", reBoundaryToken.MatchString(tb), reBoundaryToken.FindString(tb))
	for i, line := range strings.Split(tb, "\n") {
		if reMIMEHeaderLine.MatchString(line) || reBoundaryLine.MatchString(line) {
			t.Logf("  行[%d] = %q", i, clipRunes2(line, 120))
		}
	}
}

// replaySamples 挑出要重放的样本：IMAP 路径（em-<uid>-<acct>）。
// POP3 路径（em-pop3-…）由 diag_pop3_html_fallback_test.go 覆盖，
// 避免两份诊断重复报告同一批。
func replaySamples(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "em-") && !strings.HasPrefix(n, "em-pop3-") {
			names = append(names, strings.TrimSuffix(n, ".eml"))
		}
	}
	sort.Strings(names)
	return names
}

// bodyTextSection 重建 IMAP 对这封邮件的 BODY[TEXT] 响应体：
// 报文第一个空行（\r\n\r\n 或 \n\n）之后的全部内容，顶层头被切掉。
func bodyTextSection(raw []byte) []byte {
	s := string(raw)
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		return []byte(s[i+4:])
	}
	if i := strings.Index(s, "\n\n"); i >= 0 {
		return []byte(s[i+2:])
	}
	return nil
}

func firstLineOf(b []byte) string {
	s := string(b)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// clipRunes2 截断长字符串，避免日志被单条样本刷掉。
// （clipRunes 已被 snippet_flattened_part_regression_test.go 占用。）
func clipRunes2(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
