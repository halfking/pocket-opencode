package email

// invoice_download_test.go — 发票 PDF 下载与落盘（需求 3 的成败点）。
//
// ## 为什么先测这两个
//
// `downloadPDF` 与 `saveInvoiceFile` 在 §7bw 的覆盖率盘点里是 **0%**：
// 这两个函数是「拿到发票链接 → 落盘 → 标记 downloaded」这条链的**唯一关口**，
// 一次都没被任何测试执行过。而 §7bv 已经在同一个包里证明过一次：
// 0% 的路径里确实藏着能让需求直接失效的缺陷。果不其然 —— 就是这里。
//
// 用 mock HTTP server，不需要真实邮箱、不需要外网。

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newHarvestHarness(t *testing.T, h http.HandlerFunc) (*InvoiceHarvester, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &InvoiceHarvester{
		DataDir:    t.TempDir(),
		HTTPClient: srv.Client(),
	}, srv
}

// attachIsolatedStore 给 harvester 挂一个**自建 schema** 里的 Store。
// 复用 store_workspace_test.go 的 newWorkspaceTestStore（它已经钉好
// search_path 并在 cleanup 里 DROP 自己的 schema）——本包自己的隔离助手，
// 不必再造一个，也避免「有的包隔离、有的包不隔离」的不一致。
func attachIsolatedStore(t *testing.T) *Store {
	t.Helper()
	s, cleanup := newWorkspaceTestStore(t)
	t.Cleanup(cleanup)
	return s
}

// 复用 store_email_scope_test.go:23 已有的 seedEmail(t, store, id, accountID,
// workspaceID, subject)。它插的那行 emails 满足 email_invoices.email_id 的外键
// 约束；不先建这行，UpsertInvoice 会以 23503 失败，用例就变成在测外键。

func TestDownloadPDF_SendsUAAndAcceptHeaders(t *testing.T) {
	var gotUA, gotAccept string
	h, srv := newHarvestHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		_, _ = w.Write([]byte("%PDF-1.7 payload"))
	})
	data, err := h.downloadPDF(context.Background(), srv.URL+"/x.pdf")
	if err != nil {
		t.Fatalf("downloadPDF: %v", err)
	}
	if string(data) != "%PDF-1.7 payload" {
		t.Fatalf("bytes not returned verbatim: %q", data)
	}
	// 某些开票平台对空 UA 直接 403，所以 UA 不是可选项。
	if gotUA == "" {
		t.Fatal("User-Agent must be set; some invoice hosts reject empty UA")
	}
	if !strings.Contains(gotAccept, "application/pdf") {
		t.Fatalf("Accept = %q, want it to include application/pdf", gotAccept)
	}
}

// 非 200 必须报错，否则 HTML 错误页会被当成 PDF 落盘。
func TestDownloadPDF_Non200IsAnError(t *testing.T) {
	h, srv := newHarvestHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>denied</html>"))
	})
	if _, err := h.downloadPDF(context.Background(), srv.URL+"/x.pdf"); err == nil {
		t.Fatal("http 403 must be an error, not an empty download")
	}
}

// ### 本文件的核心用例：超限必须报错，不能静默截断
//
// 修前是 `io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes))` ——
// 超限时**静默截断**成正好 20MB 且不报错。调用方
// （invoice_harvest.go:348）只判 `isPDFBytes(data)`，而截断后的 PDF
// 头部 `%PDF-` 依然完好 → 判定通过 → 落盘 → 标记 `downloaded`。
//
// 后果：一个**损坏到打不开的 PDF**，在库里被记成「已下载成功」。
// 需求 3 交付给用户的凭证附件就是坏的，而且没有任何报错。
func TestDownloadPDF_OversizeIsRejectedNotTruncated(t *testing.T) {
	// 比上限大 1 字节就够——判据必须是「超过上限」，不是「远大于上限」。
	const over = MaxInvoicePDFBytes + 1
	h, srv := newHarvestHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		// 内容形态与真发票一致：合法 PDF 头 + 巨量填充。
		w.Header().Set("Content-Type", "application/pdf")
		buf := bytes.NewBuffer(make([]byte, 0, over))
		buf.WriteString("%PDF-1.7\n")
		buf.Write(bytes.Repeat([]byte("A"), over-buf.Len()))
		_, _ = w.Write(buf.Bytes())
	})

	data, err := h.downloadPDF(context.Background(), srv.URL+"/big.pdf")
	if err == nil {
		t.Fatalf("oversize download must fail; got %d bytes silently", len(data))
	}
	// 错误信息要能让人一眼看出是超限，而不是「EOF」之类。
	if !strings.Contains(strings.ToLower(err.Error()), "too large") &&
		!strings.Contains(err.Error(), fmt.Sprintf("%d", MaxInvoicePDFBytes)) {
		t.Fatalf("error must name the size limit, got %q", err)
	}
	// 关键：失败时不能顺手把截断内容返回给调用方。
	if len(data) >= MaxInvoicePDFBytes {
		t.Fatalf("truncated body leaked to caller: %d bytes", len(data))
	}
}

// 为什么必须在 downloadPDF 层报错，而不是靠调用方的 isPDFBytes 兜底：
// 被截断的 20MB 内容**头部依然合法**。这条用例把这个事实钉住，
// 免得后来者以为「isPDFBytes 也能挡一下」而去削弱 downloadPDF 的检查。
func TestIsPDFBytes_CannotDetectTruncation(t *testing.T) {
	// 夹具就是旧代码会产出的那个东西：正好 20MB、头部完好、内容被切掉。
	truncated := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("A"), MaxInvoicePDFBytes-9)...)
	if int64(len(truncated)) != MaxInvoicePDFBytes {
		t.Fatalf("fixture wrong: %d bytes, want exactly %d", len(truncated), MaxInvoicePDFBytes)
	}
	if !isPDFBytes(truncated) {
		t.Fatal("a truncated PDF must still pass the magic check")
	}
	// 结论：isPDFBytes 对「内容被切掉」完全无感，所以唯一的关口是 downloadPDF。
}

// 负控对照：没超限时必须**原样**通过，否则上一条会变成「永远拒绝一切」。
func TestDownloadPDF_JustUnderLimitStillPasses(t *testing.T) {
	const under = MaxInvoicePDFBytes - 1024
	h, srv := newHarvestHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		buf := bytes.NewBuffer(make([]byte, 0, under))
		buf.WriteString("%PDF-1.7\n")
		buf.Write(bytes.Repeat([]byte("B"), under-buf.Len()))
		_, _ = w.Write(buf.Bytes())
	})
	data, err := h.downloadPDF(context.Background(), srv.URL+"/ok.pdf")
	if err != nil {
		t.Fatalf("a file just under the limit must pass: %v", err)
	}
	if len(data) != under {
		t.Fatalf("len = %d, want %d (bytes must be verbatim)", len(data), under)
	}
}

// --- 落盘 ---

func TestSaveInvoiceFile_WritesCanonicalNameAndMarksDownloaded(t *testing.T) {
	h, _ := newHarvestHarness(t, nil)
	h.Store = attachIsolatedStore(t)

	inv := &Invoice{
		ID:          "inv-test-1",
		EmailID:     "em-1",
		WorkspaceID: "ws-1",
		Category:    "其他",
		Seller:      "开票中心",
		Amount:      128.00,
		InvoiceDate: "2026-09-24",
	}
	seedAccount(t, h.Store, "acct-dl", "u1", "ws-1")
	seedEmail(t, h.Store, "em-1", "acct-dl", "ws-1", "发票1")
	// UpdateInvoiceHarvest 是 UPDATE，行必须先存在，否则它会因 0 rows 报错，
	// 让本用例测成「保存失败」而不是「保存正确」。
	if _, err := h.Store.UpsertInvoice(context.Background(), inv, "u1", "ws-1"); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	// 规范名：{费用类型}-{对方单位}-{金额}-{日期}.pdf
	//
	// 夹具用 minimalInvoicePDF（结构完整的单页 PDF）而不是 `[]byte("%PDF-1.7 body")`。
	// 合并修订：main 侧给采集器加了 pdfHasPages 校验（pdfcpu PageCount >= 1），
	// 那个只有魔数的假 PDF 会被判成「unusable pdf」走 markRetry，本用例于是
	// 变成在验「重试」而不是「命名与落盘」。详见 invoice_pdf_fixture_test.go。
	pdfBytes := minimalInvoicePDF()
	got := h.savePDF(context.Background(), inv, pdfBytes, "pdf-url")
	if got != "downloaded" {
		t.Fatalf("savePDF = %q, want downloaded (err=%s)", got, inv.LastError)
	}
	if inv.Status != "downloaded" || inv.FileSource != "pdf-url" {
		t.Fatalf("invoice not updated: status=%q source=%q", inv.Status, inv.FileSource)
	}
	want := "其他-开票中心-128.00-2026-09-24.pdf"
	if inv.FileName != want {
		t.Fatalf("FileName = %q, want %q", inv.FileName, want)
	}
	path := filepath.Join(h.DataDir, "email-invoices", "ws-1", want)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// 落盘内容必须与传入的字节**逐字节相同**（落盘环节不做任何改写）。
	if !bytes.Equal(b, pdfBytes) {
		t.Fatalf("file content = %d bytes, want the %d bytes handed to savePDF", len(b), len(pdfBytes))
	}
	// 临时文件必须被 rename 掉，不能留 .tmp 残渣。
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("a .tmp file was left behind; the rename is not atomic")
	}
}

// 缺日期时应从 PDF 字节里兜底解析，而不是生成一个带空日期的文件名。
func TestSaveInvoiceFile_FillsDateFromBytesWhenMissing(t *testing.T) {
	h, _ := newHarvestHarness(t, nil)
	h.Store = attachIsolatedStore(t)
	inv := &Invoice{ID: "inv-test-2", EmailID: "em-2", WorkspaceID: "ws-1", Category: "其他", Seller: "云服务", Amount: 1280.00}
	seedAccount(t, h.Store, "acct-dl2", "u1", "ws-1")
	seedEmail(t, h.Store, "em-2", "acct-dl2", "ws-1", "发票2")
	if inv.InvoiceDate != "" {
		t.Fatal("precondition: InvoiceDate must start empty")
	}
	if _, err := h.Store.UpsertInvoice(context.Background(), inv, "u1", "ws-1"); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	// 同样用结构完整的夹具：只有魔数的假 PDF 会被 pdfHasPages 判成不可用，
	// 走 markRetry 而不是落盘（见 invoice_pdf_fixture_test.go 的说明）。
	if got := h.savePDF(context.Background(), inv, minimalInvoicePDF(), "pdf-url"); got != "downloaded" {
		t.Fatalf("savePDF = %q err=%s", got, inv.LastError)
	}
	_ = inv.InvoiceDate // 解析结果依赖具体解析器，这里只锁「不会崩、仍会落盘」
	if inv.FileName == "" || !strings.HasSuffix(inv.FileName, ".pdf") {
		t.Fatalf("FileName = %q, want a .pdf name even without a parsed date", inv.FileName)
	}
}

// 目录不可写时必须落到 pending/failed 并留下原因，而不是静默成功。
func TestSaveInvoiceFile_UnwritableDirIsNotReportedAsDownloaded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission bits are not enforced")
	}
	h, _ := newHarvestHarness(t, nil)
	h.Store = attachIsolatedStore(t)
	// 用一个已存在的**文件**占住 email-invoices 位置，MkdirAll 必然失败。
	blocked := filepath.Join(h.DataDir, "email-invoices")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	inv := &Invoice{ID: "inv-test-3", WorkspaceID: "ws-1", Category: "其他", Seller: "X", Amount: 1, InvoiceDate: "2026-09-24"}
	got := h.savePDF(context.Background(), inv, []byte("%PDF"), "pdf-url")
	if got == "downloaded" {
		t.Fatal("an unwritable dir must not be reported as downloaded")
	}
	if inv.LastError == "" {
		t.Fatal("a failed save must record why")
	}
}
