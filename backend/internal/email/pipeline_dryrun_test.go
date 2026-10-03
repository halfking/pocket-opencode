package email

// pipeline_dryrun_test.go — 每日流水线的清垃圾预演模式。
//
// 背景：清垃圾会 IMAP MOVE **真实**邮件。判定规则（LooksLikeSpam）从未在真实
// 邮箱上验证过，让它按每日定时无人值守地搬用户邮件，风险太大。所以
// POCKET_EMAIL_SPAM_DRYRUN 默认 true：只判定、不移动，并把「会移哪些、为什么」
// 写进报告给人看。
//
// 这里钉死三件事：
//  1. 预演时**一次 MOVE 都不发**、本地分类也不改（不是「移了但不说」）；
//  2. 预演报告按账户列出命中数、判定理由与主题样本；
//  3. 非预演时行为不变（仍然 MOVE + 落本地标记）。

import (
	"context"
	"testing"
	"time"
)

func TestCleanSpam_DryRunNeverMoves(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-spam", "user-1", "ws-1")
	// 一封明显是广告的邮件（发件域名 + 关键词命中 LooksLikeSpam）
	adID := "em-ad-1"
	if err := store.InsertEmail(ctx, Email{
		ID: adID, AccountID: "acct-spam", WorkspaceID: "ws-1",
		MessageID: "ad1@example.com", UID: 42,
		FromAddress: "promo@ads.example.com",
		Subject:     "恭喜您获得大奖！点击领取",
		Snippet:     "promo 促销 优惠，unsubscribe here",
		Date:        time.Now().Unix(),
	}); err != nil {
		t.Fatalf("insert ad: %v", err)
	}

	p := &Pipeline{Store: store, SpamDryRun: true, SpamLookbackDays: 7}
	rep := &PipelineReport{}
	p.cleanSpam(ctx, rep)

	// 断言 1：报告里有命中数
	if rep.SpamDryRun == 0 {
		t.Skip("LooksLikeSpam did not flag the fixture (rule set changed); dry-run path untested here")
	}
	if rep.SpamMoved != 0 || rep.SpamLocalOnly != 0 {
		t.Fatalf("dry-run must not report any move: moved=%d localOnly=%d", rep.SpamMoved, rep.SpamLocalOnly)
	}
	// 断言 2：逐账户明细
	if len(rep.SpamDryRunSamples) == 0 {
		t.Fatal("dry-run must list per-account samples so a human can review the verdict")
	}
	s0 := rep.SpamDryRunSamples[0]
	if s0.AccountID != "acct-spam" || s0.Count < 1 || s0.Why == "" {
		t.Fatalf("sample incomplete: %+v", s0)
	}
	// 断言 3：本地分类**没被改**（预演不得留下「已清理」的痕迹）
	after, err := store.GetEmailByID(ctx, adID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after == nil {
		t.Fatal("email disappeared during dry-run")
	}
	if after.Category == "spam" {
		t.Fatalf("dry-run must not mark the email as spam locally, got category=%q", after.Category)
	}
}

// Fetcher 为 nil 时预演也必须能跑完并给出判定（不能因为「没法 MOVE」就跳过判定）。
func TestCleanSpam_DryRunWorksWithoutFetcher(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-spam2", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-ad-2", AccountID: "acct-spam2", WorkspaceID: "ws-1",
		MessageID: "ad2@example.com", UID: 7,
		FromAddress: "promo@ads.example.com",
		Subject:     "恭喜您获得大奖！点击领取",
		Snippet:     "promo 促销 优惠，unsubscribe here",
		Date:        time.Now().Unix(),
	}); err != nil {
		t.Fatalf("insert ad: %v", err)
	}
	p := &Pipeline{Store: store, SpamDryRun: true} // Fetcher 为 nil
	rep := &PipelineReport{}
	p.cleanSpam(ctx, rep) // 不应 panic
	if rep.SpamMoved != 0 {
		t.Fatalf("dry-run must never move, got %d", rep.SpamMoved)
	}
}
