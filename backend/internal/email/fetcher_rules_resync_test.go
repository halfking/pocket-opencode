package email

// fetcher_rules_resync_test.go — 「先收信、后配规则」这条真实路径。
//
// 背景：2026-10-02 实测真库 5 个账户 rules 全为 NULL、importance 恒空，于是
// 重要邮件提醒一封也发不出去（reminderUnclassifiedHint 那条诊断就是为这个
// 写的）。解锁最便宜的办法是给账户配 rules（不需要 LLM 配额、不需要新依赖），
// 于是「先同步过邮件、之后才配规则」就是用户一定会走的那条路。
//
// 这条路今天是**断的**：InsertEmailIfNew 的 ON CONFLICT (id) DO UPDATE 只刷新
// snippet 一列，规则算出来的 importance / action_reason 在重跑同步时被直接丢掉。
// 症状极具迷惑性：新邮件会被打上 high（走 INSERT 分支），旧邮件永远补不上，
// 于是「配了规则，新邮件有提醒、老邮件没有」——很容易被当成规则写错了。
//
// 用户唯一能让旧邮件重新过一遍规则的办法是重置 last_synced_uid 重同步，
// 而那条路恰好也走 ON CONFLICT，同样被丢。
import (
	"context"
	"testing"
	"time"
)

func syncedImportance(t *testing.T, store *Store, fromAddr string) string {
	t.Helper()
	list, err := store.ListEmailsScoped(context.Background(), ListFilter{}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range list {
		if list[i].FromAddress == fromAddr {
			return list[i].Importance
		}
	}
	t.Fatalf("库里没有来自 %s 的邮件（共 %d 封）", fromAddr, len(list))
	return ""
}

// 已经同步进库的邮件，在账户补上 rules 之后重跑同步，必须拿到 importance=high。
func TestSyncAppliesNewlyConfiguredRulesToAlreadySyncedMail(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "pw"
	const from = "boss@corp.example"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	ti.appendMessage(t, from, "Urgent: sign off", "Need this today.", time.Now().UTC())

	// 第一轮：账户还没配规则。邮件入库，importance 为空。
	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("第一次 sync: %v", err)
	}
	if got := syncedImportance(t, store, from); got != "" {
		t.Fatalf("没配规则时 importance=%q，want 空", got)
	}

	// 用户在设置里给账户配上规则（走真实写路径，UpdateAccount 会写 rules 列）。
	acc, _, err := store.GetAccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	acc.Rules = `{"rules":[{"type":"sender-whitelist","pattern":"` + from + `",` +
		`"actions":[{"name":"mark-important"}]}]}`
	if err := store.UpdateAccount(ctx, acc); err != nil {
		t.Fatalf("configure rules: %v", err)
	}

	// 重置同步进度，让同一封邮件重新过一遍规则。
	if err := store.UpdateSyncState(ctx, acctID, 0, 0); err != nil {
		t.Fatalf("reset sync state: %v", err)
	}
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("第二次 sync: %v", err)
	}

	if got := syncedImportance(t, store, from); got != "high" {
		t.Fatalf("补配 rules 后重跑同步，importance=%q，want high —— 已入库的邮件被 ON CONFLICT 丢掉了规则结果", got)
	}
}

// 反向风险：ON CONFLICT 现在会刷 importance，那么**没有规则命中**的那一次重跑
// 绝不能把已有值抹成空（否则一次普通的重同步就能把 AI 分类出的 importance
// 清零，提醒再次静默失效）。判据就是 CASE 的「保留旧值」那一支。
func TestResyncWithoutRulesKeepsExistingImportance(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "pw"
	const from = "boss@corp.example"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	ti.appendMessage(t, from, "Urgent: sign off", "Need this today.", time.Now().UTC())

	rulesJSON := `{"rules":[{"type":"sender-whitelist","pattern":"` + from + `",` +
		`"actions":[{"name":"mark-important"}]}]}`
	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, rulesJSON)
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("第一次 sync: %v", err)
	}
	if got := syncedImportance(t, store, from); got != "high" {
		t.Fatalf("前置条件不成立：规则命中后 importance=%q，want high", got)
	}

	// 规则被清空（例如用户改配置改到一半、或从 AI 分类切走）。
	acc, _, err := store.GetAccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	acc.Rules = ""
	if err := store.UpdateAccount(ctx, acc); err != nil {
		t.Fatalf("clear rules: %v", err)
	}
	if err := store.UpdateSyncState(ctx, acctID, 0, 0); err != nil {
		t.Fatalf("reset sync state: %v", err)
	}
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("第二次 sync: %v", err)
	}

	if got := syncedImportance(t, store, from); got != "high" {
		t.Fatalf("规则没命中却把已有的 importance 抹成了 %q —— 一次重同步就清零了提醒依据", got)
	}
}
