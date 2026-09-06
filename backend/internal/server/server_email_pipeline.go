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
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// ensurePipeline 惰性构造流水线（单例）。依赖缺失时返回 nil。
func (s *Server) ensurePipeline() *email.Pipeline {
	s.emailPipelineOnce.Do(func() {
		if s.emailStore == nil || s.emailFetcher == nil || s.dataDir == "" {
			return
		}
		font := email.FindChineseFont(s.dataDir)
		harvester := &email.InvoiceHarvester{
			Store:   s.emailStore,
			Fetcher: s.emailFetcher,
			DataDir: s.dataDir,
			XMLRenderer: func(name string, inv *email.Invoice, xmlRaw []byte) ([]byte, error) {
				return email.RenderInvoiceXMLPDF(font, inv, xmlRaw)
			},
		}
		if font == "" {
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
			DataDir:  s.dataDir,
		}
		if !pusher.Available() {
			log.Printf("[email/pipeline] feishu pusher 未配置（POCKET_FEISHU_APP_ID/SECRET/INVOICE_CHAT_ID），发票将走共享汇总文档路径")
		}
	})
	return s.emailPipeline
}

// RunEmailPipeline 供 scheduler 定时调用（或调试）。执行位置按配置决定。
func (s *Server) RunEmailPipeline(ctx context.Context) *email.PipelineReport {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.EmailExecutionMode))
	if mode == "server" && strings.TrimSpace(s.cfg.EmailServerPipelineURL) != "" {
		return s.delegatePipeline(ctx)
	}
	p := s.ensurePipeline()
	if p == nil {
		return &email.PipelineReport{Errors: []string{"email pipeline not configured"}}
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	return p.Run(runCtx)
}

// delegatePipeline 把流水线执行委托给远端编排服务（server 模式）。
func (s *Server) delegatePipeline(ctx context.Context) *email.PipelineReport {
	client := &http.Client{Timeout: 16 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.EmailServerPipelineURL, nil)
	if err != nil {
		return &email.PipelineReport{Errors: []string{"delegate: " + err.Error()}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return &email.PipelineReport{Errors: []string{"delegate: " + err.Error()}}
	}
	defer resp.Body.Close()
	var rep email.PipelineReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return &email.PipelineReport{Errors: []string{"delegate decode: " + err.Error()}}
	}
	return &rep
}

// handleEmailPipelineRun — POST /api/email/pipeline/run
func (s *Server) handleEmailPipelineRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	rep := s.RunEmailPipeline(r.Context())
	writeJSON(w, http.StatusOK, rep)
}

// handleEmailInvoiceFile — GET /api/emails/invoices/{id}/file
// 下载采集好的发票 PDF（workspace 隔离：发票必须属于当前用户）。
func (s *Server) handleEmailInvoiceFile(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	inv, err := s.emailStore.GetInvoiceByIDScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		if err == email.ErrNotFound {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inv.FilePath == "" {
		writeError(w, http.StatusNotFound, "invoice file not harvested yet")
		return
	}
	abs := filepath.Join(s.dataDir, inv.FilePath)
	// 防路径逃逸：FilePath 来自库内数据，仍校验解析后仍在数据目录下
	if !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(s.dataDir)+string(filepath.Separator)) {
		writeError(w, http.StatusBadRequest, "invalid file path")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", inv.FileName))
	http.ServeFile(w, r, abs)
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
	outPath, err := email.ExportInvoiceGrid(outDir, files, body.Grid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 记录导出时间 + 通知前端刷新
	now := time.Now().Unix()
	for _, id := range exportedIDs {
		_ = s.emailStore.MarkInvoiceExported(r.Context(), id, uid, wsID, now)
	}
	if s.wsHub != nil {
		s.wsHub.BroadcastToUser(uid, "email.invoices.exported", map[string]any{
			"file": filepath.Base(outPath), "count": len(exportedIDs), "grid": body.Grid,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":  filepath.Base(outPath),
		"count": len(exportedIDs),
		"grid":  body.Grid,
		"url":   "/api/emails/invoices/export/download?file=" + filepath.Base(outPath),
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
		// 兜底：生成共享汇总文档（CSV + Markdown，含合计金额）
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
	writeJSON(w, http.StatusOK, map[string]any{
		"count":       len(invoices),
		"amountTotal": total,
		"downloaded":  downloaded,
		"pending":     pendingCount,
		"failed":      failed,
		"rows":        rows,
		"shareDocCsv": csvName,
		"shareDocMd":  mdName,
	})
}
