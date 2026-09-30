package server

// server_email_pipeline.go — 邮件流水线的 server 侧装配与 HTTP handlers。
//
// 路由（server.go 注册）：
//	POST /api/email/pipeline/run                手动触发一轮完整流水线
//	GET  /api/emails/invoices/{id}/file         下载单张已采集发票 PDF
//	POST /api/emails/invoices/export            合并导出 A4 网格 PDF {ids, grid}
//	GET  /api/emails/invoices/export/download   下载导出文件 ?file=<name>
//	POST /api/emails/invoices/push              推送发票到飞书 {ids?}
//	GET  /api/emails/invoices/summary           生成/获取共享汇总文档路径
//
// 执行位置：executionMode=local（默认）在本进程跑（本地部署时 pocketd 就在
// 设备本地）；executionMode=server 把 Run 委托给远端编排 URL
//（POCKET_EMAIL_SERVER_PIPELINE_URL），满足「可委托服务端进行」。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/feishu"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
)

// feishuInvoicePusher 把 email.InvoicePusher 接到 feishu.Client 上。
type feishuInvoicePusher struct {
	client *feishu.Client
	chatID string
}

func (p *feishuInvoicePusher) Available() bool {
	return p != nil && p.chatID != "" && p.client != nil && p.client.Available()
}

// PushInvoice 发文件 + 一条文字说明（群内可直接预览上下文）。
func (p *feishuInvoicePusher) PushInvoice(ctx context.Context, inv email.Invoice, absPath string) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(absPath), err)
	}
	filename := inv.FileName
	if filename == "" {
		filename = filepath.Base(absPath)
	}
	if err := p.client.SendInvoiceFile(ctx, p.chatID, filename, data); err != nil {
		return err
	}
	currency := inv.Currency
	if currency == "" {
		currency = "CNY"
	}
	note := fmt.Sprintf("已归档发票：%s\n金额 %s %.2f · 对方单位 %s · 来源邮件 %s",
		filename, currency, inv.Amount, inv.Seller, inv.Subject)
	return p.client.SendText(ctx, "chat_id", p.chatID, note)
}

// feishuLedgerPublisher 把 email.LedgerPublisher 接到飞书电子表格。
//
// 需求里的「建立共享文档及文件」此前只有本地 CSV/MD（设备上的文件，别人拿不到）。
// 这里在飞书上真的建一张电子表格：清单 + 合计行写进去，返回可分享链接。
type feishuLedgerPublisher struct {
	client *feishu.Client
	// folderToken 非空时台账建在该云空间目录下。
	folderToken string

	// published 记住本进程内已经建过的台账链接，按 (workspace, user) 归档。
	// 没有它，GET 汇总接口每刷新一次就新建一张表，把用户云盘刷屏。
	publishedMu sync.Mutex
	published   map[string]string
}

// PublishedURL 返回该用户本进程内已经建过的台账链接，没有则空串。
func (p *feishuLedgerPublisher) PublishedURL(workspaceID, userID string) string {
	if p == nil {
		return ""
	}
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	return p.published[workspaceID+"|"+userID]
}

// RememberPublished 记住刚建好的台账链接，供后续读路径复用。
func (p *feishuLedgerPublisher) RememberPublished(workspaceID, userID, url string) {
	if p == nil || url == "" {
		return
	}
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	if p.published == nil {
		p.published = map[string]string{}
	}
	p.published[workspaceID+"|"+userID] = url
}

func (p *feishuLedgerPublisher) Available() bool {
	return p != nil && p.client != nil && p.client.Available()
}

func (p *feishuLedgerPublisher) PublishLedger(ctx context.Context, title string, invs []email.Invoice) (string, error) {
	if !p.Available() {
		return "", fmt.Errorf("feishu ledger: app_id/app_secret not configured")
	}
	ss, err := p.client.CreateSpreadsheet(ctx, title, p.folderToken)
	if err != nil {
		return "", err
	}
	sheetID, err := p.client.FirstSheetID(ctx, ss.Token)
	if err != nil {
		return "", err
	}
	rows, _ := email.LedgerRows(invs)
	if err := p.client.WriteValues(ctx, ss.Token, email.LedgerCellRange(sheetID, rows), rows); err != nil {
		return ss.URL, err
	}
	return ss.URL, nil
}

// notifycenterEmailNotifier 把 email.ImportantNotifier 接到 notifycenter.Service。
type notifycenterEmailNotifier struct {
	svc   *notifycenter.Service
	store *email.Store
}

func (n *notifycenterEmailNotifier) NotifyImportantEmail(ctx context.Context, e email.Email) error {
	if n == nil || n.svc == nil {
		return fmt.Errorf("notifycenter not configured")
	}
	userID := ""
	if n.store != nil {
		if acc, _, err := n.store.GetAccountByID(ctx, e.AccountID); err == nil && acc != nil {
			userID = acc.UserID
		}
	}
	title := strings.TrimSpace(e.Subject)
	if title == "" {
		title = "重要邮件"
	}
	_, err := n.svc.Dispatch(ctx, notifycenter.Event{
		WorkspaceID: e.WorkspaceID,
		UserID:      userID,
		Source:      "email",
		Kind:        "email.important",
		Title:       "重要邮件：" + title,
		Body:        e.Snippet,
		Priority:    "high",
	})
	return err
}

// ensureInvoiceHarvester 构造发票采集器（流水线与手动 harvest 端点共用）。
// 返回 nil 表示依赖不齐（无 store / 无 dataDir）。
func (s *Server) ensureInvoiceHarvester() *email.InvoiceHarvester {
	if s.emailStore == nil || s.emailFetcher == nil || s.dataDir == "" {
		return nil
	}
	font := email.FindChineseFont(s.dataDir)
	return &email.InvoiceHarvester{
		Store:   s.emailStore,
		Fetcher: s.emailFetcher,
		DataDir: s.dataDir,
		// POP3 降级路径同步来的邮件，其 UID 是位置序号而非 IMAP UID，
		// 事后拿它去 IMAP FETCH 会取到**另一封**邮件（会下载到完全错误的
		// 发票文件）。采集器对这些邮件改读同步时加密落盘的原文缓存。
		// 见 email/body_cache.go 与 invoice_harvest.go 的 isPOP3SourcedEmail。
		// crypto 为 nil 时 NewFileBodyCache 返回 nil，采集器按「无缓存」处理。
		BodyCache: email.NewFileBodyCache(s.dataDir, s.emailCrypto),
		XMLRenderer: func(name string, inv *email.Invoice, xmlRaw []byte) ([]byte, error) {
			return email.RenderInvoiceXMLPDF(font, inv, xmlRaw)
		},
	}
}

// ensurePipeline 惰性构造流水线（单例）。依赖缺失时返回 nil。
func (s *Server) ensurePipeline() *email.Pipeline {
	s.emailPipelineOnce.Do(func() {
		if s.emailStore == nil || s.emailFetcher == nil || s.dataDir == "" {
			return
		}
		harvester := s.ensureInvoiceHarvester()
		if font := email.FindChineseFont(s.dataDir); font == "" {
			log.Printf("[email/pipeline] 中文字体不可用，XML 发票渲染降级（设置 POCKET_EMAIL_PDF_FONT_PATH）")
		}
		pusher := &feishuInvoicePusher{
			client: feishu.New(s.cfg.FeishuAppID, s.cfg.FeishuAppSecret),
			chatID: s.cfg.FeishuInvoiceChatID,
		}
		notifier := &notifycenterEmailNotifier{svc: s.notifySvc, store: s.emailStore}
		s.emailPipeline = &email.Pipeline{
			Store:    s.emailStore,
			Fetcher:  s.emailFetcher,
			Harvest:  harvester,
			Pusher:   pusher,
			Notifier: notifier,
			Ledger:   &feishuLedgerPublisher{client: pusher.client, folderToken: s.cfg.FeishuInvoiceFolderToken},
			DataDir:  s.dataDir,
			// 默认预演：清垃圾会 IMAP MOVE 真实邮件，规则没在真实邮箱上验证过，
			// 无人值守地搬用户邮件风险太大。置 POCKET_EMAIL_SPAM_DRYRUN=false 才真移。
			SpamDryRun: s.cfg.EmailSpamDryRun,
		}
		if s.cfg.EmailSpamDryRun {
			log.Printf("[email/pipeline] 清垃圾为预演模式（只判定不移动）：POCKET_EMAIL_SPAM_DRYRUN=false 可开启真实 MOVE")
		}
		if !pusher.Available() {
			log.Printf("[email/pipeline] feishu pusher 未配置（POCKET_FEISHU_APP_ID/SECRET/INVOICE_CHAT_ID），发票将走共享汇总文档路径")
		}
	})
	return s.emailPipeline
}

// RunEmailPipeline 供 scheduler 定时调用（或调试）。执行位置按配置决定。
func (s *Server) RunEmailPipeline(ctx context.Context) *email.PipelineReport {
	return s.runEmailPipeline(ctx, nil)
}

// runEmailPipeline 是唯一真正执行流水线的地方。
//
// 整个函数体都在 emailPipelineMu 里：Pipeline 是单例，而定时任务与 HTTP 手动
// 触发会并发进来。spamOverride 非 nil 时只覆盖本轮、跑完恢复配置值——
// 覆盖与执行必须处在同一把锁内，否则并发的手动跑会把另一轮正在进行的预演
// 设置改掉（一次 dryRun:false 会在别人的预演窗口里触发真实 IMAP MOVE）。
func (s *Server) runEmailPipeline(ctx context.Context, spamOverride *bool) *email.PipelineReport {
	s.emailPipelineMu.Lock()
	defer s.emailPipelineMu.Unlock()

	mode := strings.ToLower(strings.TrimSpace(s.cfg.EmailExecutionMode))
	if mode == "server" && strings.TrimSpace(s.cfg.EmailServerPipelineURL) != "" {
		return s.delegatePipeline(ctx)
	}
	p := s.ensurePipeline()
	if p == nil {
		return &email.PipelineReport{Errors: []string{"email pipeline not configured"}}
	}
	if spamOverride != nil && *spamOverride != p.SpamDryRun {
		log.Printf("[email/pipeline] 本轮清垃圾 dryRun=%v（配置值 %v）", *spamOverride, s.cfg.EmailSpamDryRun)
		prev := p.SpamDryRun
		p.SpamDryRun = *spamOverride
		defer func() { p.SpamDryRun = prev }()
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	return p.Run(runCtx)
}

// delegatePipeline 把流水线执行委托给远端编排服务（server 模式）。
func (s *Server) delegatePipeline(ctx context.Context) *email.PipelineReport {
	raw := strings.TrimSpace(s.cfg.EmailServerPipelineURL)
	// 远端地址来自部署配置（env），但它决定了本进程会把带邮箱权限的
	// 流水线 POST 到哪里。裸 http.NewRequest 对 "llm.kxpms.cn/v1"（漏写
	// scheme）、"file:///..."、"ftp://..." 这类值要么报一句难懂的
	// parse error，要么直接把请求发到不该去的地方。
	// 这里只放行 http/https，且要求显式 host，把误配挡在发请求之前。
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" ||
		(target.Scheme != "http" && target.Scheme != "https") {
		return &email.PipelineReport{Errors: []string{
			fmt.Sprintf("delegate: POCKET_EMAIL_SERVER_PIPELINE_URL 必须是带 scheme 的 http(s) 绝对地址，当前值无法使用（%q）", raw),
		}}
	}
	client := &http.Client{Timeout: 16 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return &email.PipelineReport{Errors: []string{"delegate: " + err.Error()}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return &email.PipelineReport{Errors: []string{"delegate: " + err.Error()}}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 非 200 的响应体多半是编排服务自己的错误页，直接 Decode 会得到
		// 一条与真实原因无关的 "delegate decode" 报错。
		return &email.PipelineReport{Errors: []string{
			fmt.Sprintf("delegate: remote pipeline returned %s", resp.Status)}}
	}
	var rep email.PipelineReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return &email.PipelineReport{Errors: []string{"delegate decode: " + err.Error()}}
	}
	return &rep
}

// handleEmailPipelineRun — POST /api/email/pipeline/run
// body 可选 {"dryRunSpam": true|false} 覆盖本轮清垃圾的预演开关
// （不传 = 用 POCKET_EMAIL_SPAM_DRYRUN 的配置值）。
func (s *Server) handleEmailPipelineRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	var body struct {
		DryRunSpam *bool `json:"dryRunSpam"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	// 覆盖只作用于本轮：跑完立刻恢复成配置值，否则一次 dryRunSpam:false
	// 会永久改掉后续每日定时任务的行为。覆盖与执行由 runEmailPipeline 在
	// 同一把锁内完成（Pipeline 是单例，手动跑与定时跑会并发）。
	rep := s.runEmailPipeline(r.Context(), body.DryRunSpam)
	writeJSON(w, http.StatusOK, rep)
}

// handleEmailInvoiceExport — POST /api/emails/invoices/export {ids:[], grid:2|3}
// 把已下载发票合并为 A4 网格单 PDF（2=2x2，3=3x3），打印后剪裁即凭证。
func (s *Server) handleEmailInvoiceExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.dataDir == "" {
		writeError(w, http.StatusServiceUnavailable, "data dir not configured")
		return
	}
	var body struct {
		IDs  []string `json:"ids"`
		Grid int      `json:"grid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids required")
		return
	}
	if body.Grid == 0 {
		body.Grid = 2
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	invoices, err := s.emailStore.ListInvoicesByIDScoped(r.Context(), body.IDs, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[email/export] requested ids=%d scope=user=%s ws=%s → got=%d", len(body.IDs), uid, wsID, len(invoices))
	var files []string
	var exportedIDs []string
	for _, inv := range invoices {
		if inv.FilePath == "" {
			continue
		}
		abs := filepath.Join(s.dataDir, inv.FilePath)
		st, serr := os.Stat(abs)
		if serr != nil {
			log.Printf("[email/export] stat invoice=%s file=%q failed: %v", inv.ID, abs, serr)
			continue
		}
		if st.IsDir() {
			continue
		}
		files = append(files, abs)
		exportedIDs = append(exportedIDs, inv.ID)
	}
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no harvested invoice files in selection")
		return
	}
	outDir := filepath.Join(s.dataDir, "email-invoices", "exports", wsID)
	gridExport, err := email.ExportInvoiceGridDetailed(outDir, files, body.Grid)
	if err != nil {
		// 选中的发票一个都用不了（畸形 PDF / 坏图片）是用户选择问题，回 400；
		// 之前一律 500，前端只会显示「操作失败」，看不出是哪张票坏了。
		if errors.Is(err, email.ErrNoUsableInvoiceFile) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	outPath := gridExport.Path
	// 记录导出时间 + 通知前端刷新。只给**真正进入网格**的票打时间戳：
	// 被跳过的坏文件不能算「已导出」，否则发票页会显示一张根本没导出的票已归档。
	// 注意不能用 count 前 N 个——跳过的文件可能在清单中间。
	skipped := make(map[string]bool, len(gridExport.Skipped))
	for _, name := range gridExport.Skipped {
		skipped[name] = true
	}
	now := time.Now().Unix()
	exported := 0
	for i, f := range files {
		if i >= len(exportedIDs) {
			break
		}
		if skipped[filepath.Base(f)] {
			continue
		}
		_ = s.emailStore.MarkInvoiceExported(r.Context(), exportedIDs[i], uid, wsID, now)
		exported++
	}
	if s.wsHub != nil {
		s.wsHub.BroadcastToUser(uid, "email.invoices.exported", map[string]any{
			"file": filepath.Base(outPath), "count": gridExport.Count, "grid": body.Grid,
			"skipped": gridExport.Skipped,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":    filepath.Base(outPath),
		"count":   gridExport.Count,
		"grid":    body.Grid,
		"skipped": gridExport.Skipped,
		"url":     "/api/emails/invoices/export/download?file=" + filepath.Base(outPath),
	})
}

// handleEmailInvoiceExportDownload — GET /api/emails/invoices/export/download?file=<name>
// 下载导出文件。只允许本 workspace exports 目录下的纯文件名（防路径逃逸）。
func (s *Server) handleEmailInvoiceExportDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name := filepath.Base(r.URL.Query().Get("file"))
	if name == "" || name == "." || name == "/" {
		writeError(w, http.StatusBadRequest, "file required")
		return
	}
	abs := filepath.Join(s.dataDir, "email-invoices", "exports", s.workspaceIDFromRequest(r), name)
	if _, err := os.Stat(abs); err != nil {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, abs)
}

// handleEmailInvoicePush — POST /api/emails/invoices/push {ids?: []}
// 推送发票到飞书；ids 省略 = 推送全部 downloaded 且未推送的。
// 飞书不可用/失败时返回汇总文档路径兜底。
func (s *Server) handleEmailInvoicePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	p := s.ensurePipeline()
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, "email pipeline not configured")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	// body 可为空（推送全部）
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	rep := p.PushInvoicesScoped(r.Context(), body.IDs, uid, wsID)

	result := map[string]any{
		"pushed": rep.FeishuPushed,
		"failed": rep.FeishuFailed,
		"errors": rep.Errors,
	}
	if rep.FeishuPushed == 0 {
		// 飞书发不出文件时的兜底：① 能建共享台账就建（别人可打开的电子表格），
		// ② 无论如何都生成本地 CSV/MD 清单（含合计金额）。
		if url, lerr := p.PublishLedgerScoped(r.Context(), uid, wsID); lerr != nil {
			result["ledgerError"] = lerr.Error()
		} else if url != "" {
			result["shareDocUrl"] = url
		}
		csvPath, mdPath, err := p.BuildInvoiceSummaryDocs(r.Context(), uid, wsID)
		if err == nil {
			result["shareDocCsv"] = filepath.Base(csvPath)
			result["shareDocMd"] = filepath.Base(mdPath)
			result["message"] = "飞书推送不可用或无待推发票；已生成共享汇总文档（见发票页汇总卡片）"
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// handleEmailInvoiceSummary — GET /api/emails/invoices/summary
// 生成（或刷新）共享汇总文档，返回列表行 + 合计金额 + 文件名。
func (s *Server) handleEmailInvoiceSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	p := s.ensurePipeline()
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, "email pipeline not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	invoices, err := s.emailStore.ListInvoicesScoped(r.Context(), uid, wsID, "", 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var total float64
	var downloaded, pendingCount, failed int
	rows := make([]map[string]any, 0, len(invoices))
	for _, inv := range invoices {
		total += inv.Amount
		switch inv.Status {
		case "downloaded", "filed":
			if inv.FilePath != "" {
				downloaded++
			}
		case "pending", "new":
			pendingCount++
		case "failed":
			failed++
		}
		rows = append(rows, map[string]any{
			"id": inv.ID, "category": inv.Category, "seller": inv.Seller,
			"amount": inv.Amount, "currency": inv.Currency, "invoiceNo": inv.InvoiceNo,
			"invoiceDate": inv.InvoiceDate, "status": inv.Status, "fileName": inv.FileName,
			"feishuSent": inv.FeishuSentAt > 0,
		})
	}
	csvPath, mdPath, err := p.BuildInvoiceSummaryDocs(r.Context(), uid, wsID)
	var csvName, mdName string
	if err == nil {
		csvName = filepath.Base(csvPath)
		mdName = filepath.Base(mdPath)
	}
	// 飞书可用时同时给一份可分享的共享台账链接（需求：建立共享文档及文件，
	// 整理一个列表并汇总金额）。发布失败不阻断汇总接口。
	ledgerURL, ledgerErr := p.PublishLedgerScoped(r.Context(), uid, wsID)
	if ledgerErr != nil {
		log.Printf("[email/summary] publish feishu ledger: %v", ledgerErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":       len(invoices),
		"amountTotal": total,
		"downloaded":  downloaded,
		"pending":     pendingCount,
		"failed":      failed,
		"rows":        rows,
		"shareDocCsv": csvName,
		"shareDocMd":  mdName,
		"shareDocUrl": ledgerURL,
	})
}

