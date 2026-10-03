package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// server_email_folders.go — 自定义邮件目录、本地迁移操作日志与服务端同步执行。
//
// 四条链路（对应用户四个诉求的后三段）：
//   1. 目录：POST /api/email/folders 创建（本地登记 + IMAP CREATE，同服务器能力），
//      GET 列出（含每目录邮件数），DELETE 删登记（IMAP 删除走账户操作，谨慎暴露）。
//   2. 移动：POST /api/emails/move —— 本地立刻改 folder_name，同时写操作日志，
//      再尽力即时 IMAP MOVE；IMAP 失败不打回本地状态，操作留在日志里等同步按钮。
//   3. 操作日志 + 同步按钮：GET /api/emails/ops 看日志，POST /api/emails/ops/sync
//      把 pending 逐批经 IMAP 执行（ids 可选——可选同步；不给 ids——全量同步）。
//   4. 智能整理：POST /api/emails/organize —— 启发式识别系统通知类邮件，
//      dryRun 预览，确认后整批移入「通知」目录（同链路 2）。

// notificationFolderDefault 智能整理的默认落点目录名。
const notificationFolderDefault = "通知"

// maxMoveIDs 单次移动/记录操作的上限（与 purge 同量级，防误传全库）。
const maxMoveIDs = 200

// handleEmailFolders — GET/POST /api/email/folders
func (s *Server) handleEmailFolders(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		list, err := s.emailStore.ListFoldersScoped(r.Context(), uid, wsID, r.URL.Query().Get("account_id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"folders": list})
	case http.MethodPost:
		var body struct {
			AccountID   string `json:"accountId"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AccountID == "" {
			writeError(w, http.StatusBadRequest, "accountId required")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" || strings.EqualFold(name, "INBOX") {
			writeError(w, http.StatusBadRequest, "invalid folder name")
			return
		}
		if len(name) > 200 {
			writeError(w, http.StatusBadRequest, "folder name too long")
			return
		}
		// 归属校验必须在**任何 IMAP 副作用之前**。
		// 下面 CreateMailbox → dialAndLogin → GetAccountByID 不带用户维度，
		// 拿到的是该 account 自己的凭据。只在 UpsertFolderScoped 里补校验是不够的：
		// 那样攻击者虽然登记不了本地行，却已经在别人邮箱里建出了目录。
		owned, oerr := s.emailStore.AccountOwnedBy(r.Context(), body.AccountID, uid, wsID)
		if oerr != nil {
			writeError(w, http.StatusInternalServerError, oerr.Error())
			return
		}
		if !owned {
			// 与任务写守卫同一口径返 404，不泄露"这个 account 存不存在"。
			writeError(w, http.StatusNotFound, "email account not found")
			return
		}
		// 与服务器能力对齐：真实在 IMAP 上创建。失败时不登记本地行——
		// 目录视图的数据源是 emails.folder_name，登记一个服务器上不存在、
		// 且以后 MOVE 也会失败的目录只会造成「看得到移不进」的死目录。
		f := &email.MailFolder{
			AccountID:   body.AccountID,
			Name:        name,
			DisplayName: strings.TrimSpace(body.DisplayName),
			Source:      "user",
		}
		if s.emailFetcher != nil {
			if err := s.emailFetcher.CreateMailbox(r.Context(), body.AccountID, name); err != nil {
				log.Printf("[email/folders] imap create %s: %v", name, err)
				writeError(w, http.StatusBadGateway, "创建邮件目录失败，请稍后重试")
				return
			}
			f.ServerSynced = true
		}
		if err := s.emailStore.UpsertFolderScoped(r.Context(), f, uid, wsID); err != nil {
			// 兜底：store 层也有一道同样的校验（其它调用方不走这个 handler）。
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "email account not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"folder": f})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleEmailFolderOps — DELETE /api/email/folders/{id}
func (s *Server) handleEmailFolderOps(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/email/folders/"), "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing folder id")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	// 只删本地登记：目录里的邮件回到收件箱视图（folder_name 清空），
	// 服务器目录本身不动——删除真实 IMAP 目录会连带删信，必须显式谨慎。
	folders, err := s.emailStore.ListFoldersScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *email.MailFolder
	for i := range folders {
		if folders[i].ID == id {
			target = &folders[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	// 目录内邮件退回收件箱视图（本地语义；IMAP 侧这些信大多已不在 INBOX，
	// 只有 folder_name 是本地状态，清空即恢复收件箱可见性）。
	if _, err := s.emailStore.CleanFolderName(r.Context(), target.Name, s.userIDFromRequest(r), s.workspaceIDFromRequest(r)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.emailStore.DeleteFolderScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleEmailMove — POST /api/emails/move {ids, folder}
//
// folder 传空串 = 移回收件箱。
func (s *Server) handleEmailMove(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		IDs    []string `json:"ids"`
		Folder string   `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids required")
		return
	}
	if len(body.IDs) > maxMoveIDs {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	rep := s.moveEmailsToFolder(r, body.IDs, strings.TrimSpace(body.Folder))
	writeJSON(w, http.StatusOK, rep)
}

// moveRep 是移动操作统一的响应/执行报告。
type moveRep struct {
	Moved   int      `json:"moved"`            // 本地改写 folder_name 的封数
	Applied int      `json:"applied"`          // 即时 IMAP 同步成功的封数
	Pending int      `json:"pending"`          // 留在操作日志里等待「同步到服务器」的封数
	Errors  []string `json:"errors,omitempty"` // IMAP 侧失败明细（截断）
	Folder  string   `json:"folder,omitempty"`
}

// moveEmailsToFolder 是移动/智能整理共用的执行核心：
// 本地改目录 → 写操作日志 → 尽力即时 IMAP。IMAP 失败不回滚本地，
// 操作保持 pending 由 /api/emails/ops/sync 收口。
func (s *Server) moveEmailsToFolder(r *http.Request, ids []string, folder string) moveRep {
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	ctx := r.Context()
	rep := moveRep{Folder: folder}

	refs, err := s.emailStore.SetEmailsFolderScoped(ctx, ids, uid, wsID, folder)
	if err != nil {
		return moveRep{Errors: []string{err.Error()}}
	}
	rep.Moved = len(refs)

	// 操作日志：本地的迁移要能在「同步到服务器」按钮里被看到/重放。
	entries := make([]OpsEntryInput, 0, len(refs))
	for _, ref := range refs {
		if ref.UID <= 0 {
			continue // 没有 IMAP UID 的行（客户端推送/老数据）无法回源，跳过日志
		}
		entries = append(entries, OpsEntryInput{
			AccountID: ref.AccountID, EmailID: ref.ID, UID: ref.UID,
			Action: "move", TargetFolder: folder, Subject: ref.Subject,
		})
	}
	pending := s.insertOpsEntries(r, entries)

	// 尽力即时同步（与 purge 的 best-effort 同姿态）：成功就 applied，
	// 失败留 pending 等按钮重放。
	if folder != "" && len(pending) > 0 && s.emailFetcher != nil {
		sub := s.executeOpsEntries(r, pending)
		rep.Applied = sub.Applied
		// skipped 是终态（UID 已不在 INBOX，无需重试），不计入待同步。
		rep.Pending = sub.Failed
		rep.Errors = sub.Errors
	} else {
		rep.Pending = len(pending)
	}

	// 目录登记：目标目录若还没登记过（典型是智能整理的「通知」目录），这里
	// 补一条，保证目录页能看到它。UpsertFolderScoped 按 (account, name) 幂等，
	// 重复移动不会产生重复登记行。IMAP 即时成功（或无 fetcher 的纯登记）视
	// 服务端存在性为待确认——下次 LIST/建目录时会收敛。
	if folder != "" && len(refs) > 0 {
		f := &email.MailFolder{
			AccountID:    refs[0].AccountID,
			Name:         folder,
			Source:       "user",
			ServerSynced: s.emailFetcher != nil && rep.Pending == 0,
		}
		if uerr := s.emailStore.UpsertFolderScoped(ctx, f, uid, wsID); uerr != nil {
			log.Printf("[email/folders] upsert %s after move: %v", folder, uerr)
		}
	}
	return rep
}

// handleEmailOrganize — POST /api/emails/organize
func (s *Server) handleEmailOrganize(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		AccountID string `json:"accountId"`
		Folder    string `json:"folder"`
		DryRun    bool   `json:"dryRun"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	folder := strings.TrimSpace(body.Folder)
	if folder == "" {
		folder = notificationFolderDefault
	}
	emails, err := s.emailStore.ListEmailsScoped(r.Context(), email.ListFilter{
		AccountID: body.AccountID,
		Limit:     500,
		// Folder == "" → 只看收件箱（不在任何目录里的邮件）。整理的对象
		// 就是堆积在收件箱里的系统通知；已在目录里的不重复折腾。
	}, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	picked, reasons := email.SelectNotificationEmails(emails)
	ids := make([]string, 0, len(picked))
	for _, e := range picked {
		ids = append(ids, e.ID)
	}
	if body.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{
			"dryRun": true, "folder": folder, "count": len(ids), "ids": ids,
			"reasons": reasons, "scanned": len(emails),
		})
		return
	}
	rep := moveRep{Folder: folder}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, rep)
		return
	}
	rep = s.moveEmailsToFolder(r, ids, folder)
	writeJSON(w, http.StatusOK, rep)
}
