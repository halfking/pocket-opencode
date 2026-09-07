package email

import (
	"fmt"
	"strings"
)

// CleanupFilter 是用户批量清垃圾的条件。AccountID 只缩小范围，
// 单独指定账户不足以删除（必须再带主题 / 来源 / 日期）。
type CleanupFilter struct {
	AccountID string
	Subject   string
	From      string
	Since     int64
	Until     int64
	Limit     int
}

// CleanupItem 是预览/删除用的最小信封（含 IMAP UID）。
type CleanupItem struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	UID       int64  `json:"uid,omitempty"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Date      int64  `json:"date"`
}

func (f CleanupFilter) HasConstraint() bool {
	return strings.TrimSpace(f.Subject) != "" ||
		strings.TrimSpace(f.From) != "" ||
		f.Since > 0 ||
		f.Until > 0
}

func (f CleanupFilter) Validate() error {
	if !f.HasConstraint() {
		return fmt.Errorf("cleanup requires subject, from, or date range")
	}
	if f.Since > 0 && f.Until > 0 && f.Until < f.Since {
		return fmt.Errorf("until must be >= since")
	}
	return nil
}

func (f CleanupFilter) cappedLimit() int {
	if f.Limit <= 0 {
		return 200
	}
	if f.Limit > 500 {
		return 500
	}
	return f.Limit
}

// MatchCleanup 判断一封邮件是否命中清理条件（大小写不敏感包含）。
func MatchCleanup(e Email, f CleanupFilter) bool {
	if f.AccountID != "" && e.AccountID != f.AccountID {
		return false
	}
	if sub := strings.TrimSpace(f.Subject); sub != "" {
		if !containsFold(e.Subject, sub) {
			return false
		}
	}
	if src := strings.TrimSpace(f.From); src != "" {
		if !containsFold(e.FromAddress, src) && !containsFold(e.FromName, src) {
			return false
		}
	}
	if f.Since > 0 && e.Date < f.Since {
		return false
	}
	if f.Until > 0 && e.Date > f.Until {
		return false
	}
	return true
}

func containsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// SelectDeletable 只返回 IMAP MOVE 成功的 UID 对应邮件 id。
// UID=0 或未出现在 moved 集合里的行不能删库。
func SelectDeletable(items []CleanupItem, movedByAccount map[string][]int64) []string {
	ok := map[string]map[int64]struct{}{}
	for acct, uids := range movedByAccount {
		set := make(map[int64]struct{}, len(uids))
		for _, u := range uids {
			if u > 0 {
				set[u] = struct{}{}
			}
		}
		ok[acct] = set
	}
	var ids []string
	for _, it := range items {
		if it.UID <= 0 {
			continue
		}
		if _, hit := ok[it.AccountID][it.UID]; hit {
			ids = append(ids, it.ID)
		}
	}
	return ids
}
