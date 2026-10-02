package email

// invoice_amount_precision_test.go — 发票金额带小数时，写回不能把它截断。
//
// 2026-10-02 定位到的真缺陷：UpdateInvoiceHarvest 里
// `CASE WHEN $11 > 0 THEN $11 ELSE amount END` 让 PG 从整数字面量 `0` 反推
// $11 的类型为 **int4**，pgx 传的 float64 25.5 被按整数解析，落库成 25.00。
// 后果是**每张带小数的发票在采集回写时小数部分被静默丢掉**，需求 3 的
//「汇总金额」直接少算，且没有任何地方会提示。
//
// 为什么这个护栏必须有：那两个 `::numeric` 长得像冗余的「防御性类型标注」，
// 任何人清理 SQL 冗余时都会顺手删掉，而删掉之后**整数金额的测试全绿**
// （3500、0、75 都测不出来），只有带小数的金额才会暴露。
import (
	"context"
	"testing"
)

func TestUpdateInvoiceHarvest_KeepsFractionalAmount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acc-prec", "user-prec", "ws-prec")
	seedEmail(t, store, "em-prec", "acc-prec", "ws-prec", "带小数的发票")

	inv, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-prec", AccountID: "acc-prec", WorkspaceID: "ws-prec",
		Kind: "e-invoice", Category: "餐饮", Seller: "某咖啡",
		// 25.5 是关键：整数金额即使被截断也看不出来。
		Amount: 25.5, Currency: "CNY", InvoiceNo: "PREC-1", Status: "downloaded",
	}, "user-prec", "ws-prec")
	if err != nil {
		t.Fatalf("UpsertInvoice: %v", err)
	}
	if inv.Amount != 25.5 {
		t.Fatalf("UpsertInvoice 返回 Amount=%v，want 25.5（INSERT 的位置参数是好的）", inv.Amount)
	}

	inv.FilePath = "email-invoices/ws-prec/cafe.pdf"
	inv.Status = "downloaded"
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	// 读库核对，不信任内存里的 inv（那还是 25.5，掩盖不了问题）。
	var got float64
	if err := store.pool.QueryRow(ctx,
		`SELECT amount FROM email_invoices WHERE id=$1`, inv.ID).Scan(&got); err != nil {
		t.Fatalf("read back amount: %v", err)
	}
	if got != 25.5 {
		t.Fatalf("落库 amount = %v, want 25.5 —— UpdateInvoiceHarvest 把小数截断了。"+
			"多半是 amount 上少了 ::numeric：PG 会从 `> 0` 里的整数字面量反推"+
			"参数类型为 int4，float64 25.5 于是被按整数解析成 25。"+
			"每张带小数的发票都会少算，需求 3 的汇总金额对不上账", got)
	}
}

// TestUpdateInvoiceHarvest_ZeroAmountKeepsExisting 对照组：amount=0 时
// CASE 走 ELSE 分支，保留库里的原值。这条同时钉住「加 ::numeric 之后
// 0 的语义没变」——如果哪天有人把判据从 `> 0` 改成 `>= 0`，
// 「金额为 0 的占位发票」就会把已下载的真实金额覆盖成 0。
func TestUpdateInvoiceHarvest_ZeroAmountKeepsExisting(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acc-zero", "user-zero", "ws-zero")
	seedEmail(t, store, "em-zero", "acc-zero", "ws-zero", "金额为 0 的发票")

	inv, err := store.UpsertInvoice(ctx, &Invoice{
		EmailID: "em-zero", AccountID: "acc-zero", WorkspaceID: "ws-zero",
		Kind: "e-invoice", Category: "其他", Seller: "某供应商",
		Amount: 3500, Currency: "CNY", InvoiceNo: "ZERO-1", Status: "new",
	}, "user-zero", "ws-zero")
	if err != nil {
		t.Fatalf("UpsertInvoice: %v", err)
	}

	// 解析器这一轮没能抽到金额（Amount=0），回写时不该把已入库的 3500 清掉。
	inv.Amount = 0
	inv.Status = "failed"
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	var got float64
	if err := store.pool.QueryRow(ctx,
		`SELECT amount FROM email_invoices WHERE id=$1`, inv.ID).Scan(&got); err != nil {
		t.Fatalf("read back amount: %v", err)
	}
	if got != 3500 {
		t.Fatalf("amount=0 的回写把原值 %v 覆盖成了 %v —— `> 0` 的语义变了", 3500.0, got)
	}
}
