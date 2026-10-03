package email

import (
	"testing"
)

// 汇总单口径的**端到端**核对（只读、无外部依赖、不连库）。
//
// ## 它补的是哪个洞
//
// 需求原文要「汇总金额…便于处理提交给财务人员处理」。此前核过的都是
// **台账侧**的数字（真库直查 SQL），而**财务实际拿到的是汇总单**
// （LedgerRows / CSV / 飞书表格）。台账对 ≠ 汇总单对 —— 中间隔着
// InvoiceCountsTowardTotal 与 round2 两道口径。
//
// 真实库当前 7 行（2026-10-04 核对）：6 个 downloaded 有 FilePath、
// 1 个 pending 无 FilePath，故合计应为 CNY 4038.01 / 6 张。
//
// ## 期望值是**独立字面量**，不是复用 SumByCurrency 的结果
//
// 若期望值由被测函数自己算出来，变异（改判据 / 改聚合）会同时改动
// 判据和期望值，测试照样全绿 —— 判据等于没被测。所以这里的 4038.01
// 是手算写死的。
func TestLedgerTotalsMatchIndependentExpectation(t *testing.T) {
	// 真实库 7 行的镜像（字段值取自生产库，金额/状态/路径见 handoff 第十八节）
	invs := []Invoice{
		{Amount: 3500.00, Currency: "CNY", Status: "downloaded", FilePath: "a.pdf"},
		{Amount: 58000.00, Currency: "CNY", Status: "pending", FilePath: ""},
		{Amount: 328.50, Currency: "CNY", Status: "downloaded", FilePath: "b.pdf"},
		{Amount: 126.00, Currency: "CNY", Status: "downloaded", FilePath: "c.pdf"},
		{Amount: 58.90, Currency: "CNY", Status: "downloaded", FilePath: "d.pdf"},
		{Amount: 19.00, Currency: "CNY", Status: "downloaded", FilePath: "e.pdf"},
		{Amount: 5.61, Currency: "CNY", Status: "downloaded", FilePath: "f.pdf"},
	}

	// 手算期望：6 张计入（3500+328.50+126+58.90+19+5.61），pending 那张不计
	const wantAmount = 4038.01
	const wantCount = 6

	rows, totals := LedgerRows(invs)
	if len(totals) != 1 {
		t.Fatalf("合计行数 = %d，want 1（真实数据全 CNY）", len(totals))
	}
	got := totals[0]
	if got.Currency != "CNY" {
		t.Errorf("币种 = %q，want CNY", got.Currency)
	}
	if got.Amount != wantAmount {
		t.Errorf("合计 = %.2f，want %.2f", got.Amount, wantAmount)
	}
	if got.Count != wantCount {
		t.Errorf("张数 = %d，want %d", got.Count, wantCount)
	}
	t.Logf("汇总单合计：%s %.2f / %d 张", got.Currency, got.Amount, got.Count)

	// 行数 = 表头 + 7 张明细 + 1 行合计
	if want := 1 + len(invs) + 1; len(rows) != want {
		t.Errorf("总行数 = %d，want %d（表头1+明细%d+合计1）", len(rows), want, len(invs))
	}

	// SumByCurrency 的契约是「把给它的都加起来」，**筛选是调用方职责**。
	// 生产链路（server_email_pipeline.go:667）传的是已按
	// InvoiceCountsTowardTotal 预筛过的 counted，所以口径与 LedgerRows 一致。
	// 直接把未筛的全量传进去，它当然会把 pending 的 58000 也算上 ——
	// 那不是缺陷，是契约。既有负控见 diag_toll_a4_ledger_offline_test.go。
	var canonical []Invoice
	for _, iv := range invs {
		if InvoiceCountsTowardTotal(iv) {
			canonical = append(canonical, iv)
		}
	}
	st := SumByCurrency(canonical)
	if len(st) != 1 || st[0].Amount != got.Amount || st[0].Count != got.Count {
		t.Errorf("SumByCurrency(已按判据预筛) = %+v，LedgerRows 合计 = %+v，两者应一致", st, got)
	}

	// pending 行**必须仍出现在明细里** —— 不计入合计 ≠ 从列表消失，
	// 用户要能看到「有几张没拿到」，这是合计能用来对账的前提。
	found := false
	for _, r := range rows {
		for _, c := range r {
			if s, ok := c.(string); ok && s == "pending" {
				found = true
			}
		}
	}
	if !found {
		t.Error("pending 行没出现在明细里 —— 不计入合计不等于从列表消失")
	}

	// 「核验」列口径必须与合计判据严格一致：pending 那行必须是「未核验」。
	// 不一致会出现「标着已核验却不计入合计」的行。
	labelRow := -1
	for i, r := range rows {
		if i == 0 {
			continue
		}
		if s, ok := r[6].(string); ok && s == "pending" {
			labelRow = i
		}
	}
	if labelRow < 0 {
		t.Fatal("找不到 pending 明细行")
	}
	if got := rows[labelRow][7]; got != "未核验" {
		t.Errorf("pending 行的核验列 = %v，want 未核验", got)
	}
}
