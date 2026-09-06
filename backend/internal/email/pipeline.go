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
	DataDir  string
	// SpamLookbackDays 垃圾清理扫描窗口（默认 7 天）。
	SpamLookbackDays int
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
	RemindersSent int    `json:"remindersSent"`
	Invoices      HarvestResult `json:"invoices"`
	FeishuPushed  int    `json:"feishuPushed"`
	FeishuFailed  int    `json:"feishuFailed"`
	ShareDocCSV   string `json:"shareDocCsv,omitempty"`
	ShareDocMD    string `json:"shareDocMd,omitempty"`
	Errors        []string `json:"errors,omitempty"`
}

// AddError 记录非致命错误（流水线继续跑完）。
func (r *PipelineReport) AddError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
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
	for _, acc := range accounts {
		n, err := p.Fetcher.Sync(ctx, acc.ID)
		if err != nil {
			rep.AddError("sync %s: %v", acc.EmailAddress, err)
			continue
		}
		rep.AccountsSynced++
		rep.NewEmails += n
	}

	// 2) 垃圾清理
	p.cleanSpam(ctx, rep)

	// 3) 重要邮件提醒
	p.notifyImportant(ctx, rep)

	// 4) 发票采集（下载/渲染/命名落盘）
	if p.Harvest != nil {
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
	for sc := range scopes {
		invoices, err := p.Store.ListInvoicesScoped(ctx, sc[0], sc[1], "downloaded", 500)
		if err != nil {
			rep.AddError("list downloaded invoices scope=%v: %v", sc, err)
			continue
		}
		p.pushInvoiceSet(ctx, invoices, sc[0], sc[1], rep)
		if _, _, err := p.BuildInvoiceSummaryDocs(ctx, sc[0], sc[1]); err != nil {
			rep.AddError("summary docs scope=%v: %v", sc, err)
		}
	}
	return rep
}

// cleanSpam 扫描近期邮件，把广告/垃圾移进 IMAP 垃圾箱并落本地标记。
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
		}
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
