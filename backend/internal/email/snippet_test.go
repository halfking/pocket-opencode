package email

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// 下面两段是 2026-10-01 真机上真实出现过的形态（/notifications 的正文
// 原样截取），不是构造的最小用例。

const realHTMLSnippet = `您的额度即将用尽，当前剩余额度为 ¥0.002116，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='https://u.syapi.cn/console/topup'>https://u.syapi.cn/console/topup</a>`

const realMIMEDump = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=UTF-8
Content-Transfer-Encoding: quoted-printable

=E9=87=8D=E8=A6=81=E9=82=AE=E4=BB=B6=EF=BC=9A=E6=82=A8=E7=9A=84=E9=A2=9D=E5=BA=A6
=E5=8D=B3=E5=B0=86=E7=94=A8=E5=AE=8C=EF=BC=8C=E8=AF=B7=E5=8D=B3=E6=97=B6=E5=85=85=E5=80=BC=E3=80=82
`

func TestDeriveSnippet_HTMLOnly_NoTagsLeak(t *testing.T) {
	// 只有 HTML 正文时，标签不能原样透出——真机上用户看到的是字面的 <br/> 与 <a href=…>
	got := DeriveSnippet([]byte(realHTMLSnippet), 500)
	if strings.Contains(got, "<br") || strings.Contains(got, "<a ") || strings.Contains(got, "</a>") {
		t.Fatalf("HTML 标签泄漏到摘要：%q", got)
	}
	if !strings.Contains(got, "额度即将用尽") {
		t.Fatalf("正文文字丢了：%q", got)
	}
	// 链接的可读文本要留下（href 的值本身也是有用信息）
	if !strings.Contains(got, "https://u.syapi.cn/console/topup") {
		t.Fatalf("链接文本丢了：%q", got)
	}
}

func TestDeriveSnippet_PureMIMEDump_NoRawMimeToUser(t *testing.T) {
	// 这是真机上 /notifications 正文开头的形态：整段 MIME 源码。
	got := DeriveSnippet([]byte(realMIMEDump), 500)
	if strings.Contains(got, "Content-Transfer-Encoding") || strings.Contains(got, "=_Part_") {
		t.Fatalf("原始 MIME 头转储被当成摘要透给用户：%q", got)
	}
	if strings.Contains(got, "=E9=87=8D") {
		t.Fatalf("quoted-printable 未解码：%q", got)
	}
}

func TestDeriveSnippet_PlainTextPreferred(t *testing.T) {
	raw := "Subject: 测试\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n这是一封纯文本邮件。\r\n第二段。"
	got := DeriveSnippet([]byte(raw), 500)
	if !strings.Contains(got, "这是一封纯文本邮件") {
		t.Fatalf("text/plain 正文没取到：%q", got)
	}
	if strings.Contains(got, "Subject:") {
		t.Fatalf("邮件头混进了摘要：%q", got)
	}
}

// 截断必须按 rune，不能按字节：原实现 snippet[:500] 会在多字节字符中间劈开。
func TestDeriveSnippet_TruncatesByRuneNotByte(t *testing.T) {
	long := strings.Repeat("额度提醒", 400) // 1600 字符，远超 500
	got := DeriveSnippet([]byte(long), 500)
	if !utf8.ValidString(got) {
		t.Fatalf("截断产生了非法 UTF-8（按字节切的典型症状）：%q", got[:min(40, len(got))])
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatal("截断产生了 U+FFFD 替换字符，说明劈开了多字节字符")
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n > 500 {
		t.Fatalf("截断后仍超长：%d rune", n)
	}
}

// 短文本不能被无谓截断或加省略号。
func TestDeriveSnippet_ShortTextUnchanged(t *testing.T) {
	got := DeriveSnippet([]byte("简短正文"), 500)
	if got != "简短正文" {
		t.Fatalf("短文本被改动了：%q", got)
	}
}

func TestDeriveSnippet_EmptyInput(t *testing.T) {
	if got := DeriveSnippet(nil, 500); got != "" {
		t.Fatalf("空输入应返回空串：%q", got)
	}
	if got := DeriveSnippet([]byte("   \n\n  "), 500); got != "" {
		t.Fatalf("空白输入应返回空串：%q", got)
	}
}

// script/style 的内容不能当正文抽出来。
func TestDeriveSnippet_DropsScriptAndStyleContent(t *testing.T) {
	html := `<html><head><style>body{color:red}</style></head><body><script>alert(1)</script><p>正文内容</p></body></html>`
	got := DeriveSnippet([]byte(html), 500)
	if strings.Contains(got, "alert(1)") || strings.Contains(got, "color:red") {
		t.Fatalf("script/style 内容泄漏：%q", got)
	}
	if !strings.Contains(got, "正文内容") {
		t.Fatalf("正文丢了：%q", got)
	}
}

// 实体要解，但不能二次解码：&amp;lt; 应当变成 "&lt;" 而不是 "<"。
func TestDeriveSnippet_EntityDecodingNotDouble(t *testing.T) {
	got := DeriveSnippet([]byte("<p>余额 &amp;lt; 100</p>"), 500)
	if got != "余额 &lt; 100" {
		t.Fatalf("实体解码错误（二次解码或顺序错了）：%q", got)
	}
}

// 回归护栏：DeriveSnippet 本身正确还不够，fetcher.go 的**每一个**摘要产生点都
// 必须真的走它。否则纯函数就是死代码，用户在真机上照样看到原始 MIME。
//
// 2026-10-01 记录：本测试与 fetcher.go 的三处调用点在一次并行会话的 merge
// 提交（94b55ff）里被连带删除 —— 缺陷静默回归且没有任何测试转红。所以这里断言
// 的是「源码里不允许再出现的旧写法」+「调用点数量」，而不只是跑一遍纯函数。
func TestFetcherUsesDeriveSnippetAtEverySnippetSite(t *testing.T) {
	src, err := os.ReadFile("fetcher.go")
	if err != nil {
		t.Fatalf("读不到 fetcher.go：%v", err)
	}
	text := string(src)

	// 护栏必须只看**代码**不看注释：这些禁用写法的名字经常出现在解释「原来错在哪」
	// 的注释里，不剥离就会自己把自己判成 FAIL（第一版就踩了这个坑）。
	var code strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	codeText := code.String()

	forbidden := []struct {
		desc string
		re   string
	}{
		{
			desc: "IMAP 主路径未走 DeriveSnippet（strings.TrimSpace(string(bs.Bytes))）",
			re:   `strings\.TrimSpace\(string\(bs\.Bytes\)\)`,
		},
		{
			desc: "IMAP 主路径仍按字节切摘要（snippet[:500]）",
			re:   `snippet\s*\[:500\]`,
		},
		{
			desc: "按需补拉路径未走 DeriveSnippet（truncateStr(strings.TrimSpace(string(bs.Bytes)), 500)）",
			re:   `truncateStr\(strings\.TrimSpace\(string\(bs\.Bytes\)\)`,
		},
		{
			desc: "POP3 的 HTMLBody 未剥标签（truncateStr(strings.TrimSpace(parsed.HTMLBody), 500)）",
			re:   `truncateStr\(strings\.TrimSpace\(parsed\.HTMLBody\),\s*500\)`,
		},
	}
	for _, f := range forbidden {
		re, err := regexp.Compile(f.re)
		if err != nil {
			t.Fatalf("护栏自身的正则 %q 编译失败：%v", f.re, err)
		}
		if re.MatchString(codeText) {
			t.Errorf("fetcher.go 仍存在%s", f.desc)
		}
	}

	if n := strings.Count(codeText, "DeriveSnippet("); n != 3 {
		t.Errorf("fetcher.go 里的 DeriveSnippet 调用数 = %d，期望 3（按需补拉 / IMAP 批量主路径 / POP3 HTML 回退）", n)
	}
}
