package server

// server_email_invoice.go — 邮件发票自动整理 HTTP handlers。
//
// 路由（server.go 注册）：
//   GET    /api/emails/invoices          列表（?status=new|filed&limit=）
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
		list, err := s.emailStore.ListInvoicesScoped(r.Context(), userID, wsID, status, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list == nil {
			list = []email.Invoice{}
		}
		// 汇总统计（本期金额），前端头部展示用
		var total float64
		var filed int
		for _, inv := range list {
			if inv.Status == "filed" {
				filed++
			}
			total += inv.Amount
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"invoices": list,
			"total":    len(list),
			"filed":    filed,
			"amount":   total,
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
		// {id}/file（单张发票 PDF 下载）
		s.handleEmailInvoiceFile(w, r, strings.TrimSuffix(rest, "/file"))
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
				if row, gerr := s.emailStore.GetEmailByID(ctx, e.ID); gerr == nil && row != nil &&
					row.BodyPath != "" && row.AccountID == e.AccountID {
					if body, berr := s.readCachedEmailBody(ctx, row.ID, row.UID); berr == nil && len(body) > 0 {
						if inv, hit = email.ExtractInvoice(e, string(body)); hit {
							log.Printf("[email/invoice] body-enhanced extraction email=%s", e.ID)
						}
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
	saved, err := s.emailStore.UpsertInvoice(r.Context(), inv, userID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matched": true, "invoice": saved})
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
