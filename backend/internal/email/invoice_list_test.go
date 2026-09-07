package email

import (
	"context"
	"testing"
	"time"
)

func TestListInvoicesPage_OrdersByEmailDateAndPages(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const userID = "user-page"
	const wsID = "ws-page"
	now := time.Now().Unix()
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_accounts (id, user_id, display_name, email_address, imap_host, credential_encrypted, created_at, workspace_id)
		VALUES ('acct-page', $1, 't', 'a@b.com', 'imap.example.com', 'x', $2, $3)
	`, userID, now, wsID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, message_id, uid, from_address, subject, snippet, date, is_read, created_at, workspace_id)
		VALUES
			('em-old', 'acct-page', '<old@test>', 1, 'a@b.com', '电子发票旧', '价税合计：¥10.00 发票号码：25612000000000000001', $1, FALSE, $4, $5),
			('em-new', 'acct-page', '<new@test>', 2, 'a@b.com', '电子发票新', '价税合计：¥20.00 发票号码：25612000000000000002', $2, FALSE, $4, $5),
			('em-mid', 'acct-page', '<mid@test>', 3, 'a@b.com', '电子发票中', '价税合计：¥15.00 发票号码：25612000000000000003', $3, FALSE, $4, $5)
	`, now-3000, now-100, now-2000, now, wsID); err != nil {
		t.Fatalf("seed emails: %v", err)
	}

	for _, id := range []string{"em-old", "em-new", "em-mid"} {
		e, err := store.GetEmailByID(ctx, id)
		if err != nil || e == nil {
			t.Fatalf("load %s: %v", id, err)
		}
		inv, hit := ExtractInvoice(*e, "")
		if !hit {
			t.Fatalf("extract %s", id)
		}
		if _, err := store.UpsertInvoice(ctx, inv, userID, wsID); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	first, err := store.ListInvoicesPage(ctx, userID, wsID, "", 2, 0)
	if err != nil {
		t.Fatalf("page0: %v", err)
	}
	if !first.HasMore || first.Total != 3 || len(first.Invoices) != 2 {
		t.Fatalf("page0 hasMore=%v total=%d n=%d", first.HasMore, first.Total, len(first.Invoices))
	}
	if first.Invoices[0].EmailID != "em-new" || first.Invoices[1].EmailID != "em-mid" {
		t.Fatalf("page0 order=%s,%s", first.Invoices[0].EmailID, first.Invoices[1].EmailID)
	}

	second, err := store.ListInvoicesPage(ctx, userID, wsID, "", 2, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if second.HasMore || len(second.Invoices) != 1 || second.Invoices[0].EmailID != "em-old" {
		id := ""
		if len(second.Invoices) > 0 {
			id = second.Invoices[0].EmailID
		}
		t.Fatalf("page1 hasMore=%v n=%d id=%s", second.HasMore, len(second.Invoices), id)
	}
	if second.Invoices[0].EmailDate != now-3000 {
		t.Fatalf("emailDate=%d want %d", second.Invoices[0].EmailDate, now-3000)
	}
}
