package email

// fetcher_backlog_test.go —— 首次同步时**超过 50 封的老邮件被永久跳过**。
//
// ## 缺陷机制
//
// IMAP 同步（fetcher.go 的 syncIMAP 主流程）里有三段组合在一起：
//
//  1. `criteria.UID` **只在 `LastSyncedUID > 0` 时**才设。所以首次同步
//     （LastSyncedUID==0）搜索**没有 UID 过滤**，返回 INBOX 里**全部** N 封。
//  2. `if len(uids) > 50 { uids = uids[len(uids)-50:] }` —— 只保留**最新 50 封**。
//  3. 循环结束 `UpdateSyncState(accountID, int64(highestUID), ...)`，而
//     `highestUID` 是**已插入的最大 UID**（从 `acc.LastSyncedUID` 起、只增不减）。
//
// 三段叠起来的后果：首次同步 N>50 封时，只落库最新 50 封，而
// `last_synced_uid` 被推到那 50 封里最大的 UID。下一轮搜索条件变成
// `UID last_synced_uid+1 .. UIDNEXT`，**更老的那 N-50 封再也搜不到**。
//
// 这与 fetcher.go:716-721 那段注释里明确写下的原则直接冲突——
// 「无新邮件时不推进 LastSyncedUID……若写成 uidNext，下轮从 uidNext+1 起搜会
// **永久跳过**恰好分到 uidNext 的那封新邮件（真实踩中）」。那次修的是
// uidNext 那个方向的洞，**留下了 50 封截断这个方向的同一个洞**。
//
// 对需求的直接影响：落在被跳过那批里的**发票邮件永远不会被采集**，
// 需求 2「收取发票邮件」与需求 3「发到飞书」对它完全失效，而且**没有任何报错**
// ——报告上「这轮 0 封新邮件」和「这轮没东西可收」长得一模一样。
//
// 实测背景（2026-10-03 08:25:22）：真实库 120 封邮件。§7de 记的 QQ 信箱是 444 封。
//
// ## 判据为什么必须连打两轮
//
// 只断言「首轮只落 50 封」并不能证明数据丢了——那也可能是设计如此的分批。
// 关键是**第二轮**：如果 watermark 正确推进，第二轮应该把剩下的补齐。
// 修复前第二轮返回 0 封且那批老邮件**永远**补不上，这才是不变式被破坏的证据。

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// appendN 往 INBOX 塞 n 封纯文本邮件，UID 递增。
func appendN(t *testing.T, ti *testIMAP, n int, base time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		ti.appendMessage(t,
			fmt.Sprintf("sender%d@corp.example", i),
			fmt.Sprintf("Mail %03d", i),
			"body text",
			base.Add(time.Duration(i)*time.Minute))
	}
}

func TestSyncDrainsBacklogInsteadOfSkippingOldest(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	const total = 60 // > 50，落在截断阈值之上
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	base := time.Now().UTC().Truncate(time.Second)
	appendN(t, ti, total, base)

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-backlog", password, "")

	first, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if first == 0 {
		t.Fatal("first sync saved 0")
	}
	if first > 50 {
		t.Fatalf("first sync saved %d, want <= 50（分批上限）", first)
	}

	// 第二轮必须把剩下的补齐。修复前这里返回 0，且剩下的邮件**永远**补不上。
	second, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second == 0 {
		list, _ := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-backlog")
		t.Fatalf("second sync saved 0 —— 首批之外的 %d 封被**永久跳过**了 "+
			"（last_synced_uid 被推到最新 UID，更老的再也搜不到）", total-len(list))
	}

	// 反复同步直到排空，断言总数能到 60。
	got := first + second
	for round := 3; got < total && round <= 10; round++ {
		n, err := fetcher.Sync(ctx, acctID)
		if err != nil {
			t.Fatalf("sync round %d: %v", round, err)
		}
		if n == 0 {
			break
		}
		got += n
	}

	list, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-backlog")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != total {
		missing := map[string]bool{}
		for i := 0; i < total; i++ {
			missing[fmt.Sprintf("Mail %03d", i)] = true
		}
		for _, e := range list {
			delete(missing, e.Subject)
		}
		names := make([]string, 0, len(missing))
		for k := range missing {
			names = append(names, k)
		}
		t.Fatalf("只落库 %d/%d 封；永久丢失：%v", len(list), total, names)
	}
}

// TestSyncBacklogKeepsNewestFirstOrdering 钉住「每批最多 50 封」这个上限本身
// 还在（不能因为修 bug 就把上限删了，让一轮同步把整个信箱拉完）。
func TestSyncBacklogKeepsNewestFirstOrdering(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	base := time.Now().UTC().Truncate(time.Second)
	appendN(t, ti, 55, base)

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-cap", password, "")
	saved, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if saved != 50 {
		t.Errorf("单轮落库 %d 封, want 50（每轮 50 封的上限应保留）", saved)
	}
}
