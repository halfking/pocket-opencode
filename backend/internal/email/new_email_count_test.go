package email

// new_email_count_test.go — Sync 的「新邮件」计数必须只算真正入库的那几封。
//
// 需求链路：定时/手工收信后要告诉用户「整理完成：新邮件 N」。这个 N 来自
// Sync 的返回值。2026-10-02 的真实数据里它是错的：
//
//	08:00:01 [email/pipeline] done synced=5 new=2 ...
//
// 而库里自前一晚 23:56:52 起**一行新邮件都没有**（全量 120 行的 created_at
// 落在 14 秒窗口内，是一次批量导入）。用户被告知收到了 2 封新邮件，实际一封
// 新的都没进来。
//
// 机制：InsertEmail 用 `ON CONFLICT (id) DO UPDATE` 刷新 snippet，对「这行
// 已经存在」是**成功**返回（nil）而不是错误，所以调用方没法拿返回值判断新旧；
// fetcher 的两条路径便都在其后无条件 `saved++`。已存在的邮件被重新拉一遍，就
// 被报成「新邮件」。
//
// 判据用 PostgreSQL 的 xmax 惯用法（INSERT 时 xmax=0，DO UPDATE 命中已有行
// 时非 0），见 Store.InsertEmailIfNew。

import (
	"context"
	"testing"
	"time"
)

// 底层判据：第一次插入 inserted=true，同一 id 再来一次 inserted=false，
// 而 snippet 仍被刷新（不能因为「不算新邮件」就把自愈能力弄丢）。
func TestInsertEmailIfNew_DistinguishesInsertFromResync(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-count", "u1", "ws-a")

	base := Email{
		ID: "count-1", AccountID: "acct-count", WorkspaceID: "ws-a",
		MessageID: "m-1", UID: 7, FromAddress: "boss@corp.example",
		FromName: "Boss", Subject: "Quarterly review",
		Snippet: "first snippet", Date: time.Now().Unix(),
	}

	ins, err := store.InsertEmailIfNew(ctx, base)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !ins {
		t.Fatal("第一次写入必须是 inserted=true")
	}

	// 同一封再来一次（生产里就是「这封之前同步过、这轮又拉了一遍」）。
	resync := base
	resync.Snippet = "refreshed snippet"
	ins, err = store.InsertEmailIfNew(ctx, resync)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if ins {
		t.Fatal("重复同步同一封邮件不得算作新邮件 —— 这正是 08:00 报 new=2 的成因")
	}

	// 关键：不能算「新邮件」不等于「不写」。snippet 刷新必须照旧生效，
	// 否则会退回 2026-10-01 那个「snippet 只写一次、永不自愈」的坑。
	got, err := store.GetEmailByIDScoped(ctx, "count-1", "u1", "ws-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("重复写入后行不见了 —— InsertEmailIfNew 改变了 ON CONFLICT 行为")
	}
	if got.Snippet != "refreshed snippet" {
		t.Fatalf("snippet 刷新丢了：%q（InsertEmailIfNew 改变了 DO UPDATE 行为）", got.Snippet)
	}

	// 不同的 id 仍然是新邮件。
	other := base
	other.ID = "count-2"
	other.MessageID = "m-2"
	ins, err = store.InsertEmailIfNew(ctx, other)
	if err != nil {
		t.Fatalf("other insert: %v", err)
	}
	if !ins {
		t.Fatal("另一封邮件必须是 inserted=true")
	}
}

// 接线层：Sync 报出的数字必须跟着这个判据走。
//
// 这里刻意把 last_synced_uid 拨回 0 来制造「同一批被重新拉一遍」——这是生产里
// 真正发生的事（08:00 报 new=2 而库里零新行）。如果只是连着 Sync 两次，
// 第二轮因为 UID 范围为空而什么都没拉，saved=0 是「没拉到」造成的，
// **测不到本缺陷**，那样的用例是空跑。
func TestSyncCountsOnlyGenuinelyNewEmails(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	now := time.Now().UTC().Truncate(time.Second)
	ti.appendMessage(t, "boss@corp.example", "Quarterly review", "Please send the numbers.", now.Add(-2*time.Hour))
	ti.appendMessage(t, "news@shop.example", "50% off everything", "Sale ends tonight.", now.Add(-time.Hour))

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")

	saved, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if saved != 2 {
		t.Fatalf("首轮应报 2 封新邮件，got %d", saved)
	}

	// 制造生产同款条件：同步进度落后 → 下一轮重新拉同一批。
	if _, err := store.pool.Exec(ctx,
		`UPDATE email_accounts SET last_synced_uid = 0 WHERE id = $1`, acctID); err != nil {
		t.Fatalf("rewind last_synced_uid: %v", err)
	}

	again, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if again != 0 {
		t.Fatalf("重拉已存在的 2 封邮件却报 %d 封「新邮件」——用户在界面上会被骗", again)
	}

	// 行数不该变：重同步只刷新，不新增。
	list, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("重同步后邮件行数应为 2（不得重复入库），got %d", len(list))
	}
}
