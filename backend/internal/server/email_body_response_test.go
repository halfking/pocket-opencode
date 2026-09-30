package server

import (
	"strings"
	"testing"
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
