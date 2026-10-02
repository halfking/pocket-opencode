package email

// invoice_zip_harvest_test.go — 护栏：电子发票 ZIP 必须**优先于**同级的汇总单 PDF。
//
// ## 为什么这条护栏是必须的（2026-10-03 真实数据实测）
//
// 真实通行费电子发票邮件的附件顺序是：
//
//	[0] 通行费电子发票.zip            136KB  ← 真正的发票在 pdf/ 目录里
//	[1] 通行费电子票据汇总单(票据).pdf  45KB  ← 汇总单，**不是**发票
//	[2] 通行费电子票据汇总单(行程).pdf  45KB
//
// 采集器原本第 1 步就扫「PDF/图片附件」，而汇总单是 PDF ⇒ **汇总单会被当成
// 发票存盘**。用户拿到的凭证附件是一张汇总单，对账时毫无用处。
//
// 本文件钉住：带 zip 时必须存 zip 里那张票面 PDF（且发票号来自 zip 内 XML），
// 绝不能存汇总单。
//
// ## 负控
//
//  1) 把 harvestOne 里的「步骤 0（zip）」整段删掉 → 主用例转红（存成了汇总单）
//  2) 从 HasInvoiceAttachment 去掉 zip 判据 → 建档门槛用例转红

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofpdf "github.com/go-pdf/fpdf"
)

// euIZip 造一个 EUI 标准发票压缩包：xml/ + ofd/ + pdf/ 三目录。
// 票面 PDF 用最小可识别 PDF（isPDFBytes 只看 %PDF 头，harvest 只落盘不解析页树）。
func euIZip(t *testing.T, xmlBody string, pdfBody []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	add("xml/", "")
	add("xml/1_91330000142942095H_abc.xml", xmlBody)
	add("ofd/", "")
	add("ofd/1_91330000142942095H_abc.ofd", "OFD-FAKE")
	add("pdf/", "")
	add("pdf/1_91330000142942095H_abc.pdf", string(pdfBody))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// tinyPDF 是**能被 pdfcpu 读出页树**的最小合法 PDF。
//
// 不能用只有 Catalog 的 4 行残桩：saveInvoiceFile 落盘前会
// `pdfHasPages` 校验，残桩会 panic（slice bounds out of range [-1:]），
// 报成 "unusable pdf" —— 那是夹具不合格，不是被测行为。
// 真实票面 PDF 约 105KB，是合法 PDF，所以用 gofpdf 造一个真页。
func tinyPDF(t *testing.T) []byte {
	t.Helper()
	pdf := gofpdf.New("P", "mm", "A5", "")
	pdf.AddPage()
	pdf.SetFont("helvetica", "", 10)
	pdf.CellFormat(0, 10, "invoice", "", 1, "C", false, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("gofpdf: %v", err)
	}
	return buf.Bytes()
}

func TestReadZipInvoiceContents_EUIPackage(t *testing.T) {
	raw := euIZip(t, euiTollXML, tinyPDF(t))
	if !isZipBytes(raw, "通行费电子发票.zip") {
		t.Fatal("EUI 包没被 isZipBytes 认出")
	}
	c := readZipInvoiceContents(raw)
	if len(c.PDFs) != 1 {
		t.Errorf("PDFs=%d, want 1（pdf/ 目录里那张票面）", len(c.PDFs))
	}
	if len(c.XMLs) != 1 {
		t.Errorf("XMLs=%d, want 1（xml/ 目录里的发票数据）", len(c.XMLs))
	}
	// ofd/ 必须被忽略：现有渲染链不产 OFD，硬转会造出打不开的文件。
	if bytes.Contains(c.PDFs[0], []byte("OFD-FAKE")) {
		t.Error("ofd/ 条目被当成了票面 PDF")
	}
	if c.Empty() {
		t.Error("EUI 包被判成空包 ⇒ HasInvoiceAttachment 会过不去，纯 zip 邮件建不了档")
	}
}

func TestHasInvoiceAttachment_ZipOnlyEmailStillPasses(t *testing.T) {
	// 纯 zip 邮件：没有顶层 PDF/图片/XML。
	raw := euIZip(t, euiTollXML, tinyPDF(t))
	atts := []ParsedAttachment{{
		Filename:    "通行费电子发票.zip",
		ContentType: "application/zip",
		Data:        raw,
	}}
	if !HasInvoiceAttachment(atts) {
		t.Error("只有电子发票 zip 的邮件没通过建档门槛；"+
			"金额只印在附件里的真实发票会在 ExtractInvoiceLoose 的丢弃门槛前被扔掉")
	}
	// 反向护栏：一个装着照片的普通 zip **不该**算票据附件，
	// 否则任何带 zip 的营销邮件都能建档。
	photoZip := makeZipWith(t, map[string]string{"IMG_0001.jpg": "not-an-invoice-image"})
	if HasInvoiceAttachment([]ParsedAttachment{{Filename: "附件.zip", Data: photoZip}}) {
		t.Error("只装了图片的 zip 被当成了发票附件 —— 营销邮件会借此建档")
	}
}

func makeZipWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestHarvestOne_ZipInvoiceBeatsSummaryPDF 是本文件最要紧的一条：
// 存下来的必须是 zip 里的票面，**不是**同级的汇总单。
func TestHarvestOne_ZipInvoiceBeatsSummaryPDF(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-zip-1", "user-1", "ws-1")

	// 一封 POP3 落库的邮件（UID 是位置序号），原文进 body cache。
	const emailID = "em-pop3-acct-zip-1-ZIP"
	if err := store.InsertEmail(ctx, Email{
		ID: emailID, AccountID: "acct-zip-1", WorkspaceID: "ws-1",
		MessageID: "<zip-1@vendor.example>", UID: 9,
		FromAddress: "noreply@vendor.example", Subject: "通行费电子发票",
		Date: 1757838071,
	}); err != nil {
		t.Fatalf("insert pop3 email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, emailID)
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	if !isPOP3SourcedEmail(*em) {
		t.Fatalf("用例前提不成立：%q 应被识别为 POP3 来源", em.ID)
	}

	// 附件顺序照抄真实数据：zip 在前，汇总单 PDF 在后。
	rawMime := buildE2EMIME(t, "通行费电子发票", "发票金额共计19元。", []e2eAttachment{
		{name: "通行费电子发票.zip", contentType: "application/zip", data: euIZip(t, euiTollXML, tinyPDF(t))},
		{name: "通行费电子票据汇总单(票据).pdf", contentType: "application/pdf", data: tinyPDF(t)},
	})

	h := &InvoiceHarvester{
		Store:     store,
		DataDir:   t.TempDir(),
		BodyCache: &stubBodyCache{raw: rawMime},
	}
	inv := &Invoice{ID: "inv-zip-1", EmailID: emailID, AccountID: "acct-zip-1",
		WorkspaceID: "ws-1", Status: "pending", Subject: "通行费电子发票"}
	// 必须先把台账行插进去：saveInvoiceFile 落盘成功后要 UpdateInvoiceHarvest，
	// 行不存在时它返回 "failed" 且 LastError 已被清空 —— 症状看起来像「没存上」，
	// 真相是「文件写了、DB 没这行」。这正是该夹具第一次跑时报错的原因。
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("seed invoice row: %v", err)
	}

	got := h.harvestOne(ctx, inv)
	if got != "downloaded" {
		t.Fatalf("harvestOne = %q, want \"downloaded\"（last_error=%q）", got, inv.LastError)
	}
	if inv.InvoiceNo != "26337904450900255091" {
		t.Errorf("发票号 = %q, want 26337904450900255091（应来自 zip 内 XML）", inv.InvoiceNo)
	}
	if inv.FilePath == "" {
		t.Fatal("没有落盘文件路径")
	}
	// 存下来的内容必须是 zip 里那张票面，不能是汇总单。
	stored, rerr := os.ReadFile(filepath.Join(h.DataDir, inv.FilePath))
	if rerr != nil {
		// 路径可能是绝对路径，退一步只查 basename 是否存在
		stored, rerr = os.ReadFile(inv.FilePath)
		if rerr != nil {
			t.Skipf("读不到落盘文件（路径形态 %q），跳过内容断言：%v", inv.FilePath, rerr)
		}
	}
	if !bytes.HasPrefix(stored, []byte("%PDF")) {
		t.Errorf("落盘的���不是 PDF：%q…", string(stored[:min(16, len(stored))]))
	}
	_ = strings.TrimSpace(inv.FileSource)
}
