package email

// 邮件列表的 snippet / 详情页的展示正文直接显示 MIME 源码。
//
// 2026-10-03 真机实测（Redmi 2411DRN47C / Android 14）：邮件列表 5/5 封
// 全中，开头就是：
//
//	------=_Part_8505717_93977514.1790821420306
//	Content-Type: text/html; charset=utf-8
//	Content-Transfer-Encoding: quoted-printable
//
// 另一批是 --part_8057f3aacb3e5508e… / --_000_10f7b8d35f184af…，
// 后面跟 Content-Type 与 quoted-printable 的 =D7=F0=BE=B4=…
//
// 根因不在解析器，而在**出口没过滤**，三处：
//   · fetcher.go 的 `truncateStr(strings.TrimSpace(parsed.TextBody), 500)`
//     —— TextBody 是所有 text/plain 部件的聚合（mime.go 里
//     `out.TextBody += body`），对 multipart/mixed 里嵌一整封内层报文原文的
//     形态（企业网关转发的常见形态），被聚合进来的就是那封内层报文；
//   · snippet.go DeriveSnippet 的解析成功分支 —— 拿到 TextBody 就直接返回，
//     同样不过滤；
//   · mime.go ExtractDisplayBody 的 `return string(raw)` —— 注释写着
//     「解析失败退回原文本身」，**这条兜底正是缺陷本身**。
//
// DeriveSnippet 里已经写明「这里没有『退回原文』这条兜底，那正是这个缺陷本身」，
// 同样的道理必须作用到所有取展示文本的出口上。

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 构造一封「内层报文被当成 text/plain 聚合」的邮件：
// 外层 multipart/mixed 的第一个 part 整体是一封内嵌邮件原文
// （这正是企业网关转发的常见形态）。
func buildLeakyMIME() []byte {
	inner := strings.Join([]string{
		"------=_Part_8505717_93977514.1790821420306",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<html><body>招商银行每日信用管家</body></html>",
		"------=_Part_8505717_93977514.1790821420306--",
	}, "\r\n")

	outer := strings.Join([]string{
		"From: cmb@example.com",
		"Subject: 每日信用管家",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="OUTER"`,
		"",
		"这是 MIME 格式的多部分邮件。",
		"",
		"--OUTER",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		inner,
		"--OUTER",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		"<html><body>招商银行每日信用管家</body></html>",
		"--OUTER--",
	}, "\r\n")
	return []byte(outer)
}

// recordedLeakFromDevice 是真机上实际观察到的、落库后的 snippet 形态
// （压平成一行之后的样子）。它是本文件检测器的校准样本。
//
// 为什么必须是**压平后**的形态：出口返回给用户的字符串已经被
// normalizeWhitespace 压成一行，任何依赖 `^Content-Type:` 行首锚点的检测器
// 对它都失效。本轮第一版护栏就是这么写的，于是三条用例对着实泄的输出全绿。
const recordedLeakFromDevice = `------=_Part_8505717_93977514.1790821420306 ` +
	`Content-Type: text/html; charset=utf-8 ` +
	`Content-Transfer-Encoding: quoted-printable ` +
	`<html><body>招商银行每日信用管家</body></html> ` +
	`------=_Part_8505717_93977514.1790821420306--`

// benignBody 是必须**不**被判成 MIME 源码的正常正文。
// 第 1 条防 `--` 宽松匹配的误伤（COVID-19--related / 见附件 --）。
// 第 2 条防 Content-Type token 匹配过宽。
var benignBodies = []string{
	"本周五例会议改到下午三点：记得带季报。另外 COVID-19--related 的数据也要更新。",
	"请查收附件，见附件 -- 谢谢",
	"Hi team, the release train is delayed. Please refer to the attached release-notes-v3.pdf for details.",
}

// detectMIMELeak 是本文件的检测器。
//
// ## 刻意不复用实现里的 containsMIMESource
//
// 复用被判据的同一个函数等于自己判自己：实现和测试同时改错，绿灯照亮。
// 这里只拿字符串字面量做 Contains，不引入任何正则，也不碰包内任何
// 判据函数——独立实现才有对账价值。
//
// 只用 Contains 是刻意的：它与位置无关，因此对「压平成一行」的泄漏同样
// 有效，而这正是本轮缺陷的形态。
func detectMIMELeak(s string) string {
	low := strings.ToLower(s)
	for _, needle := range []string{
		"content-type:",
		"content-transfer-encoding:",
		"content-disposition:",
		"content-id:",
		"mime-version:",
		"boundary=",
		"=_part_",
		"--_000_",
		"--part_",
	} {
		if strings.Contains(low, needle) {
			return needle
		}
	}
	return ""
}

// assertNoMIMELeak 是本文件所有「不许泄漏」用例的共同断言。
// 返回值里带上命中的 token，失败信息要能一眼看出泄的是哪一类。
func assertNoMIMELeak(t *testing.T, got, where string) {
	t.Helper()
	if leak := detectMIMELeak(got); leak != "" {
		t.Fatalf("%s 泄出了 MIME 源码（命中 %q）：\n%.400q", where, leak, got)
	}
}

// TestLeakDetectorItselfDetectsRecordedDeviceLeak 是**元护栏**。
//
// 它不检查任何业务性质，只检查本文件的检测器本身是不是有效的。
//
// 为什么要专门写这条：2026-10-03 本轮第一版护栏 3/3 全绿，而被测函数
// 正在实泄。根因是检测器只会找行首锚点，对「压平成一行」的输出永远返回
// 「没发现」。一个永远绿的检测器会让后面每一条用例都变成摆设——
// 而它们看上去全都在跑、全部通过、报告里还写着「护栏覆盖」。
//
// 负控：把 detectMIMELeak 的 needles 换成 `nil`，这条立刻转红；
// 同时 TestSnippetNeverLeaksMIMESource 的前置检查也会转红。
func TestLeakDetectorItselfDetectsRecordedDeviceLeak(t *testing.T) {
	if leak := detectMIMELeak(recordedLeakFromDevice); leak == "" {
		t.Fatal("检测器对真机实录的泄漏样本返回「没发现」——"+
			"本文件所有「不许泄漏」用例从此全部失去意义（永远绿的检测器）")
	}
	// 校准样本必须真的含泄漏 token，否则上面那句转红可能来自别的巧合。
	if !strings.Contains(recordedLeakFromDevice, "Content-Type:") {
		t.Fatal("校准样本被改坏了：里面已经没有 Content-Type: 了")
	}
	// 反向：正常正文不能被判成泄漏，否则修复会变成「一律清空摘要」。
	for _, b := range benignBodies {
		if leak := detectMIMELeak(b); leak != "" {
			t.Fatalf("检测器误伤正常正文（命中 %q）：%q", leak, b)
		}
	}
}

// TestContainsMIMESourceWorksOnFlattenedText 直接钉住「判据必须对
// 压平成一行的文本仍然有效」这条不变量。
//
// 这是本轮真实踩到的顺序错误：SnippetFromParsed 第一版写成
//
//	normalizeWhitespace(msg.TextBody) → looksLikeMIMEStructure(t)
//
// 而 looksLikeMIMEStructure 的主力判据是逐行正则 `^Content-Type:`，
// t 是一行，锚点失效 → 判据恒假 → 实泄的输入一路放行 → 护栏全绿。
// 这里从判据侧把它钉住：无论调用方以什么顺序调用，压平形态都必须命中。
func TestContainsMIMESourceWorksOnFlattenedText(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"压平后的内层报文泄漏（真机形态）", recordedLeakFromDevice},
		{"保留换行的内层报文泄漏", strings.Join([]string{
			"------=_Part_8505717_93977514.1790821420306",
			"Content-Type: text/html; charset=utf-8",
			"",
			"<html></html>",
		}, "\r\n")},
		{"--_000_ 形态", "--_000_10f7b8d35f184af7e5\r\nContent-ID: <a@x>"},
		{"--part_ 形态", "--part_8057f3aacb3e5508e18aca6f084c07d2ff86ddb6114108806b53e4b7f3822e6a"},
	}
	for _, c := range cases {
		if !containsMIMESource(c.input) {
			t.Fatalf("%s：containsMIMESource 未命中，判据对这种形态失效", c.name)
		}
	}
	// 反向：正常正文不能被判成 MIME 源码。
	for _, b := range benignBodies {
		if containsMIMESource(b) {
			t.Fatalf("containsMIMESource 误伤正常正文：%q", b)
		}
	}
}

// TestSnippetNeverLeaksMIMESource 是**前置检查**，不是行为用例：
// 它证明本文件构造的样本确实会在 TextBody 里聚合进 MIME 源码。
// 样本哪天不成立了，后面几条用例就成了在测一个不成立的场景。
func TestSnippetNeverLeaksMIMESource(t *testing.T) {
	raw := buildLeakyMIME()
	msg, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("构造的样本应可解析: %v", err)
	}
	if leak := detectMIMELeak(msg.TextBody); leak == "" {
		t.Fatalf("前置检查失败：TextBody 里已经没有 MIME 源码了（%q），"+
			"本文件的用例失去前提——要么换样本，要么确认缺陷已从源头消失", msg.TextBody)
	}
	if !strings.Contains(msg.HTMLBody, "招商银行") {
		t.Fatalf("样本的 HTMLBody 应当含有可读正文，实际 %q", msg.HTMLBody)
	}
}

func TestDeriveSnippetNeverLeaksMIMESource(t *testing.T) {
	got := DeriveSnippet(buildLeakyMIME(), 500)
	assertNoMIMELeak(t, got, "DeriveSnippet")
	// 不能只是「不泄漏」——正文明明存在，摘要必须真的拿到正文，
	// 否则「一律清空」也能让这条用例变绿。
	if !strings.Contains(got, "招商银行每日信用管家") {
		t.Fatalf("摘要里没有可读正文（可能退化成空串或退化成原文）：%.200q", got)
	}
}

func TestSnippetFromParsedNeverLeaksMIMESource(t *testing.T) {
	msg, err := ParseMIMEMessage(buildLeakyMIME())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := SnippetFromParsed(msg, 500)
	assertNoMIMELeak(t, got, "SnippetFromParsed")
	if !strings.Contains(got, "招商银行每日信用管家") {
		t.Fatalf("摘要里没有可读正文（可能退化成空串或退化成原文）：%.200q", got)
	}
}

// TestExtractDisplayBodyNeverLeaksMIMESource 覆盖 mime.go 那条
// 「解析失败退回原文本身」的兜底。
func TestExtractDisplayBodyNeverLeaksMIMESource(t *testing.T) {
	got := ExtractDisplayBody(buildLeakyMIME())
	assertNoMIMELeak(t, got, "ExtractDisplayBody")
	if !strings.Contains(got, "招商银行每日信用管家") {
		t.Fatalf("展示正文里没有可读内容：%.200q", got)
	}
}

// TestFetcherSnippetPathNeverLeaks 把「fetch 路径必须净化」钉成契约。
// SnippetFromParsed 是这条契约的唯一实现入口，fetcher.go 的 POP3 入库
// 路径（em.Snippet = …）必须经过它。
func TestFetcherSnippetPathNeverLeaks(t *testing.T) {
	raw := buildLeakyMIME()
	msg, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 旧写法：truncateStr(strings.TrimSpace(parsed.TextBody), 500)。
	// 这行留在这里当**反面对照**：它必须泄，否则说明样本不再复现缺陷，
	// 上面的用例就失去了区分能力。
	old := truncateStr(strings.TrimSpace(msg.TextBody), 500)
	if detectMIMELeak(old) == "" {
		t.Fatalf("反面对照失效：旧写法 truncateStr(TextBody) 竟然不泄了（%.120q），"+
			"样本已不复现缺陷，护栏失去区分能力", old)
	}
	assertNoMIMELeak(t, SnippetFromParsed(msg, 500), "SnippetFromParsed")
}

// TestFetcherSnippetIsWiredThroughSnippetFromParsed 是**接线护栏**。
//
// ## 为什么上面那条不够
//
// 上面 TestFetcherSnippetPathNeverLeaks 只测 SnippetFromParsed 这个函数。
// 纯函数测绿**不能**证明 fetcher.go 真的在用它——判据在文件里、判据被测绿、
// 判据没接线，三件事可以同时成立。
//
// 2026-10-03 本轮负控实测过一次：neg5 把 fetcher.go 的
// `em.Snippet = SnippetFromParsed(parsed, 500)` 退回成
// `em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)`
// （也就是重新引入本缺陷），结果**没有任何用例转红**。
// 护栏看上去齐全，实际对最核心的那条接线完全失明。
//
// 这与 body_invoice_link_wiring_test.go 是同一类，那条已经写明「判据存在 ≠
// 判据被用上」。这里沿用它的 wireFuncBodies（AST 解析，注释已被剥掉，
// 避免「把接线注释掉也算通过」）。
func TestFetcherSnippetIsWiredThroughSnippetFromParsed(t *testing.T) {
	src := wireFuncBodies(t, "fetcher.go")
	if !strings.Contains(src, "SnippetFromParsed(") {
		t.Fatal("fetcher.go 里没有 SnippetFromParsed 的调用 —— " +
			"摘要出口没接上净化层，TextBody 的 MIME 原文会直接落库")
	}
	// 反向：禁止任何地方再直接拿 TextBody 截断当摘要。
	// 这是本缺陷的原始形态，也是负控 neg5 退回的形态。
	if bad := "truncateStr(strings.TrimSpace(parsed.TextBody)"; strings.Contains(src, bad) {
		t.Errorf("fetcher.go 里仍存在 %q —— TextBody 是所有 text/plain 部件的聚合，"+
			"对嵌内层报文的 multipart 会把 boundary 行和 Content-* 头一起带进摘要", bad)
	}
	if bad := "truncateStr(strings.TrimSpace(msg.TextBody)"; strings.Contains(src, bad) {
		t.Errorf("fetcher.go 里仍存在 %q —— 同上", bad)
	}
}

// TestSnippetFromParsedKeepsRealBody 防止修复变成「一律清空」。
// 一封正常的纯文本邮件必须拿到完整摘要。
func TestSnippetFromParsedKeepsRealBody(t *testing.T) {
	body := "张经理：\r\n\r\n附件是本周的结算明细，请查收。\r\n\r\n顺颂商祺\r\n李四\r\n"
	raw := []byte(strings.Join([]string{
		"From: li@example.com",
		"Subject: 结算明细",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}, "\r\n"))
	msg, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := SnippetFromParsed(msg, 500)
	if !strings.Contains(got, "结算明细") || !strings.Contains(got, "李四") {
		t.Fatalf("正常纯文本邮件的摘要丢了内容：%.200q", got)
	}
}

// TestSnippetFromParsedTruncatesByRune 守住「按 rune 而不是按字节」：
// 多字节字符不能被劈出 U+FFFD。
func TestSnippetFromParsedTruncatesByRune(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"From: li@example.com",
		"Subject: 长文本",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		strings.Repeat("测试内容", 400),
	}, "\r\n"))
	msg, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := SnippetFromParsed(msg, 50)
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("摘要里出现替换字符 U+FFFD，说明按字节劈开了多字节字符：%.80q", got)
	}
	// truncateRunes 截断时会补一个省略号「…」，所以上限是 maxRunes+1。
	// 断言写死成 maxRunes 会把这条用例变成「对着实现细节报错」——
	// 2026-10-03 第一次跑就是这样红的：报的其实是我写错的断言。
	rs := []rune(got)
	if len(rs) != 51 {
		t.Fatalf("摘要 rune 数 = %d，期望 51（50 内容 + 1 省略号）：%.80q", len(rs), got)
	}
	if rs[50] != '…' {
		t.Fatalf("第 51 个 rune 应为省略号，实际 %q", rs[50])
	}
	// 截断必须在**字符边界**上发生：前 50 个 rune 拼起来仍应是原前缀。
	body := strings.Repeat("测试内容", 400)
	if prefix := string(rs[:50]); !strings.HasPrefix(body, prefix) {
		t.Fatalf("截断结果不是原文前缀，说明切错了位置：%.80q", prefix)
	}
}
