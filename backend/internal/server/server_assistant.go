package server

// server_assistant.go — Phase 0 个人助理模块的 HTTP handler 骨架。
//
// 这些 handler 解决审计问题 #1（0 路由接入）：路由全部注册，store 全部
// 可达。业务逻辑（IMAP 抓取、kxmemory AI 调用、原生插件桥接）在 Phase 2/3/4
// 填充；当前每个 handler 至少能做基本的 store CRUD 或返回明确的未配置提示，
// 确保端到端骨架可运行、可测试。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
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

// userIDFromRequest 提取当前请求的用户 ID。
//
// Phase 1 实现：从 Authorization: Bearer <JWT> 解析 user_id claim。
// 如果 JWT 不存在或无效，回退到 "local"（单用户兼容）。
func (s *Server) userIDFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "local" // 回退到单用户模式
	}
	token := strings.TrimSpace(auth[len("Bearer "):])
	if s.jwtSigner == nil {
		return "local"
	}
	claims, err := s.jwtSigner.Parse(token)
	if err != nil || claims.UserID == "" {
		return "local" // JWT 解析失败，回退
	}
	return claims.UserID
}

// =====================================================================
// 认证
// =====================================================================

// handleAuthLogin — Phase 0 真实 JWT 登录入口。
//
// S0-A 扩展：登录成功后，
//   1. 若 identityStore 可用，EnsureDefaultWorkspace 自动为用户建一个
//      "ws_<userID>" 默认 workspace（幂等）。
//   2. 用 SignWithWorkspace 签发带 workspace_id claim 的 JWT，让后续 handler
//      可以从 JWT 拿到隔离边界。
//
// 兼容性：identityStore 或 jwtSigner 未配置时降级到原 Sign 行为，老前端无感。
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

	// 路径 1（生产）：真实 UserStore 校验。
	var userID string
	var role string
	if s.userStore != nil {
		u, err := s.userStore.VerifyPassword(r.Context(), body.Username, body.Password)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		userID = u.ID
		role = u.Role
	} else if s.cfg.DevAuth && body.Username == "admin" && body.Password == "admin" {
		// 路径 2（dev 兼容）：POCKET_DEV_AUTH=true 时 admin/admin。
		userID = "user-admin"
		role = "admin"
	} else {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if s.jwtSigner == nil {
		writeError(w, http.StatusInternalServerError, "JWT signer not configured")
		return
	}

	// S0-A: 确保有默认 workspace，并把 workspace_id 写进 JWT claim。
	wsID := "default"
	if s.identityStore != nil {
		ws, err := s.identityStore.EnsureDefaultWorkspace(r.Context(), userID)
		if err != nil {
			// EnsureDefaultWorkspace 失败不阻断登录——降级到 "default"。
			log.Printf("WARN: EnsureDefaultWorkspace for %s failed: %v (falling back to 'default')", userID, err)
		} else if ws != nil {
			wsID = ws.ID
		}
	}

	token, err := s.jwtSigner.SignWithWorkspace(userID, role, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign JWT")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token":       token,
		"user":        body.Username,
		"user_id":     userID,
		"workspace_id": wsID,
	})
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
	switch r.Method {
	case http.MethodGet:
		domain := r.URL.Query().Get("domain")
		list, err := s.notesStore.List(r.Context(), uid, domain)
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
		if n.ID == "" {
			n.ID = randomID("note")
		}
		// 龙虾架构：异步触发 kxmemory AI 编排（分类/SSOT/关联/待办提取）
		if err := s.notesStore.Upsert(r.Context(), &n); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, n)
		s.wsHub.Broadcast("note.created", &n)
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
	switch r.Method {
	case http.MethodGet:
		// TODO Phase 3: 从 kxmemory 拉取完整内容（本地只缓存元数据）。
		list, err := s.notesStore.List(r.Context(), s.userIDFromRequest(r), "")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for i := range list {
			if list[i].ID == id {
				writeJSON(w, http.StatusOK, list[i])
				return
			}
		}
		writeError(w, http.StatusNotFound, "note not found")
	case http.MethodDelete:
		if err := s.notesStore.Delete(r.Context(), id); err != nil {
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

	// Look up the note. Single-user, ≤ 200 notes → List + filter is fine and
	// avoids adding a new store method.
	list, err := s.notesStore.List(r.Context(), s.userIDFromRequest(r), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var found *notes.Note
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
			break
		}
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
		writeError(w, http.StatusBadGateway, "kxmemory classify failed: "+err.Error())
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

	s.wsHub.Broadcast("note.classified", map[string]any{
		"noteId":         found.ID,
		"domain":         resp.Classification.Domain,
		"category":       resp.Classification.Category,
		"confidence":     resp.Classification.Confidence,
		"tags":           resp.Classification.Tags,
		"suggestedTitle": resp.Classification.SuggestedTitle,
	})

	writeJSON(w, http.StatusOK, resp)
}

// =====================================================================
// 邮箱助手
// =====================================================================

func (s *Server) handleEmailAccounts(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.emailStore.ListAccounts(r.Context(), s.userIDFromRequest(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": list})
	case http.MethodPost:
		// TODO Phase 2: 加密 credential、验证 IMAP 连通性、启动 scheduler。
		writeError(w, http.StatusNotImplemented, "account creation: Phase 2")
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

func (s *Server) handleEmailAccountOps(w http.ResponseWriter, r *http.Request) {
	// TODO Phase 2: PUT/DELETE 账户。
	writeError(w, http.StatusNotImplemented, "Phase 2")
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
	list, err := s.emailStore.ListEmails(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"emails": list})
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
	// /api/emails/{id} — GET 详情 / PATCH 标记已读。
	id := strings.TrimPrefix(r.URL.Path, "/api/emails/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing email id")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			IsRead    *bool `json:"isRead"`
			IsStarred *bool `json:"isStarred"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.IsRead != nil {
			if err := s.emailStore.MarkRead(r.Context(), id, *body.IsRead); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusNotImplemented, "Phase 2: GET/PATCH detail")
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
	// 龙虾架构：客户端抓取 IMAP 后把邮件列表 POST 到此端点，pocketd 批量分类。
	// 分类只发 snippet（前 ~500 字）给 kxmemory，不发完整邮件正文。
	var body struct {
		Emails []email.Email `json:"emails"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Emails) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"classified": 0})
		return
	}

	// 先写入本地库（去重）
	for i := range body.Emails {
		if body.Emails[i].ID == "" {
			body.Emails[i].ID = randomID("email")
		}
		_ = s.emailStore.InsertEmail(r.Context(), body.Emails[i])
	}

	// 异步调 kxmemory 批量分类（非阻塞）
	go s.classifyEmailsAsync(body.Emails)

	writeJSON(w, http.StatusOK, map[string]any{"received": len(body.Emails), "classify": "async"})
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
	statuses, err := s.emailStore.GetSyncStatus(r.Context(), s.userIDFromRequest(r))
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
func (s *Server) classifyEmailsAsync(emails []email.Email) {
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
		if err := s.emailStore.SetClassification(ctx, result.EmailID,
			result.Category, result.Importance, result.Summary, result.SuggestedAction); err != nil {
			log.Printf("[kxmemory] update email %s classification failed: %v", result.EmailID, err)
			continue
		}
		classified++
	}
	log.Printf("[kxmemory] classified %d/%d emails", classified, len(items))
}

func (s *Server) handleEmailSummaries(w http.ResponseWriter, r *http.Request) {
	// TODO Phase 2: list daily summaries.
	writeJSON(w, http.StatusOK, map[string]any{"summaries": []any{}})
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
	sum, err := s.emailStore.GetSummaryByDate(r.Context(), s.userIDFromRequest(r), sub)
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
		if err := s.vaultStore.PutLatest(r.Context(), uid, body.Blob, body.Version); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.wsHub.Broadcast("vault.synced", map[string]string{"userId": uid})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case r.Method == http.MethodGet && (sub == "latest" || sub == ""):
		blob, ver, err := s.vaultStore.GetLatest(r.Context(), uid)
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
		blob, err := s.vaultStore.GetByVersion(r.Context(), uid, ver)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err := s.vaultStore.MarkCurrent(r.Context(), uid, ver); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.wsHub.Broadcast("vault.restored", map[string]any{"userId": uid, "version": ver})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": ver, "blob": blob})
	case r.Method == http.MethodGet && strings.HasPrefix(sub, "versions/"):
		// GET /api/vault/sync/versions/{version} — 单版本加密 blob 详情
		verStr := strings.TrimPrefix(sub, "versions/")
		ver, err := strconv.Atoi(verStr)
		if err != nil || ver <= 0 {
			writeError(w, http.StatusBadRequest, "invalid version")
			return
		}
		blob, err := s.vaultStore.GetByVersion(r.Context(), uid, ver)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blob": blob, "version": ver})
	case r.Method == http.MethodGet && sub == "versions":
		versions, err := s.vaultStore.ListVersions(r.Context(), uid)
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
	} else {
		// JSON body: { "audioPath": "/path/to/audio.wav" } 或 { "audioBase64": "..." }
		var body struct {
			AudioPath    string `json:"audioPath"`
			AudioBase64  string `json:"audioBase64"`
			Filename     string `json:"filename"`
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
		} else if body.AudioPath != "" {
			// 文件路径（本地开发场景）
			var readErr error
			audioData, readErr = os.ReadFile(body.AudioPath)
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read audio file: "+readErr.Error())
				return
			}
			if filename == "" {
				filename = filepath.Base(body.AudioPath)
			}
		} else {
			writeError(w, http.StatusBadRequest, "provide 'file' (multipart), 'audioBase64', or 'audioPath'")
			return
		}
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
	if s.embedder == nil {
		writeError(w, http.StatusServiceUnavailable, "embedder not configured (set POCKET_EMBED_API_KEY)")
		return
	}
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

	embedding, model, err := s.embedder.Embed(r.Context(), body.Text)
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
func (s *Server) handleLLMChat(w http.ResponseWriter, r *http.Request) {
	if s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "llm not configured (set POCKET_LLM_API_KEY)")
		return
	}
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

	model := body.Model
	if model == "" {
		model = s.cfg.LLMModel
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "model required (set POCKET_LLM_MODEL or pass in request)")
		return
	}

	content, err := s.llm.Chat(r.Context(), model, body.Messages)
	if err != nil {
		writeError(w, http.StatusBadGateway, "llm failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content, "model": model})
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

	// TODO: 如果 resp.Status == "conflict_detected"，推送 SSOT 冲突通知给客户端
	if resp.Status == "conflict_detected" && len(resp.SSOTConflicts) > 0 {
		log.Printf("[kxmemory] SSOT conflict detected for note %s: %d conflicts", note.ID, len(resp.SSOTConflicts))
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

