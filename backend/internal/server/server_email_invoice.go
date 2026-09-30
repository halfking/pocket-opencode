package server

// server_email_invoice.go — 邮件发票自动整理 HTTP handlers。
//
// 路由（server.go 注册）：
//   GET    /api/emails/invoices          列表（?status=&limit=&offset=；按收到日期倒排）
//   POST   /api/emails/invoices/extract  对指定邮件做一次规则提取 {emailId}
//   PATCH  /api/emails/invoices/{id}     归档状态 {status: new|filed}
//   DELETE /api/emails/invoices/{id}     删除记录（不影响邮件）
//
// 自动提取：classifyEmailsAsync 分类完成后对 bill 类邮件自动尝试（见
// server_assistant.go），kxmemory 未配置时前端也可手动触发 extract。

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

func (s *Server) handleEmailInvoices(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		limit := atoiSafe(strings.TrimSpace(r.URL.Query().Get("limit")))
		offset := atoiSafe(strings.TrimSpace(r.URL.Query().Get("offset")))
		page, err := s.emailStore.ListInvoicesPage(r.Context(), userID, wsID, status, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if page.Invoices == nil {
			page.Invoices = []email.Invoice{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"invoices": page.Invoices,
			"total":    page.Total,
			"filed":    page.Filed,
			"amount":   page.Amount,
			"hasMore":  page.HasMore,
			"offset":   offset,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET only")
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
		if n > 100000 {
			return 100000
		}
	}
	return n
}

func (s *Server) handleEmailInvoiceDispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/emails/invoices/")
	switch {
	case rest == "extract":
		s.handleEmailInvoiceExtract(w, r)
	case rest == "harvest":
		s.handleEmailInvoiceHarvest(w, r)
	case rest == "export":
		s.handleEmailInvoiceExport(w, r)
	case rest == "push":
		s.handleEmailInvoicePush(w, r)
	case rest == "summary":
		s.handleEmailInvoiceSummary(w, r)
	case strings.HasPrefix(rest, "export/"):
		// export/download?file=...（导出文件下载）
		s.handleEmailInvoiceExportDownload(w, r)
	case strings.HasSuffix(rest, "/file"):
		// {id}/file（单张发票文件下载/预览）
		s.handleEmailInvoiceFile(w, r, strings.TrimSuffix(rest, "/file"))
	case strings.HasSuffix(rest, "/thumb"):
		s.handleEmailInvoiceThumb(w, r, strings.TrimSuffix(rest, "/thumb"))
	default:
		s.handleEmailInvoiceOps(w, r)
	}
}

// extractInvoicesAsync 对一批邮件做规则提取并幂等落库（异步调用，fire-and-forget）。
// 已有发票记录的邮件直接跳过；kxmemory 未配置时本函数是发票整理的唯一自动入口。
// 主题+摘要命中关键词但提取失败时，追加读缓存正文（已下载过的邮件才有）做二次提取。
func (s *Server) extractInvoicesAsync(emails []email.Email, userID, workspaceID string) {
	if s.emailStore == nil || len(emails) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// 缓存正文解密依赖主密钥与数据目录；未配置时退化为纯主题+摘要提取
		bodyEnhance := s.emailCrypto != nil && s.dataDir != ""
		extracted := 0
		for i := range emails {
			e := emails[i]
			if _, err := s.emailStore.GetInvoiceByEmailID(ctx, e.ID); err == nil {
				continue // 已提取过
			}
			inv, hit := email.ExtractInvoice(e, "")
			if !hit && bodyEnhance && email.InvoiceCandidate(e) {
				// 正文增强以 DB 权威 body_path 为门槛：客户端推送的 Email 结构体
				// BodyPath 恒空（json:"-"），必须重载落库行确认缓存确由服务端写入，
				// 防止伪造 email ID 命中他人已删邮件的残留缓存（跨租户读取）。
				var body []byte
				if row, gerr := s.emailStore.GetEmailByID(ctx, e.ID); gerr == nil && row != nil &&
					row.BodyPath != "" && row.AccountID == e.AccountID {
					if b, berr := s.readCachedEmailBody(ctx, row.ID, row.UID); berr == nil {
						body = b
					}
				}
				// 无缓存时主动拉 IMAP 原文：服务端 IMAP 抓取只落 envelope（snippet
				// 空、金额/发票号在正文），不拉原文则关键词候选永远提取不到金额。
				// 与手动提取端点（handleEmailInvoiceExtract）的 raw fetch fallback 同路径。
				if len(body) == 0 && s.emailFetcher != nil {
					if raw, ferr := s.emailFetcher.FetchMessageRaw(ctx, e.AccountID, e.UID); ferr == nil {
						if parsed, perr := email.ParseMIMEMessage(raw); perr == nil {
							body = []byte(parsed.TextBody + "\n" + parsed.HTMLBody)
						}
					}
				}
				if len(body) > 0 {
					if inv, hit = email.ExtractInvoice(e, string(body)); hit {
						log.Printf("[email/invoice] body-enhanced extraction email=%s", e.ID)
					}
				}
			}
			if !hit {
				continue
			}
			if _, err := s.emailStore.UpsertInvoice(ctx, inv, userID, workspaceID); err != nil {
				continue
			}
			extracted++
		}
		if extracted > 0 {
			log.Printf("[email/invoice] auto-extracted %d invoices (user=%s ws=%s)", extracted, userID, workspaceID)
			// 通知前端发票页刷新
			s.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: userID}, "email.invoice.extracted", map[string]any{
				"count": extracted,
			})
		}
	}()
}

func (s *Server) handleEmailInvoiceExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	var body struct {
		EmailID string `json:"emailId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EmailID == "" {
		writeError(w, http.StatusBadRequest, "emailId required")
		return
	}

	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	e, err := s.emailStore.GetEmailByID(r.Context(), body.EmailID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if e == nil {
		writeError(w, http.StatusNotFound, "email not found")
		return
	}
	// 邮件账户必须属于当前用户/工作区，防止跨工作区提取。
	if acc, _, aerr := s.emailStore.GetAccountByIDScoped(r.Context(), e.AccountID, userID, wsID); aerr != nil || acc == nil {
		writeError(w, http.StatusNotFound, "email not found")
		return
	}

inv, hit := email.ExtractInvoice(*e, "")
	log.Printf("[email/extract] enter email=%s uid=%d first_hit=%v fetcherNil=%v", e.ID, e.UID, hit, s.emailFetcher == nil)
	if !hit {
		// 摘要/主题没命中时拉正文：先 AES-GCM 缓存，无缓存主动从 IMAP
		// 拉整封原文（避免「Sync 没落 BODY[TEXT]」导致规则提取永远失败）。
		var bodyBytes []byte
		if b, berr := s.readCachedEmailBody(r.Context(), e.ID, e.UID); berr == nil && len(b) > 0 {
			bodyBytes = b
			log.Printf("[email/extract] cache hit email=%s bytes=%d", e.ID, len(b))
		}
		if len(bodyBytes) == 0 && e.UID > 0 && s.emailFetcher != nil {
			log.Printf("[email/extract] raw fetch fallback email=%s account=%s uid=%d fetcherNil=%v", e.ID, e.AccountID, e.UID, s.emailFetcher == nil)
			if raw, ferr := s.emailFetcher.FetchMessageRaw(r.Context(), e.AccountID, e.UID); ferr == nil {
				log.Printf("[email/extract] raw fetch ok bytes=%d", len(raw))
				if parsed, perr := email.ParseMIMEMessage(raw); perr == nil {
					bodyBytes = []byte(parsed.TextBody + "\n" + parsed.HTMLBody)
					log.Printf("[email/extract] parsed textLen=%d htmlLen=%d", len(parsed.TextBody), len(parsed.HTMLBody))
				} else {
					log.Printf("[email/extract] parse err=%v", perr)
				}
			} else {
				log.Printf("[email/extract] raw fetch err=%v", ferr)
			}
		}
		if len(bodyBytes) > 0 {
			inv, hit = email.ExtractInvoice(*e, string(bodyBytes))
			log.Printf("[email/extract] re-extract hit=%v", hit)
		}
	}
	if !hit {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false, "message": "未识别到发票/账单信息"})
		return
	}
	// 命中了但没有开票日期：IMAP 路径只落 envelope，正文里的「开票日期」看不到，
	// 规范文件名会退化成下载当天。补读一次正文（只针对这一封，不批量外呼）。
	if inv.InvoiceDate == "" && e.UID > 0 && s.emailFetcher != nil {
		if raw, ferr := s.emailFetcher.FetchMessageRaw(r.Context(), e.AccountID, e.UID); ferr == nil {
			if parsed, perr := email.ParseMIMEMessage(raw); perr == nil {
				text := parsed.TextBody
				if text == "" {
					text = parsed.HTMLBody
				}
				if d := email.ParseInvoiceDate(text); d != "" {
					inv.InvoiceDate = d
					log.Printf("[email/extract] backfilled invoice date=%s email=%s", d, e.ID)
				}
			}
		}
	}
	saved, err := s.emailStore.UpsertInvoice(r.Context(), inv, userID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matched": true, "invoice": saved})
}

// handleEmailInvoiceHarvest — POST /api/emails/invoices/harvest {ids?: []}
// 只下载发票文件（附件 / 正文链接 / XML 重渲染），**不做任何邮箱写操作**。
//
// 为什么必须有这个端点：手动「提取」只建档不下载，下载逻辑一直挂在
// Pipeline.Run 里；而流水线第 2 步会把广告邮件 MOVE 进真实邮箱的垃圾箱。
// 于是「我想现在就把这张发票的文件拿到手」在真实邮箱上只能靠整条流水线 ——
// 为了一个文件去改动真实邮箱，这不是一个可接受的入口。
//
// ids 省略 = 抓该 scope 下全部待下载（status 为 new/pending）的发票。
func (s *Server) handleEmailInvoiceHarvest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if s.emailFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "email fetcher not configured (IMAP unavailable)")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var invoices []email.Invoice
	if len(body.IDs) > 0 {
		got, err := s.emailStore.ListInvoicesByIDScoped(r.Context(), body.IDs, uid, wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		invoices = got
	} else {
		got, err := s.emailStore.ListInvoicesScoped(r.Context(), uid, wsID, "", 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, inv := range got {
			if inv.Status == "new" || inv.Status == "pending" || inv.FilePath == "" {
				invoices = append(invoices, inv)
			}
		}
	}
	if len(invoices) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"processed": 0, "message": "没有待下载的发票"})
		return
	}

	harvester := s.ensureInvoiceHarvester()
	if harvester == nil {
		writeError(w, http.StatusServiceUnavailable, "invoice harvester not configured")
		return
	}
	// 必须有超时：采集要连真实 IMAP 拉原文、再按需下载附件链接。
	// 直接用 r.Context() 的话，服务商一慢（实测 QQ 邮箱单封 >90s），
	// HTTP 请求会一直挂着，前端按钮转圈到天荒地老。流水线那边有 15 分钟
	// 上限，这里给 5 分钟——单张发票远够用。
	runCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res := harvester.HarvestInvoices(runCtx, invoices)
	log.Printf("[email/harvest] manual harvest user=%s ws=%s invoices=%d result=%+v", uid, wsID, len(invoices), res)
	writeJSON(w, http.StatusOK, map[string]any{
		"processed": res.Processed,
		"result":    res,
	})
}

func (s *Server) handleEmailInvoiceOps(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/emails/invoices/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing invoice id")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Status == "" {
			writeError(w, http.StatusBadRequest, "status required (new|filed)")
			return
		}
		if err := s.emailStore.SetInvoiceStatusScoped(r.Context(), id, userID, wsID, body.Status); err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "invoice not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": body.Status})
	case http.MethodDelete:
		if err := s.emailStore.DeleteInvoiceScoped(r.Context(), id, userID, wsID); err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "invoice not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodGet:
		inv, err := s.emailStore.GetInvoiceByIDScoped(r.Context(), id, userID, wsID)
		if err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "invoice not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, inv)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PATCH/DELETE only")
	}
}


