package server

// server_email_invoice_export_test.go — 需求 5 的 HTTP **成功路径**
// （POST /api/emails/invoices/export）。
//
// ## 为什么现在补
//
// 需求 5「按 A4 排版、每页 2x2 或 3x3、打印后可直接剪裁」的核心算法
// （internal/email/export_pdf.go）覆盖得相当扎实：页数取整、多页源、图片发票、
// 非法 grid 拒绝、畸形件跳过、裁切线用「不画线对照产物」证明真被画进去。
// 缺的是**接线层**：`handleEmailInvoiceExport` 的成功分支此前 35.3%，
// 也就是「文件真的落盘了、返回的 url 真能下载、exported_at 真被记上」这三件事
// 从来没有端到端跑过。
//
// ## 本文件最想守住的不变量
//
// `server_email_pipeline.go:404-406` 写着：
//
//	// 记录导出时间 + 通知前端刷新。只给**真正进入网格**的票打时间戳：
//	// 被跳过的坏文件不能算「已导出」，否则发票页会显示一张根本没导出的票已归档。
//
// 这条是「注释声明的不变量」，值得用真实数据验一遍：混合清单里塞一个畸形 PDF，
// 断言两个好票的 exported_at 被记上、坏票的仍是 0。
//
// ## 负控（实测过，见 handoff §7dj）
//
// 1) 把 skipped 的按名过滤去掉（无条件 MarkInvoiceExported）→ 转红。
// 2) 把 `body.Grid == 0 → 2` 的缺省去掉（0 直接进 NUp）→ 转红。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// countPDFPages 用 pdfcpu 的解析器数页。
// 刻意**不**用字节扫描：输出 PDF 里嵌着源发票的 Form XObject，它们各自带
// /MediaBox 与页树，正则扫出来的第一个「页」是内层对象而不是输出页。
func countPDFPages(t *testing.T, path string) int {
	t.Helper()
	n, err := api.PageCountFile(path)
	if err != nil {
		t.Fatalf("PageCountFile %s: %v", filepath.Base(path), err)
	}
	return n
}

type exportResponse struct {
	File    string   `json:"file"`
	Count   int      `json:"count"`
	Grid    int      `json:"grid"`
	Skipped []string `json:"skipped"`
	URL     string   `json:"url"`
}

func postExport(t *testing.T, s *Server, ids []string, grid int) (*httptest.ResponseRecorder, exportResponse) {
	t.Helper()
	body := map[string]any{"ids": ids}
	if grid != 0 {
		body["grid"] = grid
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/emails/invoices/export", strings.NewReader(string(raw)))
	w := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(w, r)
	var resp exportResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// seededInvoice 是一条「文件已落盘」的发票：真实 store 行 + 磁盘上真实文件。
// 走 InsertEmail / UpsertInvoice / UpdateInvoiceHarvest 三个**生产写路径**，
// 不直接写 SQL —— 将来 schema 或写路径变了会红，而不是悄悄继续跑。
func seededInvoice(t *testing.T, s *Server, store *email.Store, name string, body []byte) *email.Invoice {
	t.Helper()
	ctx := context.Background()
	emailID := "exp-" + name
	seedAccountEmail(t, store, "acc-1", emailID, 1767225600)

	rel := filepath.Join("email-invoices", name)
	abs := filepath.Join(s.dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	inv, err := store.UpsertInvoice(ctx, &email.Invoice{
		EmailID: emailID, AccountID: "acc-1", Kind: "e-invoice", Category: "餐饮",
		Seller: "某供应商", Amount: 100, Currency: "CNY",
		InvoiceNo: "EXP-" + name, Status: "downloaded", FileName: name,
	}, "local", "default")
	if err != nil {
		t.Fatalf("UpsertInvoice %s: %v", name, err)
	}
	// 采集列只能由 UpdateInvoiceHarvest 写（UpsertInvoice 刻意不写，见 §7dd）。
	if err := store.UpdateInvoiceHarvest(ctx, &email.Invoice{
		ID: inv.ID, FileName: name, FilePath: rel, FileSource: "attachment", Status: "downloaded",
	}); err != nil {
		t.Fatalf("UpdateInvoiceHarvest %s: %v", name, err)
	}
	return inv
}

func exportedAt(t *testing.T, store *email.Store, id string) int64 {
	t.Helper()
	inv, err := store.GetInvoiceByIDScoped(context.Background(), id, "local", "default")
	if err != nil {
		t.Fatalf("GetInvoiceByIDScoped %s: %v", id, err)
	}
	return inv.ExportedAt
}

// 混合清单：两张合法 + 一张畸形。三条断言合成一个用例，因为它们是同一次导出的
// 三个侧面，拆开反而看不出「同一次调用里三者对不对得上」。
func TestInvoiceExport_SuccessPathMarksOnlyUsableInvoices(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}

	good1 := seededInvoice(t, s, store, "good-1.pdf", buildMiniPDF(t))
	good2 := seededInvoice(t, s, store, "good-2.pdf", buildMiniPDF(t))
	// 畸形：与实测一致的退化 PDF（只有 Catalog、无页树）。它在 handler 的 stat
	// 阶段能通过（文件存在），要到 pdfPageCountSafe 才被跳过。
	bad := seededInvoice(t, s, store, "bad.pdf", []byte(
		"%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"))

	w, resp := postExport(t, s, []string{good1.ID, good2.ID, bad.ID}, 0)
	if w.Code != 200 {
		t.Fatalf("export => (%d, %s)", w.Code, w.Body.String())
	}

	// count 必须是**真正进入网格**的张数，不是请求数。
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2（请求了 3 张，1 张畸形）", resp.Count)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0] != "bad.pdf" {
		t.Errorf("skipped = %v, want [bad.pdf]", resp.Skipped)
	}
	// grid 缺省必须是 2。
	if resp.Grid != 2 {
		t.Errorf("grid = %d, want 2（未传时的缺省）", resp.Grid)
	}

	// 产物真的落盘了。
	out := filepath.Join(s.dataDir, "email-invoices", "exports", "default", resp.File)
	if st, err := os.Stat(out); err != nil {
		t.Errorf("导出文件不存在: %v", err)
	} else if st.Size() == 0 {
		t.Error("导出文件是空的")
	}

	// ★ 本文件最想守的不变量：只有真正进网格的票被打上 exported_at。
	if got := exportedAt(t, store, good1.ID); got == 0 {
		t.Error("good-1 已进入网格但 exported_at 仍是 0")
	}
	if got := exportedAt(t, store, good2.ID); got == 0 {
		t.Error("good-2 已进入网格但 exported_at 仍是 0")
	}
	if got := exportedAt(t, store, bad.ID); got != 0 {
		t.Errorf("被跳过的坏票被打上了 exported_at=%d —— 发票页会显示一张没导出的票已归档", got)
	}

	// 返回的 url 必须真能下到同一个文件（否则前端点下载 404）。
	if !strings.Contains(resp.URL, resp.File) {
		t.Errorf("url = %q, 应含文件名 %q", resp.URL, resp.File)
	}
	dl := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/export/download?file="+resp.File, nil)
	dlW := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(dlW, dl)
	if dlW.Code != 200 {
		t.Errorf("按返回的 url 下载 => %d, want 200", dlW.Code)
	}
	if ct := dlW.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("下载 Content-Type = %q, want application/pdf", ct)
	}
}

// grid=3 必须被接受，且**真的**走 3x3（文件名里带 3x3）。
func TestInvoiceExport_Grid3Accepted(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}
	inv := seededInvoice(t, s, store, "g3.pdf", buildMiniPDF(t))

	w, resp := postExport(t, s, []string{inv.ID}, 3)
	if w.Code != 200 {
		t.Fatalf("export => (%d, %s)", w.Code, w.Body.String())
	}
	if resp.Grid != 3 {
		t.Errorf("grid = %d, want 3", resp.Grid)
	}
	if !strings.Contains(resp.File, "3x3") {
		t.Errorf("产物文件名 %q 里没有 3x3 —— 排版参数没传下去", resp.File)
	}
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}
}

// 非法 grid 必须被拒（否则会产出无法剪裁的版式）。核心算法层已有覆盖
// （TestExportInvoiceGrid_RejectsInvalidGrid），这里钉的是**接线层有没有把它透上来**。
func TestInvoiceExport_InvalidGridRejected(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}
	inv := seededInvoice(t, s, store, "g1.pdf", buildMiniPDF(t))

	for _, grid := range []int{1, 4, -1} {
		w, _ := postExport(t, s, []string{inv.ID}, grid)
		if w.Code == 200 {
			t.Errorf("grid=%d 竟返回 200：%s", grid, w.Body.String())
		}
	}
}

// 重复导出同一批票：产物文件名带纳秒时间戳，两次不应互相覆盖
// （这正是 :142-143 注释里点名要防的事）。
func TestInvoiceExport_RepeatedExportDoesNotOverwrite(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}
	inv := seededInvoice(t, s, store, "dup.pdf", buildMiniPDF(t))

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		w, resp := postExport(t, s, []string{inv.ID}, 2)
		if w.Code != 200 {
			t.Fatalf("第 %d 次导出 => %d %s", i+1, w.Code, w.Body.String())
		}
		if seen[resp.File] {
			t.Fatalf("第 %d 次导出撞了同一个文件名 %q，后一次覆盖了前一次", i+1, resp.File)
		}
		seen[resp.File] = true
		if resp.Count != 1 {
			t.Fatalf("第 %d 次 count = %d, want 1", i+1, resp.Count)
		}
	}
	if len(seen) != 3 {
		t.Errorf("三次导出产出了 %d 个不同文件, want 3", len(seen))
	}
}

// 一次导出多张：count 必须是张数，页数按 grid² 推算（2x2 → 每页 4 张）。
func TestInvoiceExport_MultipleFilesProduceExpectedPages(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}

	var ids []string
	for i := 0; i < 9; i++ {
		inv := seededInvoice(t, s, store, fmt.Sprintf("m%d.pdf", i), buildMiniPDF(t))
		ids = append(ids, inv.ID)
	}
	w, resp := postExport(t, s, ids, 2)
	if w.Code != 200 {
		t.Fatalf("export => (%d, %s)", w.Code, w.Body.String())
	}
	if resp.Count != 9 {
		t.Fatalf("count = %d, want 9", resp.Count)
	}
	// 2x2 每页 4 张 ⇒ 9 张 = 3 页。用 pdfcpu 数页，不靠字节猜。
	if pages := countPDFPages(t, filepath.Join(s.dataDir, "email-invoices", "exports", "default", resp.File)); pages != 3 {
		t.Errorf("页数 = %d, want 3（9 张 / 每页 4 张）", pages)
	}
}
