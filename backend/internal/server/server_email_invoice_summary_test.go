package server

// server_email_invoice_summary_test.go — 需求 3 的「汇总金额」在 HTTP 汇总端点
// 上必须**按币种分组**。
//
// ## 这是同一条规则的第四处实现
//
// 「跨币种的算术和不是金额」在仓库里已有三处实现，且三处都配了用例：
//   1. `email.LedgerRows`（ledger.go）
//   2. `email.WriteInvoiceSummaryDocs`（pipeline.go）
//   3. `email.InvoiceListStats`（invoice_list.go，SQL 层聚合，最容易骗过人）
//
// 2026-10-01 修第三处时，审计范围只在 `internal/email`，**server 层这处手写
// 求和没被看到**：`handleEmailInvoiceSummary` 原本是裸 `total += inv.Amount`，
// 100.00 USD + 50.00 CNY 会得到 `amountTotal = 150`，币种信息无处可寻。
//
// ## 实测的影响面（不夸大）
//
// 前端**没有**任何地方读 `amountTotal`（`api/email.ts` 里只有类型声明）；
// 发票页合计区走的是客户端自己的 `summaryMoney(sumByCurrency(list))`。
// 所以这个错数**当前不显示**。但它是个已声明的 API 字段，任何人接上它就会
// 拿到错的账——所以按「闸门逻辑上一直关着、目前没造成损失」记录，而不是
// 「已造成错账」。
//
// ## 负控（实测过，见 handoff §7dk）
//
// 把 `len(amounts) == 1` 的条件去掉（无条件把 amounts[0] 当总额，或退回裸求和）
// → TestInvoiceSummary_MultiCurrencyHasNoCrossCurrencyTotal 转红。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

type summaryResponse struct {
	Count       int     `json:"count"`
	AmountTotal float64 `json:"amountTotal"`
	Currency    string  `json:"currency"`
	Amounts     []struct {
		Currency string  `json:"currency"`
		Amount   float64 `json:"amount"`
		Count    int     `json:"count"`
	} `json:"amounts"`
	Downloaded  int    `json:"downloaded"`
	Pending     int    `json:"pending"`
	Failed      int    `json:"failed"`
	ShareDocCSV string `json:"shareDocCsv"`
	ShareDocMD  string `json:"shareDocMd"`
}

func getSummary(t *testing.T, s *Server) (*httptest.ResponseRecorder, summaryResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/summary", nil)
	w := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(w, r)
	var resp summaryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// currencyInvoice 造一条指定币种的发票。currency 为空时走默认 CNY。
func currencyInvoice(t *testing.T, store *email.Store, name, currency string, amount float64) *email.Invoice {
	t.Helper()
	emailID := "sum-" + name
	seedAccountEmail(t, store, "acc-1", emailID, 1767225600)
	inv, err := store.UpsertInvoice(context.Background(), &email.Invoice{
		EmailID: emailID, AccountID: "acc-1", Kind: "e-invoice", Category: "交通",
		Seller: "某供应商", Amount: amount, Currency: currency,
		InvoiceNo: "SUM-" + name, Status: "downloaded",
	}, "local", "default")
	if err != nil {
		t.Fatalf("UpsertInvoice %s: %v", name, err)
	}
	return inv
}

func summaryServer(t *testing.T) (*Server, *email.Store) {
	t.Helper()
	store, cleanup := newInvoiceScopedStore(t)
	t.Cleanup(cleanup)
	return &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      t.TempDir(),
	}, store
}

// ★ 核心：混入两种币种时，响应里绝不能出现跨币种的总额。
func TestInvoiceSummary_MultiCurrencyHasNoCrossCurrencyTotal(t *testing.T) {
	s, store := summaryServer(t)
	currencyInvoice(t, store, "usd.pdf", "USD", 100)
	currencyInvoice(t, store, "cny.pdf", "CNY", 50)

	w, resp := getSummary(t, s)
	if w.Code != 200 {
		t.Fatalf("summary => %d %s", w.Code, w.Body.String())
	}
	if resp.Count != 2 {
		t.Fatalf("count = %d, want 2", resp.Count)
	}
	if len(resp.Amounts) != 2 {
		t.Fatalf("amounts = %+v, want 两组（按币种分组）", resp.Amounts)
	}
	byCur := map[string]float64{}
	for _, a := range resp.Amounts {
		byCur[a.Currency] = a.Amount
	}
	if byCur["USD"] != 100 || byCur["CNY"] != 50 {
		t.Fatalf("分组金额不对: %+v", resp.Amounts)
	}
	// 关键断言：多币种时 amountTotal 必须是 0，且 currency 为空 ——
	// 150 这种数根本不��在「金额」的语义里。
	if resp.AmountTotal != 0 {
		t.Errorf("amountTotal = %v，多币种时它必须是 0（150 不是金额）", resp.AmountTotal)
	}
	if resp.Currency != "" {
		t.Errorf("currency = %q，多币种时必须为空", resp.Currency)
	}
}

// 单币种时标量必须仍然可用（否则前端全部要改）。
func TestInvoiceSummary_SingleCurrencyKeepsScalar(t *testing.T) {
	s, store := summaryServer(t)
	currencyInvoice(t, store, "a.pdf", "CNY", 50)
	currencyInvoice(t, store, "b.pdf", "CNY", 25.5)

	w, resp := getSummary(t, s)
	if w.Code != 200 {
		t.Fatalf("summary => %d %s", w.Code, w.Body.String())
	}
	if resp.AmountTotal != 75.5 {
		t.Errorf("amountTotal = %v, want 75.5", resp.AmountTotal)
	}
	if resp.Currency != "CNY" {
		t.Errorf("currency = %q, want CNY", resp.Currency)
	}
	if len(resp.Amounts) != 1 || resp.Amounts[0].Amount != 75.5 || resp.Amounts[0].Count != 2 {
		t.Errorf("amounts = %+v, want 一组 75.5 / 2 张", resp.Amounts)
	}
}

// 币种为空按 CNY 归组（与 CurrencyTotal / 列表端点同一口径）。
func TestInvoiceSummary_EmptyCurrencyGroupsAsCNY(t *testing.T) {
	s, store := summaryServer(t)
	currencyInvoice(t, store, "nocur.pdf", "", 10)
	currencyInvoice(t, store, "cny.pdf", "CNY", 5)

	w, resp := getSummary(t, s)
	if w.Code != 200 {
		t.Fatalf("summary => %d %s", w.Code, w.Body.String())
	}
	if len(resp.Amounts) != 1 || resp.Amounts[0].Currency != "CNY" || resp.Amounts[0].Amount != 15 {
		t.Fatalf("amounts = %+v, want 一组 CNY 15", resp.Amounts)
	}
	if resp.AmountTotal != 15 {
		t.Errorf("amountTotal = %v, want 15", resp.AmountTotal)
	}
}

// 零张发票：不得 panic，金额为 0。
func TestInvoiceSummary_EmptyIsZeroNotNil(t *testing.T) {
	s, _ := summaryServer(t)
	w, resp := getSummary(t, s)
	if w.Code != 200 {
		t.Fatalf("summary => %d %s", w.Code, w.Body.String())
	}
	if resp.Count != 0 || resp.AmountTotal != 0 {
		t.Errorf("空清单 => count=%d amountTotal=%v, want 0/0", resp.Count, resp.AmountTotal)
	}
	if len(resp.Amounts) != 0 {
		t.Errorf("空清单不该有分组: %+v", resp.Amounts)
	}
}

// 汇总端点必须**真的**生成共享文档（需求 3 的兜底：发不出去就建共享文档）。
// 这条在当前部署尤其重要 —— 飞书没配，这条路就是实际生效的那条。
func TestInvoiceSummary_GeneratesShareableDocs(t *testing.T) {
	s, store := summaryServer(t)
	currencyInvoice(t, store, "a.pdf", "CNY", 50)
	currencyInvoice(t, store, "b.pdf", "CNY", 25.5)

	w, resp := getSummary(t, s)
	if w.Code != 200 {
		t.Fatalf("summary => %d %s", w.Code, w.Body.String())
	}
	if resp.ShareDocCSV == "" || resp.ShareDocMD == "" {
		t.Fatalf("shareDocCsv/Md 为空: %q / %q", resp.ShareDocCSV, resp.ShareDocMD)
	}
	if !strings.HasSuffix(resp.ShareDocCSV, ".csv") || !strings.HasSuffix(resp.ShareDocMD, ".md") {
		t.Errorf("文件名后缀不对: %q / %q", resp.ShareDocCSV, resp.ShareDocMD)
	}
	for _, name := range []string{resp.ShareDocCSV, resp.ShareDocMD} {
		p := filepath.Join(s.dataDir, "email-invoices", "exports", "default", name)
		if st, err := os.Stat(p); err != nil {
			t.Errorf("汇总文档不存在 %s: %v", name, err)
		} else if st.Size() == 0 {
			t.Errorf("汇总文档是空的: %s", name)
		}
	}
}
