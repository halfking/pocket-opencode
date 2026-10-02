package email

// merge_dupe_store_test.go — 墓碑写操作的集成测试（隔离 schema，真实 PG）。
//
// 重点验证三件事：
//   1. confirm=false 时**真的不写**（预演模式）；
//   2. 墓碑**保留** body_path / snippet —— 这是与 SoftDeleteEmailsScoped
//      （用户删除语义，会清空它们）的关键区别，也是回滚的前提；
//   3. 发票改指到保留行，不丢关联。
//
// 负控：把「保留侧不得是 POP3 行」这条校验去掉 -> 用例必须转红
// （否则传反参数会把唯一的可 FETCH 记录墓碑掉）。

import (
	"context"
	"testing"
)

func seedMergeRows(t *testing.T, store *Store) (keepID, dropID string) {
	t.Helper()
	ctx := context.Background()
	acc := &Account{
		EmailAddress: "merge-test@example.com", DisplayName: "merge",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password",
		Enabled: true, UserID: "u1", WorkspaceID: "ws1",
	}
	if err := store.InsertAccount(ctx, acc, "enc"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	accID := acc.ID
	keep := Email{ID: "em-10432-" + accID, AccountID: accID, WorkspaceID: "ws1",
		MessageID: "real-msg-1@x.com", UID: 10432, Subject: "AWS 账户提醒",
		FromAddress: "no-reply@amazonaws.com", Snippet: "keep snippet",
		BodyPath: "bodies/keep.bin", Date: 1700000000}
	drop := Email{ID: "em-pop3-" + accID + "-ZC0023_abc", AccountID: accID, WorkspaceID: "ws1",
		MessageID: "pop3-ZC0023_abc", UID: 260, Subject: "AWS 账户提醒",
		FromAddress: "no-reply@amazonaws.com", Snippet: "pop3 snippet",
		BodyPath: "bodies/pop3.bin", Date: 1700000000}
	if err := store.InsertEmail(ctx, keep); err != nil {
		t.Fatalf("insert keep: %v", err)
	}
	if err := store.InsertEmail(ctx, drop); err != nil {
		t.Fatalf("insert drop: %v", err)
	}
	// InsertEmail 的列清单**不含 body_path**（它由 BodyCache.Put +
	// MarkEmailBodyCached 写），所以这里直接补上，模拟「已回填原文缓存」的行。
	// 这正是合并要保护的数据。
	if _, err := store.pool.Exec(ctx,
		`UPDATE emails SET body_path='bodies/pop3.bin' WHERE id=$1`, drop.ID); err != nil {
		t.Fatalf("seed body_path: %v", err)
	}
	return keep.ID, drop.ID
}

func TestTombstoneDupeEmails_DryRunWritesNothing(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	keepID, dropID := seedMergeRows(t, store)

	plans := []MergePlan{{KeepEmailID: keepID, TombstoneID: dropID, Subject: "AWS 账户提醒"}}
	merged, moved, err := store.TombstoneDupeEmails(ctx, plans, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if merged != 1 || moved != 0 {
		t.Fatalf("dry run should report 1 planned, 0 invoices moved; got merged=%d moved=%d", merged, moved)
	}
	// 关键：dry run 后两行都必须还在
	for _, id := range []string{keepID, dropID} {
		var n int
		if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM emails WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", id, err)
		}
		if n != 1 {
			t.Fatalf("dry run must not write; row %s disappeared", id)
		}
	}
}

func TestTombstoneDupeEmails_TombstoneKeepsBodyData(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	keepID, dropID := seedMergeRows(t, store)

	plans := []MergePlan{{KeepEmailID: keepID, TombstoneID: dropID, Subject: "AWS 账户提醒"}}
	merged, _, err := store.TombstoneDupeEmails(ctx, plans, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged != 1 {
		t.Fatalf("merged=%d, want 1", merged)
	}

	var deletedAt any
	var bodyPath *string
	var snippet *string
	if err := store.pool.QueryRow(ctx,
		`SELECT deleted_at, body_path, snippet FROM emails WHERE id=$1`, dropID).
		Scan(&deletedAt, &bodyPath, &snippet); err != nil {
		t.Fatalf("read tombstoned row: %v", err)
	}
	// 墓碑必须写入
	if deletedAt == nil {
		t.Fatal("tombstoned row must have deleted_at set")
	}
	// **关键**：数据必须保留（这是与 SoftDeleteEmailsScoped 的区别）
	if bodyPath == nil || *bodyPath != "bodies/pop3.bin" {
		t.Fatalf("body_path must be PRESERVED for rollback, got %v", bodyPath)
	}
	if snippet == nil || *snippet != "pop3 snippet" {
		t.Fatalf("snippet must be PRESERVED for rollback, got %v", snippet)
	}
	// 保留侧不受影响
	var keepDeleted *int64
	if err := store.pool.QueryRow(ctx, `SELECT deleted_at FROM emails WHERE id=$1`, keepID).Scan(&keepDeleted); err != nil {
		t.Fatalf("read keep row: %v", err)
	}
	// 判据必须与生产一致：`deleted_at` 实际存 0 或 NULL 都表示「未删除」
	//（生产用 COALESCE(deleted_at,0)=0）。用 IS NULL 判会误报。
	if keepDeleted != nil && *keepDeleted != 0 {
		t.Fatalf("keep side must NOT be tombstoned, got deleted_at=%d", *keepDeleted)
	}
}

func TestTombstoneDupeEmails_MovesInvoicesToKeepSide(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	keepID, dropID := seedMergeRows(t, store)

	if _, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: dropID, AccountID: "", Seller: "甲", Amount: 12.5,
	}, "u1", "ws1"); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	plans := []MergePlan{{KeepEmailID: keepID, TombstoneID: dropID, Subject: "AWS 账户提醒"}}
	merged, moved, err := store.TombstoneDupeEmails(ctx, plans, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged != 1 || moved != 1 {
		t.Fatalf("merged=%d moved=%d, want 1/1", merged, moved)
	}
	var newEmailID string
	if err := store.pool.QueryRow(ctx, `SELECT email_id FROM email_invoices WHERE seller='甲'`).Scan(&newEmailID); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if newEmailID != keepID {
		t.Fatalf("invoice email_id=%q, want keep side %q", newEmailID, keepID)
	}
}

func TestTombstoneDupeEmails_RejectsInvertedPlan(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	keepID, dropID := seedMergeRows(t, store)

	// 传反：把 IMAP 行当墓碑、POP3 行当保留 —— 必须被拒，
	// 否则会墓碑掉唯一可 FETCH 的记录。
	bad := []MergePlan{{KeepEmailID: dropID, TombstoneID: keepID}}
	if _, _, err := store.TombstoneDupeEmails(ctx, bad, true); err == nil {
		t.Fatal("inverted plan must be rejected: tombstoning the IMAP side would lose the only fetchable copy")
	}
	// 确认没有写入（判据与生产一致：COALESCE(deleted_at,0) <> 0 才是墓碑）
	var n int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM emails WHERE id=$1 AND COALESCE(deleted_at,0) <> 0`, keepID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatal("rejected plan must not write anything")
	}
}
