package email

// store_spam_mark_test.go — 判垃圾的落库环节（需求 2 → 需求 7 的接缝）。
//
// 为什么这个方法值得单独钉：它是「IMAP MOVE 成功」与「邮件窗口能看到
// 垃圾分类」之间**唯一**的接缝。
//
//   - pipeline.go 在 MoveEmailsToJunk 之后调 MarkEmailsSpamByUID，
//     写 category='spam'；
//   - 前端邮件窗口的「垃圾」chip（INBOX_CATEGORY_CHIPS 里的 __spam）
//     映射成 { category: 'spam' }，由后端 store.go 的 filter 落到 SQL。
//
// 两头任一端断了，症状都是同一个：窗口里「垃圾」点进去永远空。
// 2026-10-01 实测真实库确实如此——category='spam' 全库 0 行，
// spamHits=0（判垃圾从未在真实数据上跑通），而这个方法**一个测试都没有**：
// 它从未被调用过，也就从未暴露过是否正确。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip，与其它 email 集成测试一致）。
//
// 负控对照：把 SQL 里的 category='spam' 去掉后，
// TestMarkEmailsSpamByUID_WritesSpamCategory 转红。

import (
	"context"
	"testing"
	"time"
)

func seedWithUID(t *testing.T, store *Store, id, accountID string, uid int64) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO emails (id, account_id, workspace_id, message_id, uid, from_address,
		                    subject, snippet, date, is_read, category, created_at)
		VALUES ($1,$2,'ws-spam',$3,$4,'sender@example.com','subject '||$1,'snippet',$5,FALSE,'',$5)`,
		id, accountID, id+"@example.com", uid, time.Now().Unix()); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// spamMarkCategoryOf 读一封邮件的归类结果。
//
// 合并说明：本文件原有一个 `categoryOf`，与 main 侧
// spam_clean_real_branch_test.go 里的同名 helper 在合并后撞名（两侧各自
// 独立加的测试文件，被 git 当成 add/add 合进同一包）。两者语义略有差别：
// main 那个走公开 API（GetEmailByID），本文件原来那个直查库、顺带把
// is_read 也 Scan 出来却从不使用。保留 main 那个（走公开 API，顺带覆盖
// GetEmailByID 这条读路径），本文件改用自己的名字。
//
// 为什么不直接复用 main 那个：本文件的判据只关心 category 这一个字段，
// 多经一层 GetEmailByID 会让「库里的值」与「API 读出来的值」混在一起；
// 两者一旦不一致，本文件要能独立指出是库的问题还是读路径的问题。
func spamMarkCategoryOf(t *testing.T, store *Store, id string) string {
	t.Helper()
	var cat string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT COALESCE(category,'') FROM emails WHERE id=$1`, id).
		Scan(&cat); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return cat
}

func isReadOf(t *testing.T, store *Store, id string) bool {
	t.Helper()
	var isRead bool
	if err := store.pool.QueryRow(context.Background(),
		`SELECT COALESCE(is_read,FALSE) FROM emails WHERE id=$1`, id).Scan(&isRead); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return isRead
}

// 核心契约：判为垃圾的邮件必须落 category='spam'，否则邮件窗口的
// 「垃圾」筛选（前端 __spam -> {category:'spam'}）永远返回空。
func TestMarkEmailsSpamByUID_WritesSpamCategory(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-spam", "owner", "ws-spam")
	seedWithUID(t, store, "m1", "acct-spam", 101)
	seedWithUID(t, store, "m2", "acct-spam", 102)
	seedWithUID(t, store, "m-keep", "acct-spam", 999)

	if err := store.MarkEmailsSpamByUID(context.Background(), "acct-spam", []int64{101, 102}); err != nil {
		t.Fatalf("MarkEmailsSpamByUID: %v", err)
	}
	for _, id := range []string{"m1", "m2"} {
		if got := spamMarkCategoryOf(t, store, id); got != "spam" {
			t.Errorf("%s category=%q, want \"spam\"（前端「垃圾」chip 靠这个值）", id, got)
		}
		if !isReadOf(t, store, id) {
			t.Errorf("%s is_read=false, want true（垃圾邮件应标已读）", id)
		}
	}
	// 不得波及 UID 列表之外的邮件
	if got := spamMarkCategoryOf(t, store, "m-keep"); got == "spam" {
		t.Errorf("不在 UID 列表里的 m-keep 被误标为 spam")
	}
}

// 跨账户不得误伤：UID 在不同账户下可能相同，按 account_id 限定是唯一的
// 隔离手段。没有它就会把 A 账户的邮件标成垃圾。
func TestMarkEmailsSpamByUID_DoesNotCrossAccounts(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-a", "owner", "ws-spam")
	seedAccount(t, store, "acct-b", "owner2", "ws-spam")
	seedWithUID(t, store, "ma", "acct-a", 101)
	seedWithUID(t, store, "mb", "acct-b", 101)

	if err := store.MarkEmailsSpamByUID(context.Background(), "acct-a", []int64{101}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := spamMarkCategoryOf(t, store, "ma"); got != "spam" {
		t.Errorf("acct-a 的 ma category=%q, want spam", got)
	}
	if got := spamMarkCategoryOf(t, store, "mb"); got == "spam" {
		t.Errorf("acct-b 的 mb 被误标为 spam（UID 相同但账户不同）")
	}
}

// 空输入必须无操作且不报错：pipeline 会按账户分组，空组是常态。
func TestMarkEmailsSpamByUID_EmptyInput(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	if err := store.MarkEmailsSpamByUID(context.Background(), "", nil); err != nil {
		t.Fatalf("空 accountID 应直接返回 nil, got: %v", err)
	}
	if err := store.MarkEmailsSpamByUID(context.Background(), "acct-none", nil); err != nil {
		t.Fatalf("空 uid 列表应直接返回 nil, got: %v", err)
	}
}
