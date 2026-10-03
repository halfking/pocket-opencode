package email

// invoice_sources_e2e_test.go — 需求 3 要求的**三种发票来源**的端到端验证。
//
// 目标原文：「原邮件中有 PDF 下载地址（可直接下载已有 PDF），也有 XML 数据格式
// （可解析后重新渲染）」。仓库里三条路径都有单元级测试，但单元测试各自只测
// 一个环节（MIME 解析 / URL 提取 / XML 渲染），**没有一条测试证明
// 「一封真实结构的邮件 → 落盘一个真 PDF」这条完整链路是通的**。
//
// 真实数据现状（2026-10-01，5 个真实账户）也只有 `pdf-url` 一种被真正跑通：
//
//	src=pdf-url  status=downloaded  amt=3500  ✅ 真实发票端到端
//	src=(空)      status=failed                  ← POP3 原文拿不到，handoff §7s 已证伪
//
// 也就是说 attachment 与 xml 两条路径至今**只在夹具/单测里存在过**。
// 这个文件用合成的真实结构 MIME 把两条路径端到端钉住：落盘文件必须是
// %PDF 开头、可被 pdfcpu 解析、且文件名符合
// {费用类型}-{对方单位}-{金额}-{日期}.pdf。
import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// e2eInvoiceFile 是一个最小可用的「真格式」发票 PDF（%PDF-1.4 + 页树 + Catalog）。
// 不能用退化的 69 字节占位件：pdfcpu 对无页树的 PDF 会 panic
// （见 ExportInvoiceGrid 注释与 TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes），
// 那样测出来的「成功」毫无意义。
const e2eInvoicePDF = `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 44>>stream
BT /F1 12 Tf 72 760 Td (INVOICE) Tj ET
endstream
endobj
trailer<</Root 1 0 R/Size 6>>
%%EOF`

// e2eInvoiceXML 是电子发票的 XML 数据格式。
//
// 标签名刻意用**真实发票的写法**（价税合计 / 销售方名称 / 发票号码），
// 而不是我自己发明的 <amount>/<seller>。第一版我用了英文短标签
// `<total><amount>`，ParseInvoiceXML 解析不出金额（Amount=0）——
// 查 labelMatch 才发现词典只认「价税合计 / totalamount / amounttotal」这类，
// 裸 `amount` 不在表里。**这是测试夹具不真实，不是解析器缺陷**：
// 真实数电票 XML 的金额标签就叫「价税合计」。
const e2eInvoiceXML = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice>
  <Header>
    <发票号码>24317200000907012698</发票号码>
    <开票日期>2026-09-28</开票日期>
  </Header>
  <Seller>
    <销售方名称>云服务开票中心</销售方名称>
    <纳税人识别号>91330100MA2XXXXXXX</纳税人识别号>
  </Seller>
  <Total>
    <价税合计>1280.00</价税合计>
    <税额>72.45</税额>
  </Total>
</Invoice>`

func buildE2EMIME(t *testing.T, subject, body string, attachments []e2eAttachment) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("From: billing@vendor.example.com\r\n")
	b.WriteString("To: user@example.com\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: Mon, 28 Sep 2026 10:00:00 +0800\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(attachments) == 0 {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(body + "\r\n")
		return []byte(b.String())
	}

	mixed := "----=_Part_e2e_invoice"
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + mixed + "\"\r\n\r\n")
	b.WriteString("--" + mixed + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(body + "\r\n")
	for _, a := range attachments {
		b.WriteString("--" + mixed + "\r\n")
		b.WriteString("Content-Type: " + a.contentType + "; name=\"" + a.name + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + a.name + "\"\r\n\r\n")
		b.WriteString(base64.StdEncoding.EncodeToString(a.data) + "\r\n")
	}
	b.WriteString("--" + mixed + "--\r\n")
	return []byte(b.String())
}

type e2eAttachment struct {
	name        string
	contentType string
	data        []byte
}

// TestInvoiceSource_PDFAttachment_EndToEnd 路径 1：PDF 附件直取。
//
// 走完整链路：MIME 解析 → 识别发票邮件 → ExtractInvoiceLoose 建档 →
// 采集附件 → 落盘 → 文件名合规 → 产物是可解析的真 PDF。
func TestInvoiceSource_PDFAttachment_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	raw := buildE2EMIME(t,
		"电子发票开具通知",
		"附件为增值税电子普通发票，请查收。价税合计 1280.00 元。",
		[]e2eAttachment{{name: "invoice.pdf", contentType: "application/pdf", data: []byte(e2eInvoicePDF)}})

	parsed, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("ParseMIMEMessage: %v", err)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("附件数 = %d, want 1", len(parsed.Attachments))
	}

	email := Email{
		ID: "em-e2e-attach", Subject: "电子发票开具通知", Snippet: parsed.TextBody,
	}
	if !HasInvoiceAttachment(parsed.Attachments) {
		t.Fatal("带 PDF 附件的发票邮件应被 HasInvoiceAttachment 识别")
	}
	inv, ok := ExtractInvoiceLoose(email, parsed.TextBody, true)
	if !ok {
		t.Fatal("带 PDF 附件的邮件应建档（ExtractInvoiceLoose 的破例分支）")
	}
	if inv == nil || inv.Amount == 0 {
		t.Fatalf("金额应从正文解析出来，得到 %+v", inv)
	}

	// 落盘：模拟采集器写文件的那一步。
	outDir := filepath.Join(dir, "invoices")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := InvoiceFileName(inv)
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, parsed.Attachments[0].Data, 0o600); err != nil {
		t.Fatal(err)
	}

	assertInvoiceFileContract(t, path, name, ".pdf")
	t.Logf("attachment 路径产出：%s", name)
}

// TestInvoiceSource_XMLAttachment_RendersPDF 路径 2：XML 数据格式重新渲染。
//
// 目标原文明确要求「也有 XML 数据格式（可解析后重新渲染）」。
// 这里验证 XML 附件 → 解析补全字段 → RenderInvoiceXMLPDF → 落盘真 PDF。
// 渲染依赖中文字体探测（handoff §3.1 修过 Windows/Android 候选表），
// 探不到时 XMLRenderer 返回 nil —— 那时**必须显式跳过**而不是假装通过。
func TestInvoiceSource_XMLAttachment_RendersPDF(t *testing.T) {
	dir := t.TempDir()
	raw := buildE2EMIME(t,
		"增值税电子普通发票",
		"发票数据见附件 XML。",
		[]e2eAttachment{{name: "invoice.xml", contentType: "application/xml", data: []byte(e2eInvoiceXML)}})

	parsed, err := ParseMIMEMessage(raw)
	if err != nil {
		t.Fatalf("ParseMIMEMessage: %v", err)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("附件数 = %d, want 1", len(parsed.Attachments))
	}
	if !strings.Contains(string(parsed.Attachments[0].Data), "价税合计") {
		t.Fatalf("XML 附件内容不对：%q", truncate(string(parsed.Attachments[0].Data), 60))
	}

	// 字段解析：这是 XML 路径真正的价值所在（金额/销售方/日期在 XML 里）。
	// 注意必须走**真实采集器的顺序**（invoice_harvest.go:355-366）：
	// ParseInvoiceXML → mergeXMLFields → 渲染。少了 mergeXMLFields 的话
	// 产出会退化成 `其他-未知单位-0.00-<下载日>.pdf` —— 金额与单位都丢，
	// 直接毁掉「汇总金额」和「对方单位」这两个目标要求。
	inv, ok := ExtractInvoiceLoose(Email{
		ID: "em-e2e-xml", Subject: "增值税电子普通发票", Snippet: parsed.TextBody,
	}, parsed.TextBody, true)
	if !ok || inv == nil {
		t.Fatal("带 XML 附件的邮件应建档")
	}
	fields := ParseInvoiceXML(parsed.Attachments[0].Data)
	if fields == nil {
		t.Fatalf("ParseInvoiceXML 应从 XML 里解析出字段，实际 nil（raw=%q）",
			truncate(string(parsed.Attachments[0].Data), 120))
	}
	mergeXMLFields(inv, fields)

	// XML 里的字段必须真的落到发票记录上 —— 这三项直接决定文件名能否对账。
	if inv.Amount != 1280.00 {
		t.Fatalf("金额未从 XML 解析：Amount=%v, want 1280.00（raw=%q）",
			inv.Amount, truncate(string(parsed.Attachments[0].Data), 160))
	}
	if inv.Seller != "云服务开票中心" {
		t.Fatalf("销售方未从 XML 解析：Seller=%q", inv.Seller)
	}
	if inv.InvoiceNo != "24317200000907012698" {
		t.Fatalf("发票号未从 XML 解析：InvoiceNo=%q", inv.InvoiceNo)
	}

	font := FindChineseFont(dir)
	if font == "" {
		// 环境缺中文字体 → XML 渲染按设计不可用。**如实跳过**，
		// 不能因为「没报错」就算通过 —— handoff §3.1 记的正是这个缺口。
		t.Skip("no Chinese font found on this machine; XML->PDF render unavailable by design")
	}
	pdfBytes, err := RenderInvoiceXMLPDF(font, inv, parsed.Attachments[0].Data)
	if err != nil {
		t.Fatalf("RenderInvoiceXMLPDF: %v", err)
	}
	if !strings.HasPrefix(string(pdfBytes), "%PDF") {
		t.Fatalf("渲染结果不是 PDF：%q", truncate(string(pdfBytes), 20))
	}

	outDir := filepath.Join(dir, "invoices")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := InvoiceFileName(inv)
	// 目标原文：「发票文件格式：{费用类型}-{对方单位}-{金额}-{日期}.pdf」。
	// XML 路径的价值就在这里 —— 金额和单位是从 XML 里解析出来的，
	// 文件名必须真的带上它们，否则汇总金额对不上账。
	if !strings.Contains(name, "1280.00") {
		t.Fatalf("文件名 %q 里没有 XML 解析出的金额 1280.00 —— 汇总金额会算错", name)
	}
	if !strings.Contains(name, "云服务开票中心") {
		t.Fatalf("文件名 %q 里没有 XML 解析出的销售方 —— 对方单位会认不出", name)
	}
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	assertInvoiceFileContract(t, path, name, ".pdf")
	t.Logf("XML 渲染路径产出：%s（字体 %s）", name, font)
}

// TestInvoiceSource_PDFURL_FromBodyEmail 路径 3：正文里的 PDF 下载地址。
//
// 这是三条路径里**唯一在真实邮箱上跑通过的**（2026-10-01：
// src=pdf-url / amt=3500 / downloaded）。这里用正文 URL 复核提取逻辑，
// 确保证该链接被识别、且不会被无关链接带偏。
func TestInvoiceSource_PDFURL_FromBodyEmail(t *testing.T) {
	body := "您的发票已开具，请点击链接下载 PDF：\n" +
		"https://oss.example-invoice.com/bill/2026/9/inv_3500.pdf\n" +
		"（如非本人操作请忽略本邮件，退订请回复 TD）"
	urls := extractInvoiceURLs(body)
	if len(urls) != 1 {
		t.Fatalf("应恰好提取出 1 个发票 URL，得到 %d：%v", len(urls), urls)
	}
	if !strings.Contains(urls[0], "inv_3500.pdf") {
		t.Fatalf("提取到的 URL 不对：%s", urls[0])
	}
	// 负控：非发票链接不应被算进来。
	noisy := extractInvoiceURLs("退订 https://example.com/unsubscribe 发票 https://a.com/inv.pdf")
	for _, u := range noisy {
		if strings.Contains(u, "unsubscribe") {
			t.Fatalf("退订链接被误当成发票链接：%v", noisy)
		}
	}
}

// assertInvoiceFileContract 断言落盘产物满足目标里的约定：
//   - 文件名 {费用类型}-{对方单位}-{金额}-{日期}.pdf
//   - 产物是能被 pdfcpu 解析的真 PDF（有页树，不是退化件）
func assertInvoiceFileContract(t *testing.T, path, name, ext string) {
	t.Helper()
	if !strings.HasSuffix(name, ext) {
		t.Fatalf("文件名 %q 应以 %s 结尾", name, ext)
	}
	// 命名规范：至少 4 段、以 .pdf 结尾、含金额数字。
	base := strings.TrimSuffix(name, ".pdf")
	parts := strings.Split(base, "-")
	if len(parts) < 4 {
		t.Fatalf("文件名 %q 不符合 {费用类型}-{对方单位}-{金额}-{日期}.pdf（只有 %d 段）", name, len(parts))
	}
	hasAmount := false
	for _, p := range parts {
		if strings.ContainsAny(p, "0123456789") && strings.Contains(p, ".") {
			hasAmount = true
		}
	}
	if !hasAmount {
		t.Fatalf("文件名 %q 里没有金额段", name)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("落盘文件为空")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "%PDF") {
		t.Fatalf("落盘文件不是 PDF：%q", truncate(string(data), 20))
	}
	// 必须能解析出页数：退化 PDF（只有 Catalog 无页树）会让 pdfcpu panic，
	// 只校验 %PDF 前缀会把那种坏件判成通过。
	n, err := api.PageCountFile(path)
	if err != nil {
		t.Fatalf("pdfcpu 无法解析产物（很可能是退化件）：%v", err)
	}
	if n < 1 {
		t.Fatalf("产物页数 = %d，至少要有 1 页", n)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ = context.Background
var _ = fmt.Sprintf
