package server

// server_email_invoice_wire_keys_test.go — 发票列表响应**线上字节**的字段名契约。
//
// ## 这个护栏防的是什么（2026-10-03 真机实测）
//
// Redmi 真机 /email/invoices 页面顶部合计金额显示 **¥NaN**，而同一屏的
// 「共 4 张」正常。逐层量出来的原因：
//
//	GET /api/emails/invoices?limit=30
//	  "amount":3500, "currency":"CNY",
//	  "amounts":[{"Currency":"CNY","Amount":3500,"Count":1}]   ← 键名首字母大写
//
// email.CurrencyTotal 当时没有 json tag，encoding/json 就按字段名原样输出；
// 前端 resolveSummaryGroups 读的是 a.currency / a.amount，两个都拿到
// undefined，round2(undefined) = NaN。
//
// ## 为什么已有护栏全绿
//
// - invoice_total_parity_test.go：钉住「三处实现的**数值**一致」，但三处都在
//   Go 内部比对，**从不序列化**。数值对得上与线上键名对得上是两件事。
// - invoice-totals-chain.test.mjs：12 条用例，夹具是**手写的 camelCase**——
//   它验证的是「如果服务端按 camelCase 发，前端会不会用」，而不是「服务端
//   发的到底是不是 camelCase」。夹具和被测对象来自同一个假设，所以这个假设
//   从未被检验。
//
// 结论：仓库里没有任何一条用例断言过**响应体里的字段名**。本文件补这一条，
// 而且刻意走真 handler + 真 store（隔离 schema）出字节，不用手写响应夹具——
// 手写夹具正是这次缺陷能藏身的地方。
//
// ## 负控（实测过）
//
// 把 CurrencyTotal 的 json tag 删掉 → 本文件两条用例同时转红
// （TestInvoiceListWire_SingleCurrency_... 与 ..._MultiCurrency_...），
// 且 invoice_total_parity_test.go 仍全绿——正好证明新护栏守的是另一条边界。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// seedCountingInvoice 造一张**计入合计**的发票：downloaded + 真实 file_path。
// 走 UpsertInvoice / UpdateInvoiceHarvest 两个生产写路径，不裸写 SQL——
// 将来采集列的写路径变了，本文件会跟着红，而不是继续绿。
func seedCountingInvoice(t *testing.T, store *email.Store, emailID, currency string, amount float64) {
	t.Helper()
	ctx := context.Background()
	seedAccountEmail(t, store, "acc-1", emailID, 1767225600)
	inv, err := store.UpsertInvoice(ctx, &email.Invoice{
		EmailID: emailID, AccountID: "acc-1", Kind: "e-invoice", Category: "其他",
		Seller: "wire-" + currency, Amount: amount, Currency: currency,
		InvoiceNo: "WIRE-" + emailID, Status: "downloaded", FileName: emailID + ".pdf",
	}, "local", "default")
	if err != nil {
		t.Fatalf("UpsertInvoice %s: %v", emailID, err)
	}
	if err := store.UpdateInvoiceHarvest(ctx, &email.Invoice{
		ID: inv.ID, FileName: emailID + ".pdf", FilePath: "email-invoices/" + emailID + ".pdf",
		FileSource: "attachment", Status: "downloaded",
	}); err != nil {
		t.Fatalf("UpdateInvoiceHarvest %s: %v", emailID, err)
	}
}

// getInvoiceList 打真 handler，返回状态码与**未解码**的响应体。
// 刻意返回原始字节：字段名这件事只有在原始字节上才看得见，
// 解到 struct 里就又变回「Go 类型自说自话」。
func getInvoiceList(t *testing.T, s *Server, query string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/emails/invoices"+query, nil)
	w := httptest.NewRecorder()
	s.handleEmailInvoices(w, r)
	return w.Code, w.Body.Bytes()
}

// amountsWire 是 amounts[] 元素在线上的形状。用 map 而不是 struct 解，
// 因为 struct 解码会把「键名不对」这件事直接吞掉：读 a.amount 读到零值，
// 看不出线上那个键叫 Amount。
type amountsWire map[string]any

// assertLowerCamelAmounts 断言 amounts[] 的每一个元素都恰好是
// {currency, amount, count} 三个小驼峰键，且 amount 是 JSON 数字。
func assertLowerCamelAmounts(t *testing.T, where string, amounts []amountsWire) {
	t.Helper()
	for i, a := range amounts {
		got := make([]string, 0, len(a))
		for k := range a {
			got = append(got, k)
		}
		sort.Strings(got)
		want := []string{"amount", "count", "currency"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s amounts[%d] 的键 = %v，want %v", where, i, got, want)
		}
		amt, ok := a["amount"].(float64)
		if !ok {
			t.Errorf("%s amounts[%d].amount 的类型 = %T，want JSON 数字（前端 round2 只吃数字；"+
				"读不到就是 undefined，页面显示 ¥NaN）", where, i, a["amount"])
			continue
		}
		if amt <= 0 {
			t.Errorf("%s amounts[%d].amount = %v，want > 0", where, i, amt)
		}
	}
}

type invoiceListWire struct {
	Total    float64        `json:"total"`
	Filed    float64        `json:"filed"`
	Amount   float64        `json:"amount"`
	Currency string         `json:"currency"`
	Amounts  []amountsWire  `json:"amounts"`
	HasMore  bool           `json:"hasMore"`
	Extra    map[string]any `json:"-"`
}

// 单一币种：标量 amount 与 amounts[0].amount 都要在，且键名小驼峰。
//
// 这一条正是真机上「共 4 张正常、合计金额 ¥NaN」的那一屏。
func TestInvoiceListWire_SingleCurrency_AmountsKeysAreLowerCamel(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedCountingInvoice(t, store, "wire-cny-1", "CNY", 3500)

	s := &Server{emailStore: store, dataDir: t.TempDir()}
	code, body := getInvoiceList(t, s, "?limit=30")
	if code != http.StatusOK {
		t.Fatalf("status = %d，want 200；body=%s", code, body)
	}

	var w invoiceListWire
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, body)
	}

	// 顶层五个字段：前端 invoiceTotalsFrom 只转发这五个，少一个就错账。
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("顶层解码失败: %v", err)
	}
	for _, k := range []string{"total", "filed", "amount", "currency", "amounts"} {
		if _, ok := top[k]; !ok {
			t.Errorf("响应缺顶层字段 %q（前端 invoiceTotalsFrom 读它）", k)
		}
	}
	if _, leaked := top["Amount"]; leaked {
		t.Errorf("响应顶层出现大写 Amount：%s", body)
	}

	if len(w.Amounts) != 1 {
		t.Fatalf("amounts 组数 = %d，want 1；body=%s", len(w.Amounts), body)
	}
	assertLowerCamelAmounts(t, "single", w.Amounts)
	if got := w.Amounts[0]["currency"]; got != "CNY" {
		t.Errorf("amounts[0].currency = %v，want CNY", got)
	}
	if got := w.Amounts[0]["amount"]; got != float64(3500) {
		t.Errorf("amounts[0].amount = %v，want 3500", got)
	}
	if w.Amount != 3500 || w.Currency != "CNY" {
		t.Errorf("标量 amount/currency = %v/%q，want 3500/CNY", w.Amount, w.Currency)
	}
}

// 汇总端点是**同一个类型的第二个序列化现场**，同样要钉。
//
// 现状（2026-10-03 查证）：前端 `emailApi.invoiceSummary()` 全仓**只有定义、
// 没有任何调用方**（`invoiceSummary` / `EmailInvoiceSummary` 只出现在 api/email.ts
// 与一条测试的注释里），所以这个现场的错键今天**不显示**——按「闸门逻辑上一直
// 关着、目前没造成损失」记录，而不是「已造成错账」。但它是个已声明的字段，
// 任何人接上它就会拿到 undefined。
//
// ## 为什么既有的汇总用例没抓到（这是本文件最该被记住的一段）
//
// `server_email_invoice_summary_test.go` 的 `summaryResponse.Amounts` 元素带
// `json:"currency"` 这类小驼峰 tag，却在修复前一直从 PascalCase 载荷里
// **读到真值并断言成功**（byCur["USD"] == 100、Amounts[0].Amount == 75.5……）。
//
// 原因是 Go 的 `json.Unmarshal` 对字段名做**大小写不敏感**匹配：
// `"Currency"` 能落进标了 `json:"currency"` 的字段。
//
// 于是「Go 侧用例全绿」根本不能证明线上键名对——它只证明了 Go 能读懂自己。
// 真正发作的消费者是大小写敏感的 TS（round24 真机上的 ¥NaN）。
// 这也是本文件全部断言都解到 map[string]any 的原因：解进 struct 会把这件事藏起来。
func TestInvoiceSummaryWire_AmountsKeysAreLowerCamel(t *testing.T) {
	s, store := summaryServer(t)
	currencyInvoice(t, store, "wire-cny-sum.pdf", "CNY", 3500)

	// 刻意不复用 getSummary：它把 body 解进 struct，而 struct 解码对键名
	// 大小写不敏感——用它断言就等于用「Go 能读懂自己」冒充「线上键名正确」。
	r := httptest.NewRequest(http.MethodGet, "/api/emails/invoices/summary", nil)
	w := httptest.NewRecorder()
	s.handleEmailInvoiceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d，want 200；body=%s", w.Code, w.Body.String())
	}

	// amounts 的元素解成 map（键名大小写可见），顶层标量解成 struct 无妨。
	var raw struct {
		AmountTotal float64       `json:"amountTotal"`
		Currency    string        `json:"currency"`
		Amounts     []amountsWire `json:"amounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, w.Body.String())
	}
	if len(raw.Amounts) != 1 {
		t.Fatalf("amounts 组数 = %d，want 1；body=%s", len(raw.Amounts), w.Body.String())
	}
	assertLowerCamelAmounts(t, "summary", raw.Amounts)
	if raw.Amounts[0]["currency"] != "CNY" || raw.Amounts[0]["amount"] != float64(3500) {
		t.Errorf("amounts[0] = %v，want CNY/3500", raw.Amounts[0])
	}
	if raw.AmountTotal != 3500 || raw.Currency != "CNY" {
		t.Errorf("标量 amountTotal/currency = %v/%q，want 3500/CNY", raw.AmountTotal, raw.Currency)
	}
}
//
// 为什么单币种那条不够：多币种时前端只走 amounts 分支，标量是 0。
// 「只把标量填对、amounts 仍然错键」这种实现能通过单币种用例里的一半断言，
// 而真机上恰好是这条路在显示 ¥NaN。
func TestInvoiceListWire_MultiCurrency_EveryGroupIsLowerCamel(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedCountingInvoice(t, store, "wire-cny-2", "CNY", 1280)
	seedCountingInvoice(t, store, "wire-usd-2", "USD", 3500)

	s := &Server{emailStore: store, dataDir: t.TempDir()}
	code, body := getInvoiceList(t, s, "?limit=30")
	if code != http.StatusOK {
		t.Fatalf("status = %d，want 200；body=%s", code, body)
	}

	var w invoiceListWire
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, body)
	}
	if len(w.Amounts) != 2 {
		t.Fatalf("amounts 组数 = %d，want 2（CNY + USD）；body=%s", len(w.Amounts), body)
	}
	assertLowerCamelAmounts(t, "multi", w.Amounts)

	// 跨币种的算术和不是金额：标量必须是 0 / 空，给前端一个「看起来正常」的
	// 标量会被直接渲染成 ¥4780 —— 那正是需求 3 要消灭的错账。
	if w.Amount != 0 {
		t.Errorf("多币种时标量 amount = %v，want 0（跨币种求和不是金额）", w.Amount)
	}
	got := map[string]float64{}
	for _, a := range w.Amounts {
		cur, _ := a["currency"].(string)
		amt, _ := a["amount"].(float64)
		got[cur] = amt
	}
	if got["CNY"] != 1280 || got["USD"] != 3500 {
		t.Errorf("按币种合计 = %v，want CNY=1280 USD=3500", got)
	}
}
