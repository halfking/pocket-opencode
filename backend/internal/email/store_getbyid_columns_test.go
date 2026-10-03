package email

// store_getbyid_columns_test.go —— GetEmailByID / GetEmailByIDScoped 必须
// 把 message_id 与 body_purged 读回结构体。
//
// 需要活的 PostgreSQL；见 store_workspace_test.go 的 POCKET_TEST_POSTGRES_DSN
// 约定与每测试独立 schema 的夹具（没设 DSN 时自动 skip）。
//
// 缺陷（2026-10-02）：这两个方法的 SELECT 列表漏了这两列，也没 Scan，
// 于是 Email.MessageID / Email.BodyPurged 在生产里恒为零值。
//
// 这类缺陷不产生任何错误信号——SQL 成功、Scan 成功、既有测试全绿、日志无痕，
// 只有「DB → 结构体」这一跳是空��。后果：
//
//  1. invoice_harvest.harvestOne → sameEmailMessage(em, raw) 里的
//     「真实 Message-ID 强确认/强否定」分支在生产中从不执行
//     （emHasReal 恒 false），只剩 subject+from+同日 的弱判据——而真实数据里
//     两张同名发票的头部完全一样。
//  2. server_email_summary.summarizeBody 的 `if em.BodyPurged { return "" }`
//     守卫恒不触发，用户软删并清空正文的邮件会被 IMAP 重新回源、喂给 LLM，
//     再把摘要写回已删除的行。
//
// 纯函数测试（sameEmailMessage）覆盖不到这个缺口：它们直接构造 Email 字面量，
// 天然带着 MessageID。必须打真实 PG store 才测得到这一跳。

import (
	"context"
	"testing"
	"time"
)

func TestGetEmailByID_ReadsMessageIDAndBodyPurged(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		userID   = "u-fixcols"
		wsID     = "ws-fixcols"
		acctID   = "acct-fixcols"
		msgID    = "real.12345@example.com"
		liveID   = "em-fixcols-live"
		purgedID = "em-fixcols-purged"
	)
	seedAccount(t, store, acctID, userID, wsID)

	for _, e := range []Email{
		{ID: liveID, AccountID: acctID, WorkspaceID: wsID, MessageID: msgID,
			FromAddress: "a@example.com", Subject: "正常邮件", Snippet: "s", Date: 1000},
		{ID: purgedID, AccountID: acctID, WorkspaceID: wsID, MessageID: msgID + ".purged",
			FromAddress: "b@example.com", Subject: "已清空正文", Snippet: "原文", Date: 2000},
	} {
		if err := store.InsertEmail(ctx, e); err != nil {
			t.Fatalf("insert %s: %v", e.ID, err)
		}
	}

	// 用户软删 + 清空正文（生产上的真实路径）
	if _, _, err := store.SoftDeleteEmailsScoped(ctx, []string{purgedID}, userID, wsID, time.Now().Unix()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	t.Run("GetEmailByID reads message_id", func(t *testing.T) {
		got, err := store.GetEmailByID(ctx, liveID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got == nil {
			t.Fatal("expected a row")
		}
		if got.MessageID != msgID {
			t.Fatalf("MessageID lost: want %q got %q", msgID, got.MessageID)
		}
		if got.BodyPurged {
			t.Fatal("BodyPurged should be false for a normal email")
		}
	})

	t.Run("GetEmailByIDScoped reads message_id", func(t *testing.T) {
		got, err := store.GetEmailByIDScoped(ctx, liveID, userID, wsID)
		if err != nil {
			t.Fatalf("get scoped: %v", err)
		}
		if got == nil {
			t.Fatal("expected a row")
		}
		if got.MessageID != msgID {
			t.Fatalf("MessageID lost (scoped): want %q got %q", msgID, got.MessageID)
		}
	})

	t.Run("BodyPurged is true after soft delete", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			get  func() (*Email, error)
		}{
			{"GetEmailByID", func() (*Email, error) { return store.GetEmailByID(ctx, purgedID) }},
			{"GetEmailByIDScoped", func() (*Email, error) { return store.GetEmailByIDScoped(ctx, purgedID, userID, wsID) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := tc.get()
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if got == nil {
					t.Fatal("expected a row")
				}
				if !got.BodyPurged {
					t.Fatal("BodyPurged must be true after SoftDeleteEmailsScoped; summarizeBody 的守卫依赖它")
				}
			})
		}
	})
}

// TestGetEmailByID_MessageIDFeedsSameEmailMessage 把「读回」和「消费」接起来：
// 生产里 harvestOne 拿 GetEmailByID 的返回值去调 sameEmailMessage。
// 若 message_id 没读回，强确认分支不执行，这个用例就会失败。
func TestGetEmailByID_MessageIDFeedsSameEmailMessage(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		userID  = "u-msgid"
		wsID    = "ws-msgid"
		acctID  = "acct-msgid"
		emailID = "em-msgid-1"
		msgID   = "real.777@example.com"
	)
	seedAccount(t, store, acctID, userID, wsID)
	if err := store.InsertEmail(ctx, Email{
		ID: emailID, AccountID: acctID, WorkspaceID: wsID, MessageID: msgID,
		FromAddress: "billing@example.com", Subject: "主题被改写了", Snippet: "s", Date: 3000,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetEmailByID(ctx, emailID)
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	// 主题已被服务商改写，只有真实 Message-ID 相等能确认是同一封。
	raw := []byte("From: billing@example.com\r\n" +
		"Subject: 完全不同的主题\r\n" +
		"Message-ID: <" + msgID + ">\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	if !sameEmailMessage(got, raw) {
		t.Fatalf("sameEmailMessage must confirm identity via the real Message-ID read back from DB; "+
			"em.MessageID=%q（若为空说明 message_id 仍没被读回）", got.MessageID)
	}
}
