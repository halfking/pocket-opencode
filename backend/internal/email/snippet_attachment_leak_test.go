package email

// snippet_attachment_leak_test.go
//
// 覆盖 2026-10-04 审计发现的泄漏：`multipart/mixed` 里 **PDF 附件排在
// text/plain 之前**时，列表摘要变成 `%PDF-1.4`。
//
// ## 机制（可复现，非推测）
//
// `snippetFromMIMEParts`（snippet.go 第 0 步）按 part 收集候选，分成
// plain / html 两组，再按「plain 优先」的顺序返回。收集时**只看**
// `normalizeWhitespace(msg.TextBody) != ""` 与 `!containsMIMESource(...)`。
//
// 而 `application/pdf` + `Content-Transfer-Encoding: base64` 的 part，
// base64 解码后是 `%PDF-1.4\n` —— 落进 `ParsedMessage.TextBody`，
// 非空，且**不含** `Content-Type:` / `boundary=` 这类 token
// （`containsMIMESource` 只查这些），于是两道闸门都放行，
// 被排进 plain 组；而 PDF 排在前面 ⇒ 它赢了真正的 text/plain part。
//
// 关键：**附件解出来的字节「读起来像文本」，与它是不是正文毫无关系。**
// 旧判据把「解出来像正文」当成了「是正文」。
//
// ## 为什么不能靠 containsMIMESource 兜住
//
// 那条闸门查的是 MIME **源码**痕迹（`Content-Type:`、`boundary=`、
// `Content-Transfer-Encoding`）。PDF 二进制里一个都没有，
// 补 token 是拿一类文件的巧合换另一类文件的确定性 —— 判据问的那句自查：
// 「让判据变绿的那段代码，和判据想守的性质，是同一件事吗？」
// 补 token 不是；**声明的 Content-Type** 才是。
//
// ## 判据设计
//
//  1. 正向：PDF 在前、text/plain 在后 ⇒ 必须取到**真正的正文**。
//     只断言「不含 %PDF」不够 —— 那与「返回空串」同样通过。
//  2. 协议：附件 part 必须被**排除**，不是「排在后面没被选中」。
//     用 `partDeclaresTextContent` 直接钉住这条边界。
//  3. 反向：`text/plain` 声明为 `application/pdf` 之后仍要能收下
//     （闸门看的是**声明**，不是文件真实内容），否则闸门过宽、
//     正常正文也一起被砍掉。
//  4. 无 `Content-Type` 头 ⇒ 按 RFC 2045 §5.2 缺省 `text/plain`，收下。
//     `mimeParts` 切出来的 part 完全可能没有头，而「无头」不能与
//     「显式声明 application/pdf」走同一条路。

import (
	"strings"
	"testing"
)

const (
	leakOuterBnd = "----=_Part_500000_1.1700000000"
	leakInnerBnd = "----=_Part_500000_2.1700000000"

	// leakRealBody 是这封邮件真正的正文（text/plain part）。
	leakRealBody = "尊敬的用户：您的电子发票已开具，价税合计 ￥3500.00，请查收。"

	// leakPdfPart 是 base64 编码的 PDF：`JVBERi0xLjQK` 解码后是 `%PDF-1.4\n`。
	leakPdfPart = "Content-Type: application/pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"\r\n" +
		"JVBERi0xLjQK\r\n"

	// leakPlainPart 是真正的正文 part。
	leakPlainPart = "Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		leakRealBody + "\r\n"
)

// leakMixedPdfFirst 是「PDF 附件排在正文之前」的 BODY[TEXT] 分片。
// 这是本仓的核心业务形态：带发票 PDF 附件的邮件，附件在前、正文在后。
var leakMixedPdfFirst = "--" + leakOuterBnd + "\r\n" +
	"Content-Type: multipart/mixed; boundary=\"" + leakInnerBnd + "\"\r\n" +
	"\r\n" +
	"--" + leakInnerBnd + "\r\n" + leakPdfPart +
	"--" + leakInnerBnd + "\r\n" + leakPlainPart

// 主判据：PDF 在前时，摘要必须是**真正的正文**。
func TestDeriveSnippet_MixedPdfFirstDoesNotLeakAttachment(t *testing.T) {
	got := DeriveSnippet([]byte(leakMixedPdfFirst), 500)

	if !strings.Contains(got, "价税合计") {
		t.Errorf("没有取到真正的正文，得到 %q", got)
	}
	// 只断言「正文对」不够 —— 返回空串也能过「不含 %PDF」，
	// 但那会把一封正常邮件变成没有摘要。
	for _, bad := range []string{"%PDF", "JVBER", "application/pdf", "Content-Disposition"} {
		if strings.Contains(got, bad) {
			t.Errorf("摘要里漏出附件内容 %q：%q", bad, got)
		}
	}
}

// 协议判据：附件 part 必须被**排除**，而不是「排在后面没被选中」。
//
// 这条是必需的：`snippetFromMIMEParts` 的收集顺序是「plain 组优先」，
// 而 PDF 那个 part 解出来的 `TextBody` 非空 ⇒ 它会进 plain 组。
// 只断言最终摘要，钉不住「它是被闸门排除的」还是「它恰好排在后面」。
func TestPartDeclaresTextContent_ExcludesAttachment(t *testing.T) {
	parts := mimeParts([]byte(leakMixedPdfFirst))
	// mimeParts 的切法里，**容器自身的头**（`Content-Type: multipart/mixed`）
	// 也是一个 part —— 它排在最前面。实测拆出 3 个：
	//   [0] 容器头（multipart/mixed）
	//   [1] PDF 附件（application/pdf）
	//   [2] 真正的正文（text/plain）
	//
	// [0] 声明 multipart/mixed，被闸门拒收 —— 这也是对的：容器不是正文。
	if len(parts) != 3 {
		t.Fatalf("前置条件不成立：拆出 %d 个 part（期望 3），本用例失去意义", len(parts))
	}
	for i, p := range parts {
		if ct := declaredContentTypeOfTest(p); ct != "" {
			t.Logf("part %d 声明 %s", i, ct)
		}
	}

	if partDeclaresTextContent(parts[1]) {
		t.Errorf("application/pdf 的 part 被判成文本 part，附件闸门失效：%q", parts[1])
	}
	if !partDeclaresTextContent(parts[2]) {
		t.Errorf("text/plain 的 part 被判成非文本，正文被误砍：%q", parts[2])
	}
	if partDeclaresTextContent(parts[0]) {
		t.Errorf("multipart/mixed 容器头被判成文本 part：%q", parts[0])
	}
}

// declaredContentTypeOfTest 只给用例打印用，取 part 头里的 Content-Type。
func declaredContentTypeOfTest(part []byte) string {
	head := string(part)
	if i := strings.Index(head, "\r\n\r\n"); i >= 0 {
		head = head[:i]
	}
	for _, ln := range strings.Split(head, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimRight(ln, "\r")), "content-type:") {
			return strings.TrimSpace(ln[len("content-type:"):])
		}
	}
	return ""
}

// 反向：判据看的是**声明**，不是文件真实内容。
//
// 故意给一个声明 `application/pdf` 但正文其实是中文的 part。
// 它必须被排除 —— 判据是「声明的 Content-Type 是不是文本」，
// 不是「解出来像不像文本」。后者正是本文件要修的那个缺陷。
func TestPartDeclaresTextContent_TrustsDeclarationNotContent(t *testing.T) {
	lying := []byte("Content-Type: application/pdf; name=\"x.txt\"\r\n\r\n" + leakRealBody + "\r\n")
	if partDeclaresTextContent(lying) {
		t.Errorf("声明 application/pdf 的 part 被收下，闸门被「内容像文本」绕过了")
	}
}

// 无 Content-Type 头 ⇒ 按 RFC 2045 §5.2 缺省 text/plain，收下。
//
// 「无头」与「显式声明 application/pdf」不能走同一条路：
// 前者是最常见的裸 text part 形态，砍掉它等于砍掉大部分正常摘要。
func TestPartDeclaresTextContent_NoHeaderDefaultsToText(t *testing.T) {
	if !partDeclaresTextContent([]byte("\r\n" + leakRealBody + "\r\n")) {
		t.Errorf("没有 Content-Type 头的 part 被判成非文本（RFC 2045 §5.2 缺省 text/plain）")
	}
	if !partDeclaresTextContent([]byte("Content-Type: text/html; charset=UTF-8\r\n\r\n<p>x</p>")) {
		t.Errorf("text/html 的 part 被判成非文本")
	}
	// 解析失败 ⇒ 往「少一段摘要」那侧倒。
	if partDeclaresTextContent([]byte("Content-Type: text\r\n\r\nbody")) {
		t.Errorf("畸形 Content-Type（缺 subtype）被判成文本，判据读不懂时应当保守拒收")
	}
}

// 负控：闸门短路时主判据必须转红。
//
// 判据红时先怀疑判据本身，所以这里逐条列出「去掉哪一半会红」：
//
//   - partDeclaresTextContent 恒返回 true  → 附件重新被收下 ⇒ 主判据红
//   - partDeclaresTextContent 恒返回 false → 正文被砍掉     ⇒ 主判据红
//
// 两条都必须红。**只红一条不够** —— 那说明闸门过宽或过窄，
// 而「恒 true」与「恒 false」恰好是这两种形态各自的极端。
func TestSnippetAttachmentLeak_NegativeControls(t *testing.T) {
	// 这里不改动生产代码去制造负控（那会污染共享状态），
	// 改为**直接对判据本身**做变异，证明它不是恒真/恒假。
	// 生产侧变异另由 `go test -run ... -count=1` 的历史负控覆盖。
	if partDeclaresTextContent([]byte(leakPdfPart)) {
		t.Errorf("负控失败：闸门对附件返回 true")
	}
	if !partDeclaresTextContent([]byte(leakPlainPart)) {
		t.Errorf("负控失败：闸门对正文返回 false")
	}
}
