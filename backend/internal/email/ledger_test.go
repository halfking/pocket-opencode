package email

// ledger_test.go — 共享台账的行生成与发布流程。
//
// 需求原文：「这些文件如果没有办法发送，可以建立共享文档及文件，进行整理，
// 需要整理一个列表，记录必要信息并汇总金额。」
// 这里钉两件事：
//  1. 表格里必须有「必要信息」列 + **独立的合计行**（不是只在文字里提一句）；
//  2. 发布器不可用时 PublishLedgerScoped 返回空 URL 且**不报错**——
//     本地 CSV/MD 仍是兜底，不能因为飞书没配就让整轮流水线记 error。

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeLedger struct {
	available bool
	err       error
	calls     int
	gotTitle  string
	gotInvs   int
	url       string
}

func (f *fakeLedger) Available() bool { return f.available }

func (f *fakeLedger) PublishLedger(ctx context.Context, title string, invs []Invoice) (string, error) {
	f.calls++
	f.gotTitle = title
	f.gotInvs = len(invs)
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

func sampleInvoices() []Invoice {
	return []Invoice{
		{Category: "交通", Seller: "某某出行", Amount: 1280, Currency: "CNY",
			InvoiceNo: "25332000000123456789", InvoiceDate: "2026-09-28",
			Status: "downloaded", FileName: "交通-某某出行-1280.00-2026-09-28.pdf", Subject: "行程单"},
		{Category: "通信", Seller: "云服务商", Amount: 256.5,
			InvoiceNo: "25332000000999999999", InvoiceDate: "2026-09-29",
			Status: "downloaded", FileName: "通信-云服务商-256.50-2026-09-29.pdf", Subject: "云账单"},
	}
}

func TestLedgerRows_HasHeaderDetailAndTotalRow(t *testing.T) {
	rows, total := LedgerRows(sampleInvoices())
	if len(rows) != 4 { // 表头 + 2 明细 + 合计
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	header := rows[0]
	wantHeader := []string{"费用类型", "对方单位", "金额", "币种", "发票号", "开票日期", "状态", "文件名", "来源邮件"}
	if len(header) != len(wantHeader) {
		t.Fatalf("header cols = %d, want %d", len(header), len(wantHeader))
	}
	for i, w := range wantHeader {
		if header[i] != w {
			t.Fatalf("header[%d] = %v, want %q", i, header[i], w)
		}
	}
	if total != 1536.5 {
		t.Fatalf("total = %v, want 1536.5", total)
	}
	last := rows[len(rows)-1]
	if last[0] != "合计" {
		t.Fatalf("last row must be the total row, got %v", last[0])
	}
	if last[2] != total {
		t.Fatalf("total cell = %v, want %v", last[2], total)
	}
	// 币种缺省要补 CNY，否则汇总表里会出现空币种
	if rows[1][3] != "CNY" {
		t.Fatalf("default currency not applied: %v", rows[1][3])
	}
}

func TestLedgerCellRange_CoversAllRows(t *testing.T) {
	rows, _ := LedgerRows(sampleInvoices())
	got := LedgerCellRange("0Sheet1", rows)
	if got != "0Sheet1!A1:I4" {
		t.Fatalf("range = %q", got)
	}
	if LedgerCellRange("S", [][]any{{"a"}}) != "S!A1:I1" {
		t.Fatal("single row range wrong")
	}
}

func TestColumnName(t *testing.T) {
	cases := map[int]string{1: "A", 9: "I", 26: "Z", 27: "AA", 28: "AB", 52: "AZ", 53: "BA"}
	for in, want := range cases {
		if got := columnName(in); got != want {
			t.Fatalf("columnName(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLedgerTitleAndTotalText(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	title := LedgerTitle("ws_user-admin", now)
	if title == "" || !containsAll(title, "2026-09-30", "ws_user-admin") {
		t.Fatalf("title = %q", title)
	}
	if got := LedgerTotalText(1280, 3); got != "合计 1280.00（3 张）" {
		t.Fatalf("total text = %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// 发布器不可用 → 空 URL、无错误（本地汇总继续）。
func TestPublishLedgerScoped_SkipsWhenUnavailable(t *testing.T) {
	p := &Pipeline{Ledger: &fakeLedger{available: false}}
	url, err := p.PublishLedgerScoped(context.Background(), "u", "ws")
	if err != nil {
		t.Fatalf("unavailable publisher must not error: %v", err)
	}
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
}

// seedLedgerInvoices 造一个可用的发票集合：email_invoices.email_id 有外键指向
// emails，所以必须先插邮件再插发票。
func seedLedgerInvoices(t *testing.T, store *Store, userID, wsID, acctID string, n int) {
	t.Helper()
	ctx := context.Background()
	seedAccount(t, store, acctID, userID, wsID)
	for i, inv := range sampleInvoices() {
		if i >= n {
			break
		}
		emailID := "em-ledger-" + string(rune('a'+i))
		if err := store.InsertEmail(ctx, Email{
			ID: emailID, AccountID: acctID, WorkspaceID: wsID,
			MessageID: emailID + "@example.com",
			FromAddress: "billing@vendor.test", Subject: inv.Subject, Snippet: "x",
			Date: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC).Unix(),
		}); err != nil {
			t.Fatalf("seed email %s: %v", emailID, err)
		}
		inv.EmailID = emailID
		inv.AccountID = acctID
		inv.Status = "downloaded"
		if _, err := store.UpsertInvoice(ctx, &inv, userID, wsID); err != nil {
			t.Fatalf("seed invoice %s: %v", emailID, err)
		}
	}
}

// 发布成功：发布器拿到正确的标题与明细，URL 原样回传（真库）。
func TestPublishLedgerScoped_PublishesToFeishuLedger(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedLedgerInvoices(t, store, "user-admin", "ws-ledger", "acct-ledger", 2)

	fl := &fakeLedger{available: true, url: "https://x.feishu.cn/sheets/shtcnTEST"}
	p := &Pipeline{Store: store, Ledger: fl}

	url, err := p.PublishLedgerScoped(ctx, "user-admin", "ws-ledger")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if url != fl.url {
		t.Fatalf("url = %q, want %q", url, fl.url)
	}
	if fl.calls != 1 || fl.gotInvs != 2 {
		t.Fatalf("publisher called %d times with %d invoices", fl.calls, fl.gotInvs)
	}
	if !containsAll(fl.gotTitle, "ws-ledger") {
		t.Fatalf("title must carry the workspace: %q", fl.gotTitle)
	}
}

// 发布器报错要冒泡（流水线会记进 errors，日志里能看见），且不能被吞掉。
func TestPublishLedgerScoped_SurfacesError(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedLedgerInvoices(t, store, "user-admin", "ws-ledger", "acct-ledger", 1)
	p := &Pipeline{Store: store, Ledger: &fakeLedger{available: true, err: errors.New("feishu down")}}
	if _, err := p.PublishLedgerScoped(context.Background(), "user-admin", "ws-ledger"); err == nil {
		t.Fatal("publisher error must surface to the pipeline report")
	}
}

// 一个发票都没有时不建表（避免每天在飞书里堆一张空表）。
func TestPublishLedgerScoped_SkipsEmptyLedger(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	fl := &fakeLedger{available: true, url: "https://x.feishu.cn/sheets/x"}
	p := &Pipeline{Store: store, Ledger: fl}
	url, err := p.PublishLedgerScoped(context.Background(), "user-admin", "ws-empty")
	if err != nil {
		t.Fatalf("empty ledger must not error: %v", err)
	}
	if url != "" || fl.calls != 0 {
		t.Fatalf("empty ledger must not create a sheet: url=%q calls=%d", url, fl.calls)
	}
}

