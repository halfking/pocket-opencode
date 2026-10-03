package email

// snippet_whole_message_fallback_test.go
//
// 覆盖 2026-10-03 真机实测的这一类邮件：`BODY[TEXT]` 取不到正文时，
// 抓取本身要**补拉一次 `BODY[]`**，而不是把空摘要落库。
//
// ## 这条判据的前提被上游改过一次（2026-10-04 审计实测）
//
// 初版（本文件的第一形态）用的是「外层分片头 + 内层 part 头 + 正文、
// **没有终止分隔行**」，并断言 `DeriveSnippet` 对它返回空串。
// 那个断言在合并 origin/main 之后**不再成立** —— `180a157b` 给
// `DeriveSnippet` 加了第 0 步 `snippetFromMIMEParts`（按 part 拆分、
// 优先 text/plain），它对这一形态**能**取到正文。
//
// 关键在于：**判据红不等于代码坏了。** 这里红的是「用例的前提」，
// 而前提失效的证据是它旁边那条 `TestZZSnippetFallback_Precondition_*`
// 的报错文本（「前置条件不成立：BODY[TEXT] 形态本来就能解析出 …」）。
// 若没有那条自检，用例会以「断言摘要为空」的形式红，而症状会被读成
// 「补拉逻辑坏了」——指向完全相反的方向。
//
// 所以本文件改成**先自证前提、再断言行为**，并且夹具换成
// 「`BODY[TEXT]` 只回了容器、正文压根没发过来」这一形态
// （`zzBodyTextContainerOnly`）：`snippetFromMIMEParts` 对它**仍然**
// 返空，而 `BODY[]` 能取到正文。这才是补拉真正覆盖的场景。
//
// 仍然返空的那两种形态（实测，见 §形状表）：
//   · 容器头 + 内层 part 头 + 空正文（服务端没发正文）
//   · 只有容器头（服务端只发了这么多）
//
// ## 形状表（DeriveSnippet 对各形态的实测读数）
//
//	形态                                    修复前  现在
//	分片头 + 内层 part 头 + 正文（无终止行）    空串    **正文**（180a157b 修好）
//	分片头 + 内层 part 头 + 空正文            空串    空串   ← 本文件钉住这个
//	只有容器头                               空串    空串
//	完整报文 BODY[]                          正文    正文
//
// ## 判据设计
//
// 三条，缺一不可：
//
//  1. **前置自检**：先证明 `BODY[TEXT]` 这一形态确实取不到正文、
//     且整封报文确实取得到。前提不成立就直接 t.Fatalf，
//     **不要**让它以「摘要为空」的形式红。
//  2. **正向 + 协议**：摘要里必须有正文原文，且服务器必须真的收到过
//     `BODY[]`。只看摘要无法区分「补拉了整封」与「碰巧解析出来了」。
//  3. **不为无病取药**：`BODY[TEXT]` 够用时不得再发 `BODY[]`。
//     每封邮件都补一次整封拉取，一轮同步的流量与耗时都会翻倍，
//     而这种浪费在摘要正确的邮件上完全看不出来——所以必须钉住。
//  4. **兜底不放宽**：整封也取不到时不得把 MIME 头当摘要透出去。

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	imapclient "github.com/emersion/go-imap/v2/imapclient"
)

const (
	zzInnerBnd = "----=_Part_397110_1060649035.1790214518883"
	zzOuterBnd = "------=_Part_397111_1624436759.1790214518883"

	// zzInvoiceBody 是这封邮件真正的正文。
	zzInvoiceBody = "尊敬的用户：您的电子发票已开具，价税合计 ￥3500.00，请查收。"

	// zzWholeInvoice 是 `BODY[]` 应回的整封报文。
	zzWholeInvoice = "From: billing@example.invalid\r\n" +
		"To: user@example.invalid\r\n" +
		"Subject: 电子发票\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + zzInnerBnd + "\"\r\n" +
		"\r\n" +
		"--" + zzInnerBnd + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		zzInvoiceBody + "\r\n" +
		"--" + zzInnerBnd + "--\r\n"

	// zzBodyTextContainerOnly 是本文件钉住的形态：
	// `BODY[TEXT]` 只回了「容器分片头 + 内层 part 头」，**正文没发过来**。
	// 真实服务器在首个 part 是 multipart 容器、且正文落在更靠后的位置时
	// 就会回这种前缀。
	zzBodyTextContainerOnly = "--" + zzOuterBnd + "\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + zzInnerBnd + "\"\r\n" +
		"\r\n" +
		"--" + zzInnerBnd + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n"

	// zzBodyTextPlain 是「`BODY[TEXT]` 一次就够」的正常形态：剥掉首行
	// boundary 之后就是一个完整可解析的 text part（snippet_partial_fetch_test.go
	// 的形态 1）。
	zzBodyTextPlain = "--" + zzOuterBnd + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		zzInvoiceBody + "\r\n"
)

// zzSnippetClient 起一个不依赖 PG 的 Fetcher：fetchSnippetOnConnected 只用
// 已建立的连接，不碰 store。新WorkspaceTestStore 需要 POCKET_TEST_POSTGRES_DSN，
// 而这条路径本来就不该依赖库。
func zzSnippetClient(t *testing.T, srv *imapServer) *imapclient.Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", srv.addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := imapclient.New(conn, nil)
	if err := c.Login("u@example.invalid", "pw").Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func zzSnippetServer(t *testing.T, uid int64, bodyText, whole string) *imapServer {
	t.Helper()
	srv := newIMAPServer(t, []testMailbox{{name: "INBOX"}})
	srv.loginOK = true
	srv.bodyByUID = map[int64]string{uid: bodyText}
	if whole != "" {
		srv.wholeByUID = map[int64]string{uid: whole}
	}
	return srv
}

// zzSawWholeMessageFetch 判断服务器是否收到过 `BODY[]`（section spec 为空）
// 的取件请求。
func zzSawWholeMessageFetch(srv *imapServer) bool {
	for _, c := range srv.fetchCmds() {
		if m := bodySectionRe.FindStringSubmatch(c); m != nil && strings.TrimSpace(m[2]) == "" {
			return true
		}
	}
	return false
}

// 前置条件自检：`BODY[TEXT]` 这一形态本身取不到正文，而整封报文取得到。
//
// 这条是本文件里**最要紧**的一条。2026-10-04 审计实测：初版夹具
// （分片头 + 内层 part 头 + 正文、无终止行）在 `180a157b` 之后
// `DeriveSnippet` **已经能解析出来**，于是下面三条断言全部以
// 「摘要为空」的形式红 —— 症状会被读成「补拉逻辑坏了」，
// 而真正的原因是「用例的前提被上游修好了」。
//
// 判据红时先怀疑判据本身：把前提写成可证伪的自检，红的原因就不再有歧义。
func TestZZSnippetFallback_Precondition(t *testing.T) {
	if got := DeriveSnippet([]byte(zzBodyTextContainerOnly), 500); got != "" {
		t.Fatalf("前置条件不成立：BODY[TEXT] 形态本来就能解析出 %q，"+
			"这条用例失去意义（请先确认 snippet.go 的第 0 步是否又改了）", got)
	}
	if got := DeriveSnippet([]byte(zzWholeInvoice), 500); !strings.Contains(got, "价税合计") {
		t.Fatalf("前置条件不成立：整封报文解析不出正文（%q），补拉也救不了", got)
	}
}

// 主判据：BODY[TEXT] 只给出容器前缀时，必须补拉 BODY[] 并拿到正文。
func TestZZSnippetFallback_ContainerOnlyRefetchesWholeMessage(t *testing.T) {
	const uid = 4242
	srv := zzSnippetServer(t, uid, zzBodyTextContainerOnly, zzWholeInvoice)
	f := &Fetcher{}
	snippet, _ := f.fetchSnippetOnConnected(zzSnippetClient(t, srv), imap.UID(uid))

	if !strings.Contains(snippet, "价税合计") {
		t.Fatalf("补拉 BODY[] 之后仍取不到正文：%q", snippet)
	}
	for _, bad := range []string{"_Part_", "Content-Transfer-Encoding", "Content-Type"} {
		if strings.Contains(snippet, bad) {
			t.Errorf("摘要里仍有原始 MIME 痕迹 %q：%q", bad, snippet)
		}
	}
	if !zzSawWholeMessageFetch(srv) {
		t.Errorf("服务器没收到 BODY[] 请求，却给出了正文 —— 取件项：%v", srv.fetchCmds())
	}
}

// 负控边界：BODY[TEXT] 够用时不得补拉整封。
//
// 这条不是「顺手加的」。补拉每封都做的话，一轮同步的网络与耗时翻倍，
// 而浪费发生在摘要本来就正确的邮件上——只断言「摘要对不对」完全看不出来。
func TestZZSnippetFallback_NoRefetchWhenBodyTextSuffices(t *testing.T) {
	const uid = 77
	srv := zzSnippetServer(t, uid, zzBodyTextPlain, zzWholeInvoice)
	f := &Fetcher{}
	snippet, _ := f.fetchSnippetOnConnected(zzSnippetClient(t, srv), imap.UID(uid))

	if !strings.Contains(snippet, "价税合计") {
		t.Fatalf("BODY[TEXT] 形态本就该解析成功，却得到 %q", snippet)
	}
	if zzSawWholeMessageFetch(srv) {
		t.Errorf("BODY[TEXT] 已经够用却补拉了整封报文：%v", srv.fetchCmds())
	}
}

// 负控边界：整封也取不到时不得把 MIME 头当摘要透出去。
//
// 「宁可摘要为空，也不要把 MIME 头转储给用户」是 DeriveSnippet 的既定取舍
// （snippet.go 文件头第 35-36 行）。补拉不能把这个取舍悄悄改成兜底。
func TestZZSnippetFallback_StillEmptyWhenWholeMessageUnusable(t *testing.T) {
	const uid = 909
	// 整封报文也取不到正文（服务端就只发了这么多）。
	srv := zzSnippetServer(t, uid, zzBodyTextContainerOnly, zzBodyTextContainerOnly)
	f := &Fetcher{}
	snippet, _ := f.fetchSnippetOnConnected(zzSnippetClient(t, srv), imap.UID(uid))

	if snippet != "" {
		t.Fatalf("补拉后仍取不到正文时不得返回任何内容，却得到 %q", snippet)
	}
}
