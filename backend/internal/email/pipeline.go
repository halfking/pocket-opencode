package email

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pipeline.go — 每日邮件处理流水线（可定时、可手动触发）。
//
// 对应需求：
//   - 每天定时/手动收信（步骤 1）
//   - 清理广告与垃圾邮件到垃圾箱（步骤 2，判定 spam.go + MOVE junk.go）
//   - 其它重要邮件提醒（步骤 3，notifycenter）
//   - 发票下载整理 {费用类型}-{对方单位}-{金额}-{日期}.pdf（步骤 4，
//     invoice_harvest.go，多次重试）
//   - 推送飞书；无法发送时生成共享汇总文档 + 列表 + 合计金额（步骤 5/6）
//
// 执行位置（需求「可以设备本地进行，也可以委托服务端进行，默认本地」）：
// 本地部署形态下 pocketd 就运行在设备本地，Pipeline 即"本地执行"路径；
// server 模式由上层（server 包）把同样的 Run 动作转发给远端编排 URL，
// Pipeline 自身不感知。

// InvoicePusher 把下载好的发票文件推到飞书。实现由 server 包装 feishu.Client。
type InvoicePusher interface {
	PushInvoice(ctx context.Context, inv Invoice, absPath string) error
	Available() bool
}

// ImportantNotifier 派发重要邮件提醒。实现由 server 包装 notifycenter.Service。
type ImportantNotifier interface {
	NotifyImportantEmail(ctx context.Context, e Email) error
}

// Pipeline 流水线。
type Pipeline struct {
	Store    *Store
	Fetcher  *Fetcher
	Harvest  *InvoiceHarvester
	Pusher   InvoicePusher      // 可为 nil：跳过飞书，直接走共享文档
	Notifier ImportantNotifier  // 可为 nil：跳过提醒
	// Ledger 发布飞书共享台账（电子表格）。为 nil 或不可用时只生成本地 CSV/MD。
	Ledger   LedgerPublisher
	DataDir  string
	// SpamLookbackDays 垃圾清理扫描窗口（默认 7 天）。
	SpamLookbackDays int
	// SpamDryRun=true 时第 2 步只判定不 MOVE（真实邮箱首次运行的安全阀）。
	SpamDryRun bool
}

// spamPreviewCap 每个账户在预演报告里最多列多少个主题样本。
const spamPreviewCap = 10

// SpamPreviewItem 是预演报告里一个账户的判定结果。
type SpamPreviewItem struct {
	AccountID string   `json:"accountId"`
	Count     int      `json:"count"`
	Why       string   `json:"why"`
	Subjects  []string `json:"subjects,omitempty"`
}

// PipelineReport 一轮执行的结果汇总。
type PipelineReport struct {
	StartedAt     int64  `json:"startedAt"`
	FinishedAt    int64  `json:"finishedAt"`
	DurationMs    int64  `json:"durationMs"`
	AccountsSynced int   `json:"accountsSynced"`
	NewEmails     int    `json:"newEmails"`
	SpamMoved     int    `json:"spamMoved"`
	SpamLocalOnly int    `json:"spamLocalOnly"`
	// SpamDryRun>0 表示本轮是预演：这 SpamDryRun 封「本可以移走但没移」，
	// 逐账户列在 SpamDryRunSamples 里。真实邮箱上先看这个再决定是否真移。
	SpamDryRun        int               `json:"spamDryRun,omitempty"`
	SpamDryRunSamples []SpamPreviewItem `json:"spamDryRunSamples,omitempty"`
	RemindersSent int    `json:"remindersSent"`
	Invoices      HarvestResult `json:"invoices"`
	FeishuPushed  int    `json:"feishuPushed"`
	FeishuFailed  int    `json:"feishuFailed"`
	ShareDocCSV   string `json:"shareDocCsv,omitempty"`
	ShareDocMD    string `json:"shareDocMd,omitempty"`
	// ShareDocURL 是飞书共享台账链接（未配置飞书时为空，本地 CSV/MD 仍会生成）。
	ShareDocURL string `json:"shareDocUrl,omitempty"`
	Errors        []string `json:"errors,omitempty"`
}

// AddError 记录非致命错误（流水线继续跑完）。
func (r *PipelineReport) AddError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// stepStart 打一条分步进度日志：步骤名 + 距本轮开始的耗时。
//
// 背景：真实邮箱上一轮 Run 跑了 9 分钟仍未结束，而日志里只有开头两行和最后
// 一行汇总，中途完全没有可观测信息，无法判断卡在哪个账户、哪一步。收 5 个
// 真实账户（最大 62 封）本该是秒级，这里每步都留下耗时与计数后才谈得上定位
// 与优化。
func stepStart(rep *PipelineReport, start time.Time, format string, args ...any) {
	log.Printf("[email/pipeline] step %s (t+%s)",
		fmt.Sprintf(format, args...),
		time.Since(start).Round(time.Millisecond))
}

// Run 执行一轮完整流水线。
func (p *Pipeline) Run(ctx context.Context) *PipelineReport {
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	start := time.Now()
	defer func() {
		rep.FinishedAt = time.Now().Unix()
		rep.DurationMs = time.Since(start).Milliseconds()
		log.Printf("[email/pipeline] done synced=%d new=%d spam=%d(+%d local) reminders=%d inv=%+v feishu=%d/%d errors=%d",
			rep.AccountsSynced, rep.NewEmails, rep.SpamMoved, rep.SpamLocalOnly,
			rep.RemindersSent, rep.Invoices, rep.FeishuPushed, rep.FeishuFailed, len(rep.Errors))
	}()

	// 1) 全账户收信
	accounts, err := p.Store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		rep.AddError("list accounts: %v", err)
		return rep
	}
	stepStart(rep, start, "1/5 sync %d account(s)", len(accounts))
	for _, acc := range accounts {
		// 逐步检查取消：HTTP 客户端断开或 15 分钟上限到点后，go-imap 正在
		// 进行的那次读不可中断（库内 read 30s / literal 5min 上限），但循环
		// 本身必须立刻退出，否则剩下每个账户还会各拖一个超时。
		if cerr := ctx.Err(); cerr != nil {
			rep.AddError("run cancelled at step1 before %s: %v", acc.EmailAddress, cerr)
			log.Printf("[email/pipeline] step1 cancelled before %s: %v", acc.EmailAddress, cerr)
			break
		}
		t0 := time.Now()
		n, err := p.Fetcher.Sync(ctx, acc.ID)
		cost := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			log.Printf("[email/pipeline] step1 sync %s FAILED after %s: %v", acc.EmailAddress, cost, err)
			rep.AddError("sync %s: %v", acc.EmailAddress, err)
			continue
		}
		log.Printf("[email/pipeline] step1 sync %s new=%d in %s", acc.EmailAddress, n, cost)
		rep.AccountsSynced++
		rep.NewEmails += n
	}

	// 1.5) 发票候选自动建档（供第 4 步采集下载；背景见函数注释）
	stepStart(rep, start, "1.5/5 invoice candidates")
	p.extractInvoiceCandidates(ctx, accounts, rep)

	// 2) 垃圾清理
	stepStart(rep, start, "2/5 spam clean (dryRun=%v)", p.SpamDryRun)
	p.cleanSpam(ctx, rep)

	// 3) 重要邮件提醒
	stepStart(rep, start, "3/5 important reminders")
	p.notifyImportant(ctx, rep)

	// 4) 发票采集（下载/渲染/命名落盘）
	if p.Harvest != nil {
		stepStart(rep, start, "4/5 invoice harvest")
		rep.Invoices = p.Harvest.HarvestAll(ctx)
	}

	// 5) 飞书推送 + 6) 共享汇总文档（总是生成，作为可核查的清单）。
	// 发票行带 (user, workspace) 隔离：复用 intentLoop 的 scope 去重方式，
	// 对每个 scope 独立推送与汇总，避免跨工作区串数据。
	scopes := map[[2]string]struct{}{}
	for _, acc := range accounts {
		if acc.UserID != "" {
			scopes[[2]string{acc.UserID, defaultWorkspace(acc.WorkspaceID)}] = struct{}{}
		}
	}
	stepStart(rep, start, "5/5 push+ledger over %d scope(s)", len(scopes))
	for sc := range scopes {
		invoices, err := p.Store.ListInvoicesScoped(ctx, sc[0], sc[1], "downloaded", 500)
		if err != nil {
			rep.AddError("list downloaded invoices scope=%v: %v", sc, err)
			continue
		}
		p.pushInvoiceSet(ctx, invoices, sc[0], sc[1], rep)
		// 共享台账：飞书可用时在飞书上建一张真表格（可分享），
		// 本地 CSV/MD 始终生成（离线兜底 + 对账留存）。
		if url, lerr := p.PublishLedgerScoped(ctx, sc[0], sc[1]); lerr != nil {
			rep.AddError("publish ledger scope=%v: %v", sc, lerr)
		} else if url != "" {
			rep.ShareDocURL = url
		}
		if _, _, err := p.BuildInvoiceSummaryDocs(ctx, sc[0], sc[1]); err != nil {
			rep.AddError("summary docs scope=%v: %v", sc, err)
		}
	}
	return rep
}

// extractInvoiceCandidates 把近期入库、命中发票关键词但尚未建档的邮件自动
// 建发票记录，供第 4 步 HarvestAll 下载附件。
//
// 背景缺陷：发票自动提取原本只接在客户端推送路径（handleEmailSync 模式 B 的
// classifyEmailsAsync）；服务端 IMAP 抓取（模式 A）与定时 pipeline 都不触发，
// 导致「收取发票类邮件并解析整理、下载发票文件」对纯服务端部署永远不发生。
// IMAP 路径只落 envelope（snippet 为空、金额/发票号在正文里），因此候选命中
// 后需 FetchMessageRaw 拉原文做二次提取（与手动提取端点同路径）。
func (p *Pipeline) extractInvoiceCandidates(ctx context.Context, accounts []Account, rep *PipelineReport) {
	emails, _, err := p.Store.ListEmailsSince(ctx, rep.StartedAt-86400, 500)
	if err != nil {
		rep.AddError("invoice candidates list: %v", err)
		return
	}
	scope := make(map[string][2]string, len(accounts))
	for _, a := range accounts {
		if a.UserID != "" {
			scope[a.ID] = [2]string{a.UserID, defaultWorkspace(a.WorkspaceID)}
		}
	}
	created := 0
	bodyFetches := 0
	for i := range emails {
		e := emails[i]
		sc, ok := scope[e.AccountID]
		if !ok {
			continue
		}
		if _, err := p.Store.GetInvoiceByEmailID(ctx, e.ID); err == nil {
			continue // 已建档，幂等跳过
		}
		inv, hit := ExtractInvoice(e, "")
		// 正文二次提取只对关键词命中的候选做：24h 窗口内 miss 邮件可能有
		// 几十封，每封拉一次完整 IMAP 会话会把流水线拖到分钟级甚至触发
		// 服务商连接频控。与 server 侧 extractInvoicesAsync 的门槛一致。
		if !hit && e.UID > 0 && p.Fetcher != nil && InvoiceCandidate(e) {
			bodyFetches++
			raw, ferr := p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
			if ferr != nil {
				continue
			}
			if parsed, perr := ParseMIMEMessage(raw); perr == nil {
				// 金额常常只印在附件里（主题写「对账单」、正文写「见附件」），
				// 所以把「有没有发票类附件」一起告诉规则层，否则这封邮件会在
				// 采集器看到附件之前就被丢掉。
				inv, hit = ExtractInvoiceLoose(e, parsed.TextBody+"\n"+parsed.HTMLBody,
					HasInvoiceAttachment(parsed.Attachments))
			}
		}
		// 命中了但**没有开票日期**：IMAP 路径只落 envelope，正文里的「开票日期」
		// 看不到，于是规范文件名退化成下载当天（实测真发票
		// 「其他-杭州创客家…-3500.00-2026-10-01.pdf」，票面其实是 5 月开的）。
		// 这里补一次正文读取——只针对「已命中 + 缺日期」的候选，量很小。
		if hit && inv.InvoiceDate == "" && e.UID > 0 && p.Fetcher != nil {
			bodyFetches++
			if d := p.fetchInvoiceDateFromBody(ctx, e); d != "" {
				inv.InvoiceDate = d
			}
		}
		if !hit {
			continue
		}
		if _, err := p.Store.UpsertInvoice(ctx, inv, sc[0], sc[1]); err != nil {
			rep.AddError("invoice upsert email=%s: %v", e.ID, err)
			continue
		}
		created++
	}
	log.Printf("[email/pipeline] step1.5 scanned=%d rawBodyFetches=%d autoCreated=%d",
		len(emails), bodyFetches, created)
	if created > 0 {
		log.Printf("[email/pipeline] auto-created %d invoice candidates", created)
	}
}

// fetchInvoiceDateFromBody 拉原文正文找开票日期。失败返回空串（不阻断流水线）。
func (p *Pipeline) fetchInvoiceDateFromBody(ctx context.Context, e Email) string {
	raw, err := p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
	if err != nil {
		return ""
	}
	parsed, perr := ParseMIMEMessage(raw)
	if perr != nil {
		return ""
	}
	text := parsed.TextBody
	if text == "" {
		text = parsed.HTMLBody
	}
	return ParseInvoiceDate(text)
}

// cleanSpam 扫描近期邮件，把广告/垃圾移进 IMAP 垃圾箱并落本地标记。
//
// SpamDryRun=true 时**只判定不移动**：把「会移哪些、为什么」写进报告，
// 不发任何 IMAP MOVE、也不改本地分类。这是给真实邮箱上的第一次运行留的
// 安全阀——垃圾判定规则没在真实邮箱上验证过，让它无人值守地搬真实邮件
// 风险太大；先看判定结果，确认无误再关掉 dryRun。
func (p *Pipeline) cleanSpam(ctx context.Context, rep *PipelineReport) {
	lookback := p.SpamLookbackDays
	if lookback <= 0 {
		lookback = 7
	}
	since := time.Now().AddDate(0, 0, -lookback).Unix()
	emails, _, err := p.Store.ListEmailsSince(ctx, since, 1000)
	if err != nil {
		rep.AddError("spam scan list: %v", err)
		return
	}
	byAccount := map[string][]int64{}
	whyByAccount := map[string]string{}
	samplesByAccount := map[string][]string{}
	for i := range emails {
		e := emails[i]
		if e.Category == "spam" || e.Category == "archived" {
			continue
		}
		inv := InvoiceCandidate(e)
		v := LooksLikeSpam(e.FromAddress, e.Subject, e.Snippet, inv, e.Importance == "high")
		if v.Spam {
			byAccount[e.AccountID] = append(byAccount[e.AccountID], e.UID)
			if whyByAccount[e.AccountID] == "" {
				whyByAccount[e.AccountID] = v.Why
			}
			if len(samplesByAccount[e.AccountID]) < spamPreviewCap {
				samplesByAccount[e.AccountID] = append(samplesByAccount[e.AccountID], e.Subject)
			}
		}
	}
	if p.SpamDryRun {
		for accountID, uids := range byAccount {
			rep.SpamDryRun++
			rep.SpamDryRunSamples = append(rep.SpamDryRunSamples, SpamPreviewItem{
				AccountID: accountID,
				Count:     len(uids),
				Why:       whyByAccount[accountID],
				Subjects:  samplesByAccount[accountID],
			})
		}
		log.Printf("[email/pipeline] spam dry-run: %d mail(s) would be moved, nothing was moved", rep.SpamDryRun)
		return
	}
	for accountID, uids := range byAccount {
		if p.Fetcher != nil {
			moved, err := p.Fetcher.MoveEmailsToJunk(ctx, accountID, uids)
			rep.SpamMoved += moved
			if err != nil {
				// MOVE 失败（无垃圾箱/服务器拒绝）：仍落本地标记，保持收件箱视图干净
				rep.SpamLocalOnly += len(uids) - moved
				rep.AddError("junk move account=%s: %v", accountID, err)
			}
		}
		if err := p.Store.MarkEmailsSpamByUID(ctx, accountID, uids); err != nil {
			rep.AddError("spam mark account=%s: %v", accountID, err)
		}
		if len(uids) > 0 {
			log.Printf("[email/pipeline] spam clean account=%s uids=%d why=%q", accountID, len(uids), whyByAccount[accountID])
		}
	}
}

// notifyImportant 对未提醒过的重要邮件派发通知并记录时间。
func (p *Pipeline) notifyImportant(ctx context.Context, rep *PipelineReport) {
	if p.Notifier == nil {
		return
	}
	since := time.Now().AddDate(0, 0, -2).Unix()
	emails, notified, err := p.Store.ListEmailsSince(ctx, since, 500)
	if err != nil {
		rep.AddError("reminder scan list: %v", err)
		return
	}
	var toNotify []Email
	var ids []string
	for i := range emails {
		e := emails[i]
		if notified[i] > 0 || e.Importance != "high" || e.Category == "spam" {
			continue
		}
		if err := p.Notifier.NotifyImportantEmail(ctx, e); err != nil {
			rep.AddError("notify email=%s: %v", e.ID, err)
			continue
		}
		toNotify = append(toNotify, e)
		ids = append(ids, e.ID)
	}
	if len(ids) > 0 {
		if err := p.Store.MarkEmailsNotified(ctx, ids, time.Now().Unix()); err != nil {
			rep.AddError("mark notified: %v", err)
		}
		rep.RemindersSent = len(ids)
		log.Printf("[email/pipeline] reminders sent: %v", emailSubjects(toNotify))
	}
}

// pushInvoices 已由 Run 内的 scope 循环实现（见上）。

func emailSubjects(emails []Email) []string {
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		out = append(out, e.Subject)
	}
	return out
}

// pushInvoiceSet 把给定发票推飞书（下载文件读盘），成功标记 feishu_sent_at。
// 推送失败保留 feishu_sent_at=0，由共享汇总文档兜底（需求允许两条路径）。
func (p *Pipeline) pushInvoiceSet(ctx context.Context, invoices []Invoice, userID, workspaceID string, rep *PipelineReport) {
	if p.Pusher == nil || !p.Pusher.Available() {
		return
	}
	var pushed []string
	for _, inv := range invoices {
		if inv.Status != "downloaded" || inv.FeishuSentAt > 0 || inv.FilePath == "" {
			continue
		}
		abs := filepath.Join(p.DataDir, inv.FilePath)
		if err := p.Pusher.PushInvoice(ctx, inv, abs); err != nil {
			rep.FeishuFailed++
			rep.AddError("push %s: %v", inv.FileName, err)
			continue
		}
		pushed = append(pushed, inv.ID)
	}
	if len(pushed) > 0 {
		if err := p.Store.MarkInvoicesFeishuSent(ctx, pushed, userID, workspaceID, time.Now().Unix()); err != nil {
			rep.AddError("mark feishu sent: %v", err)
		}
		rep.FeishuPushed += len(pushed)
	}
}

// PushInvoicesScoped 供 server 层手动触发：推送指定（或全部 downloaded）发票。
func (p *Pipeline) PushInvoicesScoped(ctx context.Context, ids []string, userID, workspaceID string) *PipelineReport {
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	invoices, err := p.Store.ListInvoicesByIDScoped(ctx, ids, userID, workspaceID)
	if err != nil {
		rep.AddError("list invoices: %v", err)
		return rep
	}
	p.pushInvoiceSet(ctx, invoices, userID, workspaceID, rep)
	rep.FinishedAt = time.Now().Unix()
	return rep
}

// PublishLedgerScoped 把该 scope 的全部发票清单发布成共享台账（飞书电子表格）。
//
// 与 BuildInvoiceSummaryDocs 的分工：后者写设备本地文件（离线兜底/留存），
// 这里产出**别人能打开的**共享文档。发布器为空或不可用时返回空 URL + nil error，
// 调用方据此不报错——本地汇总照常生成。
//
// 复用：同一 (workspace, user) 在进程内已经建过台账时直接返回上一次的链接。
// 没有这一步，GET /api/emails/invoices/summary 每刷新一次就新建一张飞书表格，
// 用户的云盘会被同一个清单刷屏——读接口产生这种副作用本身就是错的。
// 飞书目前没有「按标题查表」的接口可用，所以复用只能做到进程级；
// 重启后第一次读仍会新建一张，之后复用。
func (p *Pipeline) PublishLedgerScoped(ctx context.Context, userID, workspaceID string) (string, error) {
	if p.Ledger == nil || !p.Ledger.Available() {
		return "", nil
	}
	if url := p.Ledger.PublishedURL(workspaceID, userID); url != "" {
		return url, nil
	}
	invoices, err := p.Store.ListInvoicesScoped(ctx, userID, workspaceID, "", 500)
	if err != nil {
		return "", fmt.Errorf("list invoices: %w", err)
	}
	if len(invoices) == 0 {
		return "", nil
	}
	title := LedgerTitle(workspaceID, time.Now())
	url, err := p.Ledger.PublishLedger(ctx, title, invoices)
	if err != nil {
		return "", err
	}
	p.Ledger.RememberPublished(workspaceID, userID, url)
	return url, nil
}

// BuildInvoiceSummaryDocs 生成共享汇总文档（CSV 清单 + Markdown 报表，
// 含合计金额）。返回两个文件的绝对路径。文件总在每轮流水线末尾重建，
// 作为「无法发送飞书时的共享文档」兜底与对账清单。
func (p *Pipeline) BuildInvoiceSummaryDocs(ctx context.Context, userID, workspaceID string) (csvPath, mdPath string, err error) {
	invoices, err := p.Store.ListInvoicesScoped(ctx, userID, workspaceID, "", 500)
	if err != nil {
		return "", "", err
	}
	return WriteInvoiceSummaryDocs(p.DataDir, workspaceID, invoices)
}

// WriteInvoiceSummaryDocs 把发票清单写为 CSV + Markdown 汇总文档。
func WriteInvoiceSummaryDocs(dataDir, workspaceID string, invoices []Invoice) (string, string, error) {
	dir := filepath.Join(dataDir, "email-invoices", "exports", defaultWorkspace(workspaceID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	stamp := time.Now().Format("20060102-150405")
	csvPath := filepath.Join(dir, "invoices-summary-"+stamp+".csv")
	mdPath := filepath.Join(dir, "invoices-summary-"+stamp+".md")

	var total float64
	rows := make([][]string, 0, len(invoices))
	for _, inv := range invoices {
		total += inv.Amount
		rows = append(rows, []string{
			inv.Category, inv.Seller, fmt.Sprintf("%.2f", inv.Amount), inv.Currency,
			inv.InvoiceNo, inv.InvoiceDate, inv.Status, inv.FileName, inv.Subject,
		})
	}

	csv := &strings.Builder{}
	csv.WriteString("费用类型,对方单位,金额,币种,发票号,日期,状态,文件名,来源邮件\n")
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = csvSafeCell(c)
		}
		csv.WriteString(strings.Join(cells, ",") + "\n")
	}
	csv.WriteString(fmt.Sprintf("合计,,,,,,,%.2f,\n", total))
	if err := os.WriteFile(csvPath, []byte(csv.String()), 0o600); err != nil {
		return "", "", err
	}

	md := &strings.Builder{}
	md.WriteString("# 发票汇总\n\n")
	md.WriteString(fmt.Sprintf("生成时间：%s · 共 %d 张 · 合计金额 **%.2f**\n\n",
		time.Now().Format("2006-01-02 15:04"), len(invoices), total))
	md.WriteString("| 费用类型 | 对方单位 | 金额 | 发票号 | 日期 | 状态 | 文件 |\n")
	md.WriteString("|---|---|---:|---|---|---|---|\n")
	for _, r := range rows {
		md.WriteString(fmt.Sprintf("| %s | %s | %s %s | %s | %s | %s | %s |\n",
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7]))
	}
	if err := os.WriteFile(mdPath, []byte(md.String()), 0o600); err != nil {
		return csvPath, "", err
	}
	return csvPath, mdPath, nil
}

// csvSafeCell 防 CSV 公式注入（=/-/+/@ 开头的单元格前置单引号）并转义引号。
func csvSafeCell(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '=' || s[0] == '-' || s[0] == '+' || s[0] == '@' {
		s = "'" + s
	}
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
