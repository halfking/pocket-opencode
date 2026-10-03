package email

// raw_body_dead_test.go —— 「原文已被从服务端删除」的淘汰记账与报错归因。
//
// ## 要守的三件事
//
//  1. 归因按**错误身份**判定，不按文案（classifyRawBodyFetchFailure）；
//  2. streak 只在「服务端已无此消息」时累加，连续到阈值才置死信标记；
//     其它成因（网络/凭据/POP3 无缓存）必须把它清零——否则一次抖动就能把
//     好邮件永久判死，而误判的代价是它再也不参与发票建档；
//  3. 死信标记会让第 1.5 步**不再为它排 job**，也就是真正省下取原文预算。
//
// ## 负控（每条都实测可转红）
//
//  - 把 classifyRawBodyFetchFailure 改成按文案匹配 `strings.Contains(err.Error(),
//    "matched 0 messages")` → TestClassifyRawBodyFetchFailure_IdentityNotWording 转红。
//  - 把 MarkRawBodyGone 里 `WHEN $2` 的条件去掉（任何失败都累加）
//    → TestRawBodyDeadLetter_NonGoneFailureResetsStreak 转红。
//  - 把阈值从 3 改成 1 → TestRawBodyDeadLetter_StreakReachesThresholdOnlyThen 转红。
//  - 把第 1.5 步里 `if deadLetters[e.ID] { continue }` 删掉
//    → TestPipelineStep15_DeadLetterIsNotProcessed 转红。
//  - 把 mapJobsToKeptPositions 的 -1 哨兵换成零值
//    → TestMapJobsToKeptPositions_DeferredJobsDoNotAliasKeptZero 转红。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── 归因：按身份，不按文案 ─────────────────────────────────────────

func TestClassifyRawBodyFetchFailure_IdentityNotWording(t *testing.T) {
	pop3Err := &pop3RawBodyError{NoCache: true}

	cases := []struct {
		name string
		err  error
		want rawBodyFailureKind
		why  string
	}{
		{"nil 不算失败", nil, rawBodyFailureNone, "没有被拉过原文时不该被算成任何一种失败"},
		{"gone 裸哨兵", ErrRawBodyGone, rawBodyFailureGone, "最直接的形态"},
		{"gone 被包装", fmt.Errorf("fetch raw uid=%d (go-imap): %w; textproto fallback: %v",
			9, ErrRawBodyGone, nil), rawBodyFailureGone,
			"生产里的实际形态：哨兵外面还裹着 uid 与降级通道的文案"},
		{"POP3 无缓存", pop3Err, rawBodyFailurePOP3, "与 IMAP 无关，归因必须分开"},
		{"POP3 被包装", fmt.Errorf("step1.5: %w", pop3Err), rawBodyFailurePOP3, "同上，穿一层包装"},
		{"网络错误", errors.New("dial imap.exmail.qq.com:993: i/o timeout"),
			rawBodyFailureOther, "这一类才谈得上「IMAP 侧」"},
		{"凭据错误", errors.New("decrypt credential: cipher: message authentication failed"),
			rawBodyFailureOther, "同上"},
		// 这一条是本文件的核心：文案里**出现了** gone 的字样，但不是 gone。
		{"文案含 matched 0 messages 却不是 gone",
			errors.New("go-imap matched 0 messages for uid=5 (说明文字而已)"),
			rawBodyFailureOther,
			"若按文案匹配，这一条会被误记成一次死信观察；连续三轮就足以判死一封好邮件"},
	}
	for _, c := range cases {
		if got := classifyRawBodyFetchFailure(c.err); got != c.want {
			t.Errorf("%s: classifyRawBodyFetchFailure = %v, want %v — %s",
				c.name, got, c.want, c.why)
		}
	}
}

// ── job 下标映射：顺延的 job 不能别名到 kept[0] ────────────────────

func TestMapJobsToKeptPositions_DeferredJobsDoNotAliasKeptZero(t *testing.T) {
	// 3 个 job，预算只够 1 个：keptIdx 只有 {1}。
	// pos[2] 与 pos[0] 都必须是 -1 —— 它们本轮没被拉。
	pos := mapJobsToKeptPositions(3, []int{1})

	for i, want := range map[int]int{0: -1, 1: 0, 2: -1} {
		if pos[i] != want {
			t.Errorf("pos[%d] = %d, want %d", i, pos[i], want)
		}
	}
	// 正面点名被守的性质：顺延的 job 绝不能映到 0，
	// 否则它会读到 bodies[0]（另一封邮件的解析结果）。
	if pos[0] == 0 || pos[2] == 0 {
		t.Fatalf("a deferred job maps to position 0 — reading bodies[0] would splice "+
			"one message's body into another's invoice record")
	}
}

func TestMapJobsToKeptPositions_AllKept(t *testing.T) {
	pos := mapJobsToKeptPositions(3, []int{0, 1, 2})
	for i := 0; i < 3; i++ {
		if pos[i] != i {
			t.Errorf("pos[%d] = %d, want %d", i, pos[i], i)
		}
	}
}

func TestMapJobsToKeptPositions_EmptyAndOutOfRange(t *testing.T) {
	if pos := mapJobsToKeptPositions(0, nil); len(pos) != 0 {
		t.Errorf("mapJobsToKeptPositions(0, nil) = %v, want empty", pos)
	}
	// keptIdx 里越界的下标必须被忽略，而不是 panic 或写穿切片。
	pos := mapJobsToKeptPositions(2, []int{0, 7, -1})
	if pos[0] != 0 || pos[1] != -1 {
		t.Errorf("out-of-range keptIdx was not ignored: %v", pos)
	}
}

// ── 记账：阈值、重置、复检（真库） ────────────────────────────────

// insertDeadLetterFixture 造一封最小可用的邮件行，返回它的 id。
//
// 账户 ID 随邮件 ID 派生：同一个 store 里造两封邮件时，若共用一个账户 ID，
// 第二次 InsertAccount 会撞 email_accounts 主键——而那个报错与被测性质
// 无关，会让用例红在一个假的原因上。
func insertDeadLetterFixture(t *testing.T, store *Store, id string) {
	t.Helper()
	ctx := context.Background()
	acctID := "acct-dead-" + id
	if err := store.InsertAccount(ctx, &Account{
		ID: acctID, UserID: "u-dead", WorkspaceID: "ws-dead",
		DisplayName: "dead", EmailAddress: "dead@example.com",
		AuthType: "password", Enabled: true, CreatedAt: time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := store.InsertEmail(ctx, Email{
		ID: id, AccountID: acctID, WorkspaceID: "ws-dead",
		FromAddress: "noreply@example.com", Subject: "AWS 账户告警",
		Date: time.Now().Unix(), UID: 10443,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
}

func TestRawBodyDeadLetter_StreakReachesThresholdOnlyThen(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const id = "em-dead-streak"
	insertDeadLetterFixture(t, store, id)

	for round := 1; round <= rawBodyGoneStreakThreshold; round++ {
		streak, dead, err := store.MarkRawBodyGone(ctx, id, true)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if streak != round {
			t.Errorf("round %d: streak = %d, want %d", round, streak, round)
		}
		wantDead := round == rawBodyGoneStreakThreshold
		if dead != wantDead {
			t.Errorf("round %d: dead = %v, want %v（阈值 %d 轮之前不能判死）",
				round, dead, wantDead, rawBodyGoneStreakThreshold)
		}
	}

	deadIDs, err := store.ListRawBodyDeadEmailIDs(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !deadIDs[id] {
		t.Errorf("crossing the threshold did not put %s in the dead-letter set", id)
	}
}

func TestRawBodyDeadLetter_NonGoneFailureResetsStreak(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const id = "em-dead-reset"
	insertDeadLetterFixture(t, store, id)

	// 先攒到阈值前一轮。
	streak, dead, err := store.MarkRawBodyGone(ctx, id, true)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if streak != 1 || dead {
		t.Fatalf("after one gone: streak=%d dead=%v, want 1/false", streak, dead)
	}

	// 一次网络抖动：成因不是 gone，必须把 streak 清零。
	streak, dead, err = store.MarkRawBodyGone(ctx, id, false)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if streak != 0 {
		t.Errorf("streak = %d after a non-gone failure, want 0 — "+
			"a network blip must not accumulate toward retiring a healthy message", streak)
	}
	if dead {
		t.Error("dead = true after only one gone observation plus a blip")
	}

	// 清零后重新攒，仍需满阈值轮数。
	for round := 1; round <= rawBodyGoneStreakThreshold; round++ {
		if _, dead, err = store.MarkRawBodyGone(ctx, id, true); err != nil {
			t.Fatalf("re-accumulate round %d: %v", round, err)
		}
	}
	if !dead {
		t.Error("dead = false after re-accumulating a full threshold; the streak never restarted")
	}
}

func TestRawBodyDeadLetter_ReArmOnlyStale(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const stale = "em-dead-stale"
	const fresh = "em-dead-fresh"
	insertDeadLetterFixture(t, store, stale)
	insertDeadLetterFixture(t, store, fresh)
	for round := 0; round < rawBodyGoneStreakThreshold; round++ {
		if _, _, err := store.MarkRawBodyGone(ctx, stale, true); err != nil {
			t.Fatalf("mark stale: %v", err)
		}
	}
	for round := 0; round < rawBodyGoneStreakThreshold; round++ {
		if _, _, err := store.MarkRawBodyGone(ctx, fresh, true); err != nil {
			t.Fatalf("mark fresh: %v", err)
		}
	}
	// 只把 stale 那封的标记时间往前推，且必须**推过整个保留期**。
	// 按天数写字面量会与 rawBodyDeadRetryAfter 脱钩：保留期一旦调大，
	// 夹具就从「过期」变成「没过期」，而症状是复检数 0 行——红在一个
	// 与被测性质无关的地方。所以从常量本身算出该往前推多久。
	agedBy := rawBodyDeadRetryAfter + 24*time.Hour
	if _, err := store.pool.Exec(ctx,
		`UPDATE emails SET raw_body_dead_at = now() - $1::interval WHERE id = $2`,
		fmt.Sprintf("%d seconds", int(agedBy.Seconds())), stale,
	); err != nil {
		t.Fatalf("age the stale marker: %v", err)
	}
	// 夹具自检：确认这封的标记确实已经落在截止线之前，而不是靠运气。
	var age time.Duration
	if err := store.pool.QueryRow(ctx,
		`SELECT now() - raw_body_dead_at FROM emails WHERE id = $1`, stale).Scan(&age); err != nil {
		t.Fatalf("read marker age: %v", err)
	}
	if age <= rawBodyDeadRetryAfter {
		t.Fatalf("aged marker is only %v old, but the retention window is %v — the "+
			"fixture would not be stale at all", age, rawBodyDeadRetryAfter)
	}

	n, err := store.ReArmStaleRawBodyDead(ctx, time.Now().Add(-rawBodyDeadRetryAfter))
	if err != nil {
		t.Fatalf("re-arm: %v", err)
	}
	if n != 1 {
		t.Errorf("re-armed %d row(s), want exactly 1", n)
	}

	deadIDs, err := store.ListRawBodyDeadEmailIDs(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if deadIDs[stale] {
		t.Error("the stale marker survived the re-arm — the UIDVALIDITY safety valve never fires")
	}
	if !deadIDs[fresh] {
		t.Error("a fresh marker was re-armed; re-arm must only touch markers past the retention window")
	}
}

// 死信标记是 UIDVALIDITY 误判的唯一安全阀：真被误判的邮件，代价上界是
// 「保留期内少试两次」，而不是永久排除在发票建档之外。这条把上界钉死。
func TestRawBodyDeadLetter_RetentionWindowIsBounded(t *testing.T) {
	if rawBodyGoneStreakThreshold < 2 {
		t.Errorf("threshold = %d; a single observation is not evidence of permanence "+
			"(the same signal also fires when UIDVALIDITY changes)", rawBodyGoneStreakThreshold)
	}
	if rawBodyDeadRetryAfter <= 0 {
		t.Errorf("retention = %v; a non-positive window would mean dead letters never "+
			"get re-checked, so a misclassification is permanent", rawBodyDeadRetryAfter)
	}
	if rawBodyDeadRetryAfter > 90*24*time.Hour {
		t.Errorf("retention = %v, longer than the %d-day invoice-candidate lookback — "+
			"a misclassified message would outlive the window that would have re-imported it",
			rawBodyDeadRetryAfter, invoiceCandidateLookbackDays)
	}
}

// ── 第 1.5 步：死信不再被处理（真库、走完整流水线） ────────────────

// TestPipelineStep15_DeadLetterIsNotProcessed —— 核心行为判据。
//
// 夹具复用 pop3CandidatePipeline：同一个 fixture 在**没有**死信标记时一定
// 能建档（见 pipeline_pop3_candidate_test.go，且那是本仓库既有的护栏）。
// 所以「标记之后建不出台账行」这件事，只可能由死信跳过造成——判据是行为，
// 不是「某个函数被调用过」。
//
// 负控：删掉第 1.5 步里的 `if deadLetters[e.ID] { continue }` → 本条转红。
func TestPipelineStep15_DeadLetterIsNotProcessed(t *testing.T) {
	p, store, _, cleanup := pop3CandidatePipeline(t)
	defer cleanup()
	ctx := context.Background()

	// 先把邮件标记成死信（模拟「已连续多轮确认服务端没有这条消息」）。
	for round := 0; round < rawBodyGoneStreakThreshold; round++ {
		if _, dead, err := store.MarkRawBodyGone(ctx, pop3CandEmailID, true); err != nil {
			t.Fatalf("mark round %d: %v", round, err)
		} else if round == rawBodyGoneStreakThreshold-1 && !dead {
			t.Fatalf("after %d rounds the message is not marked dead; the fixture cannot "+
				"exercise the skip", rawBodyGoneStreakThreshold)
		}
	}

	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, pop3CandidateAccount(), rep)

	// 夹具自检：必须真的扫描到了这封邮件，否则「跳过」可能是因为压根没进循环。
	if rep.InvoiceCandidatesScanned == 0 {
		t.Fatalf("scanned = 0; the fixture did not reach the dead-letter check")
	}

	var rows int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_invoices WHERE email_id = $1`, pop3CandEmailID).Scan(&rows); err != nil {
		t.Fatalf("count invoice rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("email_invoices has %d row(s) for a message marked as a dead letter — "+
			"it was still fetched and archived, so the retirement saves nothing", rows)
	}
	if rep.InvoiceCandidatesCreated != 0 {
		t.Errorf("created = %d, want 0", rep.InvoiceCandidatesCreated)
	}
}

// 反向对照：没有标记时同一个 fixture 必须照常建档。
//
// 和上一条成对，缺了它「建不出台账行」就可能只是因为这个 fixture 本来就建不出。
func TestPipelineStep15_LiveMessageIsStillArchived(t *testing.T) {
	p, store, _, cleanup := pop3CandidatePipeline(t)
	defer cleanup()
	ctx := context.Background()

	// 一次 gone 观察，未达阈值 → 不是死信。
	if _, dead, err := store.MarkRawBodyGone(ctx, pop3CandEmailID, true); err != nil {
		t.Fatalf("mark: %v", err)
	} else if dead {
		t.Fatalf("one observation already marked it dead; the threshold is not being enforced")
	}

	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, pop3CandidateAccount(), rep)

	var rows int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_invoices WHERE email_id = $1`, pop3CandEmailID).Scan(&rows); err != nil {
		t.Fatalf("count invoice rows: %v", err)
	}
	if rows == 0 {
		t.Fatalf("a message that is NOT a dead letter was not archived — the retirement "+
			"is eating live messages (report errors: %v)", strings.Join(rep.Errors, " | "))
	}
}
