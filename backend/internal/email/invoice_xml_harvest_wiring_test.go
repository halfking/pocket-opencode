package email

// invoice_xml_harvest_wiring_test.go — 需求「XML 数据格式（可解析后重新渲染）」
// 走**采集器**的完整链路。
//
// 仓库里此前只有 TestInvoiceSource_XMLAttachment_RendersPDF，而它是手工拼装的：
//
//	pdfBytes, _ := RenderInvoiceXMLPDF(font, inv, parsed.Attachments[0].Data)
//	os.WriteFile(path, pdfBytes, 0o600)      ← 自己写盘，绕开采集器
//
// 它证明了解析器与渲染器各自可用，但**没有证明 harvestOne 真的会走这条路**：
// 既没产生 FileSource="xml-render"，也没验 mergeXMLFields 把 XML 里的金额/销售方
// 补进了发票记录。2026-10-02 已核实生产接线是有的
// （server_email_pipeline.go 的 ensurePipeline 注入了 XMLRenderer），
// 这个用例把那条接线钉住，免得以后有人删掉注入而测试照绿。
//
// 邮件 ID 必须带 em-pop3- 前缀：BodyCache 只在 isPOP3SourcedEmail 分支里被查
// （invoice_harvest.go:292-332），不走缓存就会掉进 Fetcher 分支。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarvestOne_XMLAttachmentRendersThroughHarvester(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-xml", "user-1", "ws-1")

	// 一封只带 XML 附件、没有 PDF、也没有下载链接的发票邮件。
	raw := buildE2EMIME(t,
		"电子发票开具通知",
		"发票为 XML 数据格式，请按附件渲染。",
		[]e2eAttachment{{name: "invoice.xml", contentType: "application/xml", data: []byte(e2eInvoiceXML)}})

	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-xml-acct-xml", AccountID: "acct-xml", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Snippet: "发票为 XML 数据格式", Date: 1750000000, UID: 7,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-xml-acct-xml")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}

	inv := &Invoice{
		ID: "inv-xml", EmailID: em.ID, AccountID: "acct-xml",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new",
		// 金额/销售方/日期一律留空：它们必须由 mergeXMLFields 从 XML 补出来。
		// 预填会让「XML 解析有没有生效」这件事测不出来。
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	font := FindChineseFont(t.TempDir())
	if font == "" {
		t.Skip("探不到中文字体，XML→PDF 渲染路径在本机不可用（生产同样会记 failed）")
	}
	dir := t.TempDir()
	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir,
		BodyCache:  &memBodyCache{data: map[string][]byte{em.ID: raw}},
		XMLRenderer: func(name string, i *Invoice, xmlRaw []byte) ([]byte, error) {
			return RenderInvoiceXMLPDF(font, i, xmlRaw)
		},
	}

	if got := h.harvestOne(ctx, inv); got != "downloaded" {
		t.Fatalf("harvestOne = %q, want \"downloaded\"（last_error=%q）", got, inv.LastError)
	}
	if inv.FileSource != "xml-render" {
		t.Fatalf("FileSource=%q, want \"xml-render\" —— 采集器没走 XML 渲染分支", inv.FileSource)
	}
	// mergeXMLFields 必须把 XML 里的字段补进来（原来全是空）。
	if inv.Amount == 0 {
		t.Error("Amount 仍为 0：mergeXMLFields 没从 XML 补出金额")
	}
	if inv.InvoiceNo == "" {
		t.Error("InvoiceNo 为空：mergeXMLFields 没从 XML 补出发票号码")
	}
	if inv.InvoiceDate == "" {
		t.Error("InvoiceDate 为空：mergeXMLFields 没从 XML 补出开票日期")
	}
	if inv.Seller == "" {
		t.Error("Seller 为空：mergeXMLFields 没从 XML 补出销售方名称")
	}
	// 落盘产物：文件名按 {费用类型}-{对方单位}-{金额}-{日期}.pdf，内容是真 PDF。
	if inv.FilePath == "" {
		t.Fatal("FilePath 为空")
	}
	full := filepath.Join(dir, inv.FilePath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("读落盘文件 %s: %v", full, err)
	}
	if !strings.HasPrefix(string(data), "%PDF") {
		t.Errorf("落盘产物不是 PDF：%q", truncate(string(data), 20))
	}
	if inv.FileName == "" || !strings.HasSuffix(inv.FileName, ".pdf") {
		t.Errorf("FileName=%q 不符合命名规范", inv.FileName)
	}
	// 金额补出来之后，文件名里应当带得上（InvoiceFileName 用补全后的字段生成）。
	if inv.Amount > 0 && !strings.Contains(inv.FileName, ".00") {
		t.Errorf("FileName=%q 未包含补全后的金额（inv.Amount=%v）", inv.FileName, inv.Amount)
	}
	t.Logf("XML 路径产出：%s（金额=%v 号码=%q 日期=%q 销售方=%q）",
		inv.FileName, inv.Amount, inv.InvoiceNo, inv.InvoiceDate, inv.Seller)

	// 落库：终态必须真的写进 DB。
	dbInv, err := store.GetInvoiceByEmailID(ctx, em.ID)
	if err != nil {
		t.Fatalf("读回发票: %v", err)
	}
	if dbInv.Status != "downloaded" || dbInv.FileSource != "xml-render" {
		t.Errorf("落库 status=%q fileSource=%q, want downloaded/xml-render", dbInv.Status, dbInv.FileSource)
	}
	if dbInv.Amount != inv.Amount {
		t.Errorf("落库金额=%v, want %v（mergeXMLFields 补出的金额没落库）", dbInv.Amount, inv.Amount)
	}
}

// XMLRenderer 为 nil（环境缺中文字体）时必须**明确失败**，不能静默成功。
// 生产里 ensurePipeline 依赖 FindChineseFont 探测，探不到就是 nil。
func TestHarvestOne_XMLRendererNilFailsExplicitly(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-xml-nil", "user-1", "ws-1")

	raw := buildE2EMIME(t, "电子发票开具通知", "XML 发票",
		[]e2eAttachment{{name: "invoice.xml", contentType: "application/xml", data: []byte(e2eInvoiceXML)}})
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-xmlnil-acct-xml-nil", AccountID: "acct-xml-nil", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Snippet: "XML", Date: 1750000000, UID: 7,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, _ := store.GetEmailByID(ctx, "em-pop3-xmlnil-acct-xml-nil")

	inv := &Invoice{ID: "inv-xml-nil", EmailID: em.ID, AccountID: "acct-xml-nil",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new"}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: t.TempDir(),
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
		// XMLRenderer 故意留 nil
	}
	got := h.harvestOne(ctx, inv)
	if got == "downloaded" {
		t.Fatal("XMLRenderer=nil 竟返回 downloaded：没渲染出 PDF 却报成功")
	}
	if inv.LastError == "" {
		t.Error("失败却没有 last_error，运维看不出是缺字体还是缺发票")
	}
}
