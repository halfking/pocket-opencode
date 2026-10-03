package email

// pipeline_classify_step_test.go — 需求 4「定时…然后进行处理」在每日流水线上的
// 分类环节（第 1.6 步）的判据。
//
// ## 这些用例在防什么
//
// 一个「接线正确但顺序错误」的实现是完全绿的、且完全没有用：分类排在第 3 步
// 提醒**之后**时，本轮新邮件的 importance 仍然在提醒判定之后才写进去，于是
// RemindersSent=0，而报告上还会写着「classified N 封」——看起来分类已经跑过、
// 需求 4 却就是不响。所以第一条用例的判据钉在 **RemindersSent** 这个后果上，
// 而不是「分类函数被调用过」。
//
// 第二条防的是可观测性回归：`ClassifySkip` 为空与「跑了但没有待分类的邮件」
// 在报告上都是「0 条提醒」。没有这个字段，需求 4 不响的时候无从判断该去配
// 分类器还是去查邮件。
//
// ## 负控
//
// 1) 把 `p.classifyPending(...)` 从第 1.6 步挪到第 3 步之后
//    → TestClassifiedEmailIsRemindedInTheSameRun 转红（RemindersSent 0≠1）。
// 2) 把 classifyPending 里的 `if p.Classifier == nil` 分支去掉
//    → TestNoClassifierIsReportedAsSkipped 转红（panic / ClassifySkip 空）。
// 两条都实测过。

import (
	"context"
	"strings"
	"testing"
)

// classifiedEmail 记一封被分类器判定为 high 的邮件。
func assertRemindedOnce(t *testing.T, rep *PipelineReport, notifier *fakeNotifier, id, why string) {
	t.Helper()
	if rep.RemindersSent != 1 {
		t.Fatalf("%s：RemindersSent=%d，want 1（errors=%v skip=%q）", why, rep.RemindersSent, rep.Errors, rep.ClassifySkip)
	}
	if len(notifier.notified) != 1 || notifier.notified[0] != id {
		t.Fatalf("%s：派发的是 %v，want [%s]", why, notifier.notified, id)
	}
}

// 一封**未归类**的邮件，在同一轮流水线里被分类为 high 之后就必须被提醒。
//
// 这正是需求 4 的全部意义：定时收信 → 分类 → 对重要邮件提醒。
// 断言钉在 RemindersSent（后果）而不是「分类器被调用过」（过程）。
func TestClassifiedEmailIsRemindedInTheSameRun(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	seedScopedAccount(t, store, "acct-c", "user-1", "ws-a")
	// category 与 importance 都空 ⇒ 这封在分类前不会被任何提醒逻辑选中。
	seedEmail(t, store, "em-unclass", "acct-c", "ws-a", "合同即将到期")

	notifier := &fakeNotifier{}
	p.Notifier = notifier

	var classifyCalls int
	p.Classifier = func(ctx context.Context, userID, wsID string, limit int) (int, error) {
		classifyCalls++
		// 必须真的写库：判据要验的是「分类的结果有没有被后面的步骤用上」。
		if err := store.SetClassificationWithReasonScoped(ctx, "em-unclass", userID, wsID,
			"work", "high", "合同到期提醒", "", "test"); err != nil {
			return 0, err
		}
		return 1, nil
	}

	rep := p.Run(context.Background())

	if classifyCalls != 1 {
		t.Fatalf("分类器被调用 %d 次，want 1", classifyCalls)
	}
	if rep.Classified != 1 {
		t.Fatalf("report.classified=%d，want 1", rep.Classified)
	}
	if rep.ClassifySkip != "" {
		t.Fatalf("分类器已注入却记了 skip=%q", rep.ClassifySkip)
	}
	assertRemindedOnce(t, rep, notifier, "em-unclass", "本轮分类为 high")
}

// 没有分类器时，第 1.6 步整步跳过，**并且**报告里要留下原因。
//
// 不留原因的后果不是「少一个字段」：没有分类器时新邮件 importance 恒空，
// 需求 4 恒 0 条提醒，而 RemindersUnclassified 这个数字读起来和「还没轮到
// 分类」一模一样——排查方向会被带到错误的配置上。
func TestNoClassifierIsReportedAsSkipped(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	seedScopedAccount(t, store, "acct-c", "user-1", "ws-a")
	seedEmail(t, store, "em-unclass", "acct-c", "ws-a", "合同即将到期")
	notifier := &fakeNotifier{}
	p.Notifier = notifier
	p.Classifier = nil // 默认：定时分类未开启

	rep := p.Run(context.Background())

	if rep.Classified != 0 {
		t.Fatalf("未注入分类器却 classified=%d", rep.Classified)
	}
	if strings.TrimSpace(rep.ClassifySkip) == "" {
		t.Fatal("ClassifySkip 为空 —— 「没配分类器」与「跑了但没有待分类的邮件」在报告上就分不开了")
	}
	// 判据必须**指向被断言的对象**：提示里要说出后果与开关名，
	// 否则它只是一句「已跳过」，读者仍然不知道该做什么。
	for _, want := range []string{"POCKET_EMAIL_CLASSIFY_VIA_GATEWAY", "importance"} {
		if !strings.Contains(rep.ClassifySkip, want) {
			t.Fatalf("ClassifySkip=%q，缺少 %q（提示必须指向开关名与后果）", rep.ClassifySkip, want)
		}
	}
}

// 配了分类器、但确实没有待分类邮件时，**不能**记 skip。
//
// 这是与上一条成对的可区分性：两种情况在 RemindersSent 上都是 0，
// 判据必须让它们在报告上不同。
func TestClassifierRanButNothingToDoIsNotReportedAsSkipped(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	seedScopedAccount(t, store, "acct-c", "user-1", "ws-a")
	// 已经归类的邮件：分类器会看到 0 条待分类。
	seedScoredEmail(t, store, "em-done", "acct-c", "ws-a", "a@b.c", "已归类", "work", "low")
	notifier := &fakeNotifier{}
	p.Notifier = notifier

	var called bool
	p.Classifier = func(context.Context, string, string, int) (int, error) {
		called = true
		return 0, nil
	}

	rep := p.Run(context.Background())

	if !called {
		t.Fatal("分类器没被调用")
	}
	if rep.Classified != 0 {
		t.Fatalf("classified=%d，want 0", rep.Classified)
	}
	if rep.ClassifySkip != "" {
		t.Fatalf("分类器跑了却记了 skip=%q —— 这会让「没配」与「没活干」分不开", rep.ClassifySkip)
	}
	if rep.RemindersSent != 0 {
		t.Fatalf("low 重要度不该被提醒，RemindersSent=%d", rep.RemindersSent)
	}
}

// 分类整体失败时不能静默：报告里必须留下错误，且成功数与失败数不混。
func TestClassifierFailureIsVisibleAndNonFatal(t *testing.T) {
	p, store, cleanup := newPipelineFixture(t)
	defer cleanup()
	seedScopedAccount(t, store, "acct-c", "user-1", "ws-a")
	seedEmail(t, store, "em-unclass", "acct-c", "ws-a", "合同即将到期")
	notifier := &fakeNotifier{}
	p.Notifier = notifier

	p.Classifier = func(context.Context, string, string, int) (int, error) {
		return 0, context.DeadlineExceeded // 模拟网关超时
	}

	rep := p.Run(context.Background())

	if rep.ClassifySkip != "" {
		t.Fatalf("分类器跑过（虽然失败）却记了 skip=%q", rep.ClassifySkip)
	}
	found := false
	for _, e := range rep.Errors {
		if strings.Contains(e, "classify") {
			found = true
		}
	}
	if !found {
		t.Fatalf("分类失败没有进 report.Errors：%v", rep.Errors)
	}
	// 分类失败不得让整轮流水线死掉：第 3 步仍然要跑完。
	if rep.FinishedAt == 0 {
		t.Fatal("FinishedAt=0，流水线没有正常收尾")
	}
}
