package email

import (
	"context"
	"testing"
	"time"
)

// 回归测试：ExtractInvoice 的产物必须能通过 UpsertInvoice 落库。
// 曾因 Status 留空（零值 ""）违反 email_invoices 的 status check 约束，
// 每次规则提取落库都失败且被调用方 continue 静默吞掉——发票自动提取从未
// 真正入库。解析类单测覆盖不到持久化约束，必须有这条落库链路的集成测试。
func TestUpsertInvoice_AcceptsExtractedInvoice(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const userID = "user-inv"
	const wsID = "ws-inv"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_accounts (id, user_id, display_name, email_address, imap_host, credential_encrypted, created_at, workspace_id)
		VALUES ('acct-inv', $1, 't', 'billing@example.com', 'imap.example.com', 'x', $2, $3)
	`, userID, time.Now().Unix(), wsID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, message_id, uid, from_address, subject, snippet, date, is_read, created_at, workspace_id)
		VALUES ('em-inv-1', 'acct-inv', '<inv-1@test>', 1, 'billing@example.com',
		        '电子发票：云服务账单', '价税合计（小写）：¥328.00，发票号码：25612000000123456789', $1, FALSE, $1, $2)
	`, time.Now().Unix(), wsID); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	e, err := store.GetEmailByID(ctx, "em-inv-1")
	if err != nil || e == nil {
		t.Fatalf("load email: %v", err)
	}
	inv, hit := ExtractInvoice(*e, "")
	if !hit {
		t.Fatalf("expected invoice keyword hit")
	}
	saved, err := store.UpsertInvoice(ctx, inv, userID, wsID)
	if err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}
	if saved.Status != "new" {
		t.Fatalf("status = %q, want new", saved.Status)
	}

	got, err := store.GetInvoiceByEmailID(ctx, "em-inv-1")
	if err != nil || got == nil {
		t.Fatalf("read back invoice: %v", err)
	}
	if got.Amount == 0 {
		t.Errorf("amount not parsed: %+v", got)
	}
}
