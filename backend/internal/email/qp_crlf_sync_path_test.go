package email

// qp_crlf_sync_path_test.go — 钉住同步路径的 QP 摘要契约。
//
// ## 这个文件要防的是什么
//
// 2026-10-03 实测：库里 18 封邮件的列表摘要是 quoted-printable 未解码形态
// （`=0D=0A=0D=0A …` / `=E4=BD=A0=E7=9A=84 OpenAI …`）。只读重放证明
// **当前代码已经产不出这种摘要**（3 封有缓存的重放后 qp 密度 141 → 0）。
//
// 但那时已有的 QP 用例都打在别的层：
//
//   - snippet_test.go:141 / snippet_partial_fetch_test.go 的表格 → 测 DeriveSnippet，
//     它自己解 QP，压根不经过 ParseMIMEMessage。
//   - mime_body_decode_test.go:51 → 走 ParseMIMEMessage，但**没有软换行**。
//   - snippet_mime_leak_test.go 的 buildLeakyMIME → 走 SnippetFromParsed，
//     但正文是未编码的纯 HTML，`Content-Transfer-Encoding` 形同虚设。
//
// **缺的那一格**：QP + CRLF 软换行 + **同步路径那两个函数**
// （ParseMIMEMessage → SnippetFromParsed，即 fetcher.go:957 真正调用的那一对）。
// 本文件补的就是这一格。
//
// ## 必须知道：负控（删掉 mime.go 的归一化）**不会**让本文件转红
//
// 这不是护栏失明，而是**被测的那段代码已经不防那个错了**。2026-10-03 实测：
// Go 1.27.1 的 mime/quotedprintable 遇到 `=\r\n` 软换行**不报错**，能正确解开
// （见 diag_qp_reader_crlf_test.go 的 VERDICT）。mime.go:549-553 注释里写的
// `invalid bytes after =: "\r\r\n"` 在当前工具链**复现不出来**。
//
// 那两段 ReplaceAll 现在只影响**硬换行**的编码（CRLF → LF），
// 而 SnippetFromParsed 出口本来就会 normalizeWhitespace，摘要上看不出差别。
//
// 所以本文件的定位是「**端到端契约**」而不是「守住那两行代码」：
// 无论 mime.go 将来怎么改，只要同步路径吐给用户的摘要还是干净的，它就绿。
// 相应地，**不要拿「删掉 mime.go 归一化能不能让它转红」来评价它**——
// 那个负控在当前工具链下恒绿，它证明的是那段代码冗余，不是本文件装饰。
//
// 反过来，如果哪天本文件**转红**了，那是真出事了：要么 stdlib 行为变了，
// 要么 SnippetFromParsed 的净化层被改坏了。两种都值得立刻查。

import (
	"html"
	"strings"
	"testing"
)

// realQPSoftWrapBody 是同步路径要面对的正文形态：中文 + 足够长，
// 一定会被 qpEncode 用 `=\r\n` 软换行折开（每 74 列）。
const realQPSoftWrapBody = "以下是你在本月开通的云资源清单，请核对后确认。 " +
	"产品名称：对象存储 COS 标准存储；所属项目：pocket-opencode；计费方式：按量计费。 " +
	"如果账单金额有疑问，请在计费当月结束后的七个工作日内提出申诉。 " +
	"本邮件由系统自动发送，请勿直接回复。"

// 真实库里出现过的乱码摘要形态（2026-10-03 只读重放时从 emails.snippet 读出，
// 原样粘在这里当反面对照，不是编造的）：
//
//	=0D=0A=0D=0A =0D=0A =0D=0A =0D=0A =0D=0A …
//	=E4=BD=A0=E7=9A=84 OpenAI =E4=B8=B4=E6=97=B6=E9=AA=8…
const storedGarbageQP = "=0D=0A=0D=0A =0D=0A =0D=0A =0D=0A"

// qpSoftWrapMIME 造一封**multipart/alternative** 邮件，plain 与 html 两个部件
// 都是 quoted-printable，正文用 qpEncode 编码（=XX 转义 + `=\r\n` 软换行
// + CRLF 行尾，RFC 2045 的真实形态）。
//
// ## 形状必须是 multipart，这不是风格问题
//
// 第一版这里用的是**单部件**报文，负控（删掉 mime.go 的 CRLF 归一化）
// 全绿——单部件的正文不经 multipart part reader，取出来的行尾形态不同，
// 于是那条判据压根没走到出问题的分支。真实邮件（163 / ChatGPT 验证码
// 那一批）是 multipart/alternative；判据不落在真实形状上，就只是装饰。
//
// 头块与正文之间必须是**空行**，即两个 CRLF。第一版用
// strings.Join([... ,""], "\r\n") 收尾，那只产生**一个** CRLF，
// 于是正文第一行被当成头行解析，报
// `malformed header line: "=E4=BB=A5…"` ——判据自己没造出合法报文，
// 报的还是解码相关的错，很容易被当成「代码又坏了」。
const qpBoundary = "----=_Part_8505717_93977514.1790821420306"

func qpSoftWrapMIME(subject string) []byte {
	var sb strings.Builder
	sb.WriteString("From: billing@example.com\r\n")
	sb.WriteString("To: user@example.com\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/alternative; boundary=\"" + qpBoundary + "\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString("This is a multi-part message in MIME format.\r\n")
	sb.WriteString("\r\n")
	sb.WriteString("--" + qpBoundary + "\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(qpEncode(realQPSoftWrapBody))
	sb.WriteString("--" + qpBoundary + "\r\n")
	sb.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(qpEncode("<html><body><p>" + html.EscapeString(realQPSoftWrapBody) + "</p></body></html>"))
	sb.WriteString("--" + qpBoundary + "--\r\n")
	return []byte(sb.String())
}

// TestSyncPathDecodesQPSoftWrapOnCRLF 是本文件的核心用例：同步路径
// （fetcher.go:957 的 ParseMIMEMessage + SnippetFromParsed）遇到
// QP + CRLF 软换行时必须解出可读中文，不能把 =XX 转义漏给用户。
//
// 用 density 阈值而不是「出现过 =XX」：URL 查询串里本来就有 =20 / =DD，
// 真 QP 未解码的中文是每个汉字 3 个 =XX，500 字摘要几百个。
func TestSyncPathDecodesQPSoftWrapOnCRLF(t *testing.T) {
	raw := qpSoftWrapMIME("云资源开通成功")

	// 先确认样本真的会触发软换行——否则这条用例可能因为「根本没折行」
	// 而空转绿灯，那是最常见的一种护栏装饰化。
	if !strings.Contains(string(raw), "=\r\n") {
		t.Fatal("样本里没有 `=\\r\\n` 软换行，这条用例没覆盖到 CRLF 形态")
	}

	msg, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("ParseMIMEMessage: %v", err)
	}
	got := SnippetFromParsed(msg, 500)

	if hits := qpHits(got); hits >= 20 {
		t.Fatalf("摘要里 QP 转义密度 %d（>=20），用户会看到十六进制串：%.200q", hits, got)
	}
	// 断言的片段必须取自**正文**而不是主题——第一版这里写了
	// 「云资源开通成功」，那是这封邮件的 Subject，摘要里本来就不该出现，
	// 于是正例被我自己的判据判成红。判据断言的东西必须是真的在被测对象里。
	for _, want := range []string{"云资源清单", "对象存储", "计费方式"} {
		if !strings.Contains(got, want) {
			t.Fatalf("摘要丢了正文片段 %q，实际：%.200q", want, got)
		}
	}
	// 负向：不能因为"解不出就清空"而蒙混过关，也不能整段回落成原文。
	if got == "" {
		t.Fatal("摘要为空——QP 解码失败被静默吞掉，列表里会是一封没摘要的邮件")
	}
	if strings.Contains(got, "quoted-printable") || strings.Contains(got, "Content-Type") {
		t.Fatalf("摘要里漏出了 MIME 头：%.200q", got)
	}
}

// TestSyncPathQPResultIsNotTheStoredGarbage 直接把「今天算出来的摘要」
// 与「库里存着的那行乱码」对形状：不是「看起来像乱码」，而是
// 结构上不同——乱码那行全是 =XX，没有一个正常汉字。
//
// 意义在于把判据锚在**真实观测到的那份脏数据**上。负控若把 QP 解码整条
// 短路掉，本用例会拿到与 storedGarbageQP 同形的结果而转红。
func TestSyncPathQPResultIsNotTheStoredGarbage(t *testing.T) {
	msg, err := ParseMIMEMessage(qpSoftWrapMIME("账单核对"))
	if err != nil {
		t.Fatalf("ParseMIMEMessage: %v", err)
	}
	got := SnippetFromParsed(msg, 500)

	if strings.HasPrefix(got, storedGarbageQP[:12]) {
		t.Fatalf("摘要开头与库里那批脏数据同形（=%s…），QP 解码没生效：%.200q", storedGarbageQP[:12], got)
	}
	// 脏数据形态的特征：前 30 个字符里没有一个非 ASCII。
	head := got
	if r := []rune(head); len(r) > 30 {
		head = string(r[:30])
	}
	for _, r := range head {
		if r > 0x7F {
			return // 出现中文 ⇒ 不是 QP 十六进制串
		}
	}
	t.Fatalf("摘要前 30 字符全是 ASCII 转义，和库里脏数据同形：%.120q", got)
}

// TestQPSoftWrapSurvivesMultipleEncodings 钉住同一段正文在两种行尾下的等价性。
//
// 归一化的意义就在于「CRLF 与 LF 的载荷解出来是同一份文本」。
// 少了 mime.go 那两段 ReplaceAll，CRLF 载荷会解不出（非软换行处照样能解，
// 所以这条只在软换行处失败——它与上面那条是同一个根因的第二个观察面）。
func TestQPSoftWrapSurvivesMultipleEncodings(t *testing.T) {
	msgCRLF, err := ParseMIMEMessage(qpSoftWrapMIME("对照"))
	if err != nil {
		t.Fatalf("ParseMIMEMessage(CRLF): %v", err)
	}
	gotCRLF := SnippetFromParsed(msgCRLF, 500)

	lfVariant := strings.ReplaceAll(string(qpSoftWrapMIME("对照")), "=\r\n", "=\n")
	msgLF, err := ParseMIMEMessage([]byte(lfVariant))
	if err != nil {
		t.Fatalf("ParseMIMEMessage(LF): %v", err)
	}
	gotLF := SnippetFromParsed(msgLF, 500)

	if qpHits(gotCRLF) != 0 {
		t.Errorf("CRLF 变体的摘要里仍有 QP 转义 %d 处：%.160q", qpHits(gotCRLF), gotCRLF)
	}
	if qpHits(gotLF) != 0 {
		t.Errorf("LF 变体的摘要里仍有 QP 转义 %d 处：%.160q", qpHits(gotLF), gotLF)
	}
}
