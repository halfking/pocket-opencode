package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// invoice_harvest.go — 发票文件采集流水线（对应需求「收取发票类邮件并解析
// 整理、下载发票文件」）。
//
// 每轮处理 email_invoices 里 status IN ('new','pending') 的记录：
//  1. 拉整封邮件原文（IMAP BODY[]），拆出附件与正文；
//  2. 优先级：PDF 附件 > 正文/HTML 里的 PDF 下载链接 > XML 附件（解析后
//     重渲染成 PDF）；
//  3. 落盘 dataDir/email-invoices/<workspace>/{费用类型}-{对方单位}-{金额}-{日期}.pdf；
//  4. 下载失败置 pending（下一轮流水线自动重试）——对应「有可能需要多次
//     操作才能下载到发票文件」；重试超限转 failed 终态。
//
// Harvest 由 Pipeline 驱动（scheduler 每日 + 手动 API），不在 IMAP 同步
// 的关键路径上。

const (
	// MaxInvoicePDFBytes 单个发票文件上限 20MB（发票 PDF 实际 <200KB）。
	MaxInvoicePDFBytes = 20 << 20
	// MaxInvoiceAttempts 下载重试上限。多数平台链路第 1-3 次内成功；
	// 超过 8 次仍失败基本是链接失效/权限问题，转 failed 由人工处理。
	MaxInvoiceAttempts = 8
)

// HarvestResult 汇总一轮采集。
type HarvestResult struct {
	Processed  int
	Downloaded int
	Pending    int
	Failed     int
	Skipped    int
}

// InvoiceHarvester 依赖注入式采集器。
type InvoiceHarvester struct {
	Store      *Store
	Fetcher    *Fetcher
	DataDir    string
	HTTPClient *http.Client
	// XMLRenderer 把 XML 发票数据渲染成 PDF 字节。nil = 环境缺中文字体等
	// 无法渲染，XML 路径记 failed。由 RenderInvoiceXMLPDF 提供（invoice_pdf.go）。
	XMLRenderer func(name string, inv *Invoice, xmlRaw []byte) ([]byte, error)
}

// invoiceLinkPatterns 常见电子发票平台下载域名特征（用于在正文链接里排序优先级）。
var invoiceLinkHints = []string{
	"pdf", "invoice", "fapiao", "fp", "etax", "inv", "download",
}

var (
	reHTMLHrefs = regexp.MustCompile(`(?i)href\s*=\s*["']([^"'h][^"']*(?:https?:)?[^"']*)["']|href\s*=\s*["'](https?://[^"']+)["']`)
	reBareURLs  = regexp.MustCompile(`https?://[^\s<>"'\)\]，。；]+`)
	reSkippable  = regexp.MustCompile(`(?i)(unsubscribe|\.png|\.jpg|\.jpeg|\.gif|\.css|\.js|\.ico|facebook|twitter|doubleclick|google-analytics|mailto:|tel:)`)
)

// HarvestAll 对所有待采集发票执行一轮下载/渲染。
func (h *InvoiceHarvester) HarvestAll(ctx context.Context) HarvestResult {
	var res HarvestResult
	if h == nil || h.Store == nil || h.Fetcher == nil || h.DataDir == "" {
		return res
	}
	invoices, err := h.Store.ListHarvestableInvoices(ctx, 100)
	if err != nil {
		log.Printf("[email/invoice-harvest] list harvestable: %v", err)
		return res
	}
	for i := range invoices {
		inv := invoices[i]
		res.Processed++
		status := h.harvestOne(ctx, &inv)
		switch status {
		case "downloaded":
			res.Downloaded++
		case "pending":
			res.Pending++
		case "failed":
			res.Failed++
		default:
			res.Skipped++
		}
	}
	// 重试耗尽的记录转 failed（终态，人工介入）
	if stale, serr := h.Store.CleanupStalePendingInvoices(ctx, MaxInvoiceAttempts, time.Now().Unix()); serr == nil && stale > 0 {
		log.Printf("[email/invoice-harvest] %d pending invoices exhausted retries -> failed", stale)
	}
	return res
}

// harvestOne 处理单条发票记录，返回最终状态。
func (h *InvoiceHarvester) harvestOne(ctx context.Context, inv *Invoice) string {
	em, err := h.Store.GetEmailByID(ctx, inv.EmailID)
	if err != nil || em == nil {
		inv.Status = "failed"
		inv.LastError = "source email missing"
		_ = h.Store.UpdateInvoiceHarvest(ctx, inv)
		return "failed"
	}
	if em.UID <= 0 {
		// 客户端推送的历史邮件没有 UID，拉不到原文：标 failed 说明原因
		inv.Status = "failed"
		inv.LastError = "no IMAP uid (pushed email)"
		_ = h.Store.UpdateInvoiceHarvest(ctx, inv)
		return "failed"
	}
	inv.Attempts++

	raw, err := h.Fetcher.FetchMessageRaw(ctx, inv.AccountID, em.UID)
	if err != nil {
		return h.markRetry(ctx, inv, fmt.Sprintf("fetch raw: %v", err))
	}
	parsed, err := ParseMIMEMessage(raw)
	if err != nil {
		return h.markRetry(ctx, inv, fmt.Sprintf("parse mime: %v", err))
	}

	// 1) PDF 附件
	for _, att := range parsed.Attachments {
		if isPDFBytes(att.Data) {
			return h.savePDF(ctx, inv, att.Data, "attachment")
		}
	}

	// 2) 正文链接（HTML href 优先，纯文本 URL 兜底）
	for _, u := range extractInvoiceURLs(parsed.HTMLBody + "\n" + parsed.TextBody) {
		data, dlErr := h.downloadPDF(ctx, u)
		if dlErr == nil && isPDFBytes(data) {
			return h.savePDF(ctx, inv, data, "pdf-url")
		}
		if dlErr != nil {
			log.Printf("[email/invoice-harvest] link download failed invoice=%s url=%s: %v", inv.ID, u, dlErr)
		}
	}

	// 3) XML 附件 → 解析补全字段 → 重渲染 PDF
	for _, att := range parsed.Attachments {
		if !isXMLFile(att) {
			continue
		}
		if fields := ParseInvoiceXML(att.Data); fields != nil {
			mergeXMLFields(inv, fields)
		}
		if h.XMLRenderer != nil {
			pdfBytes, rerr := h.XMLRenderer(inv.InvoiceNo, inv, att.Data)
			if rerr == nil && isPDFBytes(pdfBytes) {
				return h.savePDF(ctx, inv, pdfBytes, "xml-render")
			}
			log.Printf("[email/invoice-harvest] xml render failed invoice=%s: %v", inv.ID, rerr)
		}
	}

	return h.markRetry(ctx, inv, "no usable pdf/xml found in message")
}

// markRetry 下载未成功：pending 等下一轮；重试超限转 failed。
func (h *InvoiceHarvester) markRetry(ctx context.Context, inv *Invoice, msg string) string {
	if inv.Attempts >= MaxInvoiceAttempts {
		inv.Status = "failed"
		inv.LastError = msg
	} else {
		inv.Status = "pending"
		inv.LastError = msg
	}
	if err := h.Store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		log.Printf("[email/invoice-harvest] update invoice %s: %v", inv.ID, err)
	}
	if inv.Status == "failed" {
		return "failed"
	}
	return "pending"
}

// savePDF 以规范文件名落盘并置 downloaded。
func (h *InvoiceHarvester) savePDF(ctx context.Context, inv *Invoice, data []byte, source string) string {
	name := InvoiceFileName(inv)
	dir := filepath.Join(h.DataDir, "email-invoices", defaultWorkspace(inv.WorkspaceID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return h.markRetry(ctx, inv, "mkdir: "+err.Error())
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return h.markRetry(ctx, inv, "write file: "+err.Error())
	}
	if err := os.Rename(tmp, path); err != nil {
		return h.markRetry(ctx, inv, "rename file: "+err.Error())
	}
	inv.Status = "downloaded"
	inv.FileName = name
	inv.FilePath = filepath.Join("email-invoices", defaultWorkspace(inv.WorkspaceID), name)
	inv.FileSource = source
	inv.LastError = ""
	if err := h.Store.UpdateInvoiceHarvest(ctx, inv); err != nil {
		log.Printf("[email/invoice-harvest] update invoice %s: %v", inv.ID, err)
		return "failed"
	}
	log.Printf("[email/invoice-harvest] saved %s (invoice=%s source=%s attempts=%d)", name, inv.ID, source, inv.Attempts)
	return "downloaded"
}

// downloadPDF 从链接下载内容（带 UA、30s 超时、20MB 上限）。
func (h *InvoiceHarvester) downloadPDF(ctx context.Context, url string) ([]byte, error) {
	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126 Safari/537.36")
	req.Header.Set("Accept", "application/pdf,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes))
}

// isPDFBytes 检查 PDF magic（允许头部有少量空白/BOM 的服务器差异）。
func isPDFBytes(b []byte) bool {
	if len(b) < 5 {
		return false
	}
	if string(b[:4]) == "%PDF" {
		return true
	}
	// 有些服务器先发 BOM/空白；在前 1KB 内找 %PDF-
	if len(b) > 1024 {
		b = b[:1024]
	}
	return strings.Contains(string(b), "%PDF-")
}

func isXMLFile(att ParsedAttachment) bool {
	name := strings.ToLower(att.Filename)
	if strings.HasSuffix(name, ".xml") {
		return true
	}
	ct := strings.ToLower(att.ContentType)
	return strings.Contains(ct, "xml")
}

// extractInvoiceURLs 从 HTML/纯文本提取候选下载链接，按发票平台特征排序。
func extractInvoiceURLs(body string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimRight(strings.TrimSpace(u), ").,;")
		u = strings.ReplaceAll(u, "&amp;", "&")
		if u == "" || seen[u] || len(u) > 2000 {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return
		}
		if reSkippable.MatchString(u) {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	// href 先取；两个正则按组拆开处理
	for _, m := range reHTMLHrefs.FindAllStringSubmatch(body, -1) {
		for _, g := range m[1:] {
			if g != "" {
				add(g)
			}
		}
	}
	for _, m := range reBareURLs.FindAllString(body, -1) {
		add(m)
	}
	// 带发票特征的链接排前面
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && scoreInvoiceURL(out[j]) > scoreInvoiceURL(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func scoreInvoiceURL(u string) int {
	lu := strings.ToLower(u)
	score := 0
	for _, hint := range invoiceLinkHints {
		if strings.Contains(lu, hint) {
			score += 10
		}
	}
	if strings.HasSuffix(lu, ".pdf") {
		score += 20
	}
	return score
}

// InvoiceFileName 生成规范文件名 {费用类型}-{对方单位}-{金额}-{日期}.pdf。
// 非法字符（路径分隔符/空白/Windows 保留符）替换为连字符；字段缺省用
// "未知"。重名冲突由调用方（确定性命名 + 幂等 upsert）天然规避。
func InvoiceFileName(inv *Invoice) string {
	category := sanitizeFileName(inv.Category, "其他")
	seller := sanitizeFileName(inv.Seller, "未知单位")
	amount := strconv.FormatFloat(inv.Amount, 'f', 2, 64)
	date := strings.ReplaceAll(inv.InvoiceDate, "/", "-")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	return fmt.Sprintf("%s-%s-%s-%s.pdf", category, seller, amount, date)
}

func sanitizeFileName(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	// 路径分隔与文件系统保留字符
	repl := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-",
		"<", "-", ">", "-", "|", "-", "\n", "-", "\r", "-", "\t", "-",
	)
	s = repl.Replace(s)
	// 收敛空白为单个连字符
	s = strings.Join(strings.Fields(s), "-")
	if s == "" || s == "." || s == ".." {
		return fallback
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// InvoiceContentHash 供测试与幂等校验（同一文件重复下载内容一致性）。
func InvoiceContentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
