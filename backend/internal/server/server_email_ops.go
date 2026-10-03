package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// server_email_ops.go — 本地迁移操作日志的记录、查看与服务端同步执行。
//
// 用户诉求：「本地把邮件移到垃圾箱/分类邮箱时，记录操作形成 log；同步按钮
// 做可选或全量的同步更新」。对应三条 API：
//
//	POST /api/emails/ops          前端离线队列回放（幂等，idempotency_key 去重）
//	GET  /api/emails/ops?status=  日志列表（pending/applied/failed）
//	POST /api/emails/ops/sync     执行：{ids?: [...]} 可选同步；{} 全量同步
//
// 执行语义：move → IMAP UID MOVE 到目标目录（不存在则建）；delete → 找到
// 账户垃圾箱（\Trash 属性或常见命名，缺失则建 "Trash"）后移入——**永不
// 直接 EXPUNGE**，误删可在服务商垃圾箱里找回。

// OpsEntryInput 是记录一条操作日志的最小输入（服务端内部传递用）。
type OpsEntryInput struct {
	AccountID      string
	EmailID        string
	UID            int64
	Action         string // move | delete
	TargetFolder   string
	Subject        string
	IdempotencyKey string
}

// toLogEntry 换成存储层模型。
func (in OpsEntryInput) toLogEntry() email.OpsLogEntry {
	return email.OpsLogEntry{
		AccountID:      in.AccountID,
		EmailID:        in.EmailID,
		UID:            in.UID,
		Action:         in.Action,
		TargetFolder:   in.TargetFolder,
		Subject:        in.Subject,
		IdempotencyKey: in.IdempotencyKey,
	}
}

// insertOpsEntries 落操作日志（尽力而为：日志写失败只记告警，不能因此
// 阻断「本地移动已生效」这一事实），返回实际入库的 pending 条目。
func (s *Server) insertOpsEntries(r *http.Request, entries []OpsEntryInput) []email.OpsLogEntry {
	if s.emailStore == nil || len(entries) == 0 {
		return nil
	}
	models := make([]email.OpsLogEntry, 0, len(entries))
	for _, e := range entries {
		models = append(models, e.toLogEntry())
	}
	n, err := s.emailStore.InsertOpsLogScoped(r.Context(), models, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		log.Printf("[email/ops] insert log: %v", err)
		return nil
	}
	if n == 0 {
		return nil // 全部幂等去重：没有新 pending
	}
	keys := make([]string, 0, len(models))
	for _, m := range models {
		keys = append(keys, m.IdempotencyKey)
	}
	pending, err := s.emailStore.ListPendingOpsByIdemKeys(r.Context(), keys, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		log.Printf("[email/ops] reload pending after insert: %v", err)
		return nil
	}
	return pending
}

// handleEmailOps — GET /api/emails/ops（列表）/ POST（离线队列回放）/
// DELETE（清理已终结行）。/api/emails/ops/sync 在 server.go 单独注册，
// 与本精确路径互不吞噬。
func (s *Server) handleEmailOpsLog(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	ws := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		list, err := s.emailStore.ListOpsLogScoped(r.Context(), uid, ws, r.URL.Query().Get("status"), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ops": list})
	case http.MethodPost:
		// 离线队列回放：前端本地 email_ops 表里的 op 逐条推上来。
		// 幂等：idempotency_key 冲突行静默跳过，重放安全。
		var body struct {
			Ops []struct {
				AccountID      string `json:"accountId"`
				EmailID        string `json:"emailId"`
				UID            int64  `json:"uid"`
				Action         string `json:"action"`
				TargetFolder   string `json:"targetFolder"`
				Subject        string `json:"subject"`
				IdempotencyKey string `json:"idempotencyKey"`
			} `json:"ops"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Ops) == 0 {
			writeError(w, http.StatusBadRequest, "ops required")
			return
		}
		if len(body.Ops) > maxMoveIDs {
			writeError(w, http.StatusBadRequest, "too many ops")
			return
		}
		models := make([]email.OpsLogEntry, 0, len(body.Ops))
		for _, o := range body.Ops {
			models = append(models, email.OpsLogEntry{
				AccountID: o.AccountID, EmailID: o.EmailID, UID: o.UID,
				Action: o.Action, TargetFolder: o.TargetFolder, Subject: o.Subject,
				IdempotencyKey: o.IdempotencyKey,
			})
		}
		n, err := s.emailStore.InsertOpsLogScoped(r.Context(), models, uid, ws)
		if err != nil {
			// accountId 不在调用者作用域内 → 404（不回 403/500，不泄露存在性）。
			// 这条尤其要紧：ops 行会被 /ops/sync 真的拿去 IMAP 执行。
			if errors.Is(err, email.ErrNotFound) {
				writeError(w, http.StatusNotFound, "email account not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recorded": n})
	case http.MethodDelete:
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
			writeError(w, http.StatusBadRequest, "ids required")
			return
		}
		n, err := s.emailStore.DeleteOpsLogScoped(r.Context(), body.IDs, uid, ws)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST/DELETE only")
	}
}

// handleEmailOpsSync — POST /api/emails/ops/sync
//
// body {ids?: [...]}：给 ids = 只同步这些 pending 操作（可选同步）；
// 不给 = 全量同步所有 pending（上限 200/批）。返回执行报告与剩余 pending 数。
func (s *Server) handleEmailOpsSync(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "email fetcher not configured")
		return
	}
	uid := s.userIDFromRequest(r)
	ws := s.workspaceIDFromRequest(r)
	var body struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var entries []email.OpsLogEntry
	var err error
	if len(body.IDs) > 0 {
		if len(body.IDs) > maxMoveIDs {
			writeError(w, http.StatusBadRequest, "too many ids")
			return
		}
		entries, err = s.emailStore.ListPendingOpsByIdemKeys(r.Context(), body.IDs, uid, ws)
	} else {
		entries, err = s.emailStore.ClaimPendingOpsScoped(r.Context(), uid, ws, maxMoveIDs)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rep := s.executeOpsEntries(r, entries)
	rep.Remaining, _ = s.emailStore.CountPendingOps(r.Context(), uid, ws)
	writeJSON(w, http.StatusOK, rep)
}

// opsSyncReport 是 /ops/sync 与内部执行共用的报告。
type opsSyncReport struct {
	Executed  int      `json:"executed"`
	Applied   int      `json:"applied"`
	Failed    int      `json:"failed"`
	Skipped   int      `json:"skipped"`
	Remaining int      `json:"remaining"`
	Errors    []string `json:"errors,omitempty"`
}

// opsExecTimeout 单批 IMAP 执行的整体上限。200 封按每封一次 MOVE 实测
// 数十毫秒~秒级估算；超时后未执行的条目保持 pending，可再次点同步。
const opsExecTimeout = 90 * time.Second

// executeOpsEntries 按 (account, action, targetFolder) 分组批量执行，
// 每组一条 IMAP 连接；逐条回写日志状态。
func (s *Server) executeOpsEntries(r *http.Request, entries []email.OpsLogEntry) opsSyncReport {
	rep := opsSyncReport{Executed: len(entries)}
	if len(entries) == 0 {
		return rep
	}
	ws := s.workspaceIDFromRequest(r)
	ctx, cancel := context.WithTimeout(r.Context(), opsExecTimeout)
	defer cancel()

	type groupKey struct{ account, action, target string }
	groups := map[groupKey][]email.OpsLogEntry{}
	for _, e := range entries {
		k := groupKey{e.AccountID, e.Action, e.TargetFolder}
		groups[k] = append(groups[k], e)
	}
	for k, list := range groups {
		switch k.action {
		case "move":
			uids := make([]int64, 0, len(list))
			for _, e := range list {
				uids = append(uids, e.UID)
			}
			moved, err := s.emailFetcher.MoveUIDsToMailbox(ctx, k.account, uids, k.target)
			movedSet := map[int64]bool{}
			for _, m := range moved {
				movedSet[m] = true
			}
			for _, e := range list {
				if movedSet[e.UID] {
					rep.Applied++
					s.finishOp(ctx, e, ws, "applied", "")
					continue
				}
				if err == nil {
					// 整组成功但该 UID 不在成功清单（多为 UID 失效）：
					// 邮件可能已被其他端移走，标 skipped 终态防重试风暴。
					rep.Skipped++
					s.finishOp(ctx, e, ws, "skipped", "uid not moved (already gone?)")
					continue
				}
				rep.Failed++
				msg := err.Error()
				rep.Errors = append(rep.Errors, "uid "+strconv.FormatInt(e.UID, 10)+": "+msg)
				s.finishOp(ctx, e, ws, "failed", msg)
			}
		case "delete":
			trash, terr := s.emailFetcher.FindTrashMailbox(ctx, k.account)
			if terr != nil {
				for _, e := range list {
					rep.Failed++
					s.finishOp(ctx, e, ws, "failed", "find trash: "+terr.Error())
				}
				rep.Errors = append(rep.Errors, "account "+k.account+": "+terr.Error())
				continue
			}
			uids := make([]int64, 0, len(list))
			for _, e := range list {
				uids = append(uids, e.UID)
			}
			moved, err := s.emailFetcher.MoveUIDsToMailbox(ctx, k.account, uids, trash)
			movedSet := map[int64]bool{}
			for _, m := range moved {
				movedSet[m] = true
			}
			for _, e := range list {
				if movedSet[e.UID] {
					rep.Applied++
					s.finishOp(ctx, e, ws, "applied", "")
				} else {
					rep.Failed++
					msg := "move to trash failed"
					if err != nil {
						msg = err.Error()
					}
					rep.Errors = append(rep.Errors, "uid "+strconv.FormatInt(e.UID, 10)+": "+msg)
					s.finishOp(ctx, e, ws, "failed", msg)
				}
			}
		default:
			for _, e := range list {
				rep.Skipped++
				s.finishOp(ctx, e, ws, "skipped", "unsupported action "+k.action)
			}
		}
	}
	return rep
}

// finishOp 回写单条日志状态；失败只告警（执行本身已完成，不能因为状态回写
// 失败把 applied 误报成失败）。
func (s *Server) finishOp(ctx context.Context, e email.OpsLogEntry, workspaceID, status, errMsg string) {
	if err := s.emailStore.UpdateOpsLogStatusScoped(ctx, e.ID, e.UserID, workspaceID, status, errMsg, time.Now().Unix()); err != nil {
		log.Printf("[email/ops] update status %s -> %s: %v", e.ID, status, err)
	}
}
