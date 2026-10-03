package email

// sync_failure_observability_test.go —— 「同步失败」必须是可查询的事实。
//
// ## 要解决的问题
//
// last_synced_at **只在成功时写**。失败出口（无可用凭据 / 解密失败 /
// dial 失败 / login 失败 / SELECT 失败 / POP3 失败）全都直接 return error，
// 一个字都不写。于是这两种状态在库里长得一模一样：
//
//	A. 这个账户压根没被调度到
//	B. 这个账户每 60 秒被轮询一次、每次都失败
//
// 2026-10-02 真实代价：huangxutao@kxpms.cn 的水位停在 01:37:22 整整
// 19 小时，期间一直在被轮询、一直在失败，而**库里没有任何一处记录过**。
// 唯一的线索是进程 stdout，而那个进程的 stdout 没有落任何文件。
//
// ## 判据
//
// 不是「失败要落库」这种能被空实现满足的话，而是：
// **A 与 B 在读回来的字段上必须不同**，且这个差别能写成一条 SQL/Go 判据。
// 最后一条用例直接断言两种账户读回来后的差异——如果 RecordSyncFailure
// 偷偷也推进了 last_synced_at（谎报水位），那条用例立刻转红。

import (
	"context"
	"testing"
	"time"
)

type syncRow struct {
	attemptAt int64
	syncedAt  int64
	err       string
	failures  int
	syncedUID int64
}

func readSyncRow(t *testing.T, store *Store, id string) syncRow {
	t.Helper()
	var r syncRow
	err := store.pool.QueryRow(context.Background(), `
		SELECT COALESCE(last_attempt_at,0), COALESCE(last_synced_at,0),
		       COALESCE(last_sync_error,''), COALESCE(sync_failures,0), COALESCE(last_synced_uid,0)
		FROM email_accounts WHERE id=$1`, id).
		Scan(&r.attemptAt, &r.syncedAt, &r.err, &r.failures, &r.syncedUID)
	if err != nil {
		t.Fatalf("read sync row %s: %v", id, err)
	}
	return r
}

// 失败必须落库，且**不许**碰水位。
func TestRecordSyncFailure_PersistsWithoutTouchingWatermark(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-fail", "u", "ws-fail")

	// 先制造一次成功，确认「上一次成功的水位」是什么
	if err := store.UpdateSyncState(ctx, "acct-fail", 4242, 1_700_000_000); err != nil {
		t.Fatalf("seed success: %v", err)
	}
	before := readSyncRow(t, store, "acct-fail")

	if err := store.RecordSyncFailure(ctx, "acct-fail", "imap login failed: NO Login"); err != nil {
		t.Fatalf("RecordSyncFailure: %v", err)
	}
	after := readSyncRow(t, store, "acct-fail")

	if after.attemptAt <= before.attemptAt {
		t.Errorf("last_attempt_at = %d，应推进（before=%d）", after.attemptAt, before.attemptAt)
	}
	if after.syncedAt != before.syncedAt {
		t.Errorf("last_synced_at 被失败改动了：%d -> %d。失败没有推进任何进度，"+
			"改它就是谎报水位——而谎报出来的水位正是本文件要解决的问题本身",
			before.syncedAt, after.syncedAt)
	}
	if after.syncedUID != before.syncedUID {
		t.Errorf("last_synced_uid 被失败改动了：%d -> %d", before.syncedUID, after.syncedUID)
	}
	if after.err == "" {
		t.Error("last_sync_error 为空：失败原因没有留下来")
	}
	if after.failures != 1 {
		t.Errorf("sync_failures = %d，want 1", after.failures)
	}

	// 再失败一次，计数必须累加
	if err := store.RecordSyncFailure(ctx, "acct-fail", "second failure"); err != nil {
		t.Fatalf("2nd RecordSyncFailure: %v", err)
	}
	if r := readSyncRow(t, store, "acct-fail"); r.failures != 2 {
		t.Errorf("sync_failures = %d，want 2", r.failures)
	}
}

// 成功必须把失败痕迹清干净，且 last_attempt_at 与 last_synced_at 对齐。
//
// 不对齐的后果：「刚跑过一轮、显示成功」和「压根没跑过」在 attempt 列上
// 会长得一样。
func TestUpdateSyncState_ClearsFailureTrail(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-clear", "u", "ws-clear")

	if err := store.RecordSyncFailure(ctx, "acct-clear", "boom"); err != nil {
		t.Fatalf("RecordSyncFailure: %v", err)
	}
	if err := store.RecordSyncFailure(ctx, "acct-clear", "boom again"); err != nil {
		t.Fatalf("2nd RecordSyncFailure: %v", err)
	}

	now := time.Now().Unix()
	if err := store.UpdateSyncState(ctx, "acct-clear", 99, now); err != nil {
		t.Fatalf("UpdateSyncState: %v", err)
	}
	r := readSyncRow(t, store, "acct-clear")
	if r.err != "" {
		t.Errorf("成功后 last_sync_error 仍为 %q，没清干净", r.err)
	}
	if r.failures != 0 {
		t.Errorf("成功后 sync_failures = %d，want 0", r.failures)
	}
	if r.syncedUID != 99 {
		t.Errorf("last_synced_uid = %d，want 99", r.syncedUID)
	}
	if r.attemptAt != now || r.syncedAt != now {
		t.Errorf("成功后 last_attempt_at(%d) 应与 last_synced_at(%d) 都等于 %d",
			r.attemptAt, r.syncedAt, now)
	}
}

// 核心用例：两种「看起来一样」的状态，读回来必须**可区分**。
//
// 判据写法刻意贴近实际排障时的问题——「这个账户到底是没跑，还是在跑但失败？」
// 如果实现只是把错误写进一个没人读的列，这条用例不会红；但如果实现
// 顺带改了水位（最常见的「顺手一起更新」），它立刻红。
func TestSyncFailure_NotPolledAndPolledButFailingAreDistinguishable(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// A：从来没被轮到过（enabled 但一条记录也没有）
	seedAccount(t, store, "acct-idle", "u", "ws-dist")
	// B：一直被轮询、一直失败
	seedAccount(t, store, "acct-broken", "u", "ws-dist")
	if err := store.RecordSyncFailure(ctx, "acct-broken", "connection refused"); err != nil {
		t.Fatalf("RecordSyncFailure: %v", err)
	}

	idle := readSyncRow(t, store, "acct-idle")
	broken := readSyncRow(t, store, "acct-broken")

	// 两者 last_synced_at 相同（都是 0）——这正是「看不出来」的地方
	if idle.syncedAt != broken.syncedAt {
		t.Fatalf("前提变了：idle.syncedAt=%d broken.syncedAt=%d，"+
			"本用例要验的「两个状态在 last_synced_at 上无法区分」已不成立",
			idle.syncedAt, broken.syncedAt)
	}
	// 但它们必须能被区分开
	if broken.attemptAt == idle.attemptAt {
		t.Errorf("两者的 last_attempt_at 相同（%d）："+
			"「没被轮询」与「在轮询但一直失败」在库里无法区分——"+
			"这正是 kxpms 连挂 19 小时而无人发现的原因",
			broken.attemptAt)
	}
	if broken.failures == 0 {
		t.Error("broken 的 sync_failures = 0，排障时看不出它在反复失败")
	}
	if broken.err == "" {
		t.Error("broken 的 last_sync_error 为空，排障时看不到失败原因")
	}
	// 排障时真正会写的那条判据：失败中 ⇒ attempt 在推进而 synced 没有
	if !(broken.attemptAt > broken.syncedAt && broken.failures > 0) {
		t.Errorf("「失败中」判据不成立：attempt=%d synced=%d failures=%d，"+
			"want attempt > synced 且 failures > 0",
			broken.attemptAt, broken.syncedAt, broken.failures)
	}
}

// 状态接口必须把新字段带出去——落库了但没人读得到，等于没落。
func TestGetSyncStatusScoped_ExposesFailureTrail(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-exp", "u", "ws-exp")
	if err := store.RecordSyncFailure(ctx, "acct-exp", "smtp timeout"); err != nil {
		t.Fatalf("RecordSyncFailure: %v", err)
	}

	sts, err := store.GetSyncStatusScoped(ctx, "u", "ws-exp")
	if err != nil {
		t.Fatalf("GetSyncStatusScoped: %v", err)
	}
	var got *AccountSyncStatus
	for i := range sts {
		if sts[i].AccountID == "acct-exp" {
			got = &sts[i]
		}
	}
	if got == nil {
		t.Fatalf("状态里没有 acct-exp：%+v", sts)
	}
	if got.LastAttemptAt == 0 {
		t.Error("LastAttemptAt = 0，状态接口没带出可观测性字段")
	}
	if got.LastSyncError == "" {
		t.Error("LastSyncError 为空，状态接口没带出失败原因")
	}
	if got.SyncFailures != 1 {
		t.Errorf("SyncFailures = %d，want 1", got.SyncFailures)
	}
}
