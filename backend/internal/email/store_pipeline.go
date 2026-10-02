package email

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

// store_pipeline.go — 每日邮件流水线（pipeline.go）所需的存储读写。
//
// 这里只有三类操作：
//  1. 垃圾清理回写：把判定为广告/垃圾的邮件标 category='spam'（真实 IMAP
//     MOVE 由 junk.go 完成，落库与远端状态保持一致）；
//  2. 提醒去重：emails.notified_at 记录重要邮件提醒派发时间；
//  3. 近端扫描：按时间窗口列出待处理邮件，供流水线逐封判定。
//
// 调用方是 scheduler / pipeline（服务端语境），不走 (user, workspace) 请求
// 作用域；账户/邮件的归属在创建时已由 fetcher 写死。

// MarkEmailsSpamByUID 把同账户一批 UID 的邮件标记为垃圾（category='spam' +
// 置已读）。MOVE 成功后调用，保证本地视图与 IMAP 服务器一致。
func (s *Store) MarkEmailsSpamByUID(ctx context.Context, accountID string, uids []int64) error {
	if accountID == "" || len(uids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
UPDATE emails SET category='spam', is_read=TRUE
WHERE account_id=$1 AND uid = ANY($2) AND (category IS NULL OR category = '' OR category <> 'spam')
`, accountID, uids)
	return err
}

// pipelineEmailCols 是流水线扫描邮件所需的最小列集。
// uid 列允许 NULL：客户端推送的历史邮件没有 IMAP UID（不会被 harvester
// 处理；这里只是垃圾扫描/提醒判定的输入，不影响 spam 与 importance）。
const pipelineEmailCols = `id, account_id, uid, workspace_id, from_address, from_name, subject, snippet,
	date, is_read, category, importance, notified_at`

func scanPipelineEmail(row pgx.Row) (*Email, int64, error) {
	var e Email
	var notifiedAt int64
	var category, importance *string
	var uid sql.NullInt64
	err := row.Scan(&e.ID, &e.AccountID, &uid, &e.WorkspaceID, &e.FromAddress, &e.FromName,
		&e.Subject, &e.Snippet, &e.Date, &e.IsRead, &category, &importance, &notifiedAt)
	if err != nil {
		return nil, 0, err
	}
	if uid.Valid {
		e.UID = uid.Int64
	}
	if category != nil {
		e.Category = *category
	}
	if importance != nil {
		e.Importance = *importance
	}
	return &e, notifiedAt, nil
}

// ListEmailsSince 列出 date >= since 的邮件（全部账户），供流水线做垃圾判定
// 与重要提醒扫描。
func (s *Store) ListEmailsSince(ctx context.Context, since int64, limit int) ([]Email, []int64, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+pipelineEmailCols+` FROM emails WHERE date >= $1 AND COALESCE(deleted_at, 0)=0 ORDER BY date DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var emails []Email
	var notified []int64
	for rows.Next() {
		e, n, err := scanPipelineEmail(rows)
		if err != nil {
			return nil, nil, err
		}
		emails = append(emails, *e)
		notified = append(notified, n)
	}
	return emails, notified, rows.Err()
}

// CountHighImportanceOutside 统计「date < before、importance=high、且从未提醒过」
// 的邮件数。
//
// 为什么需要它：重要邮件提醒的扫描窗口是硬编码的 2 天（见 notifyImportant），
// 落在窗口之外的高重要度邮件**永远不会被提醒** —— 不是「这轮没轮到」，是
// 「不在扫描范围里」。而报告上只有 RemindersSent / RemindersScanned /
// RemindersUnclassified，0 这个数**分不清**下面两种情况：
//
//	「这批邮件里确实没有重要的」
//	「有 20 封重要的，但它们太老了，永远不会被提醒」
//
// 这正是 RemindersUnclassified 当初被加出来解决的那类问题（见
// reminder_diag_test.go 的记录），同一个坑换一个方向又出现了一次。这个计数
// 补上的是**时间维度**上的那一份可观测性。
//
// limit 防止极端情况下全表扫；被 limit 截断时本函数**不报错**——调用方把它
// 当「至少这么多」用即可，日志里另有截断提示。
func (s *Store) CountHighImportanceOutside(ctx context.Context, before int64, limit int) (int, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM (
		   SELECT 1 FROM emails
		   WHERE date < $1
		     AND COALESCE(deleted_at, 0) = 0
		     AND COALESCE(notified_at, 0) = 0
		     AND COALESCE(importance, '') = 'high'
		   LIMIT $2
		 ) t`, before, limit).Scan(&n)
	return n, err
}

// MarkEmailsNotified 批量记录重要邮件提醒已派发，防止重复推送。
func (s *Store) MarkEmailsNotified(ctx context.Context, ids []string, at int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE emails SET notified_at=$1 WHERE id = ANY($2)`, at, ids)
	return err
}

// CleanupStalePendingInvoices 把长期重试仍失败的发票置为 failed（终态可人工
// 处理）。maxAttempts 由流水线传入。
func (s *Store) CleanupStalePendingInvoices(ctx context.Context, maxAttempts int, now int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE email_invoices SET status='failed', last_error='重试次数耗尽', updated_at=$1
WHERE status='pending' AND attempts >= $2`, now, maxAttempts)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
