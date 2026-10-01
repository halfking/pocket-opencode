package email

// store_email_count_test.go — CountEmailsScoped 的作用域隔离与「不受 limit 影响」。
//
// 为什么需要它：客户端缓存自愈（frontend email-cache-heal）用
// 「服务端总行数 vs 本地行数」判定邮件丢失。此前客户端只能拿
// listEmails 的返回长度当行数，而列表接口限流（上限 500），邮箱里超过
// 500 封时该长度恒等于 limit —— 缺口信号被彻底抹平，自愈永不触发。
// CountEmailsScoped 补的就是这个真值。
//
// 需要活的 PostgreSQL；见 store_workspace_test.go 的
// POCKET_TEST_POSTGRES_DSN 约定与 per-test schema harness。

import (
	"context"
	"testing"
	"time"
)

func seedCountedEmails(t *testing.T, store *Store, accountID, prefix string, n int, date time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		seedEmail(t, store, prefix+string(rune('a'+i)), accountID, "", "subject")
	}
	// seedEmail 用 time.Now()，这里统一覆盖成同一时刻便于 since 断言。
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE emails SET date=$1 WHERE id LIKE $2`, date.UnixMilli(), prefix+"%"); err != nil {
		t.Fatalf("set date: %v", err)
	}
}

// Count 必须只统计当前 (user, workspace) 的邮件。
//
// 这条如果失效，客户端会把别人的邮件数算进「我缺了多少」，
// 于是在自己邮箱完好时也判定有缺口，无休止地回补。
func TestCountEmailsScopedIsolatesTenants(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-mine", "alice", "ws-a")
	seedAccount(t, store, "acct-theirs", "bob", "ws-b")
	seedCountedEmails(t, store, "acct-mine", "mine-", 5, time.Now())
	seedCountedEmails(t, store, "acct-theirs", "theirs-", 300, time.Now())

	n, err := store.CountEmailsScoped(ctx, ListFilter{}, "alice", "ws-a")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 5 {
		t.Errorf("alice 的 count = %d，期望 5（不能把 bob 的 300 封算进来）", n)
	}
}

// Count 与 List 统计的必须是同一个集合。
//
// 客户端拿 count 判缺口、拿 list 拉数据，两者口径不一致时会把
// 「服务端根本没有的邮件」误判成缺口。category / unread / since 三个过滤
// 条件逐一对齐。
func TestCountEmailsScopedMatchesListEmailsScoped(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-c", "alice", "ws-a")

	base := time.Now().Add(-10 * 24 * time.Hour)
	for i := 0; i < 6; i++ {
		em := Email{
			ID: "c-" + string(rune('a'+i)), AccountID: "acct-c", WorkspaceID: "ws-a",
			MessageID: "m" + string(rune('a'+i)), FromAddress: "s@example.com",
			Subject: "s", Snippet: "n",
			Date:     base.Add(time.Duration(i) * 24 * time.Hour).UnixMilli(),
			Category: "work",
			IsRead:   i%2 == 0,
		}
		if err := store.InsertEmail(ctx, em); err != nil {
			t.Fatalf("insert %s: %v", em.ID, err)
		}
	}

	cases := []struct {
		name   string
		filter ListFilter
	}{
		{"全部", ListFilter{}},
		{"分类", ListFilter{Category: "work"}},
		{"仅未读", ListFilter{UnreadOnly: true}},
		{"since 窗口", ListFilter{Since: base.Add(3 * 24 * time.Hour).UnixMilli()}},
		{"since 秒单位", ListFilter{Since: base.Add(3 * 24 * time.Hour).Unix()}},
	}
	for _, c := range cases {
		// 1) limit=1：证明 list 尊重 limit（所以不能拿它去和 count 比大小）。
		f := c.filter
		f.Limit = 1
		limited, err := store.ListEmailsScoped(ctx, f, "alice", "ws-a")
		if err != nil {
			t.Fatalf("%s list: %v", c.name, err)
		}
		if len(limited) != 1 {
			t.Errorf("%s：limit=1 的 list=%d，应为 1", c.name, len(limited))
		}

		// 2) 不带 limit（走默认 200）：与 count 比对，证明两者描述同一集合。
		//    这一步才是"count 与 limit 无关"的真正判据。
		n, err := store.CountEmailsScoped(ctx, c.filter, "alice", "ws-a")
		if err != nil {
			t.Fatalf("%s count: %v", c.name, err)
		}
		all, err := store.ListEmailsScoped(ctx, c.filter, "alice", "ws-a")
		if err != nil {
			t.Fatalf("%s list(all): %v", c.name, err)
		}
		if int(n) != len(all) {
			t.Errorf("%s：count=%d 但不限 limit 的 list=%d，两者必须是同一集合的大小", c.name, n, len(all))
		}
		if n < 2 {
			t.Errorf("%s：count=%d，用例没能区分 limit 效果，换个数据", c.name, n)
		}
	}
}

// 软删除的行不计入 count。
//
// 客户端据此算「我缺了几封」；把墓碑算进去会让刚删掉的邮件变成永久缺口，
// 每次同步都触发一轮无意义的回补。
func TestCountEmailsScopedExcludesSoftDeleted(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-sd", "alice", "ws-a")
	seedCountedEmails(t, store, "acct-sd", "sd-", 4, time.Now())

	before, err := store.CountEmailsScoped(ctx, ListFilter{}, "alice", "ws-a")
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	if _, _, err := store.SoftDeleteEmailsScoped(ctx, []string{"sd-a"}, "alice", "ws-a", time.Now().UnixMilli()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	after, err := store.CountEmailsScoped(ctx, ListFilter{}, "alice", "ws-a")
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before-1 {
		t.Errorf("软删一封后 count = %d，期望 %d（墓碑不能计入）", after, before-1)
	}
}
