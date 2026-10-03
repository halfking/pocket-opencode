package email

// invoice_meta_persist_test.go — 费用类型与抬头必须真的落库。
//
// 这是 36c6774（补写 currency 列）的同一次断点，**同一类缺陷的另外两列**。
//
// mergeXMLFields（xmlinvoice.go）会从 XML 里补出两样东西：
//
//	inv.Title    = f.BuyerTitle        // 购买方抬头
//	inv.Category = f.Category          // 费用类型（非「其他」时覆盖）
//
// 但采集链路的唯一写回点 UpdateInvoiceHarvest（invoice_store.go:249）的
// UPDATE 列表里**没有 category 与 title**——invoiceSelectCols 读得到这两列，
// 写回却不写。于是：
//
//  1. 内存里的 inv.Category 是对的（单测只断言内存时完全发现不了），
//  2. 落库后 category 仍是插入时的旧值（多半是「其他」），
//  3. 下一轮读回来又变回「其他」，XML 辛苦解析出的费用类型凭空蒸发。
//
// 真实影响不止是列表显示：需求 3 规定文件名是
// `{费用类型}-{对方单位}-{金额}-{日期}.pdf`，而 InvoiceFileName 用的正是
// inv.Category。所以 category 没落库 = 规范文件名的第一段是错的。
//
// 与 currency 的区别（有意为之）：
//   - currency 以 XML 为权威值，冲突时覆盖；
//   - category 只在 XML 给出非「其他」值时覆盖（见 mergeXMLFields:196），
//     所以「非空才写」的 CASE WHEN 语义在这里仍然正确。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip）。
//
// 负控：把 UPDATE 里的 category/title 赋值删掉
//       -> TestUpdateInvoiceHarvest_PersistsCategoryAndTitle 转红。

import (
	"context"
	"testing"
)

// 一张带购买方抬头与可识别费用类型的数电票 XML（元素名对齐 labelMatch 认得的
// 真实数电票写法：项目名称 -> item -> classifyInvoiceCategory；
// 购买方名称 -> buyer -> BuyerTitle）。
//
// 注意这里踩过一次坑：最初写成 <Item> 与 <BuyerName>，ParseInvoiceXML 一律
// 忽略（labelMatch 认的是「项目名称/货物名称/品名」与「购买方名称/发票抬头」），
// 夹具前提断言直接报 fields.Category="" —— 正是那条断言挡住了这个假阳性。
// 费用类型含「滴滴」「出行」-> 「交通」（不是「差旅」，那不是本项目的类目取值）。
const xmlInvoiceWithCategory = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice>
  <销售方名称>滴滴出行科技有限公司</销售方名称>
  <购买方名称>杭州创客家族</购买方名称>
  <发票号码>26332000008261110741</发票号码>
  <开票日期>2026-09-15</开票日期>
  <项目名称>出行服务费</项目名称>
  <价税合计>35.00</价税合计>
  <币种>CNY</币种>
</Invoice>`

// XML 解析出的费用类型与抬头必须落库。
func TestUpdateInvoiceHarvest_PersistsCategoryAndTitle(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-meta-ct")

	inv := &Invoice{
		ID: "inv-meta-ct", Status: "downloaded", Attempts: 1,
		Category: "交通", Title: "杭州创客家族",
		InvoiceNo: "26332000008261110741", InvoiceDate: "2026-09-15",
		Seller: "滴滴出行科技有限公司", Amount: 35.00, Currency: "CNY",
	}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	var cat, title string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(category,''), COALESCE(title,'') FROM email_invoices WHERE id='inv-meta-ct'`).
		Scan(&cat, &title); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cat != "交通" {
		t.Fatalf("库中 category=%q, want 交通 —— 费用类型没落库，规范文件名第一段会错", cat)
	}
	if title != "杭州创客家族" {
		t.Fatalf("库中 title=%q, want 杭州创客家族 —— 抬头没落库", title)
	}
}

// 端到端：XML -> 解析 -> 合并 -> 落库 -> 读回 -> 生成规范文件名。
// 这条钉的是需求 3 真正的落点：文件名第一段必须来自 XML 解析出的费用类型。
func TestXMLCategorySurvivesFullHarvestRoundTrip(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-e2e-ct")

	fields := ParseInvoiceXML([]byte(xmlInvoiceWithCategory))
	if fields == nil {
		t.Fatal("ParseInvoiceXML 返回 nil")
	}
	if fields.Category != "交通" {
		t.Fatalf("夹具前提失败：XML 未解析出费用类型，得到 %q", fields.Category)
	}

	inv := &Invoice{
		ID: "inv-e2e-ct", Status: "downloaded", Attempts: 1,
		Subject: fields.Seller, Amount: fields.Amount, Seller: fields.Seller,
		InvoiceDate: fields.InvoiceDate, InvoiceNo: fields.InvoiceNo,
	}
	mergeXMLFields(inv, fields)
	if inv.Category != "交通" {
		t.Fatalf("合并后 Category=%q, want 交通", inv.Category)
	}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}

	// 用生产读路径读回来，而不是直接查库。
	got, err := store.GetInvoiceByIDScoped(ctx, "inv-e2e-ct", "u", "ws-cur")
	if err != nil {
		t.Fatalf("GetInvoiceByIDScoped: %v", err)
	}
	if got == nil {
		t.Fatal("读回的发票为 nil")
	}
	if got.Category != "交通" {
		t.Fatalf("读回 Category=%q, want 交通 —— 采集解析出的费用类型在写回时丢了", got.Category)
	}
	// 需求 3：文件名第一段就是费用类型。
	name := InvoiceFileName(got)
	if !hasPrefix(name, "交通-") {
		t.Fatalf("规范文件名第一段必须是费用类型「交通」，实际 %q", name)
	}
}

// 空值不得把库里已有的费用类型/抬头抹掉（与其它字段同口径的「非空才写」）。
func TestUpdateInvoiceHarvest_EmptyMetaKeepsExisting(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedInvoiceForCurrency(t, store, "inv-meta-keep")

	if _, err := store.pool.Exec(ctx,
		`UPDATE email_invoices SET category='交通', title='某公司' WHERE id='inv-meta-keep'`); err != nil {
		t.Fatalf("preset: %v", err)
	}
	// 采集器这轮没解析出费用类型/抬头
	inv := &Invoice{ID: "inv-meta-keep", Status: "downloaded", Attempts: 1, Amount: 10}
	if err := store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		t.Fatalf("UpdateInvoiceHarvest: %v", err)
	}
	var cat, title string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(category,''), COALESCE(title,'') FROM email_invoices WHERE id='inv-meta-keep'`).
		Scan(&cat, &title); err != nil {
		t.Fatalf("read: %v", err)
	}
	if cat != "交通" {
		t.Errorf("category=%q, want 交通（空值不应抹掉已有费用类型）", cat)
	}
	if title != "某公司" {
		t.Errorf("title=%q, want 某公司（空值不应抹掉已有抬头）", title)
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
