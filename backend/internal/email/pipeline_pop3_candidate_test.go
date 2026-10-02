package email

// pipeline_pop3_candidate_test.go — 护栏：POP3 来源的发票候选必须能建档。
//
// ## 这个 bug 的形态（2026-10-03 实测，handoff §7.4.4）
//
// 两封真实「通行费电子发票」（em-pop3-…-ZL0014_…，19.00 + 5.61 = 24.61 元）
// 在 emails 表里有、`email_invoices` 里**没有对应行**，而且不是被建档门槛
// 拦掉的误报：
//
//	pipeline.go 第 2 趟（fetchInvoiceBodies）原来直接调
//	    p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
//	而 mime.go:98 里它是
//	    dial(acc.IMAPHost + ":" + acc.IMAPPort)
//	——IMAP 专用。POP3 降级路径落库的邮件 em.UID 是**位置序号**，
//	对 IMAP UID FETCH 毫无意义，于是取原文必然失败，不建档。
//
// 而它们的原文**就在磁盘上**（data/email-bodies-raw/<id>.bin，POP3 同步时落盘），
// 采集器 harvestOne 早就在读那份缓存——两条路径能力不对称，只有 pipeline 没跟上。
//
// 修法：两边共用 raw_body_resolve.go 的 resolveRawBody。
//
// ## 为什么这个 fixture 能证明「是缓存救回来的」
//
// 账户的 IMAPHost **留空**，Fetcher 也没有可用的 dial 目标。
// 任何一次真实的 IMAP 取原文尝试都会失败。所以只要台账行建出来了，
// 唯一可能的来源就是 BodyCache —— 判据是行为，不是「某个函数被调用过」。
//
// 负控见文件末尾：把 Pipeline.BodyCache 置 nil，同一个 fixture 必须建不出台账行。

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	pop3CandEmailID = "em-pop3-toll-candidate"
	pop3CandAcctID  = "acct-pop3-blind"
	pop3CandUser    = "u-junk"
	pop3CandWS      = "ws-junk"
	// 位置序号。必须 >0，否则 pipeline.go:567 的 `e.UID > 0` 门控不成立，
	// 压根不会排 body job —— 那会让本文件在守一个永远不触发的分支。
	pop3CandUID = int64(7)
)

// tollRaw 造一封带 PDF 附件的发票原文，形态照抄真实那两封（票根电子发票）。
//
// 关键是**正文里没有可直读的金额**：真实形态是
// `发票金额共计<span style='color: #FF9100;'>19</span>元`，
// 标签与数字之间夹着 HTML 标签，reAmountTotal 实测匹配不上（false）。
// 因此这封邮件靠**附件证据**进建档门槛，而不是靠正文抽到金额。
func tollRaw() []byte {
	return []byte("From: noreply@toll.example\r\n" +
		"To: acct-pop3-blind@example.com\r\n" +
		"Subject: 通行费电子发票\r\n" +
		"Message-ID: <toll-1@toll.example>\r\n" +
		"Date: Mon, 14 Sep 2026 13:41:11 +0800\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"BND\"\r\n" +
		"\r\n" +
		"--BND\r\n" +
		"Content-Type: text/html; charset=\"UTF-8\"\r\n" +
		"\r\n" +
		"<html><body>您本次通行费消费1张发票，发票金额共计" +
		"<span style='color: #FF9100;'>19</span>元。</body></html>\r\n" +
		"--BND\r\n" +
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"\r\n" +
		"JVBERi0xLjQKJcTl8uXrCg==\r\n" +
		"--BND--\r\n")
}

// pop3CandidatePipeline 造一条流水线 + 一封 POP3 来源的发票候选邮件。
//
// 邮件形态刻意是「envelope 上抽不到金额」：subject/snippet 都不含金额数字，
// 于是第 1 趟 ExtractInvoice(e,"") 必然 hit=false，
// 由 invoiceBodyReason 返回 "candidate" 排一个 body job —— 与真实一致。
func pop3CandidatePipeline(t *testing.T) (*Pipeline, *Store, *stubBodyCache, func()) {
	t.Helper()
	p, store, cleanup := newPipelineFixture(t)
	ctx := context.Background()

	// IMAPHost 留空：任何 IMAP 取原文的尝试都必然失败。这不是「测不到所以不管」，
	// 而是让「建档成功」这一事实只能由 BodyCache 解释。
	if err := store.InsertAccount(ctx, &Account{
		ID:           pop3CandAcctID,
		UserID:       pop3CandUser,
		WorkspaceID:  pop3CandWS,
		DisplayName:  "acct-pop3-blind",
		EmailAddress: "acct-pop3-blind@example.com",
		IMAPHost:     "", // 故意为空
		IMAPPort:     0,
		AuthType:     "password",
		Enabled:      true,
		CreatedAt:    time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	if err := store.InsertEmail(ctx, Email{
		ID:          pop3CandEmailID,
		AccountID:   pop3CandAcctID,
		WorkspaceID: pop3CandWS,
		MessageID:   "<toll-1@toll.example>",
		FromAddress: "noreply@toll.example",
		Subject:     "通行费电子发票",
		Snippet:     "", // envelope 上没有金额 → 第 1 趟必然 hit=false
		Date:        time.Now().Unix(),
		// uid 必须 > 0：pipeline.go:567 的门控是 `p.Fetcher != nil && e.UID > 0`，
		// 不成立就不会排 body job，那本文件就在守一个永不触发的分支。
		UID: pop3CandUID,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	// 兜底核对：InsertEmail 是否真的把 uid 落库了。读库比相信构造器可靠。
	var uidFromDB int64
	if err := store.pool.QueryRow(ctx,
		`SELECT uid FROM emails WHERE id = $1`, pop3CandEmailID).Scan(&uidFromDB); err != nil {
		t.Fatalf("read uid: %v", err)
	}
	if uidFromDB <= 0 {
		t.Fatalf("emails.uid=%d，InsertEmail 没落 uid；"+
			"pipeline 的 body-job 门控（e.UID > 0）不会成立，本文件会假绿", uidFromDB)
	}

	// 缓存里放真实形态的原文。uid 必须与 emails.uid 一致（body_cache.go 会校验）。
	cache := &stubBodyCache{raw: tollRaw()}
	p.BodyCache = cache
	p.AccountSyncTimeout = 5 * time.Second
	return p, store, cache, cleanup
}

func pop3CandidateAccount() []Account {
	return []Account{{
		ID:           pop3CandAcctID,
		UserID:       pop3CandUser,
		WorkspaceID:  pop3CandWS,
		EmailAddress: "acct-pop3-blind@example.com",
		Enabled:      true,
	}}
}

func TestPipelineStep15_POP3CandidateIsArchivedFromBodyCache(t *testing.T) {
	p, store, _, cleanup := pop3CandidatePipeline(t)
	defer cleanup()
	ctx := context.Background()

	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, pop3CandidateAccount(), rep)

	// 前置自检：必须真的扫描到候选，否则下面的断言可能在守一个永不触发的分支。
	if rep.InvoiceCandidatesScanned == 0 {
		t.Fatalf("没有扫描到任何候选邮件，fixture 失效（scanned=%d）", rep.InvoiceCandidatesScanned)
	}
	if rep.InvoiceCandidatesCreated != 1 {
		t.Logf("created=%d（下面直接查库确认，因为这才是判据）", rep.InvoiceCandidatesCreated)
	}

	var amount, status string
	err := store.pool.QueryRow(ctx, `
		SELECT COALESCE(amount::text,'<null>'), COALESCE(status,'')
		  FROM email_invoices WHERE email_id = $1`, pop3CandEmailID).Scan(&amount, &status)
	if err != nil {
		t.Fatalf("POP3 来源的发票候选没有建档：%v"+
			"\n（IMAPHost 是空的，唯一可能的原文来源是 BodyCache；"+
			"建不出说明 pipeline 第 2 趟仍在走 IMAP-only 的取原文路径）", err)
	}
	t.Logf("建档成功：amount=%s status=%s", amount, status)

	// 本用例只钉住「能建档」这一件事。金额是 0 属**预期**：正文形态是
	// `发票金额共计<span ...>19</span>元`，reAmountTotal 实测匹配不上，
	// 金额设计上留给采集器从附件补。不要在这里断言金额正确。
	for _, e := range rep.Errors {
		if strings.Contains(e, "body fetch") {
			t.Errorf("取原文仍报失败：%s", e)
		}
	}
}

// TestPipelineStep15_POP3CandidateNotArchivedWithoutBodyCache 是负控：
// 去掉 BodyCache 后必须**建不出**台账行。
//
// 作用有两个：
//  1. 证明上一条不是因为「任何 POP3 候选都能建档」而绿；
//  2. 证明缺缓存时是明确失败，而不是退化成拿位置序号去 IMAP FETCH ——
//     后者会把**另一封**邮件存成这封的发票（见 raw_body_resolve.go 文件头）。
func TestPipelineStep15_POP3CandidateNotArchivedWithoutBodyCache(t *testing.T) {
	p, store, _, cleanup := pop3CandidatePipeline(t)
	defer cleanup()
	ctx := context.Background()

	p.BodyCache = nil // 负控：唯一的原文来源被拿掉
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, pop3CandidateAccount(), rep)

	var n int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_invoices WHERE email_id = $1`, pop3CandEmailID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("没有 BodyCache 却仍建出了 %d 行台账；"+
			"那说明取原文走了别的路径（最坏情况：拿位置序号去 IMAP FETCH，"+
			"可能把别人的邮件存成这封发票）", n)
	}
}
