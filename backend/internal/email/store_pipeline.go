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

// scanPipelineEmail 读一行流水线扫描用的邮件。
//
// 2026-10-02：把 workspace_id / from_name / subject / snippet / is_read 也改成
// 容忍 NULL。起因是补 notifyImportant 派发链路的测试时，插入一行 from_name
// 为 NULL 的邮件就直接命中 `cannot scan NULL into *string`，ListEmailsSince
// 整条返回错误 —— 而它的三个调用点（垃圾清理 / 发票候选扫描 / 重要提醒）
// 都是「err 就 AddError 后继续」，于是一行坏数据就让这三步**同时**静默产出
// 0，报告上只剩一个无法解释的 0。
//
// 证据边界（别把它当成已发生的生产故障）：真库 124 行、13 列实测**零 NULL**，
// 且 emails 只有一个写入者 InsertEmailIfNew，它传 Go string（from_name 走原值
// 而非 nullStr），所以 NULL 目前不可达。DDL 侧 from_name/subject/snippet/
// workspace_id 都没有 NOT NULL，历史上这库已被手工加过列（updated_at、
// folder_name），所以这是**潜在**脆弱点而不是当前缺陷 —— 改成容忍只是把
// 「整条扫描死掉」换成「按空串处理」，不改变任何当前能跑通的行为。
func scanPipelineEmail(row pgx.Row) (*Email, int64, error) {
	var e Email
	var notifiedAt int64
	var category, importance *string
	var uid sql.NullInt64
	var workspaceID, fromName, subject, snippet *string
	var isRead *bool
	err := row.Scan(&e.ID, &e.AccountID, &uid, &workspaceID, &e.FromAddress, &fromName,
		&subject, &snippet, &e.Date, &isRead, &category, &importance, &notifiedAt)
	if err != nil {
		return nil, 0, err
	}
	if workspaceID != nil {
		e.WorkspaceID = *workspaceID
	}
	if fromName != nil {
		e.FromName = *fromName
	}
	if subject != nil {
		e.Subject = *subject
	}
	if snippet != nil {
		e.Snippet = *snippet
	}
	if isRead != nil {
		e.IsRead = *isRead
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
