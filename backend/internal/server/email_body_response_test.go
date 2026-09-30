package server

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// 邮件详情正文接口的契约回归（2026-09-30 真机审计）。
//
// 真机报「邮件详情展示不正常、缺失图片或内容」。根因是
// handleEmailBody 走的是 FetchBody（发 BODY[TEXT]），拿到的已经是服务器端
// 挑好的纯文本：HTML 分支、cid 内联图、附件结构在这一步全部丢失；随后又用
// ExtractDisplayBody 把它拍平成一段字符串。而前端唯一的详情入口
// EmailDetailView 会把响应里的 body 交给 extractEmailBody()，那个函数需要
// 完整 MIME 树才能把 <img src="cid:..."> 内联成 data URI、在
// multipart/alternative 里选出 HTML 分支。契约对不上，详情页就只剩纯文本。
//
// 修复：body 字段必须承载整封 MIME 原文。
func TestEmailBodyResponse_ReturnsRawMIMEForRealMessage(t *testing.T) {
	srv := &Server{}

	// multipart/related + cid 内联图，正是「详情缺失图片」的形态
	raw := strings.Join([]string{
		"Content-Type: multipart/related; boundary=\"REL\"",
		"MIME-Version: 1.0",
		"",
		"--REL",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<html><body><p>季度视觉规范 v3</p><img src="cid:logo@audit"></body></html>`,
		"--REL",
		"Content-Type: image/png",
		"Content-ID: <logo@audit>",
		"Content-Transfer-Encoding: base64",
		"",
		"iVBORw0KGgo=",
		"--REL--",
		"",
	}, "\r\n")

	got := srv.emailBodyResponse("em-1", "imap", []byte(raw))

	body, _ := got["body"].(string)
	if body != raw {
		t.Fatalf("body 必须是整封 MIME 原文。\n期望包含 cid 内联部件与 img 标签，实际：\n%s", body)
	}
	if !strings.Contains(body, `cid:logo@audit`) {
		t.Errorf("原文里的 cid 部件丢失，前端将无法内联成 data URI：\n%s", body)
	}
	if n, _ := got["bytes"].(int); n != len(raw) {
		t.Errorf("bytes = %d，期望 %d（应统计原文长度而非展示文本长度）", n, len(raw))
	}
	if got["source"] != "imap" {
		t.Errorf("source = %v，期望 imap", got["source"])
	}
}

// BODY[TEXT] 回退结果不是 MIME 报文，必须仍然能显示，不能直接吐空。
func TestEmailBodyResponse_FallsBackForNonMIMEText(t *testing.T) {
	srv := &Server{}
	got := srv.emailBodyResponse("em-2", "imap", []byte("就是一段纯文本正文"))
	body, _ := got["body"].(string)
	if body != "就是一段纯文本正文" {
		t.Fatalf("非 MIME 输入应原样回显，实际：%q", body)
	}
}

// 旧版本缓存里存的是拍平后的展示文本，不能因为契约变更就变成空白页。
func TestEmailBodyResponse_LegacyFlattenedCacheStillDisplays(t *testing.T) {
	srv := &Server{}
	flattened := "季度视觉规范 v3 下面的示意图请查收"
	got := srv.emailBodyResponse("em-3", "cache", []byte(flattened))
	body, _ := got["body"].(string)
	if !strings.Contains(body, "季度视觉规范 v3") {
		t.Fatalf("旧缓存正文应仍可显示，实际：%q", body)
	}
	if got["source"] != "cache" {
		t.Errorf("source = %v，期望 cache", got["source"])
	}
}

// quoted-printable + GB2312 这类需要解码的报文，必须原样透传给前端解析；
// 后端一旦提前拍平，=XX 序列与乱码就会固化进展示文本。
func TestEmailBodyResponse_KeepsTransferEncodingIntact(t *testing.T) {
	srv := &Server{}
	raw := strings.Join([]string{
		"Content-Type: text/plain; charset=GB2312",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"=D5=C5=B4=B4",
		"",
	}, "\r\n")
	got := srv.emailBodyResponse("em-4", "imap", []byte(raw))
	body, _ := got["body"].(string)
	if !strings.Contains(body, "=D5=C5=B4=B4") {
		t.Errorf("quoted-printable 未解码的原文应保留给前端解析，实际：%q", body)
	}
}

// 8bit GBK 正文（2026-10-01 真机审计 P0：正文乱码）。
//
// 这里的报文是**合法 MIME**（所以走不到上面的兜底分支），正文部分是 GBK
// 字节。它有两个必须同时满足的性质：
//
//  1. 返回值必须是合法 UTF-8 —— 否则 encoding/json 序列化时非法字节会被
//     替换成 U+FFFD（「��Ķ�」），中文在到达浏览器前就永久丢失了。
//  2. 内容必须是可读中文 —— 不能因为「转成合法 UTF-8」而丢字。
//
// 修复前这条必然失败：string(raw) 直接带着 GBK 字节进 JSON。
func TestEmailBodyResponse_Decodes8BitGBKInsteadOfCorruptingIt(t *testing.T) {
	srv := &Server{}
	// "您的发票已开具" 的 GBK 字节（.NET Encoding.GetEncoding(936) 产出，14 字节）。
	raw := append([]byte("Content-Type: text/plain; charset=GBK\r\nContent-Transfer-Encoding: 8bit\r\n\r\n"),
		0xC4, 0xFA, 0xB5, 0xC4, 0xB7, 0xA2, 0xC6, 0xB1, 0xD2, 0xD1, 0xBF, 0xAA, 0xBE, 0xDF)

	got := srv.emailBodyResponse("em-5", "imap", raw)
	body, _ := got["body"].(string)

	if !utf8.ValidString(body) {
		t.Fatalf("返回的 body 必须是合法 UTF-8，否则 JSON 序列化会把它变成 U+FFFD：%q", body)
	}
	if strings.ContainsRune(body, utf8.RuneError) {
		t.Errorf("body 不应含替换字符（说明 GBK 字节没被解码）：%q", body)
	}
	if !strings.Contains(body, "发票") {
		t.Errorf("GBK 正文应被正确解码为中文，实际：%q", body)
	}

	// 非空断言：证明「原样 string(raw) 进 JSON 会毁掉 GBK」这件事是真的，
	// 从而说明上面的解码分支不是多余的。否则本测试可能只是在验证一个
	// 永远不会发生的场景。
	corrupted, err := json.Marshal(map[string]string{"body": string(raw)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if utf8.ValidString(string(corrupted)) && !strings.ContainsRune(string(corrupted), utf8.RuneError) {
		t.Fatalf("前提不成立：GBK 字节经 JSON 后竟未损坏，本测试将失去意义")
	}
}

// 回归护栏：合法的 UTF-8 报文必须仍然原样透传（不能被新分支误伤）。
func TestEmailBodyResponse_KeepsValidUTF8MIMERaw(t *testing.T) {
	srv := &Server{}
	raw := strings.Join([]string{
		"Content-Type: text/html; charset=utf-8",
		"",
		"<html><body><p>中文正文</p></body></html>",
		"",
	}, "\r\n")
	got := srv.emailBodyResponse("em-6", "imap", []byte(raw))
	if body, _ := got["body"].(string); body != raw {
		t.Fatalf("合法 UTF-8 的 MIME 原文应原样透传，实际：%q", body)
	}
}
