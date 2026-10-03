package server

// server_email_summary_total_test.go — GET /api/emails/invoices/summary 的
// 「合计金额」口径（2026-10-01 修正的第四个缺陷）。
//
// 需求原文：「需要整理一个列表，记录必要信息并汇总金额」。这个数字是用户
// 拿去对账的，所以口径必须明确，而且**三处必须一致**：
//
//	email.LedgerRows            → 飞书共享表格的合计行
//	email.WriteInvoiceSummaryDocs → 本地 CSV/MD 的合计
//	handleEmailInvoiceSummary    → 界面上的 amountTotal
//
// 原来三处都是无条件 `total += inv.Amount`，把全部记录（含 failed）都算进去。
// 库里确实存在 status=failed 却残留脏字段的记录（两张 QQ Wallet：
// seller="name:"、invoiceNo="Issuance"，字段是从邮件错误段落抽出来的，
// 见 handoff §7o），金额当时恰好是 0 才没出事。将来某张 failed 发票若带着
// 错误抽取的非零金额，就会静默把对账总额算高，而且没有任何地方会提示。
//
// 这里把「合计」与紧邻的 `downloaded` 计数钉在一起断言：两个数字必须指向
// **同一批**发票。否则界面上会出现「已下载 1 张 / 合计 5779.99」这种
// 自相矛盾、且用户无从判断该信哪个的显示。
//
// 判据：status 属于 downloaded/filed **且** FilePath 非空。
// 不计入合计 ≠ 从列表消失 —— rows 里每一张都还在，状态列写明。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPGEmailStore 建一个隔离 schema 的 email.Store（真库）。
// 隔离 schema 是必须的：本测试要断言的是**精确的合计数字**，共用 schema
// 会把别的测试/真实数据也算进来，断言就变成噪声。
func newTestPGEmailStore(t *testing.T) (*email.Store, string, func()) {
	t.Helper()
	// 只认测试专用 DSN。回退读 POCKET_POSTGRES_DSN 会让本地 `go test ./...`
	// 零配置地打到生产库——实测已在生产库留下 meeting_test_* 残留 schema。
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping invoice summary total test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "srv_invsum_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	scopedCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	scopedCfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	scopedPool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	cleanup := func() {
		scopedPool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
	store, err := email.NewStore(scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("email.NewStore: %v", err)
	}
	return store, dsn, cleanup
}

// seedSummaryInvoices 造四种状态的发票，覆盖合计口径的每条分支。
func seedSummaryInvoices(t *testing.T, store *email.Store, userID, wsID string) {
	t.Helper()
	ctx := context.Background()
	acctID := "acct-invsum"
	if err := store.InsertAccount(ctx, &email.Account{
		ID: acctID, UserID: userID, WorkspaceID: wsID,
		DisplayName: "invsum acct", EmailAddress: "invsum@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true,
		CreatedAt: time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	cases := []struct {
		id     string
		status string
		amount float64
		file   string
	}{
		{"a", "downloaded", 3500, "email-invoices/" + wsID + "/a.pdf"},
		{"b", "filed", 1280, "email-invoices/" + wsID + "/b.pdf"},
		// failed 却带着错误抽取出的非零金额 —— 这张正是原缺陷的暴露点。
		{"c", "failed", 999.99, ""},
		{"d", "pending", 128, ""},
		// 状态说下好了、文件却不在（被清理脚本删掉 / 落盘失败却已改状态）。
		{"e", "downloaded", 777, ""},
	}
	for _, c := range cases {
		emailID := "em-invsum-" + c.id
		if err := store.InsertEmail(ctx, email.Email{
			ID: emailID, AccountID: acctID, WorkspaceID: wsID,
			MessageID:   emailID + "@example.com",
			FromAddress: "billing@vendor.test", Subject: "发票 " + c.id, Snippet: "x",
			Date: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).Unix(),
		}); err != nil {
			t.Fatalf("seed email %s: %v", emailID, err)
		}
		inv := email.Invoice{
			ID: "inv-invsum-" + c.id, EmailID: emailID, AccountID: acctID,
			WorkspaceID: wsID,
			Seller:      "seller-" + c.id, Category: "其他", Currency: "CNY",
			Amount: c.amount, Status: c.status,
			FileName: c.id + ".pdf", Subject: "发票 " + c.id,
		}
		if _, err := store.UpsertInvoice(ctx, &inv, userID, wsID); err != nil {
			t.Fatalf("seed invoice %s: %v", c.id, err)
		}
		// file_path 必须走 UpdateInvoiceHarvest：**UpsertInvoice 的列清单里
		// 没有文件字段**（invoice_store.go 的 Exec 参数只到 UpdatedAt），
		// 把 FilePath 塞进 UpsertInvoice 会被静默丢弃：五张票的 FilePath 全空，
		// 合计自然是 0，而报错信息指向合计口径、真因在夹具，排查会被带偏。
		// 这条路径也正是生产顺序：saveInvoiceFile 先落盘，再更新 harvest 结果。
		inv.FilePath = c.file
		if err := store.UpdateInvoiceHarvest(ctx, &inv); err != nil {
			t.Fatalf("seed invoice harvest %s: %v", c.id, err)
		}
	}
}

type invoiceSummaryResp struct {
	Count       int     `json:"count"`
	AmountTotal float64 `json:"amountTotal"`
	Downloaded  int     `json:"downloaded"`
	Pending     int     `json:"pending"`
	Failed      int     `json:"failed"`
	Rows        []struct {
		ID     string  `json:"id"`
		Status string  `json:"status"`
		Amount float64 `json:"amount"`
	} `json:"rows"`
}

// newInvoiceSummaryServer 造一个只带邮件栈的 Server，并返回 handler 实际会
// 解析出的身份。
//
// 身份**不硬编码**：直接从 userIDFromRequest / workspaceIDFromRequest 取。
// handler 在没有登录态时回落成 ("local", "default")，把这两个值写死在夹具里
// 的话，将来回落策略一变，测试会安静地查空列表然后以「amountTotal=0」的形式
// 失败 —— 报错信息指向合计口径，实际问题在身份，排查会被带偏。
func newInvoiceSummaryServer(t *testing.T, store *email.Store) (*Server, string, string) {
	t.Helper()
	srv := &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      t.TempDir(),
	}
	probe := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/summary", nil)
	return srv, srv.userIDFromRequest(probe), srv.workspaceIDFromRequest(probe)
}

// callInvoiceSummary 走真实 handler（httptest），不直接调内部函数 ——
// 中间任何一步（身份解析、JSON 编码）坏了都能被测出来。
func callInvoiceSummary(t *testing.T, srv *Server) invoiceSummaryResp {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/summary", nil)
	srv.handleEmailInvoiceSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out invoiceSummaryResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return out
}

// TestHandleEmailInvoiceSummary_TotalCountsOnlyDownloadedWithFile 钉住
// 合计口径，并要求它与 `downloaded` 计数完全一致。
func TestHandleEmailInvoiceSummary_TotalCountsOnlyDownloadedWithFile(t *testing.T) {
	store, _, cleanup := newTestPGEmailStore(t)
	defer cleanup()
	srv, uid, wsID := newInvoiceSummaryServer(t, store)
	seedSummaryInvoices(t, store, uid, wsID)

	got := callInvoiceSummary(t, srv)

	// 只有 a(3500) + b(1280) 有文件且状态已下载。c 的 999.99 是错误抽取的
	// 金额（failed），e 的 777 状态说下好了但文件不在，两者都不得进合计。
	const want = 3500 + 1280
	if got.AmountTotal != want {
		t.Fatalf("amountTotal = %v, want %v —— 合计必须只统计「状态已下载且文件确实落盘」的发票；"+
			"failed 的金额可能来自错误抽取，无文件的不该算", got.AmountTotal, want)
	}
	// 合计与「已下载 N 张」必须指向同一批发票。这两个数字同屏显示，
	// 口径不一致时用户无从判断该信哪个。
	if got.Downloaded != 2 {
		t.Fatalf("downloaded = %d, want 2（与 amountTotal 口径必须一致）", got.Downloaded)
	}
	if got.Failed != 1 || got.Pending != 1 {
		t.Fatalf("failed=%d pending=%d, want 1/1 —— 不计入合计不等于不计数，"+
			"用户要能看见「还有几张没拿到」", got.Failed, got.Pending)
	}
	// 不计入合计 ≠ 从列表消失：五张发票的行都得在。
	if got.Count != 5 || len(got.Rows) != 5 {
		t.Fatalf("count=%d rows=%d, want 5/5 —— 收紧合计不能把发票从列表里抹掉", got.Count, len(got.Rows))
	}
	sawFailed := false
	for _, r := range got.Rows {
		if r.Status == "failed" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatalf("failed 的那张不在 rows 里，合计就失去了对账意义：%+v", got.Rows)
	}
}

// TestHandleEmailInvoiceSummary_EmptyLedgerTotalIsZero 空清单不能凭空造金额。
func TestHandleEmailInvoiceSummary_EmptyLedgerTotalIsZero(t *testing.T) {
	store, _, cleanup := newTestPGEmailStore(t)
	defer cleanup()
	srv, uid, wsID := newInvoiceSummaryServer(t, store)
	// 只建账号不建发票。
	if err := store.InsertAccount(context.Background(), &email.Account{
		ID: "acct-empty", UserID: uid, WorkspaceID: wsID,
		DisplayName: "empty", EmailAddress: "empty@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true,
		CreatedAt: time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	got := callInvoiceSummary(t, srv)
	if got.AmountTotal != 0 {
		t.Fatalf("amountTotal = %v, want 0 —— 空清单下不得出现任何金额", got.AmountTotal)
	}
	if got.Count != 0 || got.Downloaded != 0 {
		t.Fatalf("count=%d downloaded=%d, want 0/0", got.Count, got.Downloaded)
	}
}
