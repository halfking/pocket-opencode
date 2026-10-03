package server

// server_email_pipeline_lock_test.go — 每日**定时**流水线的跨进程互斥接线。
//
// ## 为什么测这里
//
// 三个 pocketd 实例共享同一个 PG schema，各自在本进程排了同一点的每日流水线。
// 进程内的 emailPipelineMu 只挡本进程，于是同一点跑 N 轮；重要邮件提醒的
// MarkEmailsNotified 标记写在整个推送循环**之后**，而 notifications 表除主键外
// 没有唯一约束，于是同一封邮件被推 N 份（实测基线 24 行 → 最多 126 行）。
//
// 修法是给 RunEmailPipeline（**只有**它，scheduler 的定时入口）加一把
// PostgreSQL 会话级 advisory lock。这组用例守的是接线本身的四条语义。
//
// ## 为什么手工触发不在这组用例的保护范围内
//
// 需求 1 说的是「每天定时**或手工**进行邮件接收」。手工那一半走
// handleEmailPipelineRun → runEmailPipeline，刻意**不**加锁：用户显式点
// 「跑一次」是明确要求，不该被另一轮挡住。TestManualPathIgnoresTheLock 钉住
// 这条边界——有人「顺手」把锁挪进 runEmailPipeline 就会转红。
//
// ## 负控（实测）
//
//  - 把 `case state == email.DailyPipelineLockBusy` 改成 `case state != email.DailyPipelineLockAcquired`
//    → TestRunEmailPipeline_LockUnavailableStillRuns 转红。锁机制坏掉时会被
//    误判成「别人在跑」而跳过，于是一次数据库抖动让每日流水线永久静默。
//  - 删掉整个 `if s.cfg.EmailPipelineAdvisoryLock` 块
//    → TestRunEmailPipeline_SkipsWhenAnotherInstanceHoldsLock 转红。
//  - 把锁从 RunEmailPipeline 挪进 runEmailPipeline
//    → TestManualPathIgnoresTheLock 转红。

import (
	"context"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

const lockSkipMarker = "already running in another instance"

func TestRunEmailPipeline_SkipsWhenAnotherInstanceHoldsLock(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	ctx := context.Background()

	// 模拟"另一个 pocketd 实例正持有这把锁"。
	release, state, err := store.TryLockDailyPipeline(ctx)
	if err != nil {
		t.Fatalf("prime the lock: %v", err)
	}
	if state.String() != "acquired" {
		t.Fatalf("priming the lock returned %v; the rest of this test proves nothing", state)
	}
	defer release()

	s := &Server{
		emailStore: store,
		cfg:        config.Config{EmailPipelineAdvisoryLock: true},
	}
	rep := s.RunEmailPipeline(ctx)
	if rep == nil {
		t.Fatal("RunEmailPipeline returned nil report")
	}
	if !containsAny(rep.Errors, lockSkipMarker) {
		t.Fatalf("a second instance ran the daily pipeline while another held the lock; "+
			"report errors = %v, want one containing %q", rep.Errors, lockSkipMarker)
	}
	// 关键：必须是"因为锁而跳过"，不能是"因为没配 pipeline"——后者说明它
	// 其实走完了 runEmailPipeline，等于锁没起作用。
	if containsAny(rep.Errors, "email pipeline not configured") {
		t.Fatalf("report shows the skip came from the pipeline being unconfigured, "+
			"not from the advisory lock: %v", rep.Errors)
	}
}

// 三态里的 Unavailable 必须**降级照跑**，不能当 Busy 跳过。
//
// 这条是本组里最容易被写坏的：把 `state == Busy` 简化成 `state != Acquired`
// 看起来无害，实际上一次数据库抖动 / 没有连接池的部署会让每日流水线从那天
// 起永远只打一行"跳过"，而且没人分得清是"别人在跑"还是"锁坏了"。
func TestRunEmailPipeline_LockUnavailableStillRuns(t *testing.T) {
	s := &Server{
		// emailStore 为 nil ⇒ TryLockDailyPipeline 返回 Unavailable。
		emailStore: nil,
		cfg:        config.Config{EmailPipelineAdvisoryLock: true},
	}
	rep := s.RunEmailPipeline(context.Background())
	if rep == nil {
		t.Fatal("RunEmailPipeline returned nil report")
	}
	if containsAny(rep.Errors, lockSkipMarker) {
		t.Fatalf("an unavailable lock must degrade to running, not skipping; "+
			"one DB hiccup would then silence the daily pipeline forever: %v", rep.Errors)
	}
	// 它应当真的往下走了：无 pipeline 时是这条错误。
	if !containsAny(rep.Errors, "email pipeline not configured") {
		t.Fatalf("expected execution to continue to the unconfigured-pipeline error, got %v",
			rep.Errors)
	}
}

// 逃生门：显式关掉锁就应当不拦。仅供无 PG 的本地部署与手工重跑定时轮次。
func TestRunEmailPipeline_AdvisoryLockDisabledDoesNotSkip(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	ctx := context.Background()

	release, _, err := store.TryLockDailyPipeline(ctx)
	if err != nil {
		t.Fatalf("prime the lock: %v", err)
	}
	defer release()

	s := &Server{
		emailStore: store,
		cfg:        config.Config{EmailPipelineAdvisoryLock: false},
	}
	rep := s.RunEmailPipeline(ctx)
	if rep == nil {
		t.Fatal("RunEmailPipeline returned nil report")
	}
	if containsAny(rep.Errors, lockSkipMarker) {
		t.Fatalf("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK=false must not skip: %v", rep.Errors)
	}
}

// 手工触发路径不受这把锁约束。
func TestManualPathIgnoresTheLock(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	ctx := context.Background()

	release, _, err := store.TryLockDailyPipeline(ctx)
	if err != nil {
		t.Fatalf("prime the lock: %v", err)
	}
	defer release()

	s := &Server{
		emailStore: store,
		cfg:        config.Config{EmailPipelineAdvisoryLock: true},
	}
	// runEmailPipeline 正是 handleEmailPipelineRun 走的那条路。
	rep := s.runEmailPipeline(ctx, nil)
	if rep == nil {
		t.Fatal("runEmailPipeline returned nil report")
	}
	if containsAny(rep.Errors, lockSkipMarker) {
		t.Fatalf("a manual run must not be blocked by the daily-pipeline lock; "+
			"the user explicitly asked for it: %v", rep.Errors)
	}
}

func containsAny(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
