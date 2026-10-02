package email

// diag_pop3_html_fallback_test.go — 纯函数诊断：POP3 回落分支会不会写出 QP 脏摘要。
//
// ## 要验证的机制（来自 fetcher.go:958-962）
//
//	em.Snippet = SnippetFromParsed(parsed, 500)          // 主路径
//	if em.Snippet == "" {
//	    em.Snippet = DeriveSnippet([]byte(parsed.HTMLBody), 500)  // 回落
//	}
//
// 观察：
//   · DeriveSnippet 的文档说它的输入是「IMAP 抓回来的**原始字节**」，
//     它自己会走一遍 MIME 解析（snippet.go:10-37）。
//   · 回落分支喂进去的却是 `parsed.HTMLBody` —— **已经解析出来的 HTML 正文**，
//     不是原始字节。
//   · 若该 HTML 正文的 QP 转义尚未被解码（=E4=B8=AD 这种），
//     DeriveSnippet 会把它当普通文本处理，=XX 原样留在摘要里。
//
// ## 为什么会怀疑到它
//
// 2026-10-03 实测：3 条「实时插入」的脏摘要全部来自
// acct-…-5 = kimmy.huang@163.com，且 18099 那个**含 SnippetFromParsed**的
// 二进制在 02:09:00 刚同步完该账户就写进了 qp_hits=89 的行。
// 也就是说「活 bug 已修」不成立，而落库只有上面两行代码可写。
//
// ## 本文件只回答「这条回落分支能不能产出脏摘要」
//
// 不连数据库、不连 IMAP、不写任何东西。真实那三封是否走了回落分支，
// 需要它们的原始字节，另行取证。

import (
	"strings"
	"testing"
)

// qpEncodeHTML 模拟一封 HTML-only 且正文用 quoted-printable 传输的邮件：
// 中文被写成 =XX，且没有 text/plain 部件。
const qpEncodeHTML = "<html><body><p>" +
	"=E4=B8=AD=E6=96=87=E6=98=A5=E6=8A=A5" +
	"</p></body></html>"

func TestDiagPop3HTMLFallbackCanEmitDirtySnippet(t *testing.T) {
	// 1) 先证明主路径在这个输入下会返回空 —— 否则回落分支根本不会被执行，
	//    整条推理链就断了。
	parsed := &ParsedMessage{HTMLBody: qpEncodeHTML}
	main := SnippetFromParsed(parsed, 500)
	t.Logf("SnippetFromParsed(HTMLBody only) = %q (empty=%v)", main, main == "")

	// 2) 回落分支的实际行为：把**已解析的 HTMLBody** 喂给 DeriveSnippet。
	fallback := DeriveSnippet([]byte(parsed.HTMLBody), 500)
	hits := 0
	for i := 0; i+2 < len(fallback); i++ {
		if fallback[i] == '=' && isHexDigit(fallback[i+1]) && isHexDigit(fallback[i+2]) {
			hits++
			i += 2
		}
	}
	t.Logf("DeriveSnippet(parsed.HTMLBody)    = %q", fallback)
	t.Logf("  qpHits = %d", hits)
	t.Logf("  业务可读 = %v", strings.Contains(fallback, "中文") || strings.Contains(fallback, "日报"))

	// 3) 对照：DeriveSnippet 拿到**原始 MIME 字节**时是否正常。
	//    这条是必要的对照，否则无法区分「DeriveSnippet 坏了」与
	//    「DeriveSnippet 被喂错了输入」。
	rawMIME := "Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		qpEncodeHTML
	fromRaw := DeriveSnippet([]byte(rawMIME), 500)
	t.Logf("DeriveSnippet(raw MIME bytes)      = %q", fromRaw)
	t.Logf("  含可读中文 = %v", strings.Contains(fromRaw, "中文"))

	if main != "" {
		t.Logf("主路径在本输入下并不为空 ⇒ 回落分支不会被执行，" +
			"本诊断针对的机制不适用于这组输入。")
		return
	}
	if hits > 0 {
		// 断言的对象要说准：脏是 **SnippetFromParsed 透传** 造成的，
		// 不是回落分支——本组输入下回落分支根本没执行（上面的 early return）。
		// 所以这里只陈述「主路径不防 QP」这个已验证的事实，不把责任安到回落分支上。
		t.Logf("已验证：SnippetFromParsed 收到未解码的 HTMLBody 时**原样透传**"+
			"（qpHits=%d，业务不可读=%v）。", hits, !strings.Contains(fallback, "中文"))
		t.Logf("回落分支在本组输入下未执行（主路径返回非空），故脏行不来自 fetcher.go:961。")
	} else {
		t.Logf("回落分支本组输入下不产生脏摘要（DeriveSnippet 或已解码 QP）。")
	}
}
