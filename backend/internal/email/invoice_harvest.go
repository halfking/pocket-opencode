package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"unicode/utf8"
)

// invoice_harvest.go — 发票文件采集流水线（对应需求「收取发票类邮件并解析
// 整理、下载发票文件」）。
//
// 每轮处理 email_invoices 里 status IN ('new','pending') 的记录：
//  1. 拉整封邮件原文（IMAP BODY[]），拆出附件与正文；
//  2. 优先级：PDF 附件 > 正文/HTML 里的 PDF 下载链接 > XML 附件（解析后
//     重渲染成 PDF）；
//  3. 落盘 dataDir/email-invoices/<workspace>/{费用类型}-{对方单位}-{金额}-{日期}[-{发票号}].pdf
//     （发票号段与限长见 InvoiceFileName；补发票号是为了同额同日的票不互相覆盖）；
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
	// reHTMLSrcs 匹配内联资源属性（img/src、background、poster 等）里的 URL。
	//
	// 它**不参与**候选收集，只用来把「这个 URL 是图片不是下载链接」这件事
	// 记下来——见 extractInvoiceURLs 里 inline 那段注释。
	//
	// 属性名用 `\b(?:src|background|poster)\b` 而不是只写 `src`（2026-10-04 修正）：
	// 原实现的注释声称覆盖 background，正则却只匹配 `src=`，于是
	// `<div background="https://cdn.x.com/mail/banner?w=750&h=200">` 里的横幅
	// 仍会被 reBareURLs 捞进候选——**注释与代码不一致，读者无从察觉**，
	// 而那正是本函数要堵的同一个营销横幅泄漏。
	// `\b` 开头让 `data-src=` 也一并覆盖（`-` 之后是词边界）。
	reHTMLSrcs  = regexp.MustCompile(`(?i)\b(?:src|background|poster)\s*=\s*["']([^"']+)["']`)
	reBareURLs  = regexp.MustCompile(`https?://[^\s<>"'\)\]，。；]+`)
	reSkippable = regexp.MustCompile(`(?i)(unsubscribe|\.png|\.jpg|\.jpeg|\.gif|\.css|\.js|\.ico|facebook|twitter|doubleclick|google-analytics|mailto:|tel:)`)
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
	return IsPOP3SourcedEmailID(e.ID)
}

// IsPOP3SourcedEmailID 是上面那条判据的导出形式，判据只此一处。
//
// 为什么要导出：server 包的 GET /api/emails/{id}/body 也需要同一判据来拦住
// 「拿 POP3 位置序号去 UID FETCH」—— 2026-10-03 真机实测那条路径会对 POP3
// 邮件报 502，而在 IMAP 可用时它会**静默返回另一封邮件的正文**。让 server
// 自己再写一遍 `strings.HasPrefix(id, "em-pop3-")` 就是两份判据，改一处漏
// 一处的后果是错邮件正文被当成对的显示出来。
func IsPOP3SourcedEmailID(id string) bool {
	return strings.HasPrefix(id, "em-pop3-")
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
		diff := time.Duration(em.Date-parsed.Date.Unix()) * time.Second
		if diff < 0 {
			diff = -diff
		}
		if diff > day {
			return false
		}
	}
	return true
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
	// 重新判定 seller 是不是发件人兜底。
	//
	// 必须在**任何** mergeXMLFields 之前做：inv 是从库里读回来的，
	// `sellerIsFallback`（非导出字段、不落库）恒为 false，XML 里的权威
	// SellerName 因此顶不掉 FromName 兜底值。2026-10-03 15:10 生产实测：
	// 两封通行费发票的「对方单位」被写成发件人显示名「通行费电子发票」，
	// 而不是真实开票方。详见 rederiveSellerFallback 的注释。
	rederiveSellerFallback(inv, em)
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
	var rawSrc rawBodySource
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
		//
		// 取原文的**完整**顺序（缓存 → POP3 位置序号 RETR → IMAP SEARCH 反查）
		// 已经收敛到 raw_body_resolve.go 的 resolveRawBody，pipeline 的第 2 趟
		// 也调它。两处各写一份必然漂移——2026-10-03 实测正是这样：采集器
		// 早就支持 POP3，pipeline 一直没跟上，于是 POP3 发票永远建不了档
		// （handoff §7.4.4）。
		//
		// 缓存未命中**不等于**无路可走，后面两条自愈路径都在 resolveRawBody 里，
		// 拿到的原文都过 sameEmailMessage 校验——位置序号若因服务器重排漂移，
		// 或 SEARCH 多命中，会取到**另一封**邮件，把它当这封的发票存盘正是
		// 当初拒绝合成 IMAP UID 要防的事故。
		var rerr error
		raw, rawSrc, rerr = resolveRawBody(ctx, h.Fetcher, h.BodyCache, em,
			fmt.Sprintf(" invoice=%s", inv.ID))
		if rerr != nil {
			// POP3 来源失败是**终态**：没有可重试的路径（重试只会再失败一次，
			// 还会占掉 MaxInvoicesPerHarvestRound 的预算）。IMAP 来源失败才重试。
			if isPOP3SourcedEmail(*em) {
				inv.Status = "failed"
				inv.LastError = rerr.Error()
				_ = h.Store.UpdateInvoiceHarvest(ctx, inv)
				return "failed"
			}
			return h.markRetry(ctx, inv, fmt.Sprintf("fetch raw: %v", rerr))
		}
		// 自愈拿到的原文顺手回填缓存：同一封若因别的原因再被采集，不必再
		// 付一次连接成本。
		if rawSrc == rawBodyPOP3Index || rawSrc == rawBodyIMAPRealUID {
			if h.BodyCache != nil {
				if _, perr := h.BodyCache.Put(em.ID, em.UID, raw); perr != nil {
					log.Printf("[email/invoice-harvest] backfill body cache invoice=%s email=%s: %v", inv.ID, em.ID, perr)
				}
			}
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

	// 0) 电子发票 ZIP 压缩包（EUI/数电票标准形态）——**必须排在步骤 1 之前**。
	//
	// 真实通行费邮件的附件是「通行费电子发票.zip」+ 两份**汇总单 PDF**。
	// 步骤 1 认不出 zip，会先命中汇总单，把**汇总单当发票存盘**——而汇总单
	// 不是发票凭证。宁可多解一层压缩包，也不能交出汇总单。
	// 见 invoice_zip.go。
	if zc := zipAttachmentContents(parsed.Attachments); !zc.Empty() {
		// zip 内的 XML 发票数据能补全发票号/金额/日期/销售方。
		// 先合并字段再存文件，规范文件名才不会退化、发票号才不会空。
		for _, x := range zc.XMLs {
			if fields := ParseInvoiceXML(x); fields != nil {
				mergeXMLFields(inv, fields)
			}
		}
		for _, pdf := range zc.PDFs {
			if !isPDFBytes(pdf) {
				continue
			}
			return h.saveInvoiceFile(ctx, inv, pdf, "zip-pdf")
		}
		// zip 里只有 XML（没有票面 PDF）时，走「解析后重新渲染」——
		// 这正是需求原文那条路径，在这之前从未在真实数据上跑过。
		if h.XMLRenderer != nil {
			for _, x := range zc.XMLs {
				pdfBytes, rerr := h.XMLRenderer(inv.InvoiceNo, inv, x)
				if rerr == nil && isPDFBytes(pdfBytes) {
					return h.saveInvoiceFile(ctx, inv, pdfBytes, "zip-xml-render")
				}
				if rerr != nil {
					log.Printf("[email/invoice-harvest] zip xml render failed invoice=%s: %v", inv.ID, rerr)
				}
			}
		}
	}

	// bannerErrs：被 imagePlausibleAsVoucher 拒掉的非凭证图片。
	// 与 linkErrs 分开记，因为两者的处置完全不同——链接失败值得重试，
	// 而「邮件里只有一张横幅」重试多少次都不会变好。
	var bannerErrs []string

	// 1) PDF / 图片附件（拍照发票常见 jpg/png）
	//
	// 图片要多过一道 imagePlausibleAsVoucher：isImageBytes 只问「是不是图片」，
	// 而营销横幅对这个问题答 true。1da030ab 挡住了 `src=` 那条**候选收集**的路，
	// 附件这条路它没管，也管不着（内联 cid: 图片同样会被解成附件）。
	// 真实后果见 invoice_banner_voucher_admission_test.go：两张 SHA256 相同的
	// 百望宣传横幅被存成两笔「已核验」发票，金额占当轮 CNY 合计的 61.1%。
	for _, att := range parsed.Attachments {
		if isPDFBytes(att.Data) {
			return h.saveInvoiceFile(ctx, inv, att.Data, "attachment")
		}
		if isImageBytes(att.Data) {
			if !imagePlausibleAsVoucher(att.Data) {
				why := "附件 " + att.Filename + "：" + voucherRejectionReason(att.Data)
				log.Printf("[email/invoice-harvest] reject non-voucher image invoice=%s: %s", inv.ID, why)
				bannerErrs = append(bannerErrs, why)
				continue
			}
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
	//
	// 图片同样要过 imagePlausibleAsVoucher：候选收集侧只排除了 `src=`，
	// 而 `<a href="…banner.jpg">` 与正文裸 URL 两条路照收不误
	// （1da030ab 的反向保护第 1 条恰恰要求 href 必须仍被收走——收候选是对的，
	// 缺的是收下来之后的采信闸门）。
	var linkErrs []string
	for _, u := range extractInvoiceURLs(parsed.HTMLBody + "\n" + parsed.TextBody) {
		data, dlErr := h.downloadPDF(ctx, u)
		if dlErr == nil {
			switch {
			case isPDFBytes(data):
				return h.saveInvoiceFile(ctx, inv, data, "pdf-url")
			case isImageBytes(data):
				if imagePlausibleAsVoucher(data) {
					return h.saveInvoiceFile(ctx, inv, data, "pdf-url")
				}
				why := u + "：" + voucherRejectionReason(data)
				log.Printf("[email/invoice-harvest] reject non-voucher image invoice=%s: %s", inv.ID, why)
				bannerErrs = append(bannerErrs, why)
				continue
			}
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
	//
	// bannerErrs 单列：被横幅闸门拒掉的候选要能被运维一眼看出来，
	// 否则 last_error 只会报成「没有可用 pdf/xml」——那句话会把
	// 「拿回来的是张标语」误说成「邮件里没有发票」，正是 2026-10-04
	// 那两行至今没人能解释的根因。
	if len(bannerErrs) > 0 {
		if len(linkErrs) > 0 {
			return h.markRetry(ctx, inv, "取到的图片不是发票凭证（横幅/装饰图）："+
				strings.Join(bannerErrs, "; ")+"；另有链接失败："+strings.Join(linkErrs, "; "))
		}
		return h.markRetry(ctx, inv, "取到的图片不是发票凭证（横幅/装饰图）："+strings.Join(bannerErrs, "; "))
	}
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

// pickFreeInvoicePath 为一张发票挑一个不会覆盖别人的落盘路径。
//
// 为什么需要（2026-10-02 补）：`InvoiceFileName` 的格式是
// `{费用类型}-{对方单位}-{金额}-{日期}[-{发票号}].pdf`，发票号段是 2026-10-01
// 才加的防撞名措施。但它只在**规则层从邮件正文/主题解析出了发票号**时才有值——
// attachment 与 pdf-url 两条路径都是拿邮件文本里那个号，
// 发票号只印在 PDF 内部时就是空的。于是仍然会算出同名，
// 而 `os.Rename` 在 Windows 上是**替换**语义：不报错、不重试，
// 两行 DB 都是 status=downloaded 且指向同一个 file_path，
// 磁盘上只剩最后写入的那张，另一张的凭证永久丢失。
//
// 判据是**内容**而不是存在性：
//
//	· 目标不存在 → 用它；
//	· 目标已存在且内容**相同** → 仍用它（同一张票的重跑必须幂等，
//	  否则每次补跑都多出 `-2`、`-3`，台账被副本淹没）；
//	· 目标已存在但内容**不同** → 说明是另一张票，换 `-2`、`-3`……
//
// 这正是 InvoiceFileName 注释里写下的「若后续发现它也发生，应在
// saveInvoiceFile 里检测目标已存在并加序号，而不是继续往文件名里塞字段」。
// 目标路径的占用状态。区分「不存在」与「存在但内容不同」是必须的：
// 前者直接占用，后者必须换名——把两者混为一谈会让候选循环走完
// 直落回原名，等于没有防护。
type invoicePathState int

const (
	invoicePathFree         invoicePathState = iota // 文件不存在，可安全占用
	invoicePathSameContent                          // 已被**同一张**票占用（重跑）
	invoicePathOtherContent                         // 被**另一张**票占用，必须换名
)

func pickFreeInvoicePath(dir, name string, data []byte) string {
	want := sha256.Sum256(data)
	for i := 1; i < 1000; i++ {
		cand := filepath.Join(dir, name)
		if i > 1 {
			cand = filepath.Join(dir, withInvoiceSeq(name, i))
		}
		switch invoicePathStateOf(cand, data, want) {
		case invoicePathFree, invoicePathSameContent:
			return cand
		}
	}
	// 序号用尽（1000 张同名不同内容的票）：仍返回首选名，交由 os.Rename 的
	// 既有语义处理。这条实际上不可达，保留只是让函数总有返回值、不引入 panic。
	return filepath.Join(dir, name)
}

// invoicePathStateOf 报告 path 的占用状态。
//
// 先比长度：长度不同必然内容不同，省掉一次整文件读。发票 PDF 动辄几百 KB，
// 而这条在每次落盘时都会走。
//
// 长度基线必须来自 data 而不是 want（want 是 [32]byte 的摘要，
// len(want) 恒为 32）。第一版就是写成 `len(raw) != len(want)`，
// 于是 5 字节的夹具永远判成「内容不同」，幂等那条用例直接转红。
func invoicePathStateOf(path string, data []byte, want [32]byte) invoicePathState {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return invoicePathFree
	}
	if st.Size() != int64(len(data)) {
		return invoicePathOtherContent
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		// 读不出来（有权限/是坏符号链接等）：按「已被占用」处理，宁可换名
		// 也不要覆盖掉一个我们没能确认内容的东西。
		return invoicePathOtherContent
	}
	if sha256.Sum256(raw) == want {
		return invoicePathSameContent
	}
	return invoicePathOtherContent
}

// withInvoiceSeq 在扩展名前插入 `-N`：`a-b-1.00-2026-09-24.pdf` → `...-2.pdf`。
func withInvoiceSeq(name string, n int) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return fmt.Sprintf("%s-%d", name, n)
	}
	return strings.TrimSuffix(name, ext) + fmt.Sprintf("-%d", n) + ext
}

// saveInvoiceFile 以规范文件名落盘并置 downloaded。图片保留原扩展名。
func (h *InvoiceHarvester) savePDF(ctx context.Context, inv *Invoice, data []byte, source string) string {
	return h.saveInvoiceFile(ctx, inv, data, source)
}

func (h *InvoiceHarvester) saveInvoiceFile(ctx context.Context, inv *Invoice, data []byte, source string) string {
	if inv.InvoiceDate == "" {
		inv.InvoiceDate = ParseInvoiceDateFromBytes(data)
	}
	kind, ext := DetectInvoiceMedia(data)
	if kind == "pdf" {
		// 2026-10-02：只凭 magic 收下退化 PDF（只有 Catalog、无页树，实测 69 字节）。
		// 后果有两条：台账多出一张 0 元「发票」，以及导出接口被 pdfcpu 的页树
		// panic 打成 500。采集侧就该把它挡在外面走重试——这正是需求「有可能需要
		// 多次操作才能下载到发票文件」要覆盖的情形：拿回来不是发票就该重试，
		// 而不是当成下载成功。
		if ok, verr := pdfHasPages(data); verr != nil || !ok {
			if verr == nil {
				verr = errors.New("pdf has no page")
			}
			return h.markRetry(ctx, inv, "unusable pdf: "+verr.Error())
		}
	}
	if ext == "" {
		ext = ".pdf"
	}
	name := InvoiceFileNameWithExt(inv, ext)
	dir := filepath.Join(h.DataDir, "email-invoices", defaultWorkspace(inv.WorkspaceID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return h.markRetry(ctx, inv, "mkdir: "+err.Error())
	}
	path := pickFreeInvoicePath(dir, name, data)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return h.markRetry(ctx, inv, "write file: "+err.Error())
	}
	if err := os.Rename(tmp, path); err != nil {
		return h.markRetry(ctx, inv, "rename file: "+err.Error())
	}
	inv.Status = "downloaded"
	inv.FileName = filepath.Base(path)
	inv.FilePath = filepath.Join("email-invoices", defaultWorkspace(inv.WorkspaceID), inv.FileName)
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
	// 两侧各修了一个独立缺陷，必须叠加，不能二选一：
	//
	// 分支侧（超限检测）：原先是
	// `io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes))`，超限时
	// **静默截断**成正好 20MB 且不返回 error。而调用方只判 `isPDFBytes(data)`，
	// 被截断的 PDF 头部 `%PDF-` 依然完好 → 判定通过 → 落盘 → 标记 `downloaded`。
	// 后果是：需求 3 交付给用户的凭证附件是一个**打不开的 PDF**，而库里记着
	// 「已下载成功」，没有任何报错可查。修法是读 MaxInvoicePDFBytes+1 字节
	// 再回头看长度。
	//
	// main 侧（内容校验）：用魔数认 PDF/图片（有些服务器 Content-Type 不准但
	// 内容确实是发票文件）；两边都不认就是「拿回来的不是发票文件」，必须报错
	// 而不是让调用方静默丢弃。
	//
	// 顺序有意为之：先判超限（超限的 body 魔数必然完好，若先判魔数则超限的
	// 截断文件会在魔数这一步被误判为「不是 PDF」而报出误导性的 not-pdf 原因）。
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > MaxInvoicePDFBytes {
		return nil, fmt.Errorf("invoice file too large: exceeds %d bytes", MaxInvoicePDFBytes)
	}
	if isPDFBytes(body) || isImageBytes(body) {
		return body, nil
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = "(未声明)"
	}
	return nil, fmt.Errorf("not-pdf: 下载内容不是 PDF/图片（content-type=%s, %d 字节）", ct, len(body))
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
//
// **ZIP 也算**：电子发票平台的标准下发形态是把票面 PDF、发票 XML 与 OFD
// 装在一个压缩包里（2026-10-03 真实数据：通行费邮件附件是
// `通行费电子发票.zip` 136KB + 两份汇总单 PDF，zip 内才有真票）。
// 只认 PDF/图片/XML 的话，纯 zip 邮件连建档门槛都过不去。
// 判「是不是发票包」而不是「是不是 zip」：一个装着照片的普通 zip 不该算票据。
func HasInvoiceAttachment(atts []ParsedAttachment) bool {
	for _, att := range atts {
		if len(att.Data) == 0 {
			continue
		}
		if isPDFBytes(att.Data) || isImageBytes(att.Data) || isXMLFile(att) {
			return true
		}
		if isZipBytes(att.Data, att.Filename) && !readZipInvoiceContents(att.Data).Empty() {
			return true
		}
	}
	return false
}

// extractInvoiceURLs 从 HTML/纯文本提取候选下载链接，按发票平台特征排序。
func extractInvoiceURLs(body string) []string {
	seen := map[string]bool{}
	var out []string

	// 内联资源（img/src 等）里的 URL 一律不算下载候选。
	//
	// 为什么不能只靠 reSkippable 的图片扩展名（2026-10-04 真实产出教训）：
	// 那条跳过规则只认 URL 里**有没有** `.png/.jpg/.jpeg/.gif`，
	// 而动态图片地址常写成 `…/banner?w=750&h=200` —— **没有扩展名**，
	// 于是 `<img src="https://cdn.baiwang.com/mail/banner?w=750&h=200&ticket=abc123">`
	// 被 reBareURLs 捞进候选。采集器下载它、`isImageBytes` 对任意 JPEG 放行，
	// 于是营销横幅被存成了发票文件（台账里两张「票」的文件实际是印着
	// 「用心服务 贴心用户」的横幅，两个文件 SHA256 相同）。
	//
	// 「跳过内联图片」这个**意图本来就是既有的**——reSkippable 里的
	// `\.png|\.jpg|\.jpeg|\.gif` 就是它，invoice_harvest_test.go 里那个
	// `<img src="https://cdn.cn/pic.png"/>` 用例也钉住了。
	// 这次只是把实现从「看扩展名」换成「看它来自哪个属性」，
	// 意图不变，**覆盖面变大**：不再依赖对方用什么后缀发图。
	inline := map[string]bool{}
	for _, m := range reHTMLSrcs.FindAllStringSubmatch(body, -1) {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			g = strings.TrimRight(strings.TrimSpace(g), ").,;")
			g = strings.ReplaceAll(g, "&amp;", "&")
			inline[g] = true
		}
	}

	add := func(u string) {
		u = strings.TrimRight(strings.TrimSpace(u), ").,;")
		u = strings.ReplaceAll(u, "&amp;", "&")
		if u == "" || seen[u] || len(u) > 2000 {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return
		}
		if inline[u] {
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

// InvoiceFileName 生成规范文件名。
//
// 格式：`{费用类型}-{对方单位}-{金额}-{日期}[-{发票号}].pdf`
//
// 关于发票号这一段（2026-10-01 补）：需求原文写的是
// `{费用类型}-{对方单位}-{金额}-{日期}.pdf`，但**这个格式不足以唯一标识一张票**。
// 实测三张不同的发票得到同一个文件名：
//
//	云服务-AWS-100.00-2026-09-15.pdf  票 A（CNY，发票号 …741）
//	云服务-AWS-100.00-2026-09-15.pdf  票 B（USD，发票号 …001）
//	云服务-AWS-100.00-2026-09-15.pdf  票 C（CNY，发票号 …742）
//
// 而 saveInvoiceFile 用 `os.Rename(tmp, path)` 落盘——**同名直接静默覆盖**，
// 不报错、不重试。两行 DB 记录都 status='downloaded'、file_path 指向同一个
// 文件，但磁盘上只剩最后写入的那张票，另一张的凭证永久丢失，列表里两张
// 看起来都正常、点开却是同一份内容。
//
// 「同一天、同一供应商、同一金额」在真实场景很常见（充值两次、订阅续费、
// 重开发票），这不是边角情况。发票号是发票的**唯一标识**，加进去既符合
// 需求意图（凭证可追溯），又让文件名真正唯一。
//
// 发票号为空时（采集早期/XML 未解析出）不加这一段，保持需求原文的格式。
// 同一输入必须稳定：重跑采集不会换名字（幂等）。这个分支仍可能在
// 「同额同日同单位且都没有发票号」时撞名——但那要求两张票连发票号都
// 解析不出来，属于采集完全失败的场景，优先级低于「有发票号却撞名」
// 这种日常场景。若后续发现它也发生，应在 saveInvoiceFile 里检测目标
// 已存在并加序号，而不是继续往文件名里塞字段。
func InvoiceFileName(inv *Invoice) string {
	category := sanitizeFileName(inv.Category, "其他")
	seller := sanitizeFileName(inv.Seller, "未知单位")
	amount := strconv.FormatFloat(inv.Amount, 'f', 2, 64)
	date := strings.ReplaceAll(inv.InvoiceDate, "/", "-")
	if date == "" {
		// 原来这里填 `time.Now().Format("2006-01-02")`，也就是**下载当天**。
		// 那是往凭证文件名里塞了一个**编造的日期**，而且台账那一列是空的——
		// 同一份数据在文件名里「有日期」、在汇总单里「没日期」，两边自相矛盾，
		// 而文件名那份更像真的。真实产物（2026-10-04 08:00 那轮）：
		//
		//	通信-X-8.00-2026-10-04.pdf     ← 日期段是下载日，台账「日期」列是空的
		//
		// 这与 round37 §35 那条（金额取自信用额度、日期取自到期还款日）
		// 是同一类缺陷：一个**看起来权威的错值**，比留空危险得多。
		//
		// 顺带修掉一个非确定性：填当天日期意味着同一张票**隔天重试就会得到
		// 另一个文件名**，而 pickFreeInvoicePath 是按「目标名 + 内容相同」去重的，
		// 名字一变目标就不存在 → 写出第二份副本，同一张票在目录里出现两次。
		//
		// 用显式占位而不是留空：留空会让名字变成 `通信-X-8.00-.pdf`，
		// 可读性差、也容易被误当成解析失败。`未知日期` 与既有的
		// `未知单位` 兜底是同一套约定。
		date = "未知日期"
	}
	name := fmt.Sprintf("%s-%s-%s-%s", category, seller, amount, date)
	if no := sanitizeFileName(inv.InvoiceNo, ""); no != "" {
		name += "-" + no
	}
	name += ".pdf"
	// 整名兜底长度（2026-10-01 补）。
	//
	// 为什么需要：sanitizeFileName 对**每个**字段各截到 60，四个字段拼起来
	// 最坏可达 60*3 + 金额 + 日期 + 发票号 ≈ 208 字节；加上
	// `<dataDir>/email-invoices/<workspace>/` 这段前缀后实测全路径 269 字节，
	// **超过 Windows MAX_PATH 260**——os.WriteFile 会直接失败（报
	// "File name too long"），采集器 markRetry 重试也是白试。
	//
	// 名字对「可读」的要求低于对「唯一」的要求，所以超长时优先砍
	// 发票号（尾部），保住 {费用类型}-{对方单位}-{金额}-{日期} 这段
	// 需求约定的可读部分。
	// 上界 180 而不是 200：实测 200 时全路径 263 字节仍超 MAX_PATH，
	// 数据目录前缀在不同部署下可能更长，留足余量。180 对应的全路径约 243。
	const maxNameBytes = 180
	if len(name) > maxNameBytes {
		cut := name[:maxNameBytes]
		// 不要把 .pdf 切掉，也尽量不要切在多字节字符中间
		if i := strings.LastIndex(cut, ".pdf"); i > 0 {
			cut = cut[:i]
		}
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		name = strings.TrimRight(cut, "-.") + ".pdf"
	}
	return name
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
