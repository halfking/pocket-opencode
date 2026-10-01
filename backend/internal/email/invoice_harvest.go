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
	// MaxInvoicesPerHarvestRound 单轮最多尝试采集多少张发票。
	//
	// 为什么要有：HarvestAll 一次列 100 张待采集，串行逐张拉原文。发票平台
	// 限流或服务商不回命令时，每张都可能耗到 go-imap 的 5 分钟 literal
	// 上限，100 张 = 理论 8 小时。留够一天正常量（实测单轮 3~5 张），其余
	// 顺延到下一轮；`status` 仍是 pending，不会丢。
	MaxInvoicesPerHarvestRound = 20
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
	// BodyCache 用于读取 POP3 降级路径落库的邮件原文（见 body_cache.go）。
	// nil 时 POP3 来源的发票仍会明确失败。
	BodyCache BodyCache
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
	if h == nil || h.Store == nil || h.Fetcher == nil || h.DataDir == "" {
		return HarvestResult{}
	}
	invoices, err := h.Store.ListHarvestableInvoices(ctx, 100)
	if err != nil {
		log.Printf("[email/invoice-harvest] list harvestable: %v", err)
		return HarvestResult{}
	}
	// 列 100 张但本轮只处理 MaxInvoicesPerHarvestRound 张：其余保持 pending
	// 顺延下一轮。在**列库时**就截断，避免把 100 张全塞进清单再让
	// HarvestInvoices 靠 i >= 预算 去跳（那样 Skipped 计数与日志都会失真）。
	if len(invoices) > MaxInvoicesPerHarvestRound {
		log.Printf("[email/invoice-harvest] %d harvestable, processing first %d this round",
			len(invoices), MaxInvoicesPerHarvestRound)
		invoices = invoices[:MaxInvoicesPerHarvestRound]
	}
	return h.HarvestInvoices(ctx, invoices)
}

// HarvestInvoices 对指定清单执行一轮下载/渲染。
//
// 与 HarvestAll 的区别是**不自己查库**：手动入口（POST /api/emails/invoices/harvest）
// 已经按 user/workspace 取好了清单，这里直接处理，避免又走一遍无 scope 的
// ListHarvestableInvoices（那会把别的 workspace 的待采集发票也拉进来重试）。
// 「重试耗尽转 failed」的清理仍然保留——手动重试同样要受重试上限约束。
func (h *InvoiceHarvester) HarvestInvoices(ctx context.Context, invoices []Invoice) HarvestResult {
	var res HarvestResult
	if h == nil || h.Store == nil || h.Fetcher == nil || h.DataDir == "" {
		return res
	}
	for i := range invoices {
		inv := invoices[i]
		if i >= MaxInvoicesPerHarvestRound {
			res.Skipped++
			continue
		}
		res.Processed++
		t0 := time.Now()
		status := h.harvestOne(ctx, &inv)
		cost := time.Since(t0).Round(time.Millisecond)
		// 逐张记耗时：第 4 步曾在真实邮箱上把整轮拖成无上界（单张卡 13 分钟），
		// 而此前这步**一条日志都没有**，只能靠猜。UID 要回查邮件才有，这里只打
		// 发票 ID + 账户，抓取失败时 LastError 里本来就会带上 uid。
		if cost > 30*time.Second {
			log.Printf("[email/invoice-harvest] SLOW inv=%s acct=%s took %s -> %s",
				inv.ID, inv.AccountID, cost, status)
		} else {
			log.Printf("[email/invoice-harvest] inv=%s acct=%s in %s -> %s",
				inv.ID, inv.AccountID, cost, status)
		}
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
	if len(invoices) > MaxInvoicesPerHarvestRound {
		log.Printf("[email/invoice-harvest] round budget %d reached, %d invoice(s) deferred to next run",
			MaxInvoicesPerHarvestRound, len(invoices)-MaxInvoicesPerHarvestRound)
	}
	// 空清单直接返回：没有处理任何东西时不该去扫全库的 pending（既无必要，
	// 也会让没有 pool 的调用方炸在 CleanupStalePendingInvoices 上）。
	if len(invoices) == 0 {
		return res
	}
	// 重试耗尽的记录转 failed（终态，人工介入）
	if stale, serr := h.Store.CleanupStalePendingInvoices(ctx, MaxInvoiceAttempts, time.Now().Unix()); serr == nil && stale > 0 {
		log.Printf("[email/invoice-harvest] %d pending invoices exhausted retries -> failed", stale)
	}
	return res
}

// isPOP3SourcedEmail 判断这封邮件是不是 POP3 降级路径落库的。
//
// 判据用 `em-pop3-` 这个 id 前缀：它在 fetcher.go 里和「UID 写成位置序号」
// 是同一条语句里赋的值，所以比 message_id 可靠（message_id 现在已改成优先取
// 真实 Message-ID 头，不再有 `pop3-` 前缀特征）。
func isPOP3SourcedEmail(e Email) bool {
	return strings.HasPrefix(e.ID, "em-pop3-")
}

// harvestOne 处理单条发票记录，返回最终状态。
// sameEmailMessage 判断重新取回的原文 raw 是否就是库里那条 em 记录。
//
// 位置序号（POP3）或 SEARCH 反查（IMAP）都可能因为服务器重排而指向**另一封**
// 邮件——把它当这封的发票解析、存成错误的 PDF，正是当初拒绝合成 IMAP UID
// 要防的事故。判据组合（任一足够强即认为是同一封）：
//   - 真实 Message-ID 完全相等（最强，但 POP3 落库时可能没取到真实头）；
//   - 主题相等 且 发件人相等 且 日期在同一天内（秒级相等过严：POP3 与库
//     落库时间可能差几秒，用「同一天」容忍）。
//
// 抽成纯函数是为了脱离 DB/网络直接测，负控见 invoice_harvest_test.go。
func sameEmailMessage(em *Email, raw []byte) bool {
	parsed, err := ParseMIMEMessage(raw)
	if err != nil {
		return false
	}
	// 真实 Message-ID：双方都有且不等 -> 强否定（就是另一封）。
	emMsgID := strings.Trim(strings.TrimSpace(em.MessageID), "<>")
	parsedMsgID := strings.Trim(strings.TrimSpace(parsed.MessageID), "<>")
	emHasReal := emMsgID != "" && !strings.HasPrefix(emMsgID, "pop3-")
	parsedHasReal := parsedMsgID != "" && !strings.HasPrefix(parsedMsgID, "pop3-")
	if emHasReal && parsedHasReal {
		if strings.EqualFold(parsedMsgID, emMsgID) {
			return true // 强确认（即使主题被改写）
		}
		return false // 强否定：真实 Message-ID 不等就是另一封，绝不放行
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.Subject), strings.TrimSpace(em.Subject)) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.From), strings.TrimSpace(em.FromAddress)) {
		return false
	}
	if em.Date > 0 && parsed.Date.Unix() > 0 {
		const day = 24 * time.Hour
		diff := time.Duration(em.Date - parsed.Date.Unix()) * time.Second
		if diff < 0 {
			diff = -diff
		}
		if diff > day {
			return false
		}
	}
	return true
}

// recoverPOP3SourcedRaw 为 POP3 来源的发票邮件自愈取回原文。
//
// 两条路径按可靠性排序，都做 sameEmailMessage 校验：
//  1. POP3 位置序号 RETR（邮件通常只在 POP3 侧，首选）；
//  2. IMAP SEARCH 反查真实 UID 后 FETCH（邮件已同步到 IMAP 时）。
//
// 两条都失败或校验不过时返回错误，由调用方记 failed——绝不返回「疑似」的
// 原文，那会把别人的邮件存成这封发票。
func (h *InvoiceHarvester) recoverPOP3SourcedRaw(ctx context.Context, inv *Invoice, em *Email) ([]byte, error) {
	if h.Fetcher == nil {
		return nil, fmt.Errorf("no fetcher configured for self-heal")
	}
	// 路径 1：POP3 位置序号补取。位置序号在 POP3 侧有效。
	if em.UID > 0 {
		raw, err := h.Fetcher.RefetchPOP3RawByIndex(ctx, em.AccountID, int(em.UID))
		if err == nil && len(raw) > 0 && sameEmailMessage(em, raw) {
			log.Printf("[email/invoice-harvest] self-heal invoice=%s via POP3 index=%d (same message confirmed)", inv.ID, em.UID)
			return raw, nil
		}
		if err != nil {
			log.Printf("[email/invoice-harvest] self-heal POP3 index=%d failed: %v", em.UID, err)
		} else {
			log.Printf("[email/invoice-harvest] self-heal POP3 index=%d returned a DIFFERENT message — discarded", em.UID)
		}
	}
	// 路径 2：IMAP SEARCH 反查真实 UID。邮件已同步到 IMAP 时才可能命中。
	realUID, rerr := h.Fetcher.ResolveRealUIDByHeader(ctx, em.AccountID, em.FromAddress, em.Subject, em.Date)
	if rerr != nil || realUID <= 0 {
		return nil, fmt.Errorf("POP3 index self-heal unavailable and IMAP real-UID resolve failed: %v", rerr)
	}
	raw, ferr := h.Fetcher.FetchMessageRaw(ctx, em.AccountID, realUID)
	if ferr != nil {
		return nil, fmt.Errorf("IMAP fetch by resolved uid=%d: %w", realUID, ferr)
	}
	if !sameEmailMessage(em, raw) {
		return nil, fmt.Errorf("IMAP resolved uid=%d returned a DIFFERENT message — discarded", realUID)
	}
	log.Printf("[email/invoice-harvest] self-heal invoice=%s via IMAP real uid=%d (same message confirmed)", inv.ID, realUID)
	return raw, nil
}

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
	// Attempts++ 必须在**取原文之前**：BodyCache 命中 / POP3 自愈 / IMAP FETCH
	// 三条路径都要计数。
	//
	// 原来它只写在 IMAP FETCH 那一个分支里（`} else { inv.Attempts++; ... }`），
	// 于是命中缓存或走 POP3 自愈的发票虽然也会到 markRetry，Attempts 却停在
	// 进入本轮时的值 —— 状态机不再前进，pending → failed 的收敛对它们永久
	// 失效。后果：这类发票每轮都占 MaxInvoicesPerHarvestRound=20 的预算，
	// 变成一张永远重试的僵尸发票，还把同轮的正常发票挤出去。
	inv.Attempts++
	var raw []byte
	if isPOP3SourcedEmail(*em) {
		// POP3 降级路径给的 UID 是**位置序号**（第几封），不是 IMAP UID。
		// 拿它去 `UID FETCH` 会取到**完全不相干的另一封邮件**——也就是可能把
		// 别人的邮件当成这封发票的原文解析、存成错误的发票 PDF。
		// 同样地，IMAP 那边 uid=134/135 恰好是大邮件时，那次 BODY[] literal
		// 读取会把整轮采集拖到分钟级（实测单张卡 13 分钟）。
		//
		// 所以 POP3 来源一律**不走 IMAP**：改读 POP3 同步时落下的原文缓存
		// （见 body_cache.go——那是唯一还能拿到原文的机会）。缓存没命中就
		// 明确失败，绝不退化成拿合成 UID 去 FETCH。
		if h.BodyCache == nil {
			inv.Status = "failed"
			inv.LastError = "POP3-sourced email and no raw body cache configured; refusing to IMAP-FETCH a positional index (would fetch the wrong message)"
			_ = h.Store.UpdateInvoiceHarvest(ctx, inv)
			return "failed"
		}
		cached, cerr := h.BodyCache.Get(em.ID, em.UID)
		if cerr != nil || len(cached) == 0 {
			// 缓存未命中**不等于**无路可走。两条安全的自愈路径，按可靠性排序：
			//
			//  1) POP3 位置序号补取（首选）。POP3 落库的 uid 就是 POP3 自己的位置
			//     序号，在 POP3 侧**有效**；邮件本来就只在 POP3 侧（实测 QQ 账户
			//     IMAP 侧 50 封里零封发票，两张真实 QQ Wallet 发票只在 POP3 的
			//     279 封里）。回 POP3 RETR 是拿回自己原文的正规途径。
			//  2) IMAP SEARCH 反查真实 UID（邮件已同步到 IMAP 时才可能命中）。
			//
			// 两条路拿到的原文都要经过 sameEmailMessage 校验——位置序号若因
			// 服务器重排漂移，或 SEARCH 多命中，会取到**另一封**邮件，把它当这封
			// 的发票存盘正是当初拒绝合成 IMAP UID 要防的事故。
			//
			// 真实死结（2026-10-01 实测）：QQ 上 POP3 原文缓存从未落盘（POP3
			// 只在 IMAP 失败时才跑，IMAP 修好后不再跑），守卫又拒绝合成 UID，
			// 两张真实 QQ Wallet 发票因此永远 failed。
			healed, herr := h.recoverPOP3SourcedRaw(ctx, inv, em)
			if herr != nil {
				inv.Status = "failed"
				inv.LastError = fmt.Sprintf("POP3-sourced email raw body cache miss (err=%v) and self-heal failed: %v; refusing to IMAP-FETCH a positional index (would fetch the wrong message)", cerr, herr)
				_ = h.Store.UpdateInvoiceHarvest(ctx, inv)
				return "failed"
			}
			// 自愈拿到的原文顺手回填缓存：同一封若因别的原因再被采集，不必再
			// 付一次连接成本。
			if h.BodyCache != nil {
				if _, perr := h.BodyCache.Put(em.ID, em.UID, healed); perr != nil {
					log.Printf("[email/invoice-harvest] backfill body cache invoice=%s email=%s: %v", inv.ID, em.ID, perr)
				}
			}
			raw = healed
		} else {
			raw = cached
		}
	} else {
		// Attempts++ 已在本函数开头统一做过，这里不再重复计数。
		var err error
		raw, err = h.Fetcher.FetchMessageRaw(ctx, inv.AccountID, em.UID)
		if err != nil {
			return h.markRetry(ctx, inv, fmt.Sprintf("fetch raw: %v", err))
		}
	}
	parsed, err := ParseMIMEMessage(raw)
	if err != nil {
		return h.markRetry(ctx, inv, fmt.Sprintf("parse mime: %v", err))
	}

	// 1) PDF / 图片附件（拍照发票常见 jpg/png）
	for _, att := range parsed.Attachments {
		if isPDFBytes(att.Data) || isImageBytes(att.Data) {
			return h.saveInvoiceFile(ctx, inv, att.Data, "attachment")
		}
	}

	// 2) 正文链接（HTML href 优先，纯文本 URL 兜底）
	//
	// linkErrs 收集每个链接的失败原因。downloadPDF 现在会把「下载到的不是
	// PDF」（登录页/错误页）也变成 error，所以这里能拿到真正的原因；
	// 原实现那条「既不是 PDF 也不是 err」的情况是静默的，last_error 最后只
	// 报成 `no usable pdf/xml found`，把「链接存在但拿回来不对」误说成
	// 「邮件里没有发票文件」。这正是需求「多次操作才能下载到」最需要看清的一步。
	var linkErrs []string
	for _, u := range extractInvoiceURLs(parsed.HTMLBody + "\n" + parsed.TextBody) {
		data, dlErr := h.downloadPDF(ctx, u)
		if dlErr == nil && (isPDFBytes(data) || isImageBytes(data)) {
			return h.saveInvoiceFile(ctx, inv, data, "pdf-url")
		}
		why := "下载内容不是 PDF/图片"
		if dlErr != nil {
			why = dlErr.Error()
		}
		log.Printf("[email/invoice-harvest] link download failed invoice=%s url=%s: %s", inv.ID, u, why)
		linkErrs = append(linkErrs, u+" -> "+why)
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
				return h.saveInvoiceFile(ctx, inv, pdfBytes, "xml-render")
			}
			log.Printf("[email/invoice-harvest] xml render failed invoice=%s: %v", inv.ID, rerr)
		}
	}

	// 链接存在但都拿不到发票时，把每个链接的失败原因写进 last_error，
	// 而不是笼统的「邮件里没有 pdf/xml」。
	if len(linkErrs) > 0 {
		return h.markRetry(ctx, inv, "发票链接未能取到 PDF 文件："+strings.Join(linkErrs, "; "))
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

// saveInvoiceFile 以规范文件名落盘并置 downloaded。图片保留原扩展名。
func (h *InvoiceHarvester) savePDF(ctx context.Context, inv *Invoice, data []byte, source string) string {
	return h.saveInvoiceFile(ctx, inv, data, source)
}

func (h *InvoiceHarvester) saveInvoiceFile(ctx context.Context, inv *Invoice, data []byte, source string) string {
	if inv.InvoiceDate == "" {
		inv.InvoiceDate = ParseInvoiceDateFromBytes(data)
	}
	_, ext := DetectInvoiceMedia(data)
	if ext == "" {
		ext = ".pdf"
	}
	name := InvoiceFileNameWithExt(inv, ext)
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
//
// 2026-10-02 起额外校验内容：发票链接常带登录态/时效限制，对未授权请求
// 会返回 **HTTP 200 + 一个 HTML 登录页**。原实现只判 `StatusCode != 200`，
// 把这段 HTML 原样交给调用方；而调用方的判据是
// `if dlErr == nil && (isPDFBytes(data) || isImageBytes(data))`，
// 条件不成立时**既不记错误也不记录**就静默落到下一个分支，最终 last_error
// 报成 `no usable pdf/xml found in message`——把「链接存在但拿回来不是发票」
// 误报成「邮件里没有发票文件」。
//
// 这正好砸在需求「有可能我们需要多次操作才能下载到发票文件」上：真正的原因
// （登录态过期 / 链接失效 / 返回错误页）被吞掉，人和后续排查都无从判断该不该重试。
// 所以这里把内容校验前移成显式错误，并带上 Content-Type 便于定位。
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes))
	if err != nil {
		return nil, err
	}
	// 内容校验：先用魔数认 PDF/图片（有些服务器 Content-Type 不准但内容确实
	// 是发票文件）；两边都不认就是「拿回来的不是发票文件」，必须报错而不是
	// 让调用方静默丢弃。
	if isPDFBytes(data) || isImageBytes(data) {
		return data, nil
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = "(未声明)"
	}
	return nil, fmt.Errorf("not-pdf: 下载内容不是 PDF/图片（content-type=%s, %d 字节）", ct, len(data))
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

// HasInvoiceAttachment 判断一封邮件里是否带着「可归档票据」附件：
// PDF、图片（拍照发票）或 XML（电子发票数据）。用于放宽规则层的建档门槛——
// 金额只印在附件里的账单邮件必须能进采集流程（见 ExtractInvoiceLoose）。
func HasInvoiceAttachment(atts []ParsedAttachment) bool {
	for _, att := range atts {
		if len(att.Data) == 0 {
			continue
		}
		if isPDFBytes(att.Data) || isImageBytes(att.Data) || isXMLFile(att) {
			return true
		}
	}
	return false
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
