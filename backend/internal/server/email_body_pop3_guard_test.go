package server

// POP3 来源邮件的详情正文：raw 缓存读 + IMAP 回源守卫的接线护栏
// （2026-10-03 真机实测抓出）。
//
// ## 实测到的缺陷
//
//	GET /api/emails/em-pop3-…-ZC0003_R0bNl36M6LYuaWoAEWJzn10/body - 502
//	[email/body] imap fetch email=em-pop3-… account=acct-… uid=2
//
// 原文明明在 dataDir/email-bodies-raw/<id>.bin（当场解密出 49 封），但
// handleEmailBody 的缓存读只有一条 readCachedEmailBody，它硬编码读 server 层
// 的 email-bodies/，对 POP3 那份必然未命中 ⇒ 落到 IMAP 回源 ⇒
//
//  1. IMAP 不可用时（实测当时就是）→ 502，详情页只剩摘要；
//  2. IMAP 可用时 → 拿 **POP3 位置序号**当 IMAP UID 去 FETCH，取回**毫不相关
//     的另一封邮件**，而且没有任何报错。invoice_harvest.go 早就为这件事加了
//     isPOP3SourcedEmail 守卫，/invoice/harvest 走对了，这个端点漏了。
//
// 顺带一个后果要说清：POP3 邮件取不到原文 ⇒ 前端的 extractEmailBody 根本没被
// 调用过 ⇒ §4.124 修的那类首屏缺陷在 POP3 邮件上连复现机会都没有。详情页看着
// 「还过得去」只是因为退化到了 snippet，不代表解析器是对的。

import (
	"os"
	"strings"
	"testing"
)

// codeOnlyStripComments 读源码并剥掉行注释，让护栏不被注释里的示例文字满足。
func codeOnlyStripComments(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s：%v", name, err)
	}
	var b strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestHandleEmailBody_POP3RawCacheAndIMAPGuard(t *testing.T) {
	code := codeOnlyStripComments(t, "server_assistant.go")

	// 只看 handleEmailBody 这一段：别的地方有 POP3 相关代码不构成保护。
	start := strings.Index(code, "func (s *Server) handleEmailBody(")
	if start < 0 {
		t.Fatal("找不到 handleEmailBody")
	}
	end := strings.Index(code[start:], "\nfunc ")
	if end < 0 {
		end = len(code) - start
	}
	body := code[start : start+end]

	// 1) 必须读 email-bodies-raw（POP3 原文缓存），否则缓存明明在却取不到。
	if !strings.Contains(body, "email.NewFileBodyCache(") {
		t.Error("handleEmailBody 没有读 email-bodies-raw 的 POP3 原文缓存\n" +
			"⇒ 原文明明在 dataDir/email-bodies-raw/<id>.bin，详情页却会走 IMAP 回源" +
			"（实测 502，IMAP 可用时更会返回另一封邮件的正文）")
	}

	// 2) POP3 守卫必须排在 IMAP FETCH **之前**。守卫写在 FETCH 之后就等于没写：
	//    邮件已经被取回来了，此时再拒绝只是把「返回错邮件」变成「报错但已经发出去过」。
	//
	// 3) 判据取**正向形状**而不是「字符串出现过」。第一版写的是
	//    `strings.Index(body, "IsPOP3SourcedEmailID(em.ID)") >= 0`，负控把守卫改成
	//    `if false && email.IsPOP3SourcedEmailID(em.ID)` —— 字符串照样在，护栏**全绿**，
	//    而实际守卫已经失效。判据必须要求「这个 if 块里真的有 writeError（明确拒绝）」，
	//    因为拒绝动作才是守卫，条件表达式只是它的名字。
	fetchAt := strings.Index(body, "s.emailFetcher.FetchMessageRaw(")
	if fetchAt < 0 {
		t.Fatal("handleEmailBody 里找不到 IMAP 回源调用，护栏需要跟着调整")
	}
	guardAt := -1
	for off := 0; ; {
		i := strings.Index(body[off:], "IsPOP3SourcedEmailID(em.ID)")
		if i < 0 {
			break
		}
		at := off + i
		// 该 if 块之后的一小段里必须有 writeError —— 那才是「拒绝」这个动作
		tail := body[at:]
		if len(tail) > 500 {
			tail = tail[:500]
		}
		if nextIf := strings.Index(tail, "\n\tif "); nextIf < 0 || strings.Contains(tail[:nextIf], "writeError(") {
			guardAt = at
		}
		off = at + 1
	}
	if guardAt < 0 {
		t.Fatal("handleEmailBody 没有会**拒绝请求**的 POP3 守卫：" +
			"仅有条件判断、块里没有 writeError，等于没守 —— POP3 位置序号会被当 IMAP UID 用")
	}
	if guardAt > fetchAt {
		t.Error("POP3 守卫排在 IMAP FETCH 之后 —— 等于没守：正文已经被错误地取回来了")
	}
}

// 判据必须只有一份。让 server 包自己再写一遍 strings.HasPrefix(id, "em-pop3-")
// 就是两份判据，改一处漏一处的后果是「把别人的信显示成这封信」。
func TestPOP3GuardUsesSharedPredicate(t *testing.T) {
	code := codeOnlyStripComments(t, "server_assistant.go")
	if strings.Contains(code, `strings.HasPrefix(em.ID, "em-pop3-")`) ||
		strings.Contains(code, `strings.HasPrefix(emailID, "em-pop3-")`) {
		t.Error("server 包自己抄了一份 em-pop3- 前缀判据；请用 email.IsPOP3SourcedEmailID")
	}
	if !strings.Contains(code, "email.IsPOP3SourcedEmailID(") {
		t.Error("server 包没有调用 email.IsPOP3SourcedEmailID —— 判据失去单一事实源")
	}
}
