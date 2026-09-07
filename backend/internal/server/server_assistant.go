package server

// server_assistant.go — Phase 0 个人助理模块的 HTTP handler 骨架。
//
// 这些 handler 解决审计问题 #1（0 路由接入）：路由全部注册，store 全部
// 可达。业务逻辑（IMAP 抓取、kxmemory AI 调用、原生插件桥接）在 Phase 2/3/4
// 填充；当前每个 handler 至少能做基本的 store CRUD 或返回明确的未配置提示，
// 确保端到端骨架可运行、可测试。

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/finance"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	ws "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// ---- 公共辅助 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeKxmemoryError 把 kxmemory.Error 翻译为前端可读的结构化错误响应。
//
// 状态码映射：
//   - transient（可重试：5xx / 网络 / 超时）→ 503 Service Unavailable + retryable=true
//   - permanent（不可重试：4xx / JSON decode）→ 502 Bad Gateway + retryable=false
//
// 同时保留 `error` 字段做向后兼容；新增 `code` 和 `retryable` 让前端能精确判断。
func writeKxmemoryError(w http.ResponseWriter, err error) {
	var kxe *kxmemory.Error
	if !errors.As(err, &kxe) {
		writeError(w, http.StatusBadGateway, "kxmemory: "+err.Error())
		return
	}
	status := http.StatusBadGateway
	if kxe.Retryable() {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":     kxe.Error(),
		"code":      kxe.Code,
		"retryable": kxe.Retryable(),
	})
}

// userIDFromRequest 提取当前请求的用户 ID。
func (s *Server) userIDFromRequest(r *http.Request) string {
	if c := s.claimsFromRequest(r); c != nil && c.UserID != "" {
		return c.UserID
	}
	return "local"
}

// =====================================================================
// 认证
// =====================================================================

// handleAuthLogin — Phase 0 真实 JWT 登录入口。
//
// 一期切换后，三条登录路径按优先级：
//  1. RedClaw Admin 已配置（生产默认）：把 username/password 透传到
//     RedClaw /api/v1/auth/login,拿到 RedClaw 颁发的 HS256 JWT 后原样
//     回给前端（前端存进 localStorage 的 pocket_token）。
//  2. RedClaw 不可达 + POCKET_DEV_AUTH=true（开发/故障恢复）：
//     走本地 dev 旁路（admin / Veritrans&9527），本地 jwtSigner 签发。
//  3. legacy 模式（POCKET_AUTH_LEGACY_ONLY=true）：直接走原本地 users 表。
//
// 兼容性：legacy/dev 模式仍保留 EnsureDefaultWorkspace + RecordShadow
// 副作用（workspace 影子表维护）。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// 路径 1：RedClaw Admin 代理（一期主路径）
	if s.redclawAdminClient != nil {
		res, err := s.redclawAdminClient.Login(r.Context(), body.Username, body.Password)
		if err != nil {
			// RedClaw 不可达（网络/5xx/超时）+ dev 旁路开启 → 降级到本地
			// dev 登录，保证 RedClaw 故障时开发/故障恢复环境不被锁死。
			// 401/403（凭据错误）不降级——那是真实的认证失败。
			if errors.Is(err, redclaw.ErrRedClawUnavailable) && s.cfg.DevAuth {
				log.Printf("WARN: redclaw unavailable (%v); falling back to dev bypass", err)
				if u, ok := s.devBypassCredentials(body.Username, body.Password); ok {
					s.devPathIssueToken(w, r, u.id, u.role, u.username, u.email)
					return
				}
				writeError(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			s.handleRedClawAuthError(w, err, "redclaw login failed")
			return
		}
		// RedClaw 返回的 token 直接透传；user_id / role 从 employee 抽取
		userID := body.Username
		role := "user"
		displayName := body.Username
		email := ""
		if res.Employee != nil {
			if res.Employee.ID != "" {
				userID = res.Employee.ID
			}
			if res.Employee.Role != "" {
				role = res.Employee.Role
			}
			if res.Employee.Name != "" {
				displayName = res.Employee.Name
			}
			email = res.Employee.Email
		}
		// workspace_id：openpocket 仍按用户 ID 维护一个影子 workspace
		wsID := s.ensureWorkspaceForRedClawUser(r.Context(), userID)
		auth.RecordShadow("redclaw", userID, wsID, displayName, email)
		writeJSON(w, http.StatusOK, map[string]any{
			"token":        res.Token,
			"user":         displayName,
			"user_id":      userID,
			"workspace_id": wsID,
			"role":         role,
			"auth_method":  "redclaw",
			"must_change":  res.MustChangePassword,
		})
		return
	}

	// 路径 2：dev 旁路（仅当 POCKET_DEV_AUTH=true）。
	// 未命中 dev 凭据时不在此返回 401——继续尝试路径 3 的本地 users 表，
	// 否则 dev 模式下 /api/auth/register 注册的用户将永远无法登录
	//（2026-09-07 E2E 发现：register 200 → login 401）。
	if s.cfg.DevAuth {
		if u, ok := s.devBypassCredentials(body.Username, body.Password); ok {
			s.devPathIssueToken(w, r, u.id, u.role, u.username, u.email)
			return
		}
	}

	// 路径 3：legacy 模式（直接读本地 users 表）
	if s.userStore != nil {
		u, err := s.userStore.VerifyPassword(r.Context(), body.Username, body.Password)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		s.legacyPathIssueToken(w, r, u.ID, u.Role, u.Username, u.Email)
		return
	}

	writeError(w, http.StatusUnauthorized, "invalid credentials")
}

// devCreds 是 dev 旁路匹配到的账号信息。
type devCreds struct {
	id       string
	role     string
	username string
	email    string
}

// devBypassCredentials 按 POCKET_AUTH_USER/PASS（缺省 admin / Veritrans&9527）
// constant-time 校验 dev 旁路凭据。仅当 POCKET_DEV_AUTH=true 时由调用方触发。
func (s *Server) devBypassCredentials(username, password string) (devCreds, bool) {
	devUser := s.cfg.DevAuthUser
	if devUser == "" {
		devUser = "admin"
	}
	devPass := s.cfg.DevAuthPass
	if devPass == "" {
		// 未显式配置密码时给出一次性告警,便于审计发现默认凭据在用。
		devPass = "Veritrans&9527"
		log.Printf("WARN: POCKET_AUTH_PASS not set; using built-in dev default password")
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(devUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(devPass)) == 1 {
		return devCreds{id: "user-admin", role: "admin", username: "admin"}, true
	}
	return devCreds{}, false
}

// ensureWorkspaceForRedClawUser 为 RedClaw 用户在 openpocket 影子表里
// 创建一个默认 workspace（幂等）。失败降级到 "default" 字面量。
func (s *Server) ensureWorkspaceForRedClawUser(ctx context.Context, userID string) string {
	if s.identityStore == nil {
		return "default"
	}
	ws, err := s.identityStore.EnsureDefaultWorkspace(ctx, userID)
	if err != nil {
		log.Printf("WARN: EnsureDefaultWorkspace for %s failed: %v (falling back to 'default')", userID, err)
		return "default"
	}
	if ws != nil && ws.ID != "" {
		return ws.ID
	}
	return "default"
}

// devPathIssueToken 用本地 jwtSigner 颁发一个临时 token（仅 dev 旁路）。
func (s *Server) devPathIssueToken(w http.ResponseWriter, r *http.Request, userID, role, username, email string) {
	if s.jwtSigner == nil {
		writeError(w, http.StatusInternalServerError, "JWT signer not configured")
		return
	}
	wsID := s.ensureWorkspaceForRedClawUser(r.Context(), userID)
	tok, err := s.jwtSigner.SignWithWorkspace(userID, role, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign JWT")
		return
	}
	auth.RecordShadow("pocket", userID, wsID, username, email)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        tok,
		"user":         username,
		"user_id":      userID,
		"workspace_id": wsID,
		"role":         role,
		"auth_method":  "dev-bypass",
	})
}

// legacyPathIssueToken 用本地 jwtSigner 颁发一个 token（legacy 模式）。
func (s *Server) legacyPathIssueToken(w http.ResponseWriter, r *http.Request, userID, role, username, email string) {
	if s.jwtSigner == nil {
		writeError(w, http.StatusInternalServerError, "JWT signer not configured")
		return
	}
	wsID := s.ensureWorkspaceForRedClawUser(r.Context(), userID)
	tok, err := s.jwtSigner.SignWithWorkspace(userID, role, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign JWT")
		return
	}
	auth.RecordShadow("pocket", userID, wsID, username, email)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        tok,
		"user":         username,
		"user_id":      userID,
		"workspace_id": wsID,
		"role":         role,
		"auth_method":  "legacy",
	})
}

// handleRedClawAuthError 统一把 RedClaw admin 错误翻译为 HTTP 响应。
// 401/403 → 401 invalid credentials；tenant_mismatch → 403；
// 其它（5xx/网络/解析）→ 503 + 提示"RedClaw 不可用"，让前端展示降级。
func (s *Server) handleRedClawAuthError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, redclaw.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, redclaw.ErrTenantMismatch):
		writeError(w, http.StatusForbidden, "tenant mismatch")
	case errors.Is(err, redclaw.ErrRedClawUnavailable):
		log.Printf("WARN: %s: %v (redclaw unavailable)", fallback, err)
		writeError(w, http.StatusServiceUnavailable, "redclaw auth unavailable, please retry")
	default:
		log.Printf("WARN: %s: %v", fallback, err)
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// =====================================================================
// 语音笔记
// =====================================================================

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		domain := r.URL.Query().Get("domain")
		list, err := s.notesStore.ListScoped(r.Context(), uid, wsID, domain)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": list})
	case http.MethodPost:
		var n notes.Note
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		n.UserID = uid
		// Ownership comes from the JWT, never from the body: a client-supplied
		// workspaceId would otherwise let a caller write into another workspace.
		n.WorkspaceID = wsID
		if n.ID == "" {
			n.ID = randomID("note")
		}
		// 龙虾架构：异步触发 kxmemory AI 编排（分类/SSOT/关联/待办提取）
		if err := s.notesStore.Upsert(r.Context(), &n); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, n)
		s.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: uid}, "note.created", &n)
		// 异步调 kxmemory（非阻塞）
		go s.classifyNoteAsync(n)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

func (s *Server) handleNoteOperations(w http.ResponseWriter, r *http.Request) {
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing note id")
		return
	}
	// /api/notes/{id}/classify — manual re-classify (POST). Delegates to
	// handleNoteClassify so the rest of the switch can stay unchanged for
	// the simple GET/DELETE on a bare {id}.
	if strings.HasSuffix(id, "/classify") {
		realID := strings.TrimSuffix(id, "/classify")
		s.handleNoteClassify(w, r, realID)
		return
	}
	// /api/notes/{id}/summarize — generate AI summary (POST).
	if strings.HasSuffix(id, "/summarize") {
		realID := strings.TrimSuffix(id, "/summarize")
		s.handleNoteSummarize(w, r, realID)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		found, err := s.notesStore.GetByIDScoped(r.Context(), id, uid, wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if found == nil {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		writeJSON(w, http.StatusOK, found)
	case http.MethodDelete:
		if err := s.notesStore.DeleteScoped(r.Context(), id, uid, wsID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/DELETE only")
	}
}

// handleNoteClassify — POST /api/notes/{id}/classify
//
// Manual re-classification trigger for a single note. Unlike classifyNoteAsync
// (fire-and-forget on create), this returns the kxmemory classification
// synchronously so the front-end can render the result immediately. Requires
// kxmemory to be configured; otherwise returns 503.
func (s *Server) handleNoteClassify(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing note id")
		return
	}
	if s.kxmemory == nil {
		writeError(w, http.StatusServiceUnavailable, "kxmemory not configured")
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// Look up the note by ID with ownership verification
	found, err := s.notesStore.GetByIDScoped(r.Context(), id, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := s.kxmemory.ClassifyNote(ctx, kxmemory.ClassifyNoteRequest{
		Content:     found.Snippet,
		Title:       found.Title,
		ContentType: found.ContentType,
		Domain:      found.Domain,
		Tags:        parseTagsJSON(found.Tags),
	})
	if err != nil {
		writeKxmemoryError(w, err)
		return
	}

	// 回写分类结果到本地 notes 缓存
	found.Domain = resp.Classification.Domain
	found.Tags = toTagsJSON(resp.Classification.Tags)
	if found.Title == "" && resp.Classification.SuggestedTitle != "" {
		found.Title = resp.Classification.SuggestedTitle
	}
	if err := s.notesStore.Upsert(context.Background(), found); err != nil {
		log.Printf("[kxmemory] update note %s after classify failed: %v", found.ID, err)
	}

	s.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: found.UserID}, "note.classified", map[string]any{
		"noteId":         found.ID,
		"domain":         resp.Classification.Domain,
		"category":       resp.Classification.Category,
		"confidence":     resp.Classification.Confidence,
		"tags":           resp.Classification.Tags,
		"suggestedTitle": resp.Classification.SuggestedTitle,
	})

	writeJSON(w, http.StatusOK, resp)
}

// handleNoteSummarize — POST /api/notes/{id}/summarize
//
// 对笔记内容调用 LLM 生成总结。适用于长语音笔记转写后的精炼摘要。
// 返回 { summary: string }。需要 llmBFF 已配置。
func (s *Server) handleNoteSummarize(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	if s.llmBFF == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing note id")
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	found, err := s.notesStore.GetByIDScoped(r.Context(), id, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"请为以下笔记内容生成一段简洁的总结（3-5句话）：\n\n%s",
		found.Snippet,
	)
	req := llmbff.ChatRequest{
		WorkspaceID: wsID,
		Model:       "auto", // 智能路由
		Messages: []llmbff.Message{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   300,
		User:        uid,
	}

	resp, err := s.llmBFF.Chat(ctx, req, "note_summary")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("summarize failed: %v", err))
		return
	}

	summary := resp.Content

	// 自动记账：笔记内容命中金额/收支/类目关键词时自动入账（source=auto）。
	// 幂等：以 note:<id> 为 note_ref 幂等键，重复总结返回既有记录，不再重复入账。
	// Best-effort：记账失败不影响总结返回。
	autoTx := []*finance.Transaction{}
	bookkeeping := ""
	// 笔记内容可能已修改：本次解析与已入账记录不一致时提示前端（金额/方向以记账页为准）
	bookkeepingMismatch := false
	if s.financeStore != nil {
		if parsed := finance.NewRecognizer().Parse(found.Snippet); parsed != nil {
			noteRef := found.Title
			if noteRef == "" {
				noteRef = found.Snippet
			}
			// 按 rune 截断，避免切在多字节中间产生乱码入账
			if runes := []rune(noteRef); len(runes) > 60 {
				noteRef = string(runes[:60])
			}
			idempotencyKey := "note:" + found.ID
			// 先查幂等键：已入过账直接返回既有记录，避免重复入账
			if existing, gerr := s.financeStore.GetByNoteRefScoped(idempotencyKey, uid, wsID); gerr != nil {
				log.Printf("[note/summarize] idempotency check failed (note=%s): %v", found.ID, gerr)
			} else if existing != nil {
				autoTx = append(autoTx, existing)
				bookkeeping = "existing"
				// 金额按分精度比较，规避浮点噪声；方向不一致同样提示
				if noteBookkeepingMismatch(parsed.Type, parsed.Amount, existing) {
					bookkeepingMismatch = true
				}
			} else if tx, cerr := s.financeStore.CreateScoped(finance.CreateTransactionRequest{
				Type:     parsed.Type,
				Amount:   parsed.Amount,
				Category: parsed.Category,
				Note:     fmt.Sprintf("[笔记] %s｜%s", noteRef, parsed.Note),
				Source:   "auto",
				NoteRef:  idempotencyKey,
			}, uid, wsID); cerr != nil {
				log.Printf("[note/summarize] auto bookkeeping failed (note=%s): %v", found.ID, cerr)
			} else if tx != nil {
				// CreateScoped 内部按 note_ref 去重，是并发场景下的第二道保险
				autoTx = append(autoTx, tx)
				bookkeeping = "created"
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"summary":              summary,
		"model":                resp.Model,
		"usage":                resp.Usage,
		"transactions":         autoTx,
		"bookkeeping":          bookkeeping,
		"bookkeeping_mismatch": bookkeepingMismatch,
	})
}

// noteBookkeepingMismatch — 重新总结解析出的记账与该笔记已入账记录是否不一致
// （金额按分精度比较规避浮点噪声；收支方向不同同样算不一致）。用于前端提示
// 「笔记内容与已入账记录不一致」，由用户到记账页核对，不做静默改账。
func noteBookkeepingMismatch(parsedType string, parsedAmount float64, tx *finance.Transaction) bool {
	if tx == nil {
		return false
	}
	return math.Round(parsedAmount*100) != math.Round(tx.Amount*100) || parsedType != tx.Type
}

// handleEmailOAuthCallback — GET /callback/email/oauth
//
// 该路由由 OAuth provider（Google/Outlook）调用，不走 requireAuth；state
// 已经在 PendingOAuth 表里携带 user 信息。
func (s *Server) handleEmailOAuthCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.emailStore == nil || s.emailCrypto == nil || s.emailPending == nil {
			writeError(w, http.StatusServiceUnavailable, "email oauth not configured")
			return
		}
		email.HandleOAuthCallback(email.OAuthCallbackConfig{
			Store:               s.emailStore,
			Crypto:              s.emailCrypto,
			Pending:             s.emailPending,
			TargetedBroadcaster: s.wsHub,
		}).ServeHTTP(w, r)
	}
}

// =====================================================================
// 邮箱助手
// =====================================================================

// startEmailOAuth — POST /api/email/oauth/start
//
// 请求体：
//
//	{
//	  "providerId":  "google" | "outlook",
//	  "emailAddress": "user@example.com",
//	  "clientId":     "...",   // Google/Outlook developer console
//	  "clientSecret": "...",
//	  "redirectUri":  "http://localhost:8088/callback/email/oauth"
//	}
//
// 行为：
//  1. 校验 provider + 必填字段；
//  2. 生成 PKCE 对 + 32B state；
//  3. 写入 PendingOAuth 表（10 分钟过期）；
//  4. 返回 authorization URL，前端用浏览器/ASWebAuthenticationSession 打开。
//
// 调用方需要在 callback 完成后，再次 POST /api/email/oauth/complete（保留
// 给 Phase 2 使用）或者在 IMAP fetcher 检测到 oauth2 access token 自动
// 改写 password credential；本阶段我们只交付 start + callback。
func (s *Server) startEmailOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailPending == nil {
		writeError(w, http.StatusServiceUnavailable, "email oauth pending store not configured")
		return
	}
	if s.emailCrypto == nil {
		writeError(w, http.StatusServiceUnavailable, "email crypto not configured (set POCKET_EMAIL_MASTER_KEY)")
		return
	}
	var body struct {
		AccountID    string `json:"accountId"`
		ProviderID   string `json:"providerId"`
		EmailAddress string `json:"emailAddress"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		RedirectURI  string `json:"redirectUri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.AccountID == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	if _, _, err := s.emailStore.GetAccountByIDScoped(r.Context(), body.AccountID, s.userIDFromRequest(r), s.workspaceIDFromRequest(r)); err != nil {
		if errors.Is(err, email.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
		} else {
			writeError(w, http.StatusInternalServerError, "load account: "+err.Error())
		}
		return
	}
	provider, ok := email.LookupProviderByID(body.ProviderID)
	if !ok || !provider.SupportsOAuth2 {
		writeError(w, http.StatusBadRequest, "provider does not support OAuth2")
		return
	}
	pkce, err := email.GeneratePKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pkce: "+err.Error())
		return
	}
	state, err := email.RandomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state: "+err.Error())
		return
	}
	s.emailPending.Put(state, email.NewPendingEntryWithWorkspace(body.AccountID, s.userIDFromRequest(r), s.workspaceIDFromRequest(r), body.ProviderID, body.EmailAddress,
		pkce.Verifier, body.ClientID, body.ClientSecret, body.RedirectURI))
	authURL, err := email.BuildAuthURL(provider, body.ClientID, body.RedirectURI, state, pkce)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build auth url: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authUrl": authURL,
		"state":   state,
	})
}

func (s *Server) handleEmailAccounts(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.emailStore.ListAccountsScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r))

		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": list})
	case http.MethodPost:
		// Phase 2: 加密 credential、写库；IMAP 连通性验证交给 scheduler
		// 在首次 Sync 时做（不阻塞 POST 立即返回 201）。
		s.createEmailAccount(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// createEmailAccount 处理 POST /api/email/accounts。
//
// 请求体（所有字段语义见 email.Account）：
//
//	{
//	  "displayName": "Work Gmail",
//	  "emailAddress": "foo@gmail.com",
//	  "imapHost": "imap.gmail.com",
//	  "imapPort": 993,
//	  "authType": "password" | "oauth2",
//	  "password": "...",        // authType=password 必填
//	  "oauthToken": "...",      // authType=oauth2 必填（access token）
//	  "syncIntervalMin": 15,
//	  "rules": "...",
//	  "enabled": true
//	}
//
// 安全要点：
//   - credential 加密后入库，明文不持久化（即使 DB 泄漏也不能反解）；
//   - OAuth 流程下 access token 也走同样加密（refresh token 由 OAuth 回调
//     单独管理，不在本接口处理）；
//   - 创建后立即广播 email.account.created 事件，触发前端刷新列表。
func (s *Server) createEmailAccount(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if s.emailCrypto == nil {
		writeError(w, http.StatusServiceUnavailable, "email crypto not configured (set POCKET_EMAIL_MASTER_KEY)")
		return
	}
	var body struct {
		DisplayName     string `json:"displayName"`
		EmailAddress    string `json:"emailAddress"`
		IMAPHost        string `json:"imapHost"`
		IMAPPort        int    `json:"imapPort"`
		AuthType        string `json:"authType"` // password | oauth2
		Password        string `json:"password"`
		OAuthToken      string `json:"oauthToken"`
		SyncIntervalMin int    `json:"syncIntervalMin"`
		Rules           string `json:"rules"`
		Enabled         *bool  `json:"enabled"`
		// SMTP 出站配置，可选。缺省时账户以「未配置 SMTP」创建，
		// /test-smtp 会返回 "smtp not configured"，之后可通过 PUT 补齐。
		SMTPHost     string `json:"smtpHost"`
		SMTPPort     int    `json:"smtpPort"`
		SMTPPassword string `json:"smtpPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := validateEmailAccountInput(body.EmailAddress, body.IMAPHost, body.IMAPPort, body.AuthType, body.SyncIntervalMin, body.Rules); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// POST 没有 patch 语义：空 smtpHost 就是「不配置 SMTP」，不存在「清空」这一
	// 状态。port==0 在这里表示「用默认端口」，由下面补成 587。
	if body.SMTPHost == "" {
		if err := validateSMTPInput(nil, &body.SMTPPort, &body.SMTPPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if body.SMTPPort != 0 && (body.SMTPPort < 1 || body.SMTPPort > 65535) {
		writeError(w, http.StatusBadRequest, "smtpPort must be between 1 and 65535")
		return
	}
	if body.AuthType == "" {
		body.AuthType = "password"
	}
	if body.AuthType == "password" && body.Password == "" {
		writeError(w, http.StatusBadRequest, "password required for authType=password")
		return
	}
	if body.AuthType == "oauth2" && body.OAuthToken == "" {
		writeError(w, http.StatusBadRequest, "oauthToken required for authType=oauth2")
		return
	}
	if body.IMAPPort == 0 {
		body.IMAPPort = 993
	}
	if body.SyncIntervalMin == 0 {
		body.SyncIntervalMin = 15
	}

	plaintext := body.Password
	if body.AuthType == "oauth2" {
		plaintext = body.OAuthToken
	}
	encrypted, err := s.emailCrypto.EncryptString(plaintext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt credential: "+err.Error())
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	uid := s.userIDFromRequest(r)
	acc := &email.Account{
		ID:              randomID("acct"),
		UserID:          uid,
		WorkspaceID:     s.workspaceIDFromRequest(r),
		DisplayName:     body.DisplayName,
		EmailAddress:    body.EmailAddress,
		IMAPHost:        body.IMAPHost,
		IMAPPort:        body.IMAPPort,
		AuthType:        body.AuthType,
		SyncIntervalMin: body.SyncIntervalMin,
		Rules:           body.Rules,
		Enabled:         enabled,
		CreatedAt:       time.Now().Unix(),
	}
	if err := s.emailStore.InsertAccount(r.Context(), acc, encrypted); err != nil {
		writeError(w, http.StatusInternalServerError, "insert account: "+err.Error())
		return
	}
	// SMTP 列不属于 InsertAccount 的写入范围，用与 PUT 相同的 scoped upsert 补齐，
	// 避免「创建时填了 SMTP 却被静默丢弃」。
	//
	// 注意这两步不在同一个事务里：SMTP upsert 失败时账户已经创建成功，客户端
	// 会收到 500 但账户存在且 SMTP 未配置。这不是脏数据（SMTP 本就可选，之后
	// 可用 PUT 补齐），做成原子需要改 InsertAccount 签名，超出本次范围。
	if body.SMTPHost != "" {
		smtpPort := body.SMTPPort
		if smtpPort == 0 {
			smtpPort = 587
		}
		smtpCred := ""
		if body.SMTPPassword != "" {
			enc, err := s.emailCrypto.EncryptString(body.SMTPPassword)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "encrypt smtp credential: "+err.Error())
				return
			}
			smtpCred = enc
		}
		if err := s.emailStore.UpsertSMTPSettingsScoped(r.Context(), acc.ID, uid, acc.WorkspaceID,
			body.SMTPHost, smtpPort, smtpCred, body.SMTPPassword != ""); err != nil {
			writeError(w, http.StatusInternalServerError, "save smtp settings: "+err.Error())
			return
		}
		acc.SMTPHost = body.SMTPHost
		acc.SMTPPort = smtpPort
	}
	// 审计：创建账号事件。detail 只列 auth_type + smtp_password_set 标志，
	// 绝不包含 password/oauthToken 原文。
	s.Write(r, "email.account.created", "email_account:"+acc.ID, AuditFields{
		Success: true,
		Detail: fmt.Sprintf("auth_type=%s smtp_set=%t",
			body.AuthType, body.SMTPPassword != ""),
	})
	writeJSON(w, http.StatusCreated, acc)
	if s.wsHub != nil {
		s.wsHub.BroadcastToUser(uid, "email.account.created", acc)
	}
}

// validateSMTPInput 校验一次 SMTP 配置写入。
//
// host==nil 表示调用方没有提供 smtpHost，此时后端不会写 SMTP 列，因此单独出现
// 的 port/credential 是无处可用的——直接拒绝而不是静默丢弃。
//
// host 为空字符串是「清空 SMTP 配置」的显式信号，只有这种情况允许 port==0；
// 其余情况 port 必须在 1-65535 内（0 视为未提供，由调用方决定默认值）。
func validateSMTPInput(host *string, port *int, credential *string) error {
	if host == nil {
		if port != nil && *port != 0 {
			return errors.New("smtpHost required when smtpPort is provided")
		}
		if credential != nil && *credential != "" {
			return errors.New("smtpHost required when smtpPassword is provided")
		}
		return nil
	}
	clearing := strings.TrimSpace(*host) == ""
	if port == nil {
		return nil
	}
	if *port == 0 {
		if !clearing {
			return errors.New("smtpPort must be between 1 and 65535")
		}
		return nil
	}
	if *port < 1 || *port > 65535 {
		return errors.New("smtpPort must be between 1 and 65535")
	}
	return nil
}

func validateEmailAccountInput(address, host string, port int, authType string, interval int, rules string) error {
	if address == "" {
		return errors.New("emailAddress is required")
	}
	if _, err := mail.ParseAddress(address); err != nil {
		return errors.New("emailAddress is invalid")
	}
	if host == "" {
		return errors.New("imapHost is required")
	}
	if port != 0 && (port < 1 || port > 65535) {
		return errors.New("imapPort must be between 1 and 65535")
	}
	if authType != "" && authType != "password" && authType != "oauth2" {
		return errors.New("authType must be 'password' or 'oauth2'")
	}
	if interval != 0 && (interval < 5 || interval > 60) {
		return errors.New("syncIntervalMin must be between 5 and 60")
	}
	if rules != "" {
		var value any
		if err := json.Unmarshal([]byte(rules), &value); err != nil {
			return errors.New("rules must be valid JSON")
		}
	}
	return nil
}

//	PUT    — 更新账户元数据（displayName / imapHost / imapPort / syncIntervalMin / rules / enabled）；
//	         如果 body 含 password 或 oauthToken，会重新加密并更新 credential_encrypted。
//	DELETE — 删除账户（emails 表通过 ON DELETE CASCADE 自动清理）。
//
// 安全：先校验账户归属当前 user，越权访问返回 404（不暴露存在性）。
// handleEmailAccountOps 负责 /api/email/accounts/ 子树：
//   - /{id}             → GET / PUT / DELETE（账户元数据）
//   - /{id}/test-smtp   → POST（探测 SMTP 连接）
//
// 必须先校验 {id} 的归属：scoped 校验失败返回 404，不暴露账户存在性。
func (s *Server) handleEmailAccountOps(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/email/accounts/")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing account id")
		return
	}

	// /{id}/test-smtp → 单独委派探测 handler，并保留对 id 的作用域校验。
	if strings.HasSuffix(path, "/test-smtp") {
		id := strings.TrimSuffix(path, "/test-smtp")
		s.handleEmailAccountTestSMTP(w, r, id)
		return
	}

	id := path
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	acc, _, err := s.emailStore.GetAccountByIDScoped(r.Context(), id, uid, wsID)
	if errors.Is(err, email.ErrNotFound) || acc == nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch r.Method {
	case http.MethodPut:
		s.updateEmailAccount(w, r, acc, wsID)
	case http.MethodDelete:
		if err := s.emailStore.DeleteAccountScoped(r.Context(), id, uid, wsID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 审计：删除账号事件；detail 仅包含 id，不含账号元数据。
		s.Write(r, "email.account.deleted", "email_account:"+id, AuditFields{
			Success: true,
		})
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		if s.wsHub != nil {
			s.wsHub.BroadcastToUser(uid, "email.account.deleted", map[string]string{"id": id})
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PUT/DELETE only")
	}
}

// updateEmailAccount 处理 PUT /api/email/accounts/{id} 的部分更新。
//
// 字段语义：
//   - 所有字段可选；未提供则保留原值（patch 语义）。
//   - 仅允许修改自己的账户；账号所有权已在调用方校验。
//   - password / oauthToken 互斥（不能同时改）；提供任一则触发 credential
//     重加密。如果 authType 改为 oauth2，应同时提供 oauthToken。
//
// SMTP 字段语义：
//   - 只有携带 smtpHost 才会写 SMTP 列。单独传 smtpPort/smtpPassword 以前会被
//     静默丢弃，现在直接返回 400——静默成功比报错更难排查。
//   - smtpHost 传空字符串表示清空 SMTP 配置，此时 port 一并归零。
//   - smtpPassword 省略 → 保留原凭证；传 ” → 清空；传非空 → 重新加密写入。
func (s *Server) updateEmailAccount(w http.ResponseWriter, r *http.Request, acc *email.Account, workspaceID string) {
	var body struct {
		DisplayName     *string `json:"displayName"`
		IMAPHost        *string `json:"imapHost"`
		IMAPPort        *int    `json:"imapPort"`
		AuthType        *string `json:"authType"`
		SyncIntervalMin *int    `json:"syncIntervalMin"`
		Rules           *string `json:"rules"`
		Enabled         *bool   `json:"enabled"`
		Password        *string `json:"password"`
		OAuthToken      *string `json:"oauthToken"`
		// SMTP 出站配置。此前这三个字段没有任何写入入口，
		// UpsertSMTPSettingsScoped 在 server 层无调用点，导致 /test-smtp 对新
		// 账户永远返回 "smtp not configured"。
		SMTPHost     *string `json:"smtpHost"`
		SMTPPort     *int    `json:"smtpPort"`
		SMTPPassword *string `json:"smtpPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Password != nil && body.OAuthToken != nil {
		writeError(w, http.StatusBadRequest, "provide only one of password / oauthToken")
		return
	}
	if body.IMAPPort != nil && (*body.IMAPPort < 1 || *body.IMAPPort > 65535) {
		writeError(w, http.StatusBadRequest, "imapPort must be between 1 and 65535")
		return
	}
	if err := validateSMTPInput(body.SMTPHost, body.SMTPPort, body.SMTPPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.SyncIntervalMin != nil && (*body.SyncIntervalMin < 5 || *body.SyncIntervalMin > 60) {
		writeError(w, http.StatusBadRequest, "syncIntervalMin must be between 5 and 60")
		return
	}
	if body.Rules != nil {
		var v any
		if err := json.Unmarshal([]byte(*body.Rules), &v); err != nil {
			writeError(w, http.StatusBadRequest, "rules must be valid JSON")
			return
		}
	}

	if body.DisplayName != nil {
		acc.DisplayName = *body.DisplayName
	}
	if body.IMAPHost != nil {
		acc.IMAPHost = *body.IMAPHost
	}
	if body.IMAPPort != nil && *body.IMAPPort > 0 {
		acc.IMAPPort = *body.IMAPPort
	}
	if body.SyncIntervalMin != nil && *body.SyncIntervalMin > 0 {
		acc.SyncIntervalMin = *body.SyncIntervalMin
	}
	if body.Rules != nil {
		acc.Rules = *body.Rules
	}
	if body.Enabled != nil {
		acc.Enabled = *body.Enabled
	}
	if body.AuthType != nil {
		if *body.AuthType != "password" && *body.AuthType != "oauth2" {
			writeError(w, http.StatusBadRequest, "authType must be 'password' or 'oauth2'")
			return
		}
		acc.AuthType = *body.AuthType
	}

	var encrypted string
	updateCredential := false
	if body.Password != nil || body.OAuthToken != nil {
		if s.emailCrypto == nil {
			writeError(w, http.StatusServiceUnavailable, "email crypto not configured")
			return
		}
		if body.AuthType != nil && *body.AuthType == "oauth2" && body.OAuthToken == nil {
			writeError(w, http.StatusBadRequest, "oauthToken required when authType=oauth2")
			return
		}
		plaintext := ""
		if body.Password != nil {
			plaintext = *body.Password
		} else {
			plaintext = *body.OAuthToken
		}
		enc, err := s.emailCrypto.EncryptString(plaintext)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt credential: "+err.Error())
			return
		}
		encrypted = enc
		updateCredential = true
	}

	uid := s.userIDFromRequest(r)
	if err := s.emailStore.UpdateAccountScoped(r.Context(), acc, uid, workspaceID, encrypted, updateCredential); err != nil {
		if errors.Is(err, email.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update account: "+err.Error())
		return
	}

	// SMTP 设置走独立的 scoped upsert（host/port/credential 在单独的列里）。
	// 只在调用方显式提供 smtpHost 时才写，避免每次 PUT 都清空 SMTP 配置。
	if body.SMTPHost != nil {
		smtpPort := acc.SMTPPort
		if body.SMTPPort != nil {
			smtpPort = *body.SMTPPort
		}
		// 清空 host 时端口一并归零，不留「空 host + 陈旧端口」的半配置状态。
		if strings.TrimSpace(*body.SMTPHost) == "" {
			smtpPort = 0
		}
		smtpCred := ""
		updateSMTPCred := false
		if body.SMTPPassword != nil {
			if s.emailCrypto == nil {
				writeError(w, http.StatusServiceUnavailable, "email crypto not configured")
				return
			}
			// 空字符串表示"清空凭证"，非空则加密后写入。两种都算显式更新。
			if *body.SMTPPassword != "" {
				enc, err := s.emailCrypto.EncryptString(*body.SMTPPassword)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "encrypt smtp credential: "+err.Error())
					return
				}
				smtpCred = enc
			}
			updateSMTPCred = true
		}
		if err := s.emailStore.UpsertSMTPSettingsScoped(r.Context(), acc.ID, uid, workspaceID,
			*body.SMTPHost, smtpPort, smtpCred, updateSMTPCred); err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account not found")
				return
			}
			writeError(w, http.StatusBadRequest, "update smtp settings: "+err.Error())
			return
		}
		acc.SMTPHost = *body.SMTPHost
		acc.SMTPPort = smtpPort
	}

	// 审计：变更字段集合（password/oauthToken/smtpPassword 出现与否），
	// 真实凭据绝不进入 detail。
	fields := []string{}
	if body.Password != nil {
		fields = append(fields, "password_set")
	}
	if body.OAuthToken != nil {
		fields = append(fields, "oauth_token_set")
	}
	if body.SMTPHost != nil {
		fields = append(fields, "smtp_password_set")
	}
	if body.SyncIntervalMin != nil {
		fields = append(fields, "sync_interval")
	}
	if body.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if body.IMAPHost != nil {
		fields = append(fields, "imap_host")
	}
	s.Write(r, "email.account.updated", "email_account:"+acc.ID, AuditFields{
		Success: true,
		Detail:  fmt.Sprintf("changed=%v", fields),
	})
	writeJSON(w, http.StatusOK, acc)
	if s.wsHub != nil {
		s.wsHub.BroadcastToUser(uid, "email.account.updated", acc)
	}
}

func (s *Server) handleEmails(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	f := email.ListFilter{
		AccountID:  r.URL.Query().Get("account_id"),
		Category:   r.URL.Query().Get("category"),
		Importance: r.URL.Query().Get("importance"),
		UnreadOnly: r.URL.Query().Get("unread") == "1",
	}
	list, err := s.emailStore.ListEmailsScoped(r.Context(), f, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"emails": list})
}

// handleEmailVacations — GET /api/email/vacations
func (s *Server) handleEmailVacations(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		accountID := r.URL.Query().Get("account_id")
		list, err := s.emailStore.ListVacationsScoped(r.Context(), accountID, uid, wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"vacations": list})
	case http.MethodPost:
		var v email.VacationReply
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if v.AccountID == "" {
			writeError(w, http.StatusBadRequest, "accountId required")
			return
		}
		if v.Subject == "" || v.BodyText == "" {
			writeError(w, http.StatusBadRequest, "subject and bodyText required")
			return
		}
		if v.EndAt <= v.StartAt {
			writeError(w, http.StatusBadRequest, "endAt must be greater than startAt")
			return
		}
		v.WorkspaceID = wsID
		if err := s.emailStore.UpsertVacationScoped(r.Context(), &v, uid, wsID); err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, v)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleEmailVacationOps — DELETE /api/email/vacations/{id}
func (s *Server) handleEmailVacationOps(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/email/vacations/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing vacation id")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	if err := s.emailStore.DeleteVacationScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r)); err != nil {
		if errors.Is(err, email.ErrNotFound) {
			writeError(w, http.StatusNotFound, "vacation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleEmailAccountTestSMTP — POST /api/email/accounts/{id}/test-smtp
//
// 探测流程：
//   - 用 scoped query 取账户 SMTP 配置与明文凭证；
//   - 调 smtpProbe(host, port, username, password)：
//     465 → 直接 TLS；
//     587 → 明文 dial + EHLO，服务器宣告 STARTTLS 时升级；
//     其它端口 → 同 587 行为；
//   - 凭证格式为 "user:password" 时拆分为 username/password；纯密码时
//     username 取账户的 email_address（RFC 5321 兼容）。
//   - 任何错误只返回脱敏的阶段信息（smtp/auth/tls），绝不回显明文。
func (s *Server) handleEmailAccountTestSMTP(w http.ResponseWriter, r *http.Request, id string) {
	if s.emailStore == nil || s.emailCrypto == nil {
		writeError(w, http.StatusServiceUnavailable, "email store / crypto not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing account id")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	host, emailAddr, port, encCred, err := s.emailStore.GetSMTPCredentialScoped(r.Context(), id, uid, wsID)
	if err != nil {
		if errors.Is(err, email.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if host == "" {
		writeError(w, http.StatusBadRequest, "smtp not configured")
		return
	}
	username := emailAddr
	password := ""
	if encCred != "" {
		cred, err := s.emailCrypto.DecryptString(encCred)
		if err != nil || cred == "" {
			writeError(w, http.StatusBadRequest, "smtp credential invalid")
			return
		}
		// 允许的格式：
		//   "password"             → password=cred, username=emailAddr
		//   "user:password"        → username/password 拆分
		if u, p, ok := strings.Cut(cred, ":"); ok {
			username = u
			password = p
		} else {
			password = cred
		}
	}
	if err := smtpProbe(host, port, username, password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "smtp": addr})
}

// handleEmailSend — POST /api/email/send
//
// 用账户已保存的 SMTP 配置实际发送一封邮件。这是从「配置/探测」到「副作用」
// 的最后一环：请求体带 accountId 时必须属于当前 user/workspace，SMTP 凭证从
// 加密列解密后经 TLS/STARTTLS 发送。响应不回显任何凭证或上游响应。
//
// 请求体：
//
//	{ "accountId": "acc-...", "to": ["a@x.com"], "subject": "...", "body": "..." }
//
// accountId 可省略：此时按当前 user/workspace 第一个配置了 SMTP 的启用账户发送
// （便于前端「默认发信账户」场景）。
func (s *Server) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil || s.emailCrypto == nil {
		writeError(w, http.StatusServiceUnavailable, "email store / crypto not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		AccountID string   `json:"accountId"`
		To        []string `json:"to"`
		Subject   string   `json:"subject"`
		Body      string   `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.To) == 0 {
		writeError(w, http.StatusBadRequest, "missing recipients")
		return
	}
	cleanedTo := make([]string, 0, len(body.To))
	for _, addr := range body.To {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, err := mail.ParseAddress(addr); err != nil {
			writeError(w, http.StatusBadRequest, "invalid recipient address: "+addr)
			return
		}
		cleanedTo = append(cleanedTo, addr)
	}
	if len(cleanedTo) == 0 {
		writeError(w, http.StatusBadRequest, "no valid recipients")
		return
	}
	if len(body.Subject) > 500 {
		writeError(w, http.StatusBadRequest, "subject too long")
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var host, emailAddr string
	var port int
	var encCred string
	if body.AccountID != "" {
		var err error
		host, emailAddr, port, encCred, err = s.emailStore.GetSMTPCredentialScoped(r.Context(), body.AccountID, uid, wsID)
		if err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		// 无显式 accountId：取当前 scope 第一个配置了 SMTP 的启用账户。
		accounts, err := s.emailStore.ListAccountsScoped(r.Context(), uid, wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, a := range accounts {
			if !a.Enabled || a.SMTPHost == "" {
				continue
			}
			h, addr, p, enc, err := s.emailStore.GetSMTPCredentialScoped(r.Context(), a.ID, uid, wsID)
			if err != nil || h == "" || p == 0 {
				continue
			}
			host = h
			port = p
			emailAddr = addr
			encCred = enc
			break
		}
	}
	if host == "" {
		writeError(w, http.StatusBadRequest, "smtp not configured for account")
		return
	}

	username := emailAddr
	password := ""
	if encCred != "" {
		cred, err := s.emailCrypto.DecryptString(encCred)
		if err != nil || cred == "" {
			writeError(w, http.StatusBadRequest, "smtp credential invalid")
			return
		}
		if u, p, ok := strings.Cut(cred, ":"); ok {
			username = u
			password = p
		} else {
			password = cred
		}
	}

	if err := smtpSend(host, port, username, password, emailAddr, cleanedTo, body.Subject, body.Body); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "to": cleanedTo, "from": emailAddr})
}

// handleEmailBody — GET /api/emails/{id}/body
//
// 返回单封邮件的完整正文（IMAP TEXT part）。流程：
//  1. 按 (user, workspace) 取邮件，不信任 client 提供的 account/workspace；
//  2. 命中加密缓存 (dataDir/email-bodies/<id>.bin) 直接解密返回；
//  3. 未命中时按邮件所属 account + IMAP UID 拉取，并写到加密缓存后返回。
//
// 任意阶段账户/工作区不匹配返回 404，不暴露其他 workspace 的存在性。
func (s *Server) handleEmailBody(w http.ResponseWriter, r *http.Request, emailID string) {
	if s.emailFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "email fetcher not configured")
		return
	}
	if s.emailCrypto == nil {
		writeError(w, http.StatusServiceUnavailable, "email crypto not configured")
		return
	}
	if s.dataDir == "" {
		writeError(w, http.StatusServiceUnavailable, "data dir not configured")
		return
	}
	em, err := s.emailStore.GetEmailByIDScoped(r.Context(), emailID, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if em == nil {
		writeError(w, http.StatusNotFound, "email not found")
		return
	}
	if em.AccountID == "" {
		writeError(w, http.StatusUnprocessableEntity, "email missing account id")
		return
	}

	const maxBodyBytes = 256 * 1024
	ctx := r.Context()

	// 1) 缓存命中：body_path 非空才尝试读缓存文件，避免对未缓存邮件做无谓的 IO。
	//    （em.BodyPath 来自 GetEmailByIDScoped 的 body_path 列；旧逻辑每次盲试文件。）
	if em.BodyPath != "" {
		if cached, _ := s.readCachedEmailBody(ctx, emailID, em.UID); cached != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"emailId": emailID,
				"source":  "cache",
				"bytes":   len(cached),
				"body":    string(cached),
			})
			return
		}
	}

	// 2) 未命中 → IMAP 拉取
	body, err := s.emailFetcher.FetchBody(ctx, em.AccountID, em.UID, maxBodyBytes)
	if err != nil {
		log.Printf("[email/body] imap fetch email=%s account=%s uid=%d: %v", emailID, em.AccountID, em.UID, err)
		writeError(w, http.StatusBadGateway, "imap fetch failed")
		return
	}
	if writeErr := s.writeCachedEmailBody(ctx, emailID, body); writeErr != nil {
		log.Printf("[email/body] cache write email=%s: %v", emailID, writeErr)
		// 缓存写失败仍返回内容，避免阻塞前端
	}
	if markErr := s.emailStore.MarkEmailBodyCached(ctx, emailID, bodyCacheRelativePath(emailID), len(body)); markErr != nil && !errors.Is(markErr, email.ErrNotFound) {
		log.Printf("[email/body] mark body cached email=%s: %v", emailID, markErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"emailId": emailID,
		"source":  "imap",
		"bytes":   len(body),
		"body":    string(body),
	})
}

// bodyCacheDirName 缓存目录名；放在 dataDir 内、模式 0700，仅进程可读。
const bodyCacheDirName = "email-bodies"

// bodyCacheRelativePath 返回相对 dataDir 的稳定路径，供 emails.body_path 存储。
// 用 email ID 而非 UID：UID 可能在 sync 时变化；缓存失效则重拉并覆盖原文件。
func bodyCacheRelativePath(emailID string) string {
	return filepath.Join(bodyCacheDirName, emailID+".bin")
}

func (s *Server) bodyCacheDir() (string, error) {
	if s.dataDir == "" {
		return "", fmt.Errorf("data dir not configured")
	}
	dir := filepath.Join(s.dataDir, bodyCacheDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// readCachedEmailBody 读缓存并解密；不存在 / 损坏 / UID 不匹配时返回 nil+nil。
// 缓存头部写入 8 字节 UID，便于账号迁移后定位旧 UID 失效。
func (s *Server) readCachedEmailBody(ctx context.Context, emailID string, expectedUID int64) ([]byte, error) {
	dir, err := s.bodyCacheDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, emailID+".bin")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) < 8 {
		return nil, nil
	}
	prefixUID := int64(binary.BigEndian.Uint64(data[:8]))
	if expectedUID > 0 && prefixUID != expectedUID {
		return nil, nil // 旧缓存，视为未命中
	}
	bodyEnc := string(data[8:])
	body, err := s.emailCrypto.DecryptString(bodyEnc)
	if err != nil {
		return nil, nil // 损坏视为未命中
	}
	return []byte(body), nil
}

// writeCachedEmailBody 原子写入（临时文件 + rename），避免 reader 撞上半文件。
func (s *Server) writeCachedEmailBody(ctx context.Context, emailID string, body []byte) error {
	dir, err := s.bodyCacheDir()
	if err != nil {
		return err
	}
	encrypted, err := s.emailCrypto.EncryptString(string(body))
	if err != nil {
		return err
	}
	finalPath := filepath.Join(dir, emailID+".bin")
	tmp, err := os.CreateTemp(dir, ".body-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	hdr := make([]byte, 8)
	// UID 在缓存写入时被省略（0），让 readCachedEmailBody 跳过 UID 校验，
	// 避免 sync 增量 UID 改变导致命中旧内容。
	binary.BigEndian.PutUint64(hdr, 0)
	if _, err := tmp.Write(hdr); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(encrypted); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return err
	}
	_ = os.Chmod(finalPath, 0600)
	return nil
}

func (s *Server) handleEmailOps(w http.ResponseWriter, r *http.Request) {
	// /api/emails/sync/status — POST, fetch per-account sync status.
	if strings.HasSuffix(r.URL.Path, "/sync/status") {
		s.handleEmailSyncStatus(w, r)
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured (remote-only mode)")
		return
	}
	// /api/emails/{id}/body — GET 拉取完整正文（IMAP 懒加载）。
	remain := strings.TrimPrefix(r.URL.Path, "/api/emails/")
	remain = strings.TrimSuffix(remain, "/")
	if strings.HasSuffix(remain, "/body") {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		id := strings.TrimSuffix(remain, "/body")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing email id")
			return
		}
		s.handleEmailBody(w, r, id)
		return
	}
	// /api/emails/{id} — GET 详情 / PATCH 标记已读。
	id := remain
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing email id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		em, err := s.emailStore.GetEmailByIDScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if em == nil {
			writeError(w, http.StatusNotFound, "email not found")
			return
		}
		writeJSON(w, http.StatusOK, em)
	case http.MethodPatch:
		var body struct {
			IsRead    *bool `json:"isRead"`
			IsStarred *bool `json:"isStarred"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.IsRead == nil && body.IsStarred == nil {
			writeError(w, http.StatusBadRequest, "provide at least one of isRead / isStarred")
			return
		}
		if err := s.emailStore.UpdateEmailFlagsScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r), body.IsRead, body.IsStarred); err != nil {
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "email not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PATCH only")
	}
}

func (s *Server) handleEmailSync(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 两种模式（由 body 内容区分）：
	//
	// A) 主动 IMAP 抓取（v1.0 主路径）：POST 空 body 或 {"account_id":"..."}。
	//    后端用 emailFetcher.Sync 连 IMAP 拉新邮件，落库后异步分类。
	//    account_id 省略 = 同步该用户所有 enabled 账户。
	//
	// B) 客户端推送（旧路径，保留兼容）：POST {"emails":[...]}。
	//    客户端自己抓 IMAP 后把邮件列表推上来，pocketd 只做去重落库 + 分类。
	var body struct {
		Emails    []email.Email `json:"emails"`
		AccountID string        `json:"account_id"`
	}
	// 空 body 合法（触发模式 A），decode 错误仅在非空时致命
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
	}

	// 模式 B：客户端推了 emails 数组 → 走老路径
	if len(body.Emails) > 0 {
		// 客户端推送路径也必须绑定到当前请求的账户和 workspace。
		userID := s.userIDFromRequest(r)
		wsID := s.workspaceIDFromRequest(r)
		for i := range body.Emails {
			if body.Emails[i].ID == "" {
				body.Emails[i].ID = randomID("email")
			}
			if body.Emails[i].AccountID == "" {
				writeError(w, http.StatusBadRequest, "accountId required for pushed email")
				return
			}
			acc, _, err := s.emailStore.GetAccountByIDScoped(r.Context(), body.Emails[i].AccountID, userID, wsID)
			if err != nil || acc == nil {
				writeError(w, http.StatusNotFound, "account not found")
				return
			}
			body.Emails[i].WorkspaceID = wsID
			if err := s.emailStore.InsertEmail(r.Context(), body.Emails[i]); err != nil {
				writeError(w, http.StatusInternalServerError, "insert email: "+err.Error())
				return
			}
		}
		go s.classifyEmailsAsync(body.Emails, userID, wsID)
		writeJSON(w, http.StatusOK, map[string]any{"received": len(body.Emails), "classify": "async"})
		return
	}

	// 模式 A：主动 IMAP 抓取
	if s.emailFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "email fetcher not configured (IMAP disabled)")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var accounts []email.Account
	if body.AccountID != "" {
		acc, _, err := s.emailStore.GetAccountByIDScoped(r.Context(), body.AccountID, userID, wsID)
		if errors.Is(err, email.ErrNotFound) || acc == nil {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load account: "+err.Error())
			return
		}
		accounts = []email.Account{*acc}
	} else {
		listed, err := s.emailStore.ListAccountsScoped(r.Context(), userID, wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
			return
		}
		accounts = listed
	}

	totalSaved := 0
	synced := 0
	failed := []string{}
	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}
		n, ferr := func() (int, error) {
			syncCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			return s.emailFetcher.Sync(syncCtx, acc.ID)
		}()
		if ferr != nil {
			log.Printf("[email/sync] account %s (%s): %v", acc.ID, acc.EmailAddress, ferr)
			failed = append(failed, acc.EmailAddress)
			continue
		}
		totalSaved += n
		synced++
	}
	// 有新邮件就触发发票自动提取。Sync 只返回数量，这里按邮件日期把近期
	// 邮件拉回来（extractInvoicesAsync 内部 GetInvoiceByEmailID 幂等，纯规
	// 则零外呼）。注意邮件 date 是头日期而非入库时间，可能早于本轮（缺
	// Date 头时甚至可能重复），窗口放宽到 24h 保证覆盖。
	if totalSaved > 0 {
		if recent, _, lerr := s.emailStore.ListEmailsSince(r.Context(), time.Now().Unix()-86400, 200); lerr == nil && len(recent) > 0 {
			go s.extractInvoicesAsync(recent, userID, wsID)
		}
	}

	result := map[string]any{
		"mode":   "imap_fetch",
		"synced": synced,
		"new":    totalSaved,
	}
	if len(failed) > 0 {
		result["failed"] = failed
	}
	writeJSON(w, http.StatusOK, result)
}

// handleEmailSyncStatus — POST /api/email/sync/status
//
// Returns the sync state of every email account for the current user so the
// front-end EmailAccountSetup / status panel can render last-synced times,
// pending unread counts, and account enabled flags.
func (s *Server) handleEmailSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	statuses, err := s.emailStore.GetSyncStatusScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if statuses == nil {
		statuses = []email.AccountSyncStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}

// classifyEmailsAsync 异步调 kxmemory 批量分类邮件（IMAP 同步后触发）
func (s *Server) classifyEmailsAsync(emails []email.Email, userID, workspaceID string) {
	// 发票自动提取：纯规则、不依赖 kxmemory，主题/摘要命中即提取落库。
	// Best-effort：失败只记日志，不影响分类主流程。
	s.extractInvoicesAsync(emails, userID, workspaceID)

	if s.kxmemory == nil {
		return // kxmemory 未配置，跳过
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 构造分类请求（只发 snippet，不发完整正文）
	items := make([]kxmemory.EmailForClassification, 0, len(emails))
	for _, e := range emails {
		// 跳过已分类的（性能优化：只分类未分类的邮件）
		if e.Category != "" && e.AISummary != "" {
			continue
		}
		items = append(items, kxmemory.EmailForClassification{
			EmailID:     e.ID,
			Subject:     e.Subject,
			Snippet:     e.Snippet,
			FromAddress: e.FromAddress,
			FromName:    e.FromName,
		})
	}
	if len(items) == 0 {
		return
	}

	resp, err := s.kxmemory.ClassifyEmails(ctx, kxmemory.ClassifyEmailsRequest{Emails: items})
	if err != nil {
		log.Printf("[kxmemory] classify %d emails failed: %v", len(items), err)
		return
	}

	// 回写分类结果
	classified := 0
	for _, result := range resp.Results {
		if err := s.emailStore.SetClassificationScoped(ctx, result.EmailID, userID, workspaceID,
			result.Category, result.Importance, result.Summary, result.SuggestedAction); err != nil {
			log.Printf("[kxmemory] update email %s classification failed: %v", result.EmailID, err)
			continue
		}
		classified++
	}
	log.Printf("[kxmemory] classified %d/%d emails", classified, len(items))
}

func (s *Server) handleEmailSummaries(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	// 可选 ?limit=N（默认 30，上限 200）。
	// 单用户每日一封的频率下 limit=30 已足够覆盖一个月，无须支持游标。
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	list, err := s.emailStore.ListSummariesScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []email.DailySummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": list})
}

func (s *Server) handleEmailSummaryOps(w http.ResponseWriter, r *http.Request) {
	// GET /api/email/summaries/{date} — daily summary detail.
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/api/email/summaries/")
	sub = strings.TrimSuffix(sub, "/")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing date (YYYY-MM-DD)")
		return
	}
	// 验证日期格式 YYYY-MM-DD,避免无效输入直接打到 PG 触发 500。
	if _, err := time.Parse("2006-01-02", sub); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date (expected YYYY-MM-DD)")
		return
	}
	sum, err := s.emailStore.GetSummaryByDateScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r), sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sum == nil {
		writeError(w, http.StatusNotFound, "summary not generated yet")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// =====================================================================
// 密码箱
// =====================================================================

func (s *Server) handleVaultSync(w http.ResponseWriter, r *http.Request) {
	if s.vaultStore == nil {
		writeError(w, http.StatusServiceUnavailable, "vault store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	sub := strings.TrimPrefix(r.URL.Path, "/api/vault/sync/")
	switch {
	case r.Method == http.MethodPost && sub == "":
		// 上传加密 blob（整体 vault 密文）
		var body struct {
			Blob    string `json:"blob"`
			Version int    `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if err := s.vaultStore.PutLatest(r.Context(), s.workspaceIDFromRequest(r), uid, body.Blob, body.Version); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 审计：记录版本与字节数，绝不写 blob 内容。
		s.Write(r, "vault.sync.upload", "vault:"+uid, AuditFields{
			Success: true,
			Detail:  fmt.Sprintf("version=%d bytes=%d", body.Version, len(body.Blob)),
		})
		s.wsHub.BroadcastToUser(uid, "vault.synced", map[string]string{"userId": uid})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case r.Method == http.MethodGet && (sub == "latest" || sub == ""):
		blob, ver, err := s.vaultStore.GetLatest(r.Context(), s.workspaceIDFromRequest(r), uid)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blob": blob, "version": ver})
	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/restore"):
		// POST /api/vault/sync/{version}/restore — 回滚到指定历史版本（不重写 blob）
		verStr := strings.TrimSuffix(sub, "/restore")
		ver, err := strconv.Atoi(verStr)
		if err != nil || ver <= 0 {
			writeError(w, http.StatusBadRequest, "invalid version")
			return
		}
		blob, err := s.vaultStore.GetByVersion(r.Context(), s.workspaceIDFromRequest(r), uid, ver)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err := s.vaultStore.MarkCurrent(r.Context(), s.workspaceIDFromRequest(r), uid, ver); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 审计：restore 不重写 blob，仅记录指针翻转到的版本。
		s.Write(r, "vault.sync.restore", "vault:"+uid, AuditFields{
			Success: true,
			Detail:  fmt.Sprintf("version=%d", ver),
		})
		s.wsHub.BroadcastToUser(uid, "vault.restored", map[string]any{"userId": uid, "version": ver})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": ver, "blob": blob})
	case r.Method == http.MethodGet && strings.HasPrefix(sub, "versions/"):
		// GET /api/vault/sync/versions/{version} — 单版本加密 blob 详情
		verStr := strings.TrimPrefix(sub, "versions/")
		ver, err := strconv.Atoi(verStr)
		if err != nil || ver <= 0 {
			writeError(w, http.StatusBadRequest, "invalid version")
			return
		}
		blob, err := s.vaultStore.GetByVersion(r.Context(), s.workspaceIDFromRequest(r), uid, ver)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blob": blob, "version": ver})
	case r.Method == http.MethodGet && sub == "versions":
		versions, err := s.vaultStore.ListVersions(r.Context(), s.workspaceIDFromRequest(r), uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported: %s %s", r.Method, sub))
	}
}

// =====================================================================
// STT 云端兜底
// =====================================================================

func audioFilenameForContentType(contentType string) string {
	mediaType := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	switch mediaType {
	case "audio/webm":
		return "audio.webm"
	case "audio/mpeg":
		return "audio.mp3"
	case "audio/mp4":
		return "audio.m4a"
	case "audio/wav", "audio/x-wav":
		return "audio.wav"
	case "audio/ogg":
		return "audio.ogg"
	case "audio/flac":
		return "audio.flac"
	case "audio/3gpp":
		return "audio.3gp"
	default:
		return "audio.bin"
	}
}

func (s *Server) handleSttTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.transcriber == nil {
		writeError(w, http.StatusServiceUnavailable, "STT cloud not configured (set POCKET_GROQ_API_KEY)")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var audioData []byte
	var filename string

	// 优先尝试 multipart/form-data（前端录音上传）
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// 解析 multipart，限制 25 MB
		if err := r.ParseMultipartForm(25 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart: "+err.Error())
			return
		}
		defer file.Close()
		filename = header.Filename
		audioData, err = io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read audio file: "+err.Error())
			return
		}
	} else if strings.HasPrefix(ct, "application/json") || ct == "" {
		// JSON body: { "audioBase64": "..." }
		var body struct {
			AudioBase64 string `json:"audioBase64"`
			Filename    string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		filename = body.Filename
		if body.AudioBase64 != "" {
			// Base64 编码的音频
			var decodeErr error
			audioData, decodeErr = base64.StdEncoding.DecodeString(body.AudioBase64)
			if decodeErr != nil {
				writeError(w, http.StatusBadRequest, "invalid base64 audio: "+decodeErr.Error())
				return
			}
			if filename == "" {
				filename = "audio.wav"
			}
		} else {
			writeError(w, http.StatusBadRequest, "provide 'file' (multipart) or 'audioBase64'")
			return
		}
	} else if strings.HasPrefix(ct, "audio/") {
		// Raw audio is supported for callers of the documented binary contract.
		var readErr error
		audioData, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "failed to read audio data")
			return
		}
		filename = audioFilenameForContentType(ct)
	} else {
		writeError(w, http.StatusBadRequest, "unsupported content type; use audio/*, multipart/form-data, or application/json")
		return
	}

	if len(audioData) == 0 {
		writeError(w, http.StatusBadRequest, "empty audio data")
		return
	}
	if len(audioData) > 25<<20 {
		writeError(w, http.StatusBadRequest, "audio too large (max 25 MB)")
		return
	}
	if filename == "" {
		filename = "audio.wav"
	}

	// 调用 Groq Whisper Large v3 Turbo
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result, err := s.transcriber.Transcribe(ctx, audioData, filename)
	if err != nil {
		log.Printf("[stt] transcribe failed: %v", err)
		writeError(w, http.StatusBadGateway, "transcription failed: "+err.Error())
		return
	}

	log.Printf("[stt] transcribed %d bytes (%s) -> %d chars", len(audioData), filename, len(result.Text))
	writeJSON(w, http.StatusOK, map[string]any{
		"text":       result.Text,
		"confidence": result.Confidence,
	})
}

// =====================================================================
// 辅助
// =====================================================================

// randomID 生成带前缀的简易 ID。Phase 0 骨架用，后续可换 UUID/kseq。
func randomID(prefix string) string {
	// 用纳秒级时间戳足够避免单用户场景冲突。
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

var _ = notes.Note{} // keep import if temporarily unused

// =====================================================================
// Phase C: 无状态 AI 网关（嵌入 / LLM 代理）
//
// 隐私契约：这些 handler 只转发请求给 AI 提供商，不写任何持久存储。
// 日志只记请求大小，不记内容。
// =====================================================================

// handleEmbed — 接收文本片段，返回嵌入向量。
//
// 请求: { "text": "..." }
// 响应: { "embedding": [0.1, ...], "model": "text-embedding-3-small" }
func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Text) == 0 {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if len(body.Text) > 16000 {
		writeError(w, http.StatusBadRequest, "text too long (max 16000 chars)")
		return
	}

	// 2026-09-05 迁移动态网关：llmBFF 已装配时走 BFF Embed（与 /api/llm/chat
	// 同源：按 workspace 实时解析 /api/llm-gateway/config，usage 计量走
	// Recorder），静态 s.embedder 仅在 BFF 未装配的部署兜底。响应形状不变。
	if s.llmBFF != nil {
		resp, err := s.llmBFF.Embed(r.Context(), llmbff.EmbedRequest{
			WorkspaceID: s.workspaceIDFromRequest(r),
			Model:       s.cfg.EmbedModel,
			Input:       body.Text,
		}, "embed")
		auditModel := s.cfg.EmbedModel
		if resp != nil {
			auditModel = resp.Model
		}
		// 审计：模型调用事件；detail 只含 model 与字符数，绝不记 body.Text。
		s.Write(r, "llm.embed", "llm:embed", AuditFields{
			Success: err == nil,
			Detail:  fmt.Sprintf("model=%s chars=%d", auditModel, len(body.Text)),
		})
		if err != nil {
			if errors.Is(err, llmbff.ErrNotConfigured) {
				writeError(w, http.StatusServiceUnavailable, "embed gateway not configured (set POCKET_LLM_GATEWAY_URL/_API_KEY or POST /api/llm-gateway/config)")
				return
			}
			writeError(w, http.StatusBadGateway, "embed failed: "+err.Error())
			return
		}
		// 注意：绝不记 body.Text 内容
		writeJSON(w, http.StatusOK, map[string]any{
			"embedding": resp.Embedding,
			"model":     resp.Model,
			"dim":       len(resp.Embedding),
		})
		return
	}

	if s.embedder == nil {
		writeError(w, http.StatusServiceUnavailable, "embedder not configured (set POCKET_EMBED_API_KEY)")
		return
	}
	embedding, model, err := s.embedder.Embed(r.Context(), body.Text)
	// 审计：模型调用事件；detail 只含 model 与字符数，绝不记 body.Text。
	s.Write(r, "llm.embed", "llm:embed", AuditFields{
		Success: err == nil,
		Detail:  fmt.Sprintf("model=%s chars=%d", model, len(body.Text)),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "embed failed: "+err.Error())
		return
	}
	// 注意：绝不记 body.Text 内容
	writeJSON(w, http.StatusOK, map[string]any{
		"embedding": embedding,
		"model":     model,
		"dim":       len(embedding),
	})
}

// handleLLMChat — 无状态 LLM 代理。每次调用独立，不维护对话历史。
//
// 请求: { "messages": [{ "role": "user", "content": "..." }], "model"? }
// 响应: { "content": "...", "model": "..." }
//
// 2026-09-05 迁移动态网关：llmBFF 已装配时转投 BFF（dynamicGatewayBFFProvider
// 按 workspace 实时解析 /api/llm-gateway/config 的运行时配置，空/"auto" model
// 用 preferred 列表本地解析，no_candidate 自动回退），消除「启动时未配
// POCKET_LLM_API_KEY → 静态 s.llm 恒 503」的旧分支。s.llm 仅在 BFF 未装配的
// 部署里兜底（server_meeting.go 经 llmChatOnce、/api/embed 经 BFF Embed 同日迁移）。
func (s *Server) handleLLMChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Messages []aigate.ChatMessage `json:"messages"`
		Model    string               `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}
	// 输入大小限制：防止滥用（与 /api/embed 一致的 16K/消息上限 + 50 条消息上限）
	if len(body.Messages) > 50 {
		writeError(w, http.StatusBadRequest, "too many messages (max 50)")
		return
	}
	for _, m := range body.Messages {
		if len(m.Content) > 32000 {
			writeError(w, http.StatusBadRequest, "message too long (max 32000 chars per message)")
			return
		}
	}

	if s.llmBFF != nil {
		s.llmChatViaBFF(w, r, body.Messages, body.Model)
		return
	}

	if s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured (set POCKET_LLM_GATEWAY_URL/_API_KEY or POCKET_LLM_API_KEY)")
		return
	}
	model := body.Model
	if model == "" {
		model = s.cfg.LLMModel
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "model required (set POCKET_LLM_MODEL or pass in request)")
		return
	}

	content, err := s.llm.Chat(r.Context(), model, body.Messages)
	// 2026-09-01 AI 网关测试修复：当 llm-gateway-go 返回 no_candidate（用户选的 model
	// 没可用 provider）时，按 workspace 的 preferred 列表依次回退重试，让
	// /api/llm/chat 与 /api/llm/stream 行为一致——BFF 路径已实现此回退，
	// 此处补齐非流式路径。
	if err != nil && isNoCandidateError(err) {
		wsID := s.workspaceIDFromRequest(r)
		gw := s.ResolveGateway(wsID)
		if fallback := pickFallbackModel(model, gw.PreferredModels, gw.Models); fallback != "" {
			log.Printf("llm.chat: %s no_candidate, falling back to %s", model, fallback)
			content, err = s.llm.Chat(r.Context(), fallback, body.Messages)
			if err == nil {
				model = fallback
			}
		}
	}
	// 审计：模型调用事件（P3 §2「模型调用…有可检索审计事件」）。
	// detail 只含 model 与消息条数，绝不写消息内容。
	s.Write(r, "llm.chat", "llm:chat", AuditFields{
		Success: err == nil,
		Detail:  fmt.Sprintf("model=%s messages=%d", model, len(body.Messages)),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "llm failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content, "model": model})
}

// llmChatViaBFF 把非流式 chat 转投 S0-B BFF：provider 按 workspace 动态解析
// 网关连接（env 默认值 + 运行时 /api/llm-gateway/config），空/"auto" model 由
// preferred 列表解析，no_candidate 自动回退，usage 计量走 Recorder。响应形状
// 与旧 s.llm 路径完全一致（{"content","model"}），前端无需改动。
func (s *Server) llmChatViaBFF(w http.ResponseWriter, r *http.Request, msgs []aigate.ChatMessage, reqModel string) {
	model := reqModel
	if model == "" {
		// 部署级缺省仍生效；两者皆空时交给 provider 解析 preferred[0]/兜底 auto，
		// 不再像旧路径那样 400——动态网关下「开箱即用」优先。
		model = s.cfg.LLMModel
	}
	bffMsgs := make([]llmbff.Message, len(msgs))
	for i, m := range msgs {
		bffMsgs[i] = llmbff.Message{Role: llmbff.Role(m.Role), Content: m.Content}
	}
	resp, err := s.llmBFF.Chat(r.Context(), llmbff.ChatRequest{
		WorkspaceID: s.workspaceIDFromRequest(r),
		Model:       model,
		Messages:    bffMsgs,
		User:        s.userIDFromRequest(r),
	}, "chat")
	// 审计：与旧路径同构；成功时用 provider 实际使用的 model（含回退）。
	auditModel := model
	if resp != nil {
		auditModel = resp.Model
	}
	s.Write(r, "llm.chat", "llm:chat", AuditFields{
		Success: err == nil,
		Detail:  fmt.Sprintf("model=%s messages=%d", auditModel, len(msgs)),
	})
	if err != nil {
		if errors.Is(err, llmbff.ErrNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "llm gateway not configured (set POCKET_LLM_GATEWAY_URL/_API_KEY or POST /api/llm-gateway/config)")
			return
		}
		writeError(w, http.StatusBadGateway, "llm failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": resp.Content, "model": resp.Model})
}

// llmChatOnce — meeting 摘要/润色等后端内部一次性 chat 的统一入口：
// llmBFF 已装配时走动态网关（与 /api/llm/chat 同源：按 workspace 实时解析
// /api/llm-gateway/config，空 model 用 preferred 首选，no_candidate 自动回退），
// 否则回退静态 s.llm。返回 (content, provider 实际使用的 model, err)。
func (s *Server) llmChatOnce(ctx context.Context, r *http.Request, model string, msgs []aigate.ChatMessage) (string, string, error) {
	if s.llmBFF != nil {
		bffMsgs := make([]llmbff.Message, len(msgs))
		for i, m := range msgs {
			bffMsgs[i] = llmbff.Message{Role: llmbff.Role(m.Role), Content: m.Content}
		}
		resp, err := s.llmBFF.Chat(ctx, llmbff.ChatRequest{
			WorkspaceID: s.workspaceIDFromRequest(r),
			Model:       model,
			Messages:    bffMsgs,
			User:        s.userIDFromRequest(r),
		}, "meeting")
		if err != nil {
			return "", "", err
		}
		return resp.Content, resp.Model, nil
	}
	if s.llm == nil {
		return "", "", llmbff.ErrNotConfigured
	}
	content, err := s.llm.Chat(ctx, model, msgs)
	return content, model, err
}

// =====================================================================
// 后端集成：kxmemory AI 编排（分类/SSOT/总结）
// =====================================================================

// classifyNoteAsync 异步调 kxmemory 分类笔记（创建后触发）
func (s *Server) classifyNoteAsync(note notes.Note) {
	if s.kxmemory == nil {
		return // kxmemory 未配置，跳过
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := s.kxmemory.ClassifyNote(ctx, kxmemory.ClassifyNoteRequest{
		Content:     note.Snippet, // Note 只有 Snippet，完整内容在客户端
		Title:       note.Title,
		ContentType: note.ContentType,
		Domain:      note.Domain,
		Tags:        parseTagsJSON(note.Tags),
	})
	if err != nil {
		log.Printf("[kxmemory] classify note %s failed: %v", note.ID, err)
		// WS 广播失败事件，前端可展示重试按钮
		var kxe *kxmemory.Error
		retryable := true
		code := "KXMEMORY_UNREACHABLE"
		if errors.As(err, &kxe) {
			retryable = kxe.Retryable()
			code = kxe.Code
		}
		s.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: note.UserID}, "note.classification_failed", map[string]any{
			"noteId":    note.ID,
			"code":      code,
			"retryable": retryable,
			"error":     err.Error(),
		})
		return
	}

	// 更新笔记分类结果（回写 domain/tags）
	note.Domain = resp.Classification.Domain
	note.Tags = toTagsJSON(resp.Classification.Tags)
	if note.Title == "" && resp.Classification.SuggestedTitle != "" {
		note.Title = resp.Classification.SuggestedTitle
	}
	if err := s.notesStore.Upsert(context.Background(), &note); err != nil {
		log.Printf("[kxmemory] update note %s after classify failed: %v", note.ID, err)
	}

	log.Printf("[kxmemory] note %s classified: domain=%s category=%s tags=%v confidence=%.2f",
		note.ID, resp.Classification.Domain, resp.Classification.Category,
		resp.Classification.Tags, resp.Classification.Confidence)

	// SSOT 冲突检测：当 kxmemory 报告 conflict_detected 时，把冲突明细推
	// 给前端，让用户决定是 "merge / supersede / keep both"。
	//
	// 为什么不放在 classifyNoteAsync 外层统一 broadcast？
	//  - 成功分类的 note.classified 已经在 handleNoteClassify 同步路径里
	//    推过，避免重复广播。
	//  - 异步路径只在成功分类后才会走到这里，所以不会有遗漏。
	if resp.Status == "conflict_detected" && len(resp.SSOTConflicts) > 0 {
		log.Printf("[kxmemory] SSOT conflict detected for note %s: %d conflicts", note.ID, len(resp.SSOTConflicts))
		s.wsHub.BroadcastTo(ws.BroadcastTarget{UserID: note.UserID}, "note.ssot_conflict", map[string]any{
			"noteId":    note.ID,
			"conflicts": resp.SSOTConflicts,
			"category":  resp.Classification.Category,
			"domain":    resp.Classification.Domain,
		})
	}
}

// parseTagsJSON 把 JSON 字符串数组解析为 []string，解析失败返回空切片
func parseTagsJSON(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	_ = json.Unmarshal([]byte(s), &tags)
	return tags
}

// toTagsJSON 把 []string 序列化为 JSON 字符串数组
func toTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	b, _ := json.Marshal(tags)
	return string(b)
}
