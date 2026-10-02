package email

// fetcher_attachments_test.go —— `has_attachments` 在 IMAP 路径上从来没有被置真过。
//
// ## 缺陷
//
// `emails.has_attachments` 全仓库只有一处被赋值：`fetcher.go` 里 POP3 降级路径的
// `em.HasAttachments = len(parsed.Attachments) > 0`。IMAP 同步路径的 FETCH
// （见 fetcher.go 的 fetchOpts）只请求 Envelope / UID / InternalDate，
// **envelope 里没有附件信息**，而 go-imap v2 的 `imap.Envelope` 结构体
// （fetch.go:85-95）**也没有 Body 字段**（v1 才有），所以那条路径上根本拿不到
// 附件信息，`em.HasAttachments` 恒为 Go 零值 false。
//
// 实测（2026-10-03 08:25:22，schema opencode_pocket）：真实库 120 封里
// pop3_sourced=0 / imap_sourced=120 / has_attachments_true=0 —— 唯一能置位的那条
// 路径在本部署产出为 0，于是前端 `EmailCard.vue` 的 📎 标记**永不显示**（需求 7）。
//
// ## 修法与它的边界
//
// 修法是给 fetchOpts 加 `BodyStructure`，再用 `imap.BodyStructure` 的
// `Walk` / `Filename` 判附件。
//
// 诚实说明它**证明了什么、没证明什么**：
//   - 证明了：go-imap 客户端 ↔ 服务端这一对能正确协商并解析 BODYSTRUCTURE，
//     附件判定逻辑对纯文本 / 附件 / 内嵌图片 / 嵌套 multipart 都成立。
//   - **没证明**：真实第三方 IMAP server 不会因此挂掉。fetcher.go 里那条注释
//     记录了「加过数据项导致部分 server 响应解析失败」的历史，imapmemserver
//     是 go-imap 自己的实现，**不可能**复现那种畸形响应。要覆盖那一半仍需
//     Greenmail（卡在 Docker daemon 未运行）。
//
// POP3 路径已经正确（fetcher.go 的 syncPOP3Fallback），本文件不重复覆盖。

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

// appendRawMessage 把一段原始 RFC 5322 报文塞进 INBOX。
func (ti *testIMAP) appendRawMessage(t *testing.T, raw string, when time.Time) {
	t.Helper()
	if _, err := ti.user.Append("INBOX", newLiteral(raw), &imap.AppendOptions{Time: when}); err != nil {
		t.Fatalf("append raw: %v", err)
	}
}

const testBoundary = "MIXED-BOUNDARY-9d2f"

// withAttachment 造一封 multipart/mixed：正文 + 一个带 filename 的附件。
func withAttachment(from, subject string, when time.Time) string {
	return fmt.Sprintf("From: %s\r\n"+
		"To: recipient@example.com\r\n"+
		"Subject: %s\r\n"+
		"Message-ID: <att-%s@example.com>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: multipart/mixed; boundary=\"%s\"\r\n"+
		"\r\n"+
		"--%s\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"\r\n"+
		"发票见附件。\r\n"+
		"--%s\r\n"+
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n"+
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n"+
		"Content-Transfer-Encoding: base64\r\n"+
		"\r\n"+
		"JVBERi0xLjQKJ\r\n"+
		"--%s--\r\n",
		from, subject, strings.ReplaceAll(subject, " ", "-"),
		when.Format(time.RFC1123Z), testBoundary, testBoundary, testBoundary, testBoundary)
}

// withInlineImage 造一封 multipart/related：正文 + 一个 **内联** 图片。
//
// 这是「不能只看 Content-Type 是不是 image 就说有附件」的反例：内联图（cid）
// 在邮件客户端里是正文的一部分，不该被标成附件，否则每个带签名图的邮件都会
// 亮 📎。
func withInlineImage(from, subject string, when time.Time) string {
	const b = "RELATED-BOUNDARY-4a1c"
	return fmt.Sprintf("From: %s\r\n"+
		"To: recipient@example.com\r\n"+
		"Subject: %s\r\n"+
		"Message-ID: <inline-%s@example.com>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: multipart/related; boundary=\"%s\"\r\n"+
		"\r\n"+
		"--%s\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n"+
		"\r\n"+
		"<p>hi</p>\r\n"+
		"--%s\r\n"+
		"Content-Type: image/png\r\n"+
		"Content-ID: <logo@corp.example>\r\n"+
		"Content-Disposition: inline\r\n"+
		"Content-Transfer-Encoding: base64\r\n"+
		"\r\n"+
		"iVBORw0KGgo=\r\n"+
		"--%s--\r\n",
		from, subject, strings.ReplaceAll(subject, " ", "-"),
		when.Format(time.RFC1123Z), b, b, b, b)
}

// plainMessage 造一封最普通的纯文本邮件，作为对照组。
func plainMessage(from, subject string, when time.Time) string {
	return fmt.Sprintf("From: %s\r\n"+
		"To: recipient@example.com\r\n"+
		"Subject: %s\r\n"+
		"Message-ID: <plain-%s@example.com>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"\r\n%s\r\n",
		from, subject, strings.ReplaceAll(subject, " ", "-"),
		when.Format(time.RFC1123Z), "no attachments here")
}

// hasAttachmentsBySubject 跑一次 Sync 并把每封邮件的 has_attachments 取回来。
func hasAttachmentsBySubject(t *testing.T) map[string]bool {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)

	now := time.Now().UTC().Truncate(time.Second)
	ti.appendMessage(t, "boss@corp.example", "Quarterly review", "Please send the numbers.", now.Add(-3*time.Hour))
	ti.appendRawMessage(t, withAttachment("billing@vendor.example", "发票 A", now.Add(-2*time.Hour)), now.Add(-2*time.Hour))
	ti.appendRawMessage(t, withInlineImage("hr@corp.example", "团建照片", now.Add(-1*time.Hour)), now.Add(-1*time.Hour))
	ti.appendRawMessage(t, plainMessage("friend@example.com", "周末有空吗", now.Add(-30*time.Minute)), now.Add(-30*time.Minute))

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-att", password, "")
	saved, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if saved != 4 {
		t.Fatalf("want 4 emails saved, got %d", saved)
	}

	list, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-att")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := map[string]bool{}
	for _, e := range list {
		out[e.Subject] = e.HasAttachments
	}
	return out
}

func TestSyncSetsHasAttachments_AttachmentOnly(t *testing.T) {
	got := hasAttachmentsBySubject(t)
	if !got["发票 A"] {
		t.Errorf("带附件的邮件 has_attachments = false, want true —— " +
			"IMAP 路径没请求 BODYSTRUCTURE，前端 📎 标记因此永不显示")
	}
}

func TestSyncLeavesHasAttachmentsFalse_ForPlainText(t *testing.T) {
	got := hasAttachmentsBySubject(t)
	if got["Quarterly review"] {
		t.Error("纯文本邮件 has_attachments = true, want false")
	}
	if got["周末有空吗"] {
		t.Error("纯文本邮件 has_attachments = true, want false")
	}
}

func TestSyncDoesNotCountInlineImageAsAttachment(t *testing.T) {
	got := hasAttachmentsBySubject(t)
	if got["团建照片"] {
		t.Error("内联图片（Content-Disposition: inline）被当成了附件 —— " +
			"每个带签名图的邮件都会亮 📎，那不是附件")
	}
}
