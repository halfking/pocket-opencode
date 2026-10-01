package email

// push_invoice_set_test.go — 「发票推飞书」在流水线侧的筛选与记账。
//
// 这一层此前零覆盖：feishu.Client 有 sheet_test.go、现在又补了 client_send_test.go，
// 但中间那层——「哪些发票该推」「推成功后怎么记 feishu_sent_at」「失败后怎么办」
// —— 没有任何测试。
//
// 它是需求「发送到我们的飞书上」与真实邮箱之间最后一环，而它整条链路因为
// 飞书凭证未提供而一次都没跑过。中间这层出问题（比如把已推过的又推一遍、
// 或者失败也记成功）不会有任何用例转红。
//
// 判据全部落在**数据库读回的副本**上，不用内存里的 inv 变量——只有这样才能
// 证明记账真的写进去了。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// stubPusher 记录每次推送，并按 invoice ID 决定成败。
type stubPusher struct {
	available bool
	calls     []string
	failFor   map[string]bool
}

func (s *stubPusher) Available() bool { return s.available }

func (s *stubPusher) PushInvoice(ctx context.Context, inv Invoice, absPath string) error {
	s.calls = append(s.calls, inv.ID)
	if s.failFor[inv.ID] {
		return errStubPush{}
	}
	return nil
}

type errStubPush struct{}

func (errStubPush) Error() string { return "stub: chat rejected" }

// seedPushableInvoice 建一张发票 + 它的源邮件 + 落盘文件。
// 账户由调用方建一次（同一测试里会建多张发票，重复建会撞主键）。
func seedPushableInvoice(t *testing.T, store *Store, dir, id, fileName string, status string, feishuSentAt int64) Invoice {
	t.Helper()
	ctx := context.Background()
	// 主题必须各不相同：emails 上有 idx_emails_subject_date 唯一约束。
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-push-" + id, AccountID: "acct-push", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "发票 " + id, Snippet: "发票",
		Date: 1750000000, UID: 1,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	rel := filepath.Join("email-invoices", "ws_user-admin", fileName)
	// 只建父目录：MkdirAll(整条含文件名的路径) 会把文件名本身建成目录。
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("%PDF-1.4 x"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	inv := Invoice{
		ID: id, EmailID: "em-pop3-push-" + id, AccountID: "acct-push",
		UserID: "user-1", WorkspaceID: "ws-1", Status: status,
		Amount: 100, Seller: "甲", FileName: fileName, FilePath: rel,
		FeishuSentAt: feishuSentAt,
	}
	if _, err := store.UpsertInvoice(ctx, &inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}
	if feishuSentAt > 0 {
		// 构造「已推送过」的历史行
		if _, err := store.UpsertInvoice(ctx, &inv, "user-1", "ws-1"); err != nil {
			t.Fatalf("upsert again: %v", err)
		}
	}
	return inv
}

func reload(t *testing.T, store *Store, emailID string) Invoice {
	t.Helper()
	inv, err := store.GetInvoiceByEmailID(context.Background(), emailID)
	if err != nil || inv == nil {
		t.Fatalf("读回发票: %v", err)
	}
	return *inv
}

// 只推「已下载、未推过、有落盘路径」的那几张，其余一律跳过。
func TestPushInvoiceSet_OnlyPushesEligibleInvoices(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	dir := t.TempDir()
	ctx := context.Background()

	seedAccount(t, store, "acct-push", "user-1", "ws-1")
	ok := seedPushableInvoice(t, store, dir, "inv-ok", "a-100.00.pdf", "downloaded", 0)
	seedPushableInvoice(t, store, dir, "inv-sent", "b-200.00.pdf", "downloaded", 1700000000)
	seedPushableInvoice(t, store, dir, "inv-pending", "c-300.00.pdf", "pending", 0)
	seedPushableInvoice(t, store, dir, "inv-failed", "d-400.00.pdf", "failed", 0)

	sp := &stubPusher{available: true}
	p := &Pipeline{Store: store, Pusher: sp, DataDir: dir}
	rep := &PipelineReport{}
	p.pushInvoiceSet(ctx, []Invoice{ok}, "user-1", "ws-1", rep)

	// 本轮只喂了 ok，但下面把四张一起喂一遍，验证筛选规则。
	all := []Invoice{
		ok,
		reload(t, store, "em-pop3-push-inv-sent"),
		reload(t, store, "em-pop3-push-inv-pending"),
		reload(t, store, "em-pop3-push-inv-failed"),
	}
	sp.calls = nil
	rep = &PipelineReport{}
	p.pushInvoiceSet(ctx, all, "user-1", "ws-1", rep)

	if len(sp.calls) != 1 || sp.calls[0] != "inv-ok" {
		t.Fatalf("实际推送=%v, want 仅 [inv-ok]（已推过 / pending / failed 都要跳过）", sp.calls)
	}
	if rep.FeishuPushed != 1 {
		t.Errorf("FeishuPushed=%d, want 1", rep.FeishuPushed)
	}
	// 记账必须真的落库，否则下一轮会把同一张再推一遍（群里重复刷屏）。
	got := reload(t, store, "em-pop3-push-inv-ok")
	if got.FeishuSentAt == 0 {
		t.Error("推送成功后 feishu_sent_at 仍为 0 —— 下一轮会重复推送")
	}
}

// 推送失败必须**不**记账：否则这张发票永远不会重试，也永远不会进台账兜底。
func TestPushInvoiceSet_FailureDoesNotMarkSent(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	dir := t.TempDir()
	ctx := context.Background()

	seedAccount(t, store, "acct-push", "user-1", "ws-1")

	inv := seedPushableInvoice(t, store, dir, "inv-bad", "a-100.00.pdf", "downloaded", 0)
	sp := &stubPusher{available: true, failFor: map[string]bool{"inv-bad": true}}
	p := &Pipeline{Store: store, Pusher: sp, DataDir: dir}
	rep := &PipelineReport{}
	p.pushInvoiceSet(ctx, []Invoice{inv}, "user-1", "ws-1", rep)

	if rep.FeishuPushed != 0 {
		t.Errorf("FeishuPushed=%d, want 0", rep.FeishuPushed)
	}
	if rep.FeishuFailed != 1 {
		t.Errorf("FeishuFailed=%d, want 1", rep.FeishuFailed)
	}
	if len(rep.Errors) == 0 {
		t.Error("推送失败却没有记进 Errors，报告会显示成「一切正常」")
	}
	got := reload(t, store, "em-pop3-push-inv-bad")
	if got.FeishuSentAt != 0 {
		t.Errorf("推送失败却写了 feishu_sent_at=%d —— 这张发票会被永久跳过，"+
			"再也不会重试、也不会进台账兜底", got.FeishuSentAt)
	}
}

// 飞书不可用时必须整轮跳过、且一个字节都不写（台账兜底才是正路）。
func TestPushInvoiceSet_SkipsEntirelyWhenPusherUnavailable(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	dir := t.TempDir()
	ctx := context.Background()

	seedAccount(t, store, "acct-push", "user-1", "ws-1")

	inv := seedPushableInvoice(t, store, dir, "inv-nochat", "a-100.00.pdf", "downloaded", 0)
	sp := &stubPusher{available: false}
	p := &Pipeline{Store: store, Pusher: sp, DataDir: dir}
	rep := &PipelineReport{}
	p.pushInvoiceSet(ctx, []Invoice{inv}, "user-1", "ws-1", rep)

	if len(sp.calls) != 0 {
		t.Errorf("Pusher 不可用却仍发起了推送: %v", sp.calls)
	}
	if rep.FeishuPushed != 0 || rep.FeishuFailed != 0 {
		t.Errorf("不可用时不该有计数: pushed=%d failed=%d", rep.FeishuPushed, rep.FeishuFailed)
	}
	if got := reload(t, store, "em-pop3-push-inv-nochat"); got.FeishuSentAt != 0 {
		t.Errorf("不可用时却写了 feishu_sent_at=%d", got.FeishuSentAt)
	}
	// 也不该报错：飞书没配不是错误，需求本来就允许走台账兜底。
	if len(rep.Errors) != 0 {
		t.Errorf("Pusher=nil/不可用不该报错: %v", rep.Errors)
	}
}

// Pusher 字段为 nil（ensurePipeline 未注入）时同样必须安全返回。
func TestPushInvoiceSet_NilPusherIsSafe(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	dir := t.TempDir()
	seedAccount(t, store, "acct-push", "user-1", "ws-1")
	inv := seedPushableInvoice(t, store, dir, "inv-nil", "a-100.00.pdf", "downloaded", 0)

	p := &Pipeline{Store: store, Pusher: nil, DataDir: dir}
	rep := &PipelineReport{}
	p.pushInvoiceSet(context.Background(), []Invoice{inv}, "user-1", "ws-1", rep)
	if rep.FeishuPushed != 0 || len(rep.Errors) != 0 {
		t.Errorf("Pusher=nil 时应安全返回: %+v", rep)
	}
}
