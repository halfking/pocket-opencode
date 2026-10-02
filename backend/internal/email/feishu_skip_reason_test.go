package email

// feishu_skip_reason_test.go — 「飞书没跑」与「跑了但 0 条」必须在报告上可区分。
//
// ## 缺陷（2026-10-02 真实库发现）
//
// 真实库两张发票行的 feishu_sent_at 都是 0。而
// POCKET_FEISHU_APP_ID / POCKET_FEISHU_APP_SECRET /
// POCKET_FEISHU_INVOICE_CHAT_ID / POCKET_FEISHU_INVOICE_FOLDER_TOKEN
// 四项在本机进程/用户/机器/.env/scripts **全都未配置**。
//
// 也就是说：需求 3 的主交付物「发送到我们的飞书上」**一次都没执行过**。
// 但修复前的 pushInvoiceSet 是这么写的：
//
//	if p.Pusher == nil || !p.Pusher.Available() { return }   // 静默
//
// 于是报告上 FeishuPushed=0、FeishuFailed=0 —— 与「飞书配好了、这轮确实
// 没有可推的发票」**逐字段相同**。看报告的人无从判断该去配凭证，还是
// 该去查为什么没有发票。
//
// 这个缺口不是我发现的，是代码自己记着的：
// server_email_pipeline_adapters_test.go:205 的注释原话——「一个『什么都没做』
// 的报告和一个『全都推成功了』的报告在这几个字段上无法区分」。本文件把它补上。
//
// ## 为什么判据直接调 pushInvoiceSet
//
// 它是本包内的未导出函数，测试同包可以直接调；不需要 Store、也不碰数据库
// （跳过分支在任何 I/O 之前就 return）。**不复制判定逻辑**——
// account-stamp-units 与 flashcards-sync-watermark 各栽过一次「测试抄了
// 一份生产判定」的跟头。
//
// ## 负控
//
// 把 pushInvoiceSet 里两处 `rep.FeishuSkip = ...` 删掉 → 本文件转红。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 不可用的 pusher：模拟「凭证没配」。
type skipReasonUnavailablePusher struct{ InvoicePusher }

func (skipReasonUnavailablePusher) Available() bool { return false }
func (skipReasonUnavailablePusher) PushInvoice(context.Context, Invoice, string) error {
	panic("不可用的 pusher 不该被调用到 PushInvoice")
}

// 可用的 pusher：反向护栏，保证这条判据不是靠「一律跳过」蒙对的。
//
// 让它**返回错误**而不是成功：这样 PushInvoice 确实被调到了（推送路径真的
// 走过），但 pushed 保持为空，函数末尾的 Store.MarkInvoicesFeishuSent
// 不会被触到——本用例不需要数据库。返回成功反而会在 nil Store 上 panic。
type skipReasonAvailablePusher struct{ calls int }

func (p *skipReasonAvailablePusher) Available() bool { return true }
func (p *skipReasonAvailablePusher) PushInvoice(context.Context, Invoice, string) error {
	p.calls++
	return errFakePush
}

var errFakePush = errors.New("fake push failure (test)")

func TestPushInvoiceSet_RecordsSkipReasonWhenFeishuUnavailable(t *testing.T) {
	p := &Pipeline{Pusher: skipReasonUnavailablePusher{}}
	rep := &PipelineReport{}

	p.pushInvoiceSet(context.Background(), nil, "u", "ws", rep)

	if rep.FeishuSkip == "" {
		t.Fatal("FeishuSkip 为空：飞书没跑与「跑了但 0 条」在报告上又变得无法区分，" +
			"而这正是本文件要防的回归")
	}
	if !strings.Contains(rep.FeishuSkip, "feishu") {
		t.Errorf("FeishuSkip=%q，应能看出是飞书这一环", rep.FeishuSkip)
	}
	// 原因里必须带出「要配什么」——运维看到这句话就该知道下一步做什么，
	// 而不是只知道「被跳过了」。
	if !strings.Contains(rep.FeishuSkip, "POCKET_FEISHU") {
		t.Errorf("FeishuSkip=%q，应点名要配的环境变量", rep.FeishuSkip)
	}
}

func TestPushInvoiceSet_NilPusherAlsoRecordsSkip(t *testing.T) {
	// Pusher 为 nil 与 Available()==false 是两种不同的失灵，原因文案不该相同，
	// 否则又退回「分不清是哪一种」。
	p := &Pipeline{}
	rep := &PipelineReport{}

	p.pushInvoiceSet(context.Background(), nil, "u", "ws", rep)

	if rep.FeishuSkip == "" {
		t.Fatal("Pusher 为 nil 时也必须留下原因")
	}
	if strings.Contains(rep.FeishuSkip, "POCKET_FEISHU") {
		t.Errorf("Pusher 为 nil 不等于凭证缺失，原因文案不该指向环境变量：%q", rep.FeishuSkip)
	}
}

func TestPushInvoiceSet_AvailableLeavesSkipEmpty(t *testing.T) {
	// 反向护栏：飞书可用时不得写跳过原因，否则这个字段会变成永远非空的摆设。
	pusher := &skipReasonAvailablePusher{}
	p := &Pipeline{Pusher: pusher}
	rep := &PipelineReport{}

	p.pushInvoiceSet(context.Background(), []Invoice{
		{ID: "inv-1", Status: "downloaded", FilePath: "a.pdf", FileName: "a.pdf"},
	}, "u", "ws", rep)

	if pusher.calls != 1 {
		t.Errorf("飞书可用时应当真的尝试推送，calls=%d —— 本用例的前提不成立", pusher.calls)
	}
	if rep.FeishuSkip != "" {
		t.Errorf("飞书可用时 FeishuSkip 必须是空串，得到 %q", rep.FeishuSkip)
	}
	// 推失败要落在 FeishuFailed 而不是 FeishuSkip：两者是不同性质的失灵。
	if rep.FeishuFailed != 1 {
		t.Errorf("FeishuFailed=%d，want 1（推送失败属失败，不是跳过）", rep.FeishuFailed)
	}
}
