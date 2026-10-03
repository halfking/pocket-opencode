package email

// invoice_attachment_harvest_wiring_test.go — 需求「PDF 下载地址…也有 XML 数据格式」
// 之外的第三类来源：**直接附件**（含拍照发票图片），且必须走过采集器。
//
// 仓库里对 "attachment" 这个 FileSource 的唯一断言在 invoice_retry_test.go，
// 而那个用例是直接调 saveInvoiceFile：
//
//	if got := h.saveInvoiceFile(ctx, inv, []byte(e2eInvoicePDF), "attachment"); ...
//
// 它绕开了 harvestOne 里真正做判定的那个分支（invoice_harvest.go:347-351：
// 遍历附件 → isPDFBytes || isImageBytes → saveInvoiceFile(..., "attachment")）。
// 也就是说「采集器会不会把附件发票认出来」这件事从来没被测过。
//
// 需求原文还写了「形成 pdf 文件或**其它相关文件**」，所以图片（jpg/png）分支
// 一并覆盖：saveInvoiceFile 用 DetectInvoiceMedia 决定扩展名，
// InvoiceFileNameWithExt 把 .pdf 后缀换成图片后缀。
//
// 邮件 ID 必须带 em-pop3- 前缀：BodyCache 只在 isPOP3SourcedEmail 分支里被查。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PDF 附件：必须走 attachment 分支，而不是掉到链接或 XML 分支去。
func TestHarvestOne_PDFAttachmentTakesAttachmentBranch(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	raw := buildE2EMIME(t, "电子发票开具通知", "发票 PDF 见附件。",
		[]e2eAttachment{{name: "invoice.pdf", contentType: "application/pdf", data: []byte(e2eInvoicePDF)}})

	dir := t.TempDir()
	seedAccount(t, store, "acct-att", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-att-acct-att", AccountID: "acct-att", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "电子发票开具通知",
		Snippet: "发票 PDF 见附件", Date: 1750000000, UID: 11,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-att-acct-att")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	inv := &Invoice{
		ID: "inv-att", EmailID: em.ID, AccountID: "acct-att",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new",
		Category: "其他", Seller: "云服务开票中心", Amount: 1280, InvoiceDate: "2026-09-28",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir,
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
	}
	if got := h.harvestOne(ctx, inv); got != "downloaded" {
		t.Fatalf("harvestOne = %q, want \"downloaded\"（last_error=%q）", got, inv.LastError)
	}
	if inv.FileSource != "attachment" {
		t.Fatalf("FileSource=%q, want \"attachment\" —— 采集器没把 PDF 附件识别成发票来源", inv.FileSource)
	}
	if !strings.HasSuffix(inv.FileName, ".pdf") {
		t.Errorf("FileName=%q 应保留 .pdf 扩展名", inv.FileName)
	}
	full := filepath.Join(dir, inv.FilePath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("读落盘文件: %v", err)
	}
	if !strings.HasPrefix(string(data), "%PDF") {
		t.Errorf("落盘产物不是 PDF：%q", data[:min(20, len(data))])
	}
	t.Logf("附件路径产出：%s（source=%s）", inv.FileName, inv.FileSource)
}

// 图片附件（拍照发票）：需求写的是「pdf 文件或**其它相关文件**」，
// 采集器用 isImageBytes 认这类附件，saveInvoiceFile 再按 DetectInvoiceMedia
// 换掉 .pdf 扩展名。两步都要对，否则会落出一个「名为 .pdf 实为 jpg」的文件。
func TestHarvestOne_ImageAttachmentKeepsImageExtension(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 最小合法 PNG：签名 + IHDR 头。只需过 isImageBytes 的魔数判定。
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 'I', 'D', 'A', 'T',
		0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82,
	}
	raw := buildE2EMIME(t, "发票拍照件", "发票图片见附件。",
		[]e2eAttachment{{name: "photo.png", contentType: "image/png", data: png}})

	dir := t.TempDir()
	seedAccount(t, store, "acct-img", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-img-acct-img", AccountID: "acct-img", WorkspaceID: "ws-1",
		FromAddress: "billing@vendor.example.com", Subject: "发票拍照件",
		Snippet: "发票图片见附件", Date: 1750000000, UID: 12,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-img-acct-img")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	inv := &Invoice{
		ID: "inv-img", EmailID: em.ID, AccountID: "acct-img",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new",
		Category: "其他", Seller: "某公司", Amount: 88, InvoiceDate: "2026-09-20",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir,
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
	}
	if got := h.harvestOne(ctx, inv); got != "downloaded" {
		t.Fatalf("harvestOne = %q, want \"downloaded\"（last_error=%q）—— 拍照发票没被认出来", got, inv.LastError)
	}
	if inv.FileSource != "attachment" {
		t.Fatalf("FileSource=%q, want \"attachment\"", inv.FileSource)
	}
	if !strings.HasSuffix(strings.ToLower(inv.FileName), ".png") {
		t.Errorf("FileName=%q 应保留 .png 扩展名（会落出名为 .pdf 实为 png 的错件）", inv.FileName)
	}
	if strings.HasSuffix(strings.ToLower(inv.FileName), ".pdf") {
		t.Errorf("FileName=%q 仍是 .pdf，但内容是图片", inv.FileName)
	}
	data, err := os.ReadFile(filepath.Join(dir, inv.FilePath))
	if err != nil {
		t.Fatalf("读落盘文件: %v", err)
	}
	if !isImageBytes(data) {
		t.Error("落盘内容不是图片，扩展名与内容对不上")
	}
	t.Logf("拍照发票产出：%s（source=%s）", inv.FileName, inv.FileSource)
}
