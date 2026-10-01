package email

// invoice_retry_test.go — 需求 3「有可能我们需要多次操作才能下载到发票文件」。
//
// 这条需求的实现是三段：
//   1. invoice_store.go:201 的 ListHarvestableInvoices 用
//      status IN ('new','pending') 捞起待采集记录 —— 下一轮仍能捡回来；
//   2. invoice_harvest.go:325 每尝试一次 Attempts++；
//   3. invoice_harvest.go:377 的 markRetry 按 Attempts 与 MaxInvoiceAttempts
//      的关系决定回到 pending（等下一轮）还是转 failed（终态，人工处理）。
//
// 三段任一断开，「多次操作」就退化成「只试一次」或「无限重试」：
//   - 捞取条件里漏掉 'pending' -> 第二轮就再也捡不回来，等于只试一次；
//   - markRetry 不看 Attempts -> 要么永远 pending 无限重试，要么第一次就 failed。
//
// 此前这三段**都没有测试**。这里补上，重点是状态机的边界。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip，与其它 email 集成测试一致）。
//
// 负控对照：
//   - 负控A：ListHarvestableInvoices 的条件去掉 'pending' -> 抓取不到已 pending 的发票；
//   - 负控B：markRetry 里的 Attempts 判断反转 -> 边界状态全错。

import (
	"context"
	"testing"
)

// seedInvoice 插入一条指定状态的发票记录。
//
// email_id 有 UNIQUE + 外键指向 emails(id)，account_id NOT NULL，
// 所以必须先建好对应的 account 与 email，不能凭空插。
// account 由 ensureInvoiceAccount 建一次（多次调用安全），本函数只补 email/invoice。
func ensureInvoiceAccount(t *testing.T, store *Store) {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_accounts WHERE id='acct-inv'`).Scan(&n); err != nil {
		t.Fatalf("count account: %v", err)
	}
	if n == 0 {
		seedAccount(t, store, "acct-inv", "u", "ws-inv")
	}
}

func seedInvoice(t *testing.T, store *Store, id, status string, attempts int) {
	t.Helper()
	ctx := context.Background()
	ensureInvoiceAccount(t, store)
	emailID := "em-" + id
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ($1,'acct-inv','ws-inv',$1,'s@example.com','subject','snippet',1700000000,1700000000)`,
		emailID); err != nil {
		t.Fatalf("seed email for %s: %v", id, err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO email_invoices (id, email_id, account_id, user_id, workspace_id,
		                            invoice_no, invoice_date, subject, status, attempts,
		                            created_at, updated_at)
		VALUES ($1,$2,'acct-inv','u','ws-inv','','','',$3,$4::int,1700000000,1700000000)`,
		id, emailID, status, attempts); err != nil {
		t.Fatalf("seed invoice %s: %v", id, err)
	}
}

// 捞取条件必须同时包含 new 与 pending。
//
// 只捞 new 的话：第一次下载失败 → markRetry 置 pending → **下一轮再也不会
// 被捡回来**，需求「多次操作才能下载到」直接失效。
func TestListHarvestableInvoices_PicksUpPendingForRetry(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoice(t, store, "inv-new", "new", 0)
	seedInvoice(t, store, "inv-pending", "pending", 3)
	seedInvoice(t, store, "inv-downloaded", "downloaded", 1)
	seedInvoice(t, store, "inv-failed", "failed", 8)

	got, err := store.ListHarvestableInvoices(ctx, 100)
	if err != nil {
		t.Fatalf("ListHarvestableInvoices: %v", err)
	}
	seen := map[string]bool{}
	for _, inv := range got {
		seen[inv.ID] = true
	}
	for _, want := range []string{"inv-new", "inv-pending"} {
		if !seen[want] {
			t.Errorf("%s 未被捞起（status IN ('new','pending') 少了状态）", want)
		}
	}
	for _, dontWant := range []string{"inv-downloaded", "inv-failed"} {
		if seen[dontWant] {
			t.Errorf("%s 被捞起了（已终态的记录不该重新采集）", dontWant)
		}
	}
}

// markRetry 的状态机：未达上限回 pending 等下一轮，达到上限转 failed。
func TestMarkRetry_StatusMachine(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name      string
		attempts  int
		wantState string
		why       string
	}{
		{"第一次失败", 1, "pending", "还有余量，应等下一轮再试"},
		{"接近上限仍重试", MaxInvoiceAttempts - 1, "pending", "还差一次，不该提前判死"},
		{"刚好到上限", MaxInvoiceAttempts, "failed", "已达上限，应转终态"},
		{"远超上限", MaxInvoiceAttempts + 5, "failed", "早就该判死了"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seedInvoice(t, store, "inv-"+c.name, "pending", c.attempts)
			inv := &Invoice{ID: "inv-" + c.name, Status: "pending", Attempts: c.attempts}

			got := (&InvoiceHarvester{Store: store}).markRetry(ctx, inv, "模拟下载失败")
			if got != c.wantState {
				t.Errorf("markRetry(attempts=%d) = %q, want %q —— %s",
					c.attempts, got, c.wantState, c.why)
			}
			// 必须真的落库，否则下一轮读到的还是旧状态。
			var dbStatus string
			var dbErr string
			if err := store.pool.QueryRow(ctx,
				`SELECT status, COALESCE(last_error,'') FROM email_invoices WHERE id=$1`, inv.ID).
				Scan(&dbStatus, &dbErr); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if dbStatus != c.wantState {
				t.Errorf("库中 status=%q, want %q（markRetry 没有真正落库）", dbStatus, c.wantState)
			}
			if dbErr != "模拟下载失败" {
				t.Errorf("last_error=%q, want %q（失败原因要留给人看）", dbErr, "模拟下载失败")
			}
		})
	}
}

// 上限耗尽后必须可被收尾逻辑捞出来转 failed（CleanupStalePendingInvoices）。
// 上一条测的是 harvest 内部判定，这条测的是跨轮次的收尾，两者是不同的路径。
func TestCleanupStalePendingInvoices_FailsExhausted(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoice(t, store, "inv-exhausted", "pending", MaxInvoiceAttempts)
	seedInvoice(t, store, "inv-alive", "pending", 2)

	n, err := store.CleanupStalePendingInvoices(ctx, MaxInvoiceAttempts, 1700000000)
	if err != nil {
		t.Fatalf("CleanupStalePendingInvoices: %v", err)
	}
	if n != 1 {
		t.Errorf("n=%d, want 1（只有 attempts 已达上限的才该被转 failed）", n)
	}
	var status string
	if err := store.pool.QueryRow(ctx,
		`SELECT status FROM email_invoices WHERE id='inv-exhausted'`).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "failed" {
		t.Errorf("inv-exhausted status=%q, want failed", status)
	}
	if err := store.pool.QueryRow(ctx,
		`SELECT status FROM email_invoices WHERE id='inv-alive'`).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "pending" {
		t.Errorf("inv-alive status=%q, want pending（未达上限不应被动）", status)
	}
}
