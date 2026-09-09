package server

import (
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/chat_summary"
	"github.com/halfking/pocket-opencode/backend/internal/chatagent"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// server_since.go — 列表 API 的增量（since）过滤助手。
//
// 统一约定：parseSinceQuery 返回 Unix 秒（毫秒输入自动归一）；各 filter
// 对无法解析时间戳的行 fail-open 保留，避免上游时钟缺失时把整页数据清空。

// parseSinceQuery 解析 since 查询参数：兼容 Unix 秒与毫秒，统一为秒；
// 空/非法返回 0（表示不过滤）。
func parseSinceQuery(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	if n > 1_000_000_000_000 {
		n /= 1000
	}
	return n
}

// stampAfterSince 判断秒/毫秒混存的时间戳是否晚于 since（秒）。
// stamp<=0 视为无时间戳，由调用方决定去留。
func stampAfterSince(stamp, sinceSec int64) bool {
	if stamp <= 0 {
		return false
	}
	if stamp > 1_000_000_000_000 {
		stamp /= 1000
	}
	return stamp > sinceSec
}

func filterInstancesSince(instances []model.PocketInstance, since int64) []model.PocketInstance {
	if since <= 0 {
		return instances
	}
	out := make([]model.PocketInstance, 0, len(instances))
	for _, inst := range instances {
		hb, err := time.Parse(time.RFC3339, inst.LastHeartbeatAt)
		if err != nil {
			out = append(out, inst) // 心跳时间不可解析：fail-open
			continue
		}
		if hb.Unix() > since {
			out = append(out, inst)
		}
	}
	return out
}

func filterSessionsSince(sessions []adapter.OpenCodeSession, since int64) []adapter.OpenCodeSession {
	if since <= 0 {
		return sessions
	}
	out := make([]adapter.OpenCodeSession, 0, len(sessions))
	for _, sess := range sessions {
		// OpenCode session time.updated 为毫秒；无时间戳的行 fail-open 保留。
		if sess.TimeUpdated <= 0 || stampAfterSince(sess.TimeUpdated, since) {
			out = append(out, sess)
		}
	}
	return out
}

func filterTasksSince(tasks []task.Task, since int64) []task.Task {
	if since <= 0 {
		return tasks
	}
	out := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.UpdatedAt.IsZero() || t.UpdatedAt.Unix() > since {
			out = append(out, t)
		}
	}
	return out
}

func filterVacationsSince(vacations []email.VacationReply, since int64) []email.VacationReply {
	if since <= 0 {
		return vacations
	}
	out := make([]email.VacationReply, 0, len(vacations))
	for _, v := range vacations {
		if v.UpdatedAt <= 0 || stampAfterSince(v.UpdatedAt, since) {
			out = append(out, v)
		}
	}
	return out
}

func filterEmailAccountsSince(accounts []email.Account, since int64) []email.Account {
	if since <= 0 {
		return accounts
	}
	out := make([]email.Account, 0, len(accounts))
	for _, acc := range accounts {
		// UpdatedAt 是账户配置 SSOT（Unix 秒）；0 视为旧数据，fail-open 保留。
		if acc.UpdatedAt <= 0 || stampAfterSince(acc.UpdatedAt, since) {
			out = append(out, acc)
		}
	}
	return out
}

func filterEmailSummariesSince(summaries []email.DailySummary, since int64) []email.DailySummary {
	if since <= 0 {
		return summaries
	}
	out := make([]email.DailySummary, 0, len(summaries))
	for _, d := range summaries {
		if d.CreatedAt <= 0 || stampAfterSince(d.CreatedAt, since) {
			out = append(out, d)
		}
	}
	return out
}

func filterNotesSince(items []notes.Note, since int64) []notes.Note {
	if since <= 0 {
		return items
	}
	out := make([]notes.Note, 0, len(items))
	for _, n := range items {
		// Note.UpdatedAt 为 epoch 秒；0 视为无时间戳，fail-open 保留。
		if n.UpdatedAt <= 0 || stampAfterSince(n.UpdatedAt, since) {
			out = append(out, n)
		}
	}
	return out
}

func filterScheduledTasksSince(items []*scheduledtask.Task, since int64) []*scheduledtask.Task {
	if since <= 0 {
		return items
	}
	out := make([]*scheduledtask.Task, 0, len(items))
	for _, t := range items {
		if t == nil {
			continue
		}
		if t.UpdatedAt <= 0 || stampAfterSince(t.UpdatedAt, since) {
			out = append(out, t)
		}
	}
	return out
}

func filterNotificationsSince(items []notifycenter.Notification, since int64) []notifycenter.Notification {
	if since <= 0 {
		return items
	}
	out := make([]notifycenter.Notification, 0, len(items))
	for _, n := range items {
		// 通知行不可变（只有 created_at / read_at），按创建时间增量。
		if n.CreatedAt <= 0 || stampAfterSince(n.CreatedAt, since) {
			out = append(out, n)
		}
	}
	return out
}

func filterChatSummariesSince(items []*chat_summary.ChatSummary, since int64) []*chat_summary.ChatSummary {
	if since <= 0 {
		return items
	}
	out := make([]*chat_summary.ChatSummary, 0, len(items))
	for _, c := range items {
		if c == nil {
			continue
		}
		if c.CreatedAt.IsZero() || c.CreatedAt.Unix() > since {
			out = append(out, c)
		}
	}
	return out
}

func filterChatAgentsSince(items []*chatagent.Agent, since int64) []*chatagent.Agent {
	if since <= 0 {
		return items
	}
	out := make([]*chatagent.Agent, 0, len(items))
	for _, a := range items {
		if a == nil {
			continue
		}
		// UpdatedAt 为 epoch 秒；0 视为无时间戳（内置种子行），fail-open 保留。
		if a.UpdatedAt <= 0 || stampAfterSince(a.UpdatedAt, since) {
			out = append(out, a)
		}
	}
	return out
}

func filterCachedSessionsSince(items []*opencode.CachedSession, since int64) []*opencode.CachedSession {
	if since <= 0 {
		return items
	}
	out := make([]*opencode.CachedSession, 0, len(items))
	for _, c := range items {
		if c == nil {
			continue
		}
		if c.UpdatedAt.IsZero() || c.UpdatedAt.Unix() > since {
			out = append(out, c)
		}
	}
	return out
}

// remoteTaskUpdatedAt 把 OpenCode 会话的更新时间（毫秒）落成任务时间戳；
// 无时间戳时退回聚合时刻（秒），保证增量过滤不会漏掉旧会话任务。
func remoteTaskUpdatedAt(updatedAtMs, fallbackUnix int64) time.Time {
	if updatedAtMs > 0 {
		if updatedAtMs < 1_000_000_000_000 {
			updatedAtMs *= 1000
		}
		return time.UnixMilli(updatedAtMs)
	}
	return time.Unix(fallbackUnix, 0)
}

// enrichSessionsFromCompanion 用 agent-companion 的原生会话元数据补齐
// 标题为空的会话（fail-open：companion 不可用或查询失败时保持原样）。
// 只对缺标题的会话发起查询并设上限，避免列表接口被逐条回源放大。
func (s *Server) enrichSessionsFromCompanion(sessions []adapter.OpenCodeSession) []adapter.OpenCodeSession {
	if s == nil || s.companion == nil || len(sessions) == 0 {
		return sessions
	}
	const maxLookups = 50
	lookups := 0
	for i := range sessions {
		if lookups >= maxLookups {
			break
		}
		if sessions[i].Title != "" {
			continue
		}
		lookups++
		sess, err := s.companion.GetSession("", sessions[i].ID)
		if err != nil || sess.Title == "" {
			continue
		}
		sessions[i].Title = sess.Title
	}
	return sessions
}
