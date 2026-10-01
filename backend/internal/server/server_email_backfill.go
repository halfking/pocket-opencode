package server

// 邮件历史回补接口（2026-10-01：「一天前的邮件看不到」的服务端侧）。
//
// 背景：Sync 每轮只取最近 50 封新邮件，用户一天没打开 App 时那一天的邮件
// 从未进过服务端库。客户端的缓存自愈只能补「服务端已有」的，补不到这里。
// 本接口让客户端能显式触发一次按日期窗口的历史回补。
//
// 设计要点：
//   - 作用域（user/workspace）从请求取，与 /api/emails 保持一致，
//     绝不跨 workspace 回补——那会把别人的邮件拉进当前空间。
//   - 逐账户独立执行并各自报错：一个账户凭据失效不该让其余账户的
//     回补全部作废。
//   - 不推进 LastSyncedUID：见 backfill.go 顶部说明，推进会让增量同步
//     跳过尚未拉取的新邮件。

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// handleEmailBackfill — POST /api/email/backfill
//
// body 可选：{"accountId":"…","days":30,"maxMessages":2000}
//
// 不传 accountId 时对当前作用域下**所有已启用账户**执行回补。
// accountId 传了但不属于当前作用域时返回 404，而不是静默成功——
// 静默成功会让调用方以为回补完成、实际什么都没做。
func (s *Server) handleEmailBackfill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil || s.emailFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "email store or fetcher not configured")
		return
	}
	var body struct {
		AccountID   string `json:"accountId"`
		Days        int    `json:"days"`
		MaxMessages int    `json:"maxMessages"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	accounts, err := s.emailStore.ListAccountsScoped(r.Context(), uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	opts := email.BackfillOptions{
		Days:        body.Days,
		MaxMessages: body.MaxMessages,
	}

	var targets []email.Account
	if body.AccountID != "" {
		for _, a := range accounts {
			if a.ID == body.AccountID {
				targets = append(targets, a)
				break
			}
		}
		if len(targets) == 0 {
			// 账户不属于当前作用域：明确 404，不静默成功。
			writeError(w, http.StatusNotFound, "account not found in current scope")
			return
		}
	} else {
		for _, a := range accounts {
			if a.Enabled {
				targets = append(targets, a)
			}
		}
	}

	reports := make([]email.BackfillReport, 0, len(targets))
	totalSaved := 0
	for _, a := range targets {
		rep := s.emailFetcher.BackfillHistory(r.Context(), a.ID, opts)
		totalSaved += rep.Saved
		reports = append(reports, rep)
	}

	// 键名必须是 "accounts"：客户端 emailApi.backfill 读的就是这个字段。
	// 早先这里返回的是 "reports"，客户端拿不到就当成 0 封、也不报错——
	// 一条「回补其实失败了但界面显示成功」的静默路径。
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts":   reports,
		"totalSaved": totalSaved,
		"days":       opts.Days,
	})
}
