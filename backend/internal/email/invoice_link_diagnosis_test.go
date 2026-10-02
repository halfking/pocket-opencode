package email

// invoice_link_diagnosis_test.go —— 正文 PDF 链接「下载到非 PDF 内容」时的
// 可观测性缺口（2026-10-02）。
//
// 缺陷现场：发票链接常带登录态/时效限制，服务端对未授权请求会返回
// **HTTP 200 + 一个 HTML 登录页**。downloadPDF 只判 `StatusCode != 200`，
// 于是把这段 HTML 原样返回；调用方的判据是
// `if dlErr == nil && (isPDFBytes(data) || isImageBytes(data))`——
// 条件不成立时**既不记错误也不 continue 记录**，直接静默落到下一个分支。
//
// 后果正好落在需求「有可能我们需要多次操作才能下载到发票文件」上：
//   - 最后一封 XML 也没有时，last_error 记成
//     `no usable pdf/xml found in message`，把「链接存在但拿回来不是 PDF」
//     误报成「邮件里没有发票文件」；
//   - 真正的原因（登录态过期 / 链接失效 / 返回了错误页）被吞掉，
//     人和后续排查都无从判断该不该重试。
//
// 纯函数层面用 httptest 起一个返回 HTML 的服务器复现，不碰 IMAP/PG。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDownloadPDF_HTMLResponseIsNotSilentlyAccepted 钉住 downloadPDF 的契约：
// 返回 200 但内容不是 PDF/图片时必须报错，不能让调用方拿到一段 HTML 字节。
func TestDownloadPDF_HTMLResponseIsNotSilentlyAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>请登录后查看发票</body></html>"))
	}))
	defer srv.Close()

	h := &InvoiceHarvester{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	data, err := h.downloadPDF(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("downloadPDF returned ok for an HTML page (%d bytes, isPDF=%v isImage=%v); "+
			"调用方会静默丢弃它并误报成「邮件里没有发票文件」",
			len(data), isPDFBytes(data), isImageBytes(data))
	}
	if data != nil && isPDFBytes(data) {
		t.Fatal("contradiction: err != nil 但内容是 PDF")
	}
	if !strings.Contains(err.Error(), "text/html") && !strings.Contains(err.Error(), "not-pdf") {
		t.Logf("错误信息未点明「返回的是 HTML」：%v（建议带上 content-type 便于排查）", err)
	}
}

// TestDownloadPDF_Non200StillReports 对照：非 200 仍要报错。
func TestDownloadPDF_Non200StillReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	h := &InvoiceHarvester{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	if _, err := h.downloadPDF(context.Background(), srv.URL); err == nil {
		t.Fatal("403 must be reported as an error")
	}
}

// TestDownloadPDF_RealPDFStillPasses 回归：正常 PDF 不能被新判据误伤。
func TestDownloadPDF_RealPDFStillPasses(t *testing.T) {
	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n%%EOF\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	}))
	defer srv.Close()

	h := &InvoiceHarvester{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	data, err := h.downloadPDF(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("真实 PDF 被误判：%v", err)
	}
	if !isPDFBytes(data) {
		t.Fatal("returned bytes are not a PDF")
	}
}
