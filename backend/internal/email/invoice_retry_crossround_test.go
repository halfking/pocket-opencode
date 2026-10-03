package email

// invoice_retry_crossround_test.go — 需求「有可能我们需要多次操作才能下载到
// 发票文件」的**跨轮**验证。
//
// invoice_retry_test.go 已经覆盖了状态机（markRetry 的 pending/failed 收敛、
// 缓存命中路径的 Attempts 计数），但那些用例都**直接调 harvestOne 并在内存里
// 推进轮次**。于是有一条生产链路从未被任何测试走过：
//
//	第 N 轮失败落库(status=pending, attempts=N)
//	    → 下一轮 HarvestAll
//	    → ListHarvestableInvoices 重新把这条 pending 捞出来
//	    → 再次尝试并最终成功
//
// 这正是「配好了却永远不触发」那一族的缝隙：选取查询若写错（例如只捞 'new'），
// 上面的状态机测试照样全绿，而生产里重试永远不会发生——每张下载失败的发票会
// 永远停在 pending，每轮白占 MaxInvoicesPerHarvestRound 的预算。
//
// 真实库目前没有这种样本（唯一一条发票 attempts=1、status=downloaded），
// 所以这里用 httptest 构造「先 503 后 200」的平台行为来验证，不依赖真实邮箱。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// flakyInvoiceServer 模拟「发票平台第一次取不到、过一会儿才放行」。
// 前 failFirst 次请求返回 503，之后返回真 PDF。
type flakyInvoiceServer struct {
	hits      atomic.Int64
	failFirst int32
}

func (f *flakyInvoiceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.hits.Add(1) <= int64(f.failFirst) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream not ready"))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write([]byte(e2eInvoicePDF))
}

// TestInvoiceRetry_CrossRoundReselectsPendingAndSucceeds 走完整两轮 HarvestAll：
// 第 1 轮链接 503 → 必须落库为 pending 且 attempts=1；
// 第 2 轮链接恢复 → 必须被**重新选中**（不是靠内存里的 inv 变量）并成功落盘。
func TestInvoiceRetry_CrossRoundReselectsPendingAndSucceeds(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-retry-x", "user-1", "ws-1")

	flaky := &flakyInvoiceServer{failFirst: 1}
	srv := httptest.NewServer(flaky)
	defer srv.Close()

	// 正文里的下载链接必须是运行期才知道的地址，所以 MIME 在 server 起来之后才拼。
	raw := buildE2EMIME(t,
		"电子发票开具通知",
		"您的发票已开具，请点击链接下载 PDF：\n"+srv.URL+"/invoice/inv_3500.pdf\n"+"（请忽略本邮件）",
		nil)

	// 邮件 ID 必须带 em-pop3- 前缀：BodyCache 只在 isPOP3SourcedEmail 分支里
	// 被查（invoice_harvest.go:292-332），非 POP3 来源会直接走 Fetcher.FetchMessageRaw。
	// 真实的两张 QQ Wallet 发票正是 POP3 来源，所以这里必须走缓存分支，
	// 否则测的是另一条路。
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-retry-x", AccountID: "acct-retry-x", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Snippet: "发票已开具", Date: 1750000000, UID: 42,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-retry-x")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}

	inv := &Invoice{
		ID: "inv-retry-x", EmailID: em.ID, AccountID: "acct-retry-x",
		UserID: "user-1", WorkspaceID: "ws-1",
		Category: "其他", Seller: "杭州创客家投资管理有限公司",
		Amount: 3500, InvoiceDate: "2026-09-24", Status: "new",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	dir := t.TempDir()
	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir, HTTPClient: srv.Client(),
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
	}

	// ---- 第 1 轮：链接 503，应落 pending ----
	r1 := h.HarvestAll(ctx)
	if r1.Processed != 1 || r1.Pending != 1 || r1.Downloaded != 0 {
		t.Fatalf("第 1 轮结果 = %+v, want Processed=1 Pending=1 Downloaded=0", r1)
	}
	got1, err := store.GetInvoiceByEmailID(ctx, em.ID)
	if err != nil {
		t.Fatalf("读回发票: %v", err)
	}
	if got1.Status != "pending" {
		t.Fatalf("第 1 轮落库 status=%q, want \"pending\"", got1.Status)
	}
	if got1.Attempts != 1 {
		t.Fatalf("第 1 轮落库 attempts=%d, want 1", got1.Attempts)
	}
	if got1.LastError == "" {
		t.Fatal("第 1 轮失败却没有记录 last_error，运维看不懂为什么没下载下来")
	}

	// ---- 第 2 轮：链接恢复，必须被重新选中并成功 ----
	// 判据用「DB 读回的副本」，而不是继续用 inv 变量：只有这样才能证明
	// ListHarvestableInvoices 真的把 pending 重新捞了出来。
	r2 := h.HarvestAll(ctx)
	db2, dbErr := store.GetInvoiceByEmailID(ctx, em.ID)
	if dbErr != nil {
		t.Fatalf("读回发票: %v", dbErr)
	}
	if r2.Processed != 1 || r2.Downloaded != 1 {
		t.Fatalf("第 2 轮结果 = %+v, want Processed=1 Downloaded=1 —— "+
			"pending 记录没被重新选中，重试在生产里就不会发生。"+
			"（诊断：status=%q attempts=%d last_error=%q 平台命中次数=%d）",
			r2, db2.Status, db2.Attempts, db2.LastError, flaky.hits.Load())
	}
	got2, err := store.GetInvoiceByEmailID(ctx, em.ID)
	if err != nil {
		t.Fatalf("读回发票: %v", err)
	}
	if got2.Status != "downloaded" {
		t.Fatalf("第 2 轮落库 status=%q, want \"downloaded\"", got2.Status)
	}
	if got2.Attempts != 2 {
		t.Fatalf("第 2 轮落库 attempts=%d, want 2（两轮各计一次）", got2.Attempts)
	}
	if got2.FileSource != "pdf-url" {
		t.Fatalf("FileSource=%q, want \"pdf-url\"", got2.FileSource)
	}
	if got2.FilePath == "" {
		t.Fatal("FilePath 为空，下载成功的发票没有记录落盘位置")
	}
	if _, err := os.Stat(filepath.Join(dir, got2.FilePath)); err != nil {
		t.Fatalf("第 2 轮落盘文件不存在：%v", err)
	}
	// 负控：失败那轮绝不能已经写下 FilePath，否则第 2 轮的「成功」是假的。
	if got1.FilePath != "" {
		t.Fatalf("第 1 轮就写了 FilePath=%q，失败路径不该落盘", got1.FilePath)
	}
}
