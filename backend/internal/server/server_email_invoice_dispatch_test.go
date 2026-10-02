package server

// server_email_invoice_dispatch_test.go — 需求 2/3/5 发票端点的 **HTTP 接线层**。
//
// ## 为什么必须测这里
//
// `server_email_invoice.go` 此前是 0% 覆盖（138/138 未执行），意味着「哪个 URL
// 落到哪个 handler」「每个 handler 的方法/参数守卫长什么样」全靠读代码保证。
// 而 `handleEmailInvoiceDispatch` 的 switch 里有一个非常容易退化的顺序依赖：
//
//	case rest == "export":                       // 精确相等
//	...
//	case strings.HasPrefix(rest, "export/"):     // 前缀
//
// 若有人把第一条从 `==` 改成 `HasPrefix`，`export/download`（下载端点）会被前一条
// 抢走、变成去生成新 PDF。这类 bug 在真实链路上表现为「下载按钮点了生成了一个
// 新文件」——功能看起来还在跑，不会有人从日志里发现。
//
// ## 本文件分两部分，因为单靠一种状态**证明不了**接线
//
// 1) `nil-store` 部分（不需要 DB）：钉住每道守卫的**顺序**（先查库还是先查方法）。
//    它**做不到**区分「同样回 503 email store」的 ops / file / thumb —— 这不是
//    缺陷，是 nil 状态本身的表达力上限。所以：
// 2) `真 store` 部分（需要 DB）：给每个 handler 造一个**唯一**的响应签名，
//    从而真正证明「这个 URL 落到了这个 handler」。
//
// ## 负控（实测过，见 handoff §7dd）
//
// 把 `case rest == "export"` 改成 `strings.HasPrefix(rest, "export")`
// → TestInvoiceDispatch_RealStore_EachSubPathHasUniqueSignature 转红。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------- 第一部分：nil-store，钉守卫顺序 ----------

func dispatchOnce(t *testing.T, s *Server, method, path, body string) (int, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(w, r)
	var payload struct {
		Error string `json:"error"`
	}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
	}
	return w.Code, payload.Error
}

// 无库时，每个子路径都必须被自己的守卫接住，而不是掉进 default。
//
// 这里**刻意不**断言「签名互不相同」：extract/harvest 都说 "POST only"，
// ops/file/thumb 都说 "email store not configured"，这是真实且正确的。
// 强行要求唯一只会逼出一堆假区分。可区分性由下面第二部分负责。
func TestInvoiceDispatch_NilStore_EverySubPathHitsItsOwnGuard(t *testing.T) {
	s := &Server{} // nil emailStore / 空 dataDir / 无 pipeline

	cases := []struct {
		name, method, path, body string
		want                     int
		msg                      string
	}{
		{"extract 拒非 POST", http.MethodGet, "/api/emails/invoices/extract", "", 405, "POST only"},
		{"extract 无库", http.MethodPost, "/api/emails/invoices/extract", `{"emailId":"e1"}`, 503, "email store not configured"},
		{"harvest 拒非 POST", http.MethodGet, "/api/emails/invoices/harvest", "", 405, "POST only"},
		{"harvest 无库", http.MethodPost, "/api/emails/invoices/harvest", "", 503, "email store not configured"},
		{"export 拒非 POST", http.MethodGet, "/api/emails/invoices/export", "", 405, "POST only"},
		// export 的第一道守卫是 dataDir 而不是 store —— 顺序与同族相反，钉住。
		{"export 无 dataDir", http.MethodPost, "/api/emails/invoices/export", `{"ids":["a"]}`, 503, "data dir not configured"},
		{"push 拒非 POST", http.MethodGet, "/api/emails/invoices/push", "", 405, "POST only"},
		{"push 无 pipeline", http.MethodPost, "/api/emails/invoices/push", "", 503, "email pipeline not configured"},
		{"summary 拒非 GET", http.MethodPost, "/api/emails/invoices/summary", "", 405, "GET only"},
		{"summary 无 pipeline", http.MethodGet, "/api/emails/invoices/summary", "", 503, "email pipeline not configured"},

		{"下载拒非 GET", http.MethodPost, "/api/emails/invoices/export/download", "", 405, "GET only"},
		{"下载缺 file", http.MethodGet, "/api/emails/invoices/export/download", "", 400, "file required"},
		// export 下的任意子路径都归下载端点，不该掉进 ops（default 分支）。
		{"export 任意子路径仍归下载", http.MethodGet, "/api/emails/invoices/export/whatever/x", "", 400, "file required"},

		{"file 无库", http.MethodGet, "/api/emails/invoices/abc123/file", "", 503, "email store not configured"},
		{"thumb 无库", http.MethodGet, "/api/emails/invoices/abc123/thumb", "", 503, "email store not configured"},
		// loadScopedInvoiceFile 先查库后查方法，所以非 GET 也报 503 而不是 405。
		{"file 非 GET 仍先撞无库", http.MethodPost, "/api/emails/invoices/abc123/file", "", 503, "email store not configured"},

		{"ops 无库", http.MethodGet, "/api/emails/invoices/abc123", "", 503, "email store not configured"},
		// handleEmailInvoiceOps 也是先查库（:338）再取 id（:342），
		// 所以「缺 invoice id」这个 400 在无库实例上**永远出不来**。
		{"ops 缺 id 也先撞无库", http.MethodGet, "/api/emails/invoices/", "", 503, "email store not configured"},
		{"ops 缺 id 非 GET 同样先撞无库", http.MethodDelete, "/api/emails/invoices/", "", 503, "email store not configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, msg := dispatchOnce(t, s, c.method, c.path, c.body)
			if code != c.want || msg != c.msg {
				t.Fatalf("%s %s => (%d, %q), want (%d, %q)", c.method, c.path, code, msg, c.want, c.msg)
			}
		})
	}
}

// 列表端点 /api/emails/invoices 不经 Dispatch，走自己的 handler。
// 它的守卫顺序同样被钉住：这里**永远**是 503，"GET only" 的 405 在无库实例上不可达。
func TestInvoiceListHandler_NilStore_NeverReportsMethodError(t *testing.T) {
	s := &Server{}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		r := httptest.NewRequest(method, "/api/emails/invoices", nil)
		w := httptest.NewRecorder()
		s.handleEmailInvoices(w, r)
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
		if w.Code != 503 || payload.Error != "email store not configured" {
			t.Errorf("%s /api/emails/invoices => (%d, %q), want (503, email store not configured)",
				method, w.Code, payload.Error)
		}
	}
}

// atoiSafe 是 limit/offset 的唯一入口。它决定「?limit=abc」是「取前 0 条」
// 还是「取全部」——对用户是「列表空了」和「慢」的区别，此前 0% 覆盖。
func TestAtoiSafe(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0}, {"0", 0}, {"20", 20}, {"007", 7},
		{"-5", 0}, {"12a", 0}, {"1e3", 0}, {" 20", 0},
		{"100000", 100000}, {"100001", 100000}, {"999999999", 100000}, {"1000000", 100000},
	}
	for _, c := range cases {
		if got := atoiSafe(c.in); got != c.want {
			t.Errorf("atoiSafe(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ---------- 第二部分：真 store，唯一签名证明接线 ----------

// newInvoiceScopedStore 自建**完全隔离**的 schema：search_path 只指向它，
// 不追加 public（与仓库其余 PG 测试助手的约定不同，理由见 handoff §7dd）。
// 追加 public 会让「表不存在」的测试悄悄读到真实库，从而把结论变成假绿。
func newInvoiceScopedStore(t *testing.T) (*email.Store, func()) {
	t.Helper()
	dsn := ""
	for _, key := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(key); v != "" {
			dsn = v
			break
		}
	}
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN or POCKET_POSTGRES_DSN not set; skipping invoice dispatch integration test")
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
	schema := "invoice_dispatch_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	scopedPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
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
	return store, cleanup
}

// miniPDF 是一个**结构完整**的 PDF（用 gofpdf 生成），能被 DetectInvoiceMedia
// 认成 PDF、但正文里没有任何图片，所以 thumb 分支会走「抽不出图」那条路。
//
// 为什么不用手写的 `%PDF-1.4 ...` 裸字节：那类畸形 PDF 会让 pdfcpu v0.11.0
// 的 model.skipStringLit（pkg/pdfcpu/model/parse.go:1273）panic
// （slice bounds out of range [-1:]）—— 第三方库的 bug，不是本仓库的，
// 但它会经 api.ExtractImagesRaw 冒到 /thumb 端点上。已单独记录，见 handoff §7dd。
// 这里用「合法 PDF」是为了让接线测试测接线，不去踩那个雷。
func buildMiniPDF(t *testing.T) []byte {
	t.Helper()
	pdf := gofpdf.New("P", "mm", "A5", "")
	pdf.AddPage()
	pdf.SetFont("helvetica", "", 10)
	pdf.CellFormat(0, 10, "invoice", "", 1, "C", false, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("gofpdf.Output: %v", err)
	}
	return buf.Bytes()
}

// 接线层的真证明：每个子路径给出**唯一**的响应签名。
// 签名重复就说明两个 URL 落到了同一个 handler —— 正是 export/download 被
// export 抢走那类退化会呈现的样子。
func TestInvoiceDispatch_RealStore_EachSubPathHasUniqueSignature(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()

	miniPDF := buildMiniPDF(t)
	dataDir := t.TempDir()
	relPath := filepath.Join("email-invoices", "wiring-test.pdf")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dataDir, relPath)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, relPath), miniPDF, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	// 建档要串三层 FK：account → email → invoice（实测 23503 会逐层逼出来：
	// 先 emails.account_id 缺 account，再 email_invoices.email_id 缺 email）。
	// 这条链本身就是一条隐含约束：发票不可能脱离真实邮件凭空存在。
	if err := store.InsertAccount(context.Background(), &email.Account{
		ID: "acc-1", UserID: "local", WorkspaceID: "default",
		DisplayName: "wiring", EmailAddress: "billing@vendor.example",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password", Enabled: true,
	}, ""); err != nil {
		t.Fatalf("InsertAccount: %v", err)
	}
	if err := store.InsertEmail(context.Background(), email.Email{
		ID: "wiring-email-1", AccountID: "acc-1", WorkspaceID: "default",
		FromAddress: "billing@vendor.example", FromName: "某餐饮公司",
		Subject: "电子发票", Date: 1767225600,
	}); err != nil {
		t.Fatalf("InsertEmail: %v", err)
	}

	inv, err := store.UpsertInvoice(context.Background(), &email.Invoice{
		EmailID:   "wiring-email-1",
		AccountID: "acc-1",
		Kind:      "e-invoice",
		Category:  "餐饮",
		Seller:    "某餐饮公司",
		Amount:    100,
		Currency:  "CNY",
		InvoiceNo: "WIRING123",
		Status:    "downloaded",
		FileName:  "餐饮-某餐饮公司-100元-20260101.pdf",
		FilePath:  relPath,
	}, "local", "default")
	if err != nil {
		t.Fatalf("UpsertInvoice: %v", err)
	}
	if inv.ID == "" {
		t.Fatal("UpsertInvoice returned empty id")
	}

	// UpsertInvoice **刻意不写** 采集列（file_name/file_path/...，见 invoice_store.go:94
	// 的 INSERT 列表），所以刚返回的 inv 里的 FilePath 是内存回显、不是读回来的值。
	// 落盘路径必须走 UpdateInvoiceHarvest —— 顺带钉住这个「返回值不是读回值」的事实。
	if err := store.UpdateInvoiceHarvest(context.Background(), &email.Invoice{
		ID: inv.ID, FileName: "餐饮-某餐饮公司-100元-20260101.pdf",
		FilePath: relPath, FileSource: "attachment", Status: "downloaded",
	}); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	s := &Server{emailStore: store, dataDir: dataDir}

	type probe struct {
		label  string
		method string
		path   string
		body   string
		want   int
		msg    string
	}
	probes := []probe{
		{"extract", http.MethodPost, "/api/emails/invoices/extract", `{"emailId":"no-such-email-id"}`,
			404, "email not found"},
		{"harvest", http.MethodPost, "/api/emails/invoices/harvest", `{}`,
			503, "email fetcher not configured (IMAP unavailable)"},
		{"export", http.MethodPost, "/api/emails/invoices/export", `{"ids":["no-such-invoice"]}`,
			400, "no harvested invoice files in selection"},
		{"exportDownload", http.MethodGet, "/api/emails/invoices/export/download?file=missing.pdf", "",
			404, "export not found"},
		{"ops-missing", http.MethodGet, "/api/emails/invoices/no-such-invoice", "",
			404, "invoice not found"},
		{"thumb", http.MethodGet, "/api/emails/invoices/" + inv.ID + "/thumb", "",
			404, "thumbnail unavailable"},
	}
	seen := map[string]string{}
	for _, p := range probes {
		code, msg := dispatchOnce(t, s, p.method, p.path, p.body)
		if code != p.want || msg != p.msg {
			t.Errorf("%s: %s %s => (%d, %q), want (%d, %q)", p.label, p.method, p.path, code, msg, p.want, p.msg)
			continue
		}
		key := strconv.Itoa(code) + "|" + msg
		if prev, dup := seen[key]; dup {
			t.Errorf("签名 %q 被 %q 与 %q 共用：这两个 URL 无法证明落到了不同 handler", key, prev, p.label)
		}
		seen[key] = p.label
	}

	// ops（存在的记录）返回发票 JSON；file（同一 id）返回 PDF 字节。
	// 同一个 URL 形态下两种内容彻底分开，才算真正钉住了 {id} 与 {id}/file 两条腿。
	opsReq := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/"+inv.ID, nil)
	opsW := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(opsW, opsReq)
	if opsW.Code != 200 || !strings.Contains(opsW.Body.String(), "WIRING123") {
		t.Errorf("ops GET existing => %d %s, want 200 with invoice JSON", opsW.Code, trunc(opsW.Body.String()))
	}

	fileReq := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/"+inv.ID+"/file", nil)
	fileW := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(fileW, fileReq)
	if fileW.Code != 200 {
		t.Errorf("file GET existing => %d %s, want 200", fileW.Code, trunc(fileW.Body.String()))
	}
	if got := fileW.Body.Bytes(); string(got) != string(miniPDF) {
		t.Errorf("file body mismatch: got %d bytes, want %d", len(got), len(miniPDF))
	}
	if cd := fileW.Header().Get("Content-Disposition"); !strings.Contains(cd, "餐饮-某餐饮公司-100元-20260101.pdf") {
		t.Errorf("Content-Disposition = %q, want the规范文件名", cd)
	}
}

func trunc(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
