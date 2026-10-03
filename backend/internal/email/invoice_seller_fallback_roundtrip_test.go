package email

// invoice_seller_fallback_roundtrip_test.go — 「XML 开票方能不能顶掉发件人兜底」
// 必须在**过一遍数据库**之后仍然成立。
//
// ## 背景：上一轮的验收为什么没抓到
//
// §7.7.3 给 `Invoice` 加了 `sellerIsFallback` 标记，让 XML 里的权威
// `SellerName` 能覆盖「发件人兜底」值。它在离线用例里是通过的——
// 但那些用例（`diag_toll_e2e_offline_test.go` / `diag_toll_a4_ledger_offline_test.go`）
// 把**内存里刚解析出来的 Invoice 直接交给 harvestOne**，全程没有数据库往返。
//
// 生产路径不是这样：`HarvestAll` 走 `ListHarvestableInvoices`，**每张发票
// 都是从库里读回来的**。而 `sellerIsFallback` 是非导出字段、不落库，
// 读回来恒为 false ⇒ `mergeXMLFields` 的
// `inv.Seller == "" || inv.sellerIsFallback` 恒为假分支 ⇒ XML 里的真实
// 开票方**永远顶不掉** FromName 兜底值。
//
// 2026-10-03 15:10 生产实测（手工补跑一轮，logs/zz-run-pipeline.mjs）：
//
//	seller = 通行费电子发票                        ← 发件人显示名
//	应为    = 浙江沪杭甬高速公路股份有限公司
//
// 需求原文 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 里的「对方单位」
// 直接是错的，交付财务时拿不到真实开票方。
//
// ## 这个用例与旧用例的差别（这才是重点）
//
// 旧用例：内存 → harvestOne
// 本用例：内存 → **UpsertInvoice 落库** → **ListInvoicesScoped 读回** → harvestOne
//
// 同一段业务代码，两条数据通路，结论相反。**只测前一条等于没测。**

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 真实数据形态（2026-10-03 只读核对 opencode_pocket.emails）：
// from_name 与 subject 都是「通行费电子发票」，from_address 是
// service@invoice.txffp.com。正文里没有「销售方：」，所以 seller 必然
// 落到 FromName 兜底分支。
func TestSellerFallbackSurvivesDatabaseRoundTrip(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	dir := t.TempDir()

	const userID, wsID = "user-admin", "ws_user-admin"
	seedAccount(t, store, "acct-rt", userID, wsID)

	// 邮件 ID 沿用 POP3 形态，好让 resolveRawBody 走 body cache
	// （与生产一致：这两封正是 POP3 降级路径来的）。
	const emailID = "em-pop3-acct-rt-ZL0014_NzbN7QSM14kuaWoAEvTJP10"
	if err := store.InsertEmail(ctx, Email{
		ID: emailID, AccountID: "acct-rt", WorkspaceID: wsID,
		MessageID: "<rt@vendor.example>", UID: 32,
		FromAddress: "service@invoice.txffp.com",
		FromName:    "通行费电子发票",
		Subject:     "通行费电子发票",
		Date:        1789364471,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, emailID)
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}

	// 1) 第一趟：解析信封。正文刻意不含「销售方：」，逼它走 FromName 兜底。
	invPtr, hit := ExtractInvoiceLoose(*em, "发票金额共计5.61元。", true)
	if !hit {
		t.Fatal("放宽建档 hit=false")
	}
	inv := *invPtr
	if !inv.sellerIsFallback {
		t.Fatalf("用例前提不成立：正文无「销售方：」时应打上兜底标记，实际 seller=%q", inv.Seller)
	}
	if inv.Seller != "通行费电子发票" {
		t.Fatalf("第一趟 seller = %q，want 通行费电子发票（FromName 兜底）", inv.Seller)
	}
	inv.ID = "inv-rt"
	inv.EmailID = emailID
	inv.AccountID = "acct-rt"
	inv.WorkspaceID = wsID
	inv.Status = "pending"
	if _, uerr := store.UpsertInvoice(ctx, &inv, userID, wsID); uerr != nil {
		t.Fatalf("upsert invoice: %v", uerr)
	}

	// 2) 第二趟：**从库里读回来**。这是与旧离线用例的唯一区别。
	got := readBackInvoice(t, store, userID, wsID, "inv-rt")
	if got.sellerIsFallback {
		t.Skip("这个非导出字段居然落库了 ⇒ 本用例前提不成立，判据要重新设计")
	}
	if got.Seller != "通行费电子发票" {
		t.Fatalf("读回来的 seller = %q，want 通行费电子发票（落库的就是这个值）", got.Seller)
	}

	// 3) 采集：XML 里的权威 SellerName 必须能顶掉它。
	rawMime := buildE2EMIME(t, "通行费电子发票", "发票金额共计5.61元。", []e2eAttachment{
		{name: "通行费电子发票.zip", contentType: "application/zip",
			data: euIZip(t, euiTollXML, tinyPDF(t))},
	})
	h := &InvoiceHarvester{
		Store: store, DataDir: dir,
		BodyCache: &stubBodyCache{raw: rawMime},
	}
	if status := h.harvestOne(ctx, got); status != "downloaded" {
		t.Fatalf("harvestOne = %q（last_error=%q）", status, got.LastError)
	}
	if got.Seller != "浙江沪杭甬高速公路股份有限公司" {
		t.Errorf("过库之后再采集，seller = %q，want 浙江沪杭甬高速公路股份有限公司 —— "+
			"XML 的权威开票方顶不掉发件人兜底值，「对方单位」交给财务就是错的", got.Seller)
	}
	if got.FileSource != "zip-pdf" {
		t.Errorf("FileSource = %q，want zip-pdf", got.FileSource)
	}
	// 文件名同样受影响：{费用类型}-{对方单位}-{金额}-{日期}-{发票号}
	if got.FileName == "" {
		t.Fatal("没有规范文件名")
	}
	if contains(got.FileName, "通行费电子发票") {
		t.Errorf("规范文件名 %q 里仍是发件人显示名，不是开票方", got.FileName)
	}
	if !contains(got.FileName, "浙江沪杭甬高速公路股份有限公司") {
		t.Errorf("规范文件名 %q 里没有开票方", got.FileName)
	}
	if st, serr := os.Stat(filepath.Join(dir, got.FilePath)); serr != nil {
		t.Fatalf("落盘文件读不到：%v", serr)
	} else if st.Size() < 512 {
		t.Errorf("落盘只有 %d 字节，夹具可能没构造成功", st.Size())
	}
}

// readBackInvoice 用 ListInvoicesScoped 把行读回来——**不用**任何按 ID 的
// 私有取法，因为生产走的正是这条；用别的取法会重蹈「夹具通路与生产不一致」。
func readBackInvoice(t *testing.T, store *Store, userID, wsID, id string) *Invoice {
	t.Helper()
	invs, err := store.ListInvoicesScoped(context.Background(), userID, wsID, "", 500)
	if err != nil {
		t.Fatalf("ListInvoicesScoped: %v", err)
	}
	for i := range invs {
		if invs[i].ID == id {
			return &invs[i]
		}
	}
	t.Fatalf("读回后找不到 %s（共 %d 行）", id, len(invs))
	return nil
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// 真证据不许被顶掉：正文「销售方：」解析出的值必须**原样保留**。
// 这条是 rederiveSellerFallback 刻意不比对 Subject 的原因。
func TestSellerFromBodyIsNotDowngradedToFallback(t *testing.T) {
	em := Email{
		FromName: "通行费电子发票",
		FromAddress: "service@invoice.txffp.com",
		Subject:     "通行费电子发票",
	}
	inv := &Invoice{Seller: "某某高速公路运营中心"}
	rederiveSellerFallback(inv, &em)
	if inv.sellerIsFallback {
		t.Error("正文解析出的单位名被打成了兜底 —— 它是真证据，XML 不该顶掉它")
	}
	if inv.Seller != "某某高速公路运营中心" {
		t.Errorf("seller 被改成了 %q", inv.Seller)
	}
}

// 与 FromName/FromAddress 相同的值**必须**被重新标成兜底。
func TestSellerEqualToSenderIsMarkedFallback(t *testing.T) {
	cases := []struct {
		name   string
		em     Email
		seller string
		want   bool
	}{
		{"same as FromName", Email{FromName: "通行费电子发票"}, "通行费电子发票", true},
		{"same as FromAddress", Email{FromAddress: "SERVICE@invoice.txffp.com"},
			"service@invoice.txffp.com", true},
		{"case-insensitive", Email{FromName: "Acme Corp"}, "acme corp", true},
		{"unrelated value", Email{FromName: "通行费电子发票"}, "某某高速公路运营中心", false},
		{"empty email", Email{}, "某某高速公路运营中心", false},
		{"empty seller", Email{FromName: "通行费电子发票"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := &Invoice{Seller: c.seller}
			em := c.em
			rederiveSellerFallback(inv, &em)
			if inv.sellerIsFallback != c.want {
				t.Errorf("seller=%q em={FromName:%q FromAddress:%q} 标记=%v, want %v",
					c.seller, c.em.FromName, c.em.FromAddress, inv.sellerIsFallback, c.want)
			}
		})
	}
}
