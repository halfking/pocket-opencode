package server

// server_email_invoice_unmarked_test.go — §945 判据：发票**导出标记写失败**
// 必须可区分（而且**不**否定已经成功的产物）。
//
// # 这条判据在量什么
//
//	§945 修掉的那处是 `handleEmailInvoiceExport` 里的
//	`_ = s.emailStore.MarkInvoiceExported(...)` ⇒ ⇒⇒ 标记写失败时**完全无声**。
//
//	⚠ 后果（为什么它不是「小错」）：
//	**选取口径**是「已下载且 exported_at=0」（email/pipeline_a4.go 的口径注释）
//	⇒ ⇒⇒⇒ 标记写失败 ⇒ 库里 `exported_at` 仍是 0 ⇒ ⇒⇒⇒ ⇒ **下一轮会把
//	⇒ ⇒⇒⇒ ⇒ 同一批票再拼一次 A4** ⇒ ⇒⇒⇒ ⇒ 而 exports 目录每天多一个文件、
//	⇒ ⇒⇒⇒ ⇒ 里面是同一批票 ⇒ ⇒⇒⇒ ⇒ ⇒ 而**定时流水线那份已经是这么处理的**
//	⇒ ⇒⇒⇒ ⇒ （pipeline_a4.go：`rep.AddError(...)` + `continue` ⇒ 不计入 marked）
//	⇒ ⇒⇒⇒ ⇒ ⇒ ⇒ ⇒ **同一段逻辑的两份实现，一份对、一份静默**。
//
//	⚠ 处置口径（§945 的四类语义之四）：
//	**产物成功就是成功**（文件真的生成了）⇒ ⇒⇒ 所以**不改整体响应码**
//	⇒ ⇒⇒ 但「哪些票没记上」必须**可区分** ⇒ ⇒⇒ ⇒ 响应里带一个**可选**
//	⇒ ⇒⇒ 字段 `unmarked`（`omitempty` ⇒ 老客户端不受影响 ⇒ 形状不变）。
//
// # 怎么造这个失败
//
//	`email.Store` 是**具体类型**（不是接口）⇒ ⇒⇒ 包一层假 store 是不行的
//	⇒ ⇒⇒ ⇒ 所以用**数据库触发器**：在 `email_invoices` 上挂一个
//	⇒ ⇒⇒ ⇒ `BEFORE UPDATE` 触发器，直接 RAISE EXCEPTION
//	⇒ ⇒⇒ ⇒ ⇒ 只影响 `MarkInvoiceExported`（那正是它唯一的 UPDATE）。
//
//	⚠ 触发器在**建完夹具之后**才装 ⇒ ⇒⇒ ⇒ 否则连种数据都写不进去。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/jackc/pgx/v5/pgxpool"
)

// s945InvoiceStore 造一个隔离 schema 的 email store，**并把 pool 一起返回**
// （需要它来装触发器）。
func s945InvoiceStore(t *testing.T) (*email.Store, *pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping invoice export failure test")
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
	schema := "s945_invoice_test_" + hex.EncodeToString(suffix)
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
	return store, scopedPool, cleanup
}

// s945ExportResponse 是导出响应（比既有那个多一个 `unmarked` 字段）。
type s945ExportResponse struct {
	File    string   `json:"file"`
	Count   int      `json:"count"`
	Grid    int      `json:"grid"`
	Skipped []string `json:"skipped"`
	URL     string   `json:"url"`
	// ⚠ `unmarked` 是 §945 新增的**可选**字段（omitempty ⇒ 正常路径不出现）。
	Unmarked []string `json:"unmarked"`
}

// s945PostExport POST /api/emails/invoices/export。
func s945PostExport(t *testing.T, s *Server, ids []string) (int, s945ExportResponse, string) {
	t.Helper()
	body := map[string]any{"ids": ids}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/emails/invoices/export", strings.NewReader(string(raw)))
	rr := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(rr, req)
	var resp s945ExportResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return rr.Code, resp, rr.Body.String()
}

// s945BreakInvoiceExportMark 装一个让 `email_invoices` 的 UPDATE 失败的触发器。
func s945BreakInvoiceExportMark(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION s945_fail_invoice_update() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 's945 injected invoice export mark failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("建函数: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER s945_invoice_update_fail BEFORE UPDATE ON email_invoices
		FOR EACH ROW EXECUTE FUNCTION s945_fail_invoice_update()`); err != nil {
		t.Fatalf("建触发器: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS s945_invoice_update_fail ON email_invoices")
	})
}

// TestS945InvoiceExportMarkFailureIsVisible 标记写失败 ⇒ 200 + unmarked 列出那张票。
func TestS945InvoiceExportMarkFailureIsVisible(t *testing.T) {
	store, pool, cleanup := s945InvoiceStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}

	inv := seededInvoice(t, s, store, "good-1.pdf", buildMiniPDF(t))
	s945BreakInvoiceExportMark(t, pool)

	code, resp, raw := s945PostExport(t, s, []string{inv.ID})

	// ① 响应码仍是 200：产物**真的生成成功**了 ⇒ 「产物成功就是成功」。
	if code != http.StatusOK {
		t.Errorf("导出响应码 = %d（期望 200）⇒ 标记写失败不该否定已经成功的产物。body=%s", code, raw)
	}
	// ② `unmarked` 必须**点名**那张票（可区分）。
	if len(resp.Unmarked) != 1 || resp.Unmarked[0] != inv.ID {
		t.Errorf("unmarked = %v，期望 [%s]（没记上「已导出」的票必须能被点名）", resp.Unmarked, inv.ID)
	}
	// ③ 库里 `exported_at` 仍是 0（写确实失败了，不是「写成功了但没报」）。
	if got := exportedAt(t, store, inv.ID); got != 0 {
		t.Errorf("exported_at = %d（期望 0）⇒ 触发器没生效，这条用例量不到东西", got)
	}
	// ④ 产物**仍然落盘**（这是「不改整体响应码」的前提）。
	out := filepath.Join(s.dataDir, "email-invoices", "exports", "default", resp.File)
	if st, err := os.Stat(out); err != nil {
		t.Errorf("导出文件不存在: %v", err)
	} else if st.Size() == 0 {
		t.Error("导出文件是空的")
	}
}

// TestS945InvoiceExportMarkSuccessHasNoUnmarked 负控：没失败时 **不出现** unmarked。
//
// ⚠ 为什么必须有这条：`unmarked` 是 `omitempty` 的**可选**字段
//
//	⇒ ⇒⇒ 若它总是出现，老客户端会看到多出来的键 ⇒ ⇒⇒ ⇒ 形状就变了
//	⇒ ⇒⇒ ⇒ 而「形状不变」是 §945 的硬约束 ⇒ ⇒⇒ ⇒ 必须钉住。
func TestS945InvoiceExportMarkSuccessHasNoUnmarked(t *testing.T) {
	store, _, cleanup := s945InvoiceStore(t)
	defer cleanup()
	s := &Server{emailStore: store, dataDir: t.TempDir()}

	inv := seededInvoice(t, s, store, "good-1.pdf", buildMiniPDF(t))

	code, resp, raw := s945PostExport(t, s, []string{inv.ID})
	if code != http.StatusOK {
		t.Fatalf("export => (%d, %s)", code, raw)
	}
	if len(resp.Unmarked) != 0 {
		t.Errorf("正常路径不该出现 unmarked：%v", resp.Unmarked)
	}
	if strings.Contains(raw, "unmarked") {
		t.Errorf("正常路径的响应体里出现了 unmarked 键（omitempty 失效 ⇒ 响应形状变了）：%s", raw)
	}
	if got := exportedAt(t, store, inv.ID); got == 0 {
		t.Error("正常路径 exported_at 仍是 0 ⇒ 标记没写上，这条负控量不到东西")
	}
}
