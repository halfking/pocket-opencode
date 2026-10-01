package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type purgeEmailsBody struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleEmailPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	var body purgeEmailsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids required")
		return
	}
	if len(body.IDs) > 100 {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	n, paths, err := s.emailStore.SoftDeleteEmailsScoped(
		r.Context(), body.IDs, s.userIDFromRequest(r), s.workspaceIDFromRequest(r), time.Now().UnixMilli(),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.dataDir != "" {
		for _, rel := range paths {
			_ = os.Remove(filepath.Join(s.dataDir, rel))
		}
	}
	// 删除也进操作日志：本地已删的邮件，服务器侧（IMAP 移入垃圾箱）留给
	// 「同步到服务器」按钮执行——用户要求「记录我们的操作形成 log，同步按钮
	// 做可选/全量更新」。这里只记录 pending，不即时动服务器。
	if n > 0 {
		if refs, rerr := s.emailStore.GetEmailsRefsScoped(r.Context(), body.IDs, s.userIDFromRequest(r), s.workspaceIDFromRequest(r)); rerr == nil {
			entries := make([]OpsEntryInput, 0, len(refs))
			for _, ref := range refs {
				if ref.UID <= 0 {
					continue
				}
				entries = append(entries, OpsEntryInput{
					AccountID: ref.AccountID, EmailID: ref.ID, UID: ref.UID,
					Action: "delete", Subject: ref.Subject,
					IdempotencyKey: "del:" + ref.ID,
				})
			}
			s.insertOpsEntries(r, entries)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": n})
}
