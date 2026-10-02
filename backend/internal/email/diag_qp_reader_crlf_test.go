package email

// 诊断：mime.go:561-566 那两段 CRLF 归一化，在**当前 Go 版本**下是否还在防那个错。
// 用法：go test ./internal/email/ -run TestDiagQPReaderCRLF -v
//
// 为什么需要这个文件：2026-10-03 两次负控（删掉归一化）整包都全绿。
// 有两种解释，处置完全不同：
//
//	(a) 归一化确实在保护某条路径，只是我的样本没走到 —— 护栏还需补
//	(b) Go 的 mime/quotedprintable 已经自己认 =CRLF 了，归一化不再防错
//
// ## 2026-10-03 的实测结论：是 (b)，而且这推翻了一个错误归因
//
// Go 1.27.1 的 quotedprintable.Reader 遇到 `=\r\n` **不报错**，软换行被正确
// 去掉、`=E4=BD=A0` 正确解成「你」。mime.go:549-553 注释里写的
// `quotedprintable: invalid bytes after =: "\r\r\n"` **在当前工具链复现不出来**。
//
// 后果要说清楚：上一轮 handoff 写「18 封乱码摘要的根因是 064ce292c 在 mime.go
// 加的 CRLF 归一化」——**这个归因不成立，已更正**。那段代码现在只影响硬换行的
// 编码（CRLF→LF），而摘要出口本来就会 normalizeWhitespace。
// 真正让摘要变干净的是同一提交里的**净化层**（SnippetFromParsed / DeriveSnippet
// 那条线，见 snippet_mime_leak_test.go 的接线护栏），不是这两行。
//
// 本用例的断言是有意的：如果哪天工具链升级后 stdlib 行为变了（比如又开始报错），
// 这里会红，提示该重新评估 mime.go 那两行——**包括它们是否已经变成负担**。

import (
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
)

func TestDiagQPReaderCRLF(t *testing.T) {
	// RFC 2045 软换行：行尾一个 `=`，随后 CRLF。
	payload := "abc=\r\ndef=\r\n=E4=BD=A0=E5=A5=BD\r\n"
	want := "abc" + "def" + "你好"

	got, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(payload)))
	t.Logf("payload=%q", payload)
	t.Logf("err  = %v", err)
	t.Logf("got  = %q", string(got))
	t.Logf("want = %q", want)

	if err != nil {
		t.Logf("VERDICT: 当前 Go 的 quotedprintable.Reader 遇到 =CRLF **报错** " +
			"=> mime.go 的归一化重新变得必要（当初那条注释描述的行为回来了）")
		return
	}
	if !strings.Contains(string(got), "你好") {
		t.Errorf("=E4=BD=A0=E5=A5=BD 没解成中文：%q", string(got))
		return
	}
	if strings.Contains(string(got), "=E4") {
		t.Errorf("转义序列残留在解码结果里：%q", string(got))
	}
	// 软换行 `=\r\n` 必须被去掉（不是留在结果里）。
	if strings.Contains(string(got), "=\r\n") || strings.Contains(string(got), "=\n") {
		t.Errorf("软换行标记残留在解码结果里：%q", string(got))
	}
	t.Logf("VERDICT: 当前 Go 的 quotedprintable.Reader 已经正确处理 =CRLF（err=nil，解出中文）" +
		"=> mime.go:561-566 不再防那个错；18 封乱码摘要**不能**归因于 064ce292c")
}
