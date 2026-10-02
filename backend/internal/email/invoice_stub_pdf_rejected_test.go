package email

// invoice_stub_pdf_rejected_test.go — 采集器不得把**退化 PDF**（只有 Catalog、
// 没有页树）当成下载成功的发票。
//
// 缺陷背景（2026-10-02 实测）：`isPDFBytes` 只验 `%PDF` magic，于是 69 字节的
// 空壳 PDF 能一路走通 saveInvoiceFile，被写盘并置 status=downloaded。后果有两条：
//
//  1. 台账里凭空多出一张**金额为 0** 的「发票」（文件名也就成了
//     `其他-<单位>-0.00-<日期>.pdf`），汇总金额被污染；
//  2. 这正是导出侧 pdfcpu 遍历页树会 panic 的那种输入——见
//     export_pdf_test.go 的 TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes
//     与 pdfPageCountSafe 的注释。采集器在制造导出器不得不防御性跳过的文件。
//
// 修法是在落盘前用 pdfcpu 的权威解析确认「至少有 1 页」，不满足就 markRetry
// （需求：「有可能我们需要多次操作才能下载到发票文件」——拿回来不是发票就该
// 重试，而不是当成成功）。
//
// 真实样本已随 2026-10-01 的 schema 重建丢失，所以退化件用**合成**字节；
// 形态与 export_pdf_test.go / invoice_sources_e2e_test.go 里的实测退化件一致。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPDFBytes 是实测退化件的逐字节形态：只有 Catalog、没有页树。
var stubPDFBytes = []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")

// 空壳件必须被 pdfHasPages 判否，而真发票判是——否则 saveInvoiceFile 的
// 门禁本身就是个摆设（判据必须能区分两者才有意义）。
func TestPdfHasPages_DistinguishesStubFromRealInvoice(t *testing.T) {
	if len(stubPDFBytes) != 69 {
		t.Errorf("退化件字节数=%d，want 69（要与实测产物一致，否则这个夹具已失真）", len(stubPDFBytes))
	}
	if ok, err := pdfHasPages(stubPDFBytes); err == nil {
		t.Errorf("退化件被判定为有页（err=%v）：门禁拦不住它，saveInvoiceFile 会照收", ok)
	}
	if ok, err := pdfHasPages([]byte(e2eInvoicePDF)); err != nil || !ok {
		t.Errorf("真发票被判成无页：ok=%v err=%v —— 会把好件误杀", ok, err)
	}
}

// 承重用例：退化件走 saveInvoiceFile 必须是 pending/failed、不得落盘、
// 不得置 downloaded，且 last_error 要能让人看出是「拿回来不是发票」。
func TestSaveInvoiceFile_RejectsStubPDF(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	dataDir := t.TempDir()
	h := &InvoiceHarvester{Store: store, DataDir: dataDir}

	inv := &Invoice{
		ID: "inv-stub", EmailID: "em-stub", AccountID: "acct-stub",
		UserID: "user-1", WorkspaceID: "ws-1",
		Category: "其他", Seller: "云服务开票中心", Amount: 1280,
		InvoiceDate: "2026-09-28", Status: "pending", Attempts: 1,
	}
	seedAccount(t, store, "acct-stub", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-stub", AccountID: "acct-stub", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Date: 1750000000,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	got := h.saveInvoiceFile(ctx, inv, stubPDFBytes, "attachment")
	if got == "downloaded" {
		t.Fatalf("退化件被判为下载成功（status=%q）：台账会多出一张 0 元假发票", got)
	}
	if inv.Status == "downloaded" {
		t.Fatalf("Status=%q，want 非 downloaded", inv.Status)
	}
	if inv.FileName != "" || inv.FilePath != "" {
		t.Errorf("退化件不该留下文件名/路径：FileName=%q FilePath=%q", inv.FileName, inv.FilePath)
	}
	if inv.LastError == "" {
		t.Error("拒收时必须写 last_error，否则运维看不出是拿回来不是发票")
	} else if !strings.Contains(inv.LastError, "pdf") {
		t.Errorf("last_error=%q，应能看出是 PDF 的问题", inv.LastError)
	}
	// 目录要么不存在，要么不含 pdf——两种都不算「落盘成功」。
	dir := filepath.Join(dataDir, "email-invoices", "ws_user-admin")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".pdf") {
				t.Errorf("退化件被写盘了：%s", e.Name())
			}
		}
	}
}

// 正控：真发票在同一套夹具下必须照旧成功——否则门禁可能过严。
func TestSaveInvoiceFile_StillAcceptsRealPDF(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	dataDir := t.TempDir()
	h := &InvoiceHarvester{Store: store, DataDir: dataDir}

	inv := &Invoice{
		ID: "inv-real", EmailID: "em-real", AccountID: "acct-real",
		UserID: "user-1", WorkspaceID: "ws-1",
		Category: "其他", Seller: "云服务开票中心", Amount: 1280,
		InvoiceDate: "2026-09-28", Status: "pending", Attempts: 1,
	}
	seedAccount(t, store, "acct-real", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-real", AccountID: "acct-real", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Date: 1750000000,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	if got := h.saveInvoiceFile(ctx, inv, []byte(e2eInvoicePDF), "attachment"); got != "downloaded" {
		t.Fatalf("真发票 saveInvoiceFile = %q, want \"downloaded\"（last_error=%q）", got, inv.LastError)
	}
	if inv.Status != "downloaded" || inv.FilePath == "" {
		t.Fatalf("真发票应落盘并置 downloaded：status=%q path=%q", inv.Status, inv.FilePath)
	}
	if !strings.HasSuffix(inv.FileName, "-1280.00-2026-09-28.pdf") {
		t.Errorf("文件名 %q 不符合 {费用类型}-{对方单位}-{金额}-{日期}.pdf", inv.FileName)
	}
}
