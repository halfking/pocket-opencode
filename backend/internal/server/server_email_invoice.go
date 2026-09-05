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
	if rest == "extract" {
		s.handleEmailInvoiceExtract(w, r)
		return
	}
	s.handleEmailInvoiceOps(w, r)
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
				if body, berr := s.readCachedEmailBody(ctx, e.ID, e.UID); berr == nil && len(body) > 0 {
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
	if !hit {
		// 摘要没命中时尝试已缓存的正文（解密失败按未命中处理）
		if bodyBytes, berr := s.readCachedEmailBody(r.Context(), e.ID, e.UID); berr == nil && len(bodyBytes) > 0 {
			inv, hit = email.ExtractInvoice(*e, string(bodyBytes))
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
