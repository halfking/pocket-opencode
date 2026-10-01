package email

// store_messageid_test.go — 守住 `Email.MessageID` 的**读**路径。
//
// ## 背景（2026-10-02 修的真 bug）
//
// `emails.message_id` 一直在**写**（InsertEmail / Sync 都填了），但**没有任何
// 读路径把它读出来**：`GetEmailByID` 与 `GetEmailByIDScoped` 的 SELECT 列表里
// 都没有这一列，Scan 也没有对应目标。于是 `Email.MessageID` 在生产里恒为空串。
//
// 后果不是「少一个字段」，而是**静默废掉一道安全闸**：
// `harvestOne` -> `recoverPOP3SourcedRaw` -> `sameEmailMessage` 用
// `em.MessageID` 做「真实 Message-ID 强确认 / 强否定」。em.MessageID 恒空
// ⇒ `emHasReal` 恒 false ⇒ 那条分支在生产里**从不执行**，只剩
// subject+from+同日 的弱判据。
//
// 弱判据在真实数据上区分不了同名邮件：`invoice_selfheal_test.go` 的
// `TestSameEmailMessage_DifferentInvoiceRejected` 已经把这条边界写明了
// —— 两张 QQ Wallet 发票主题/发件人/日期全同，只有正文发票号不同。
// 也就是说这道闸门恰恰在最需要它的场景下是失效的。
//
// 负控：修复前下面第一条断言为红（实测 em.MessageID=""），修复后转绿；
// 同时 `invoice_harvest_selfheal_test.go` 里两条成功路径由红转绿。

import (
	"context"
	"testing"
)

// TestGetEmailByID_ReturnsMessageID 单封读取必须带回 message_id。
func TestGetEmailByID_ReturnsMessageID(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-msgid", "u", "ws-msgid")

	const want = "REAL-MSG-ID@example.com"
	if err := store.InsertEmail(ctx, Email{
		ID: "em-msgid", AccountID: "acct-msgid", WorkspaceID: "ws-msgid",
		MessageID:   want,
		FromAddress: "billing@vendor.example",
		Subject:     "s", Snippet: "x", Date: 1700000000,
	}); err != nil {
		t.Fatalf("InsertEmail: %v", err)
	}

	// 先确认写进去了——否则下面的断言会因为「根本没写进去」而误判成读的问题。
	var stored string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(message_id,'') FROM emails WHERE id='em-msgid'`).Scan(&stored); err != nil {
		t.Fatalf("read column: %v", err)
	}
	if stored != want {
		t.Fatalf("message_id 未落库（=%q, want %q）；下面验的就不是读路径了", stored, want)
	}

	em, err := store.GetEmailByID(ctx, "em-msgid")
	if err != nil || em == nil {
		t.Fatalf("GetEmailByID: %v (em=%v)", err, em)
	}
	if em.MessageID != want {
		t.Errorf("GetEmailByID 的 MessageID=%q, want %q；读路径漏了 message_id，"+
			"会让发票自愈的强身份判据在生产里失效", em.MessageID, want)
	}
}

// TestGetEmailByIDScoped_ReturnsMessageID 带 scope 的那条读路径同样不能漏。
func TestGetEmailByIDScoped_ReturnsMessageID(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-msgid2", "u", "ws-msgid2")

	const want = "SCOPED-MSG-ID@example.com"
	if err := store.InsertEmail(ctx, Email{
		ID: "em-msgid2", AccountID: "acct-msgid2", WorkspaceID: "ws-msgid2",
		MessageID:   want,
		FromAddress: "billing@vendor.example",
		Subject:     "s", Snippet: "x", Date: 1700000000,
	}); err != nil {
		t.Fatalf("InsertEmail: %v", err)
	}

	em, err := store.GetEmailByIDScoped(ctx, "em-msgid2", "u", "ws-msgid2")
	if err != nil || em == nil {
		t.Fatalf("GetEmailByIDScoped: %v (em=%v)", err, em)
	}
	if em.MessageID != want {
		t.Errorf("GetEmailByIDScoped 的 MessageID=%q, want %q", em.MessageID, want)
	}
}

// TestGetEmailByID_MessageIDIsOptional 旧行 message_id 为空/NULL 时不能报错。
//
// emails.message_id 允许为空（客户端推送的历史邮件），所以这条读路径必须
// 容忍空值——修 bug 不能顺手把 NULL 变成崩溃。
func TestGetEmailByID_MessageIDIsOptional(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-msgid3", "u", "ws-msgid3")

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ('em-msgid3','acct-msgid3','ws-msgid3',NULL,'a@b.example','s','x',1700000000,1700000000)
		ON CONFLICT (id) DO UPDATE SET message_id = NULL`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	em, err := store.GetEmailByID(ctx, "em-msgid3")
	if err != nil || em == nil {
		t.Fatalf("GetEmailByID on NULL message_id: %v (em=%v)", err, em)
	}
	if em.MessageID != "" {
		t.Errorf("MessageID=%q, want 空串", em.MessageID)
	}
}
