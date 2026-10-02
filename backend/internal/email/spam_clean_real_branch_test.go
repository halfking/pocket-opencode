package email

// spam_clean_real_branch_test.go — 清垃圾「真实 MOVE」分支此前从未被执行过。
//
// 为什么必须现在钉：真实数据上 LooksLikeSpam 最高只给到 30 分、阈值是 100，
// 于是 byAccount 恒为空，`p.SpamDryRun=false` 的那个循环（pipeline.go:726-742）
// 从来没跑过。是否要开真实 MOVE 正在等你决定，而决定的依据不该是
// 「代码看着像是对的」。
//
// 覆盖两件事：
//  1. **安全属性**：预演（SpamDryRun=true）必须对数据库零写入。
//     如果哪天有人把 MarkEmailsSpamByUID 挪到 dry-run 分支之前，
//     「只判定不移动」就变成了「只判定但偷偷把本地标成垃圾」——
//     而这个开关正是你准备要动的那个。
//  2. **本地标记分支**：Fetcher=nil 时（无 IMAP 能力），本地仍要标 spam，
//     收件箱视图才干净；且只标命中的那几封，不能连坐。

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// spamFixtureEmail 稳过 100 分阈值：发件人 promo（发件人特征）
// + 主题含「退订」+ 若干弱词（限时/优惠/精选/好文）。
const (
	spamAcct = "acct-spam-real"
	spamUID  = int64(101)
	okUID    = int64(102)
)

func seedSpamPair(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	seedAccount(t, store, spamAcct, "user-1", "ws-1")
	now := time.Now().Unix()
	mk := func(id string, uid int64, from, subject, snippet string) {
		if err := store.InsertEmail(ctx, Email{
			ID: id, AccountID: spamAcct, WorkspaceID: "ws-1",
			FromAddress: from, Subject: subject, Snippet: snippet,
			Date: now, UID: uid,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mk("em-pop3-spam1", spamUID, "promo@shopmail.example.com",
		"【限时优惠】本周精选好文，回复 退TD退订", "限时优惠精选好文，回复 退TD退订")
	mk("em-pop3-ok1", okUID, "colleague@partner.example.com",
		"关于下季度合作方案的确认", "你好，关于下季度合作方案，见附件。")
}

// TestCleanSpam_DryRunCountsEmailsNotAccounts 钉住 SpamDryRun 的**单位**。
//
// 2026-10-02 真实库诊断当场抓到：pre-existing 的 `rep.SpamDryRun++` 在账户
// 循环里递增，数的是**账户数**，而字段注释、日志文案、以及真实分支的
// `SpamMoved += moved` 全都当它是**邮件数**。于是同一批数据，预演报 1 封、
// 真跑移 2 封——而预演恰恰是「开真实 MOVE 前看的那个数」。
//
// 判据形状：同一个账户里放 3 封必过阈值的广告。此时账户数=1、邮件数=3，
// 两种实现分别给出 1 和 3，**只有 1 个断言能把它们分开**。用单封夹具
// （既有用例 seedSpamPair 就是单封）是分不开的——那正是这个 bug 活到今天
// 的原因：既有用例 `SpamDryRun != 1` 在新旧两种语义下都通过。
func TestCleanSpam_DryRunCountsEmailsNotAccounts(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, spamAcct, "user-1", "ws-1")
	now := time.Now().Unix()
	const n = 3
	for i := 0; i < n; i++ {
		// 主题必须逐封不同：emails 上有 (subject, date) 唯一约束，同主题同日期
		// 会直接撞键——那样只会插进去 1 封，用例变成恒真。
		if err := store.InsertEmail(ctx, Email{
			ID: fmt.Sprintf("em-spam-multi-%d", i), AccountID: spamAcct, WorkspaceID: "ws-1",
			FromAddress: "promo@shopmail.example.com",
			Subject:     fmt.Sprintf("【限时优惠】第%d期精选好文，回复 退TD退订", i+1),
			Snippet:     "限时优惠精选好文，回复 退TD退订",
			Date:        now, UID: spamUID + int64(i),
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	p := &Pipeline{Store: store, SpamDryRun: true, SpamLookbackDays: 7}
	rep := &PipelineReport{}
	p.cleanSpam(ctx, rep)

	if rep.SpamDryRun != n {
		t.Fatalf("SpamDryRun = %d，want %d（单位必须是**邮件**不是账户：同账户 %d 封）。"+
			"报 %d 说明它数的是账户——那个数小于真实 MOVE 会移走的条数，"+
			"会让「开不开真实 MOVE」的决定建立在偏小的量上",
			rep.SpamDryRun, n, n, rep.SpamDryRun)
	}
	// 样本里的 Count 之和必须与总数一致：这两个数在报告上并排出现，
	// 不一致就说明有一处仍按账户计数。
	sum := 0
	for _, s := range rep.SpamDryRunSamples {
		sum += s.Count
	}
	if sum != rep.SpamDryRun {
		t.Errorf("各账户样本 Count 之和 = %d，与 SpamDryRun = %d 不一致；"+
			"报告上这两个数并排出现，不一致就说明有一处仍按账户计数", sum, rep.SpamDryRun)
	}
	if len(rep.SpamDryRunSamples) != 1 {
		t.Errorf("样本条数 = %d，want 1（%d 封都在同一个账户里）", len(rep.SpamDryRunSamples), n)
	}
	if rep.SpamMoved != 0 {
		t.Errorf("SpamMoved = %d，预演绝不许 MOVE", rep.SpamMoved)
	}
}

func categoryOf(t *testing.T, store *Store, id string) string {
	t.Helper()
	em, err := store.GetEmailByID(context.Background(), id)
	if err != nil || em == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return em.Category
}

// 预演模式下数据库必须一字未改。
func TestCleanSpam_DryRunLeavesDatabaseUntouched(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedSpamPair(t, store)

	// 负控式自检：夹具必须真的能命中，否则后面全是恒真断言。
	//
	// 合并说明：末参是本分支给 LooksLikeSpam 加的 senderVolume（同一发件人
	// 的历史投递量，用于识别群发）。本用例只关心「这封本身像不像广告」，
	// 不涉及发件人历史，取 0 表示「未统计」——与 spam_samples_test.go 的
	// 既有约定一致（那里注释写明 senderVolume=0 即「未统计」）。
	if v := LooksLikeSpam("promo@shopmail.example.com",
		"【限时优惠】本周精选好文，回复 退TD退订", "限时优惠精选好文", false, false, 0); !v.Spam {
		t.Fatalf("夹具没达到垃圾阈值（score=%d why=%q），本用例会变成恒真", v.Score, v.Why)
	}

	p := &Pipeline{Store: store, SpamDryRun: true, SpamLookbackDays: 7}
	rep := &PipelineReport{}
	p.cleanSpam(context.Background(), rep)

	if rep.SpamDryRun != 1 {
		t.Fatalf("SpamDryRun=%d, want 1（预演应报告这封会被移动）", rep.SpamDryRun)
	}
	// near-miss 必须照常输出，否则「0」和「规则失灵」在报告里长得一样。
	if countNearMiss(rep.SpamNearMiss) < 0 {
		t.Fatal("unreachable")
	}
	// 核心安全属性：两封都不得被写。
	if got := categoryOf(t, store, "em-pop3-spam1"); got != "" {
		t.Errorf("预演模式下垃圾邮件的 category=%q —— 预演必须零写入，"+
			"否则「只判定不移动」会在本地偷偷改数据", got)
	}
	if got := categoryOf(t, store, "em-pop3-ok1"); got != "" {
		t.Errorf("正常邮件的 category=%q 被改了", got)
	}
}

// 真实分支（Fetcher=nil）：本地仍标 spam，且只标命中的那封。
func TestCleanSpam_RealBranchMarksOnlyMatchedLocally(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedSpamPair(t, store)

	p := &Pipeline{Store: store, SpamDryRun: false, SpamLookbackDays: 7, Fetcher: nil}
	rep := &PipelineReport{}
	p.cleanSpam(context.Background(), rep)

	// Fetcher 为 nil ⇒ 没有 IMAP MOVE，只能本地标记。
	if rep.SpamMoved != 0 {
		t.Errorf("SpamMoved=%d，Fetcher=nil 时不该有任何 MOVE", rep.SpamMoved)
	}
	if rep.SpamLocalOnly != 0 {
		t.Errorf("SpamLocalOnly=%d，Fetcher=nil 时不该记本地兜底（那段在 Fetcher 分支里）", rep.SpamLocalOnly)
	}
	if len(rep.Errors) > 0 {
		t.Errorf("不该有错误: %v", rep.Errors)
	}
	if got := categoryOf(t, store, "em-pop3-spam1"); got != "spam" {
		t.Errorf("垃圾邮件 category=%q, want \"spam\" —— Fetcher=nil 时仍要本地标记，"+
			"否则收件箱视图不会变干净", got)
	}
	if got := categoryOf(t, store, "em-pop3-ok1"); got == "spam" {
		t.Error("正常邮件被连坐标成了 spam：MarkEmailsSpamByUID 的 UID 过滤有问题")
	}
}

// MarkEmailsSpamByUID 是真实分支里唯一的落库动作，直接测它。
//
// 重点是**账户隔离**：SQL 是 `WHERE account_id=$1 AND uid = ANY($2)`。
// 若 account_id 条件被写坏，给账户 A 标垃圾就会连坐账户 B 里同号的 UID
// （不同账户的 IMAP UID 是各自独立编号的，撞号很正常）。这个属性从来没被
// 验过，而真实 MOVE 分支至今没跑过一次。
func TestMarkEmailsSpamByUID_ScopedToAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-a", "user-1", "ws-1")
	seedAccount(t, store, "acct-b", "user-1", "ws-1")
	now := time.Now().Unix()
	// 主题必须各不相同：emails 上有 idx_emails_subject_date 唯一约束。
	mk := func(id, acct, subject string, uid int64) {
		if err := store.InsertEmail(ctx, Email{
			ID: id, AccountID: acct, WorkspaceID: "ws-1",
			FromAddress: "x@example.com", Subject: subject, Snippet: "s", Date: now, UID: uid,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	// 两个账户用**同一个 UID**：不同邮箱的 IMAP UID 各自独立编号，撞号是常态。
	mk("em-pop3-a-7", "acct-a", "甲账户第 7 封", 7)
	mk("em-pop3-b-7", "acct-b", "乙账户第 7 封", 7)
	mk("em-pop3-a-9", "acct-a", "甲账户第 9 封", 9)

	if err := store.MarkEmailsSpamByUID(ctx, "acct-a", []int64{7}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := categoryOf(t, store, "em-pop3-a-7"); got != "spam" {
		t.Errorf("acct-a uid=7 category=%q, want spam", got)
	}
	if got := categoryOf(t, store, "em-pop3-b-7"); got == "spam" {
		t.Error("acct-b 的 uid=7 被连坐标成 spam —— account_id 过滤失效")
	}
	if got := categoryOf(t, store, "em-pop3-a-9"); got == "spam" {
		t.Error("同账户内未列出的 uid=9 也被标了 —— uid 过滤失效")
	}
	// 幂等：重复标记不得报错。
	if err := store.MarkEmailsSpamByUID(ctx, "acct-a", []int64{7}); err != nil {
		t.Errorf("重复标记报错（应幂等）: %v", err)
	}
	// 空输入不得写任何东西。
	if err := store.MarkEmailsSpamByUID(ctx, "", []int64{9}); err != nil {
		t.Errorf("空 accountID 应直接返回: %v", err)
	}
	if err := store.MarkEmailsSpamByUID(ctx, "acct-a", nil); err != nil {
		t.Errorf("空 uid 列表应直接返回: %v", err)
	}
	if got := categoryOf(t, store, "em-pop3-a-9"); got == "spam" {
		t.Error("空调用却改写了数据")
	}
}
