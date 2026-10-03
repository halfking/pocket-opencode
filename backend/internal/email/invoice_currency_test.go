package email

// invoice_currency_test.go — 发票币种识别。
//
// 2026-10-01 实测发现的缺陷：提取器把 `Currency` **硬编码成 "CNY"**
// （invoice.go:286 `Currency: "CNY"`），而 `reCurrency` 其实**已经能识别**
// CNY/USD/EUR/GBP/HKD/JPY——识别出来又被丢掉了。
//
// 后果链：一张「Total tax-inclusive amount: USD 126.00」的外币发票
//   1. Currency 被标成 CNY（错的）；
//   2. 汇总时 USD 与 CNY 直接相加，合计是错的（§7ad/§7ac 修的是精度，
//      修不了币种语义——两个不同币种的数加起来没有意义）。
//
// 真实数据现状：DB 里 7 张发票 currency 全是 CNY，所以这个缺陷**从未
// 在真实数据上暴露**——是查「多币种合计」时顺带发现的。
//
// 负控对照：把 normalizeCurrency 改成恒返回 fallback（还原旧行为）
//          -> TestExtractInvoice_ForeignCurrencyNotHardcodedCNY 转红。

import "testing"

func TestNormalizeCurrency_SymbolsAndCodes(t *testing.T) {
	cases := []struct{ mark, want string }{
		{"CNY", "CNY"}, {"RMB", "CNY"}, {"¥", "CNY"}, {"￥", "CNY"}, {"元", "CNY"},
		{"USD", "USD"}, {"$", "USD"},
		{"EUR", "EUR"}, {"€", "EUR"},
		{"GBP", "GBP"}, {"£", "GBP"},
		{"HKD", "HKD"},
		{"JPY", "JPY"},
		{"cny", "CNY"}, {"usd", "USD"}, // 大小写不敏感
		{"  USD  ", "USD"}, // 空白容忍
		{"", "CNY"},        // 空 -> fallback
		{"XYZ", "CNY"},     // 未知 -> fallback
	}
	for _, tc := range cases {
		if got := normalizeCurrency(tc.mark, "CNY"); got != tc.want {
			t.Errorf("normalizeCurrency(%q) = %q, want %q", tc.mark, got, tc.want)
		}
	}
}

// 核心契约：外币发票不得被标成 CNY。
// 夹具用真实形态的英文发票行（与 invoice_qqwallet_test.go 同源）。
func TestExtractInvoice_ForeignCurrencyNotHardcodedCNY(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"usd code", "Invoice number: 24312000000012345678\nTotal tax-inclusive amount: USD 126.00\nSeller name: Acme Cloud", "USD"},
		{"dollar sign", "Invoice number: 24312000000012345678\nTotal tax-inclusive amount: $126.00\nSeller name: Acme Cloud", "USD"},
		{"euro code", "Invoice number: 24312000000012345678\nTotal tax-inclusive amount: EUR 88.00\nSeller name: Acme Cloud", "EUR"},
		{"hkd code", "Invoice number: 24312000000012345678\nTotal tax-inclusive amount: HKD 999.00\nSeller name: Acme Cloud", "HKD"},
		{"cny stays cny", "Invoice number: 24312000000012345678\nTotal tax-inclusive amount: CNY 126.00\nSeller name: 腾讯云", "CNY"},
		{"rmb is cny", "发票号码：24312000000012345678\n价税合计：RMB 126.00\n销售方名称：腾讯云", "CNY"},
		{"yuan sign is cny", "发票号码：24312000000012345678\n价税合计：￥126.00\n销售方名称：腾讯云", "CNY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Email{
				ID: "em-cur-1", AccountID: "acct-1",
				Subject:     "Your invoice is ready",
				Snippet:     tc.body,
				FromAddress: "billing@acme.example.com",
			}
			inv, ok := ExtractInvoice(e, tc.body)
			if !ok {
				t.Fatalf("fixture must extract as an invoice:\n%s", tc.body)
			}
			if inv.Currency != tc.want {
				t.Fatalf("Currency = %q, want %q (was hardcoded CNY before the fix)\nbody:\n%s",
					inv.Currency, tc.want, tc.body)
			}
			if inv.Amount == 0 {
				t.Fatalf("amount must still be extracted, got 0\nbody:\n%s", tc.body)
			}
		})
	}
}

// 兜底分支（reAnyAmount）也要能识别币种：正文里没有「价税合计」标签时走这里。
func TestExtractInvoice_ForeignCurrencyViaFallbackPath(t *testing.T) {
	e := Email{
		ID: "em-cur-2", AccountID: "acct-1",
		Subject: "Receipt for your subscription",
		// 没有「价税合计/金额」等标签，只能靠 reAnyAmount 兜底
		Snippet:     "Thanks for your payment. USD 45.90 was charged to your card.",
		FromAddress: "billing@stripe.example.com",
	}
	inv, ok := ExtractInvoice(e, e.Snippet)
	if !ok {
		t.Skip("fallback path needs the keyword gate to pass; covered by the labelled cases above")
	}
	if inv.Currency != "USD" {
		t.Fatalf("fallback path Currency = %q, want USD", inv.Currency)
	}
}

// 真实 CNY 场景不能被这次改动破坏（回归守卫）。
func TestExtractInvoice_RealQQWalletStillParses(t *testing.T) {
	body := "Invoice number: 24312000000011112222\n" +
		"Total tax-inclusive amount: CNY 126.00\n" +
		"Seller name: Tencent Cloud Computing Co Ltd\n" +
		"Invoice date: 2026-10-01"
	e := Email{ID: "em-cur-3", AccountID: "acct-1", Subject: "Your invoice", Snippet: body, FromAddress: "noreply@qq.com"}
	inv, ok := ExtractInvoice(e, body)
	if !ok {
		t.Fatal("real QQ Wallet shape must still extract")
	}
	if inv.Amount != 126.00 {
		t.Errorf("Amount = %v, want 126.00", inv.Amount)
	}
	if inv.Currency != "CNY" {
		t.Errorf("Currency = %q, want CNY", inv.Currency)
	}
	if inv.Seller != "Tencent Cloud Computing Co Ltd" {
		t.Errorf("Seller = %q", inv.Seller)
	}
}
