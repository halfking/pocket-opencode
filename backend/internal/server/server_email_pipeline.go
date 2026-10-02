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
	// 合计已经按币种写进 rows 里（多币种时每币种一行），所以这里不需要
	// 也不应该再取一个总额出来。LedgerRows 的第二个返回值是 []CurrencyTotal，
	// 需要按币种展示时从它取，别自己把各币种加起来——那不是金额。
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
	// 收件人必须解析出来，否则这封提醒有两个**无声**的坏结局：
	//
	//  1. UserID 为空时 notifycenter 的 websocket 通道退化成**工作区全体广播**
	//     （WebsocketSender.Send 的注释写明「无 user_id 退化为全局广播」）——
	//     别人邮箱里的重要邮件会推给同 workspace 所有在线用户；
	//  2. Dispatch 本身不报错，流水线的 notifyImportant 于是把这封记进
	//     notified_at ＝「已提醒」。上面那条广播是一次的，这一条是**永久**的：
	//     提醒再也不会重发。
	//
	// 所以解析不出归属就返回错误：流水线会把它记进 rep.Errors 且**不**写
	// notified_at，下一轮还有机会重试。
	if n.store == nil {
		return fmt.Errorf("notify important email=%s: email store not configured", e.ID)
	}
	acc, _, err := n.store.GetAccountByID(ctx, e.AccountID)
	if err != nil {
		return fmt.Errorf("notify important email=%s: resolve account %s: %w", e.ID, e.AccountID, err)
	}
	if acc == nil || acc.UserID == "" {
		return fmt.Errorf("notify important email=%s: account %s has no user owner", e.ID, e.AccountID)
	}
	// 主题为空时不要再套一层前缀（否则标题变成「重要邮件：重要邮件」）。
	title := "重要邮件"
	if subject := strings.TrimSpace(e.Subject); subject != "" {
		title = "重要邮件：" + subject
	}
	_, err = n.svc.Dispatch(ctx, notifycenter.Event{
		WorkspaceID: e.WorkspaceID,
		UserID:      acc.UserID,
		Source:      "email",
		Kind:        "email.important",
		Title:       title,
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

// harvesterBodyCache 取采集器的原文缓存，好让流水线第 2 趟与采集器共用同一份。
// harvester 为 nil（依赖不齐）时返回 nil，流水线会按「无缓存」处理。
func harvesterBodyCache(h *email.InvoiceHarvester) email.BodyCache {
	if h == nil {
		return nil
	}
	return h.BodyCache
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
		p := &email.Pipeline{
			Store:    s.emailStore,
			Fetcher:  s.emailFetcher,
			Harvest:  harvester,
			// 与采集器共用**同一个** BodyCache 实例：第 2 趟的取原文和
			// harvestOne 的取原文必须走同一条 POP3 感知路径（email/raw_body_resolve.go）。
			// 此前第 2 趟直接调 IMAP-only 的 FetchMessageRaw，于是 POP3 来源的
			// 发票候选永远取不到原文、建不了档（实测两封通行费电子发票
			// 24.61 元，而它们的原文就在 email-bodies-raw/ 里）。
			BodyCache: harvesterBodyCache(harvester),
			Pusher:   pusher,
			Notifier: notifier,
			Ledger:   &feishuLedgerPublisher{client: pusher.client, folderToken: s.cfg.FeishuInvoiceFolderToken},
			DataDir:  s.dataDir,
			// 默认预演：清垃圾会 IMAP MOVE 真实邮件，规则没在真实邮箱上验证过，
			// 无人值守地搬用户邮件风险太大。置 POCKET_EMAIL_SPAM_DRYRUN=false 才真移。
			SpamDryRun: s.cfg.EmailSpamDryRun,
		}
		// 第 1.6 步分类。默认不注入 Classifier ⇒ 整步跳过，报告里记 ClassifySkip。
		// 需求 4 的提醒依赖 importance，而 importance 只在这里被写入。
		if s.cfg.EmailClassifyViaGateway {
			p.Classifier = s.classifyViaGatewayBatch
		}
		logClassifyWiring(s.cfg.EmailClassifyViaGateway)
		s.emailPipeline = p
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
//
// 这里是**唯一**加跨进程互斥锁的地方，而且是刻意只加在这里：
//   - 定时路径（scheduler 的每日 8 点）会在多实例下同时进来，进程内的
//     emailPipelineMu 拦不住它们。重复跑的代价是重复推送——重要邮件提醒
//     的已通知标记在整个推送循环跑完之后才写，而 notifications 表除主键外
//     没有唯一约束，于是同一封邮件被推 N 份。
//   - 手工路径（handleEmailPipelineRun）直接调 runEmailPipeline，**不**走这里，
//     所以用户显式点"跑一次"不会被另一轮挡住。
func (s *Server) RunEmailPipeline(ctx context.Context) *email.PipelineReport {
	if s.cfg.EmailPipelineAdvisoryLock {
		release, state, err := s.emailStore.TryLockDailyPipeline(ctx)
		switch state {
		case email.DailyPipelineLockBusy:
			log.Printf("[email/pipeline] 每日定时流水线跨进程锁已被其它实例持有，本轮跳过")
			return &email.PipelineReport{
				Errors: []string{"daily pipeline already running in another instance; skipped"},
			}
		case email.DailyPipelineLockAcquired:
			// 只有真的拿到锁才 defer release。
			//
			// 这里必须按 state 分派而不是写 default: err==nil 且非 Busy 时
			// release 可能是 nil（锁机制不可用），`defer release()` 会在函数
			// 返回时 nil-pointer panic —— 而这个 goroutine 是 scheduler 的，
			// 未捕获的 panic 会直接终止整个 pocketd 进程。
			defer release()
		default:
			// Unavailable：没有连接池，或取连接/查询失败。**不是**"别人在跑"。
			// 必须降级照跑：若也跳过，一次数据库抖动就会让每日流水线永久
			// 静默，而日志只会写"跳过"，看不出真实原因。
			if err != nil {
				log.Printf("[email/pipeline] 每日定时流水线跨进程锁不可用（%v），本轮降级为直接执行", err)
			} else {
				log.Printf("[email/pipeline] 每日定时流水线跨进程锁不可用（无数据库连接池），本轮降级为直接执行")
			}
		}
	}
	return s.runEmailPipeline(ctx, nil)
}

// shouldDelegatePipeline 判定本轮是否委托远端编排服务（需求 6）。
//
// 需求 6：「这些操作可以在设备本地进行，也可以委托服务端进行，**默认放在
// 设备本地进行**」。所以只有显式配成 server **且**给了远端 URL 才委托：
//   - mode 为空/其它值/大小写不同/带空白 → 本地（默认）
//   - mode=server 但 URL 为空 → 本地（配了却没法委托，落回本地而不是报错）
//
// 抽成纯函数是为了能脱离 Server 测这条「默认本地」契约——它原先埋在
// runEmailPipeline 里，没有测试守护：有人把判断改成 `mode != ""` 就静默
// 变成「默认委托」，而带邮箱权限的流水线会被 POST 到远端。
func shouldDelegatePipeline(executionMode, serverPipelineURL string) bool {
	mode := strings.ToLower(strings.TrimSpace(executionMode))
	return mode == "server" && strings.TrimSpace(serverPipelineURL) != ""
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

	if shouldDelegatePipeline(s.cfg.EmailExecutionMode, s.cfg.EmailServerPipelineURL) {
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
	var downloaded, pendingCount, failed int
	// 合计**按币种分组**。此前这里是裸 `total += inv.Amount`，把 USD 与 CNY
	// 直接相加后放进 amountTotal —— 跨币种的算术和不是金额。
	// 这条规则在仓库里已有三处实现（LedgerRows、WriteInvoiceSummaryDocs、
	// InvoiceListStats），三处都配了用例；那三次修复的审计范围只在 internal/email，
	// server 层这处手写求和没被看到。它是第四处，也是唯一一处直接把标量交给前端。
	//
	// 行为与列表端点 handleEmailInvoices 保持一致：单一币种时 amountTotal 可用，
	// 混入多币种时它是 0 且 amounts 非空 —— 给一个「看起来正常」的标量会直接
	// 误导（前端会把它渲染成 ¥）。
	//
	// 【合并后的叠加】先按「只统计真正拿到文件的发票」筛出 counted，再对它按币种
	// 分组。两个修复解决的是**不同**问题，都要成立：
	//   - 只统计已下载且 FilePath 非空：库里存在 status=failed 却残留脏字段的记录
	//     （两张 QQ Wallet：seller="name:"、invoiceNo="Issuance"，字段是从邮件错误
	//     段落抽出来的，见 handoff §7o），金额当时恰好是 0 才没出事。将来某张 failed
	//     发票若带着错误抽取的非零金额，就会被静默算进总额，让对账虚高且无处提示。
	//     判定与紧邻的 downloaded 计数完全对齐 —— 界面上「已下载 N 张」和「合计 X 元」
	//     指的是同一批发票，否则两个数字会互相矛盾。
	//   - 按币种分组：跨币种的算术和不是金额。
	var counted []email.Invoice
	rows := make([]map[string]any, 0, len(invoices))
	for _, inv := range invoices {
		switch inv.Status {
		case "downloaded", "filed":
			if inv.FilePath != "" {
				downloaded++
				counted = append(counted, inv)
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
	amounts := email.SumByCurrency(counted)
	var total float64
	var totalCurrency string
	if len(amounts) == 1 {
		total = amounts[0].Amount
		totalCurrency = amounts[0].Currency
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
		"count": len(invoices),
		// amountTotal 仅在**单一币种**时有意义（多币种时为 0 且 amounts 非空）。
		// 前端绝不能把跨币种的数渲染成 ¥。
		"amountTotal": total,
		"currency":    totalCurrency,
		"amounts":     amounts,
		"downloaded":  downloaded,
		"pending":     pendingCount,
		"failed":      failed,
		"rows":        rows,
		"shareDocCsv": csvName,
		"shareDocMd":  mdName,
		"shareDocUrl": ledgerURL,
	})
}
