package email

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// store_folders.go — 自定义邮件目录 + 操作日志的持久化。
//
// 目录 = IMAP mailbox 的本地登记（email_folders）；邮件归属记录在
// emails.folder_name（目录名，不是目录 id——IMAP 侧的真实主键就是名字）。
// 操作日志（email_ops_log）记录本地迁移操作，供「同步到服务器」按钮消费，
// 语义见 model.go 的 OpsLogEntry 注释。

// migrateFolders 目录 + 操作日志建表/加列（幂等）。由 NewStore 调用。
func (s *Store) migrateFolders(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS folder_name TEXT NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS idx_emails_folder ON emails(folder_name) WHERE folder_name <> '';

		CREATE TABLE IF NOT EXISTS email_folders (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			user_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			special TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'user',
			server_synced BOOLEAN NOT NULL DEFAULT FALSE,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			UNIQUE(account_id, name)
		);
		CREATE INDEX IF NOT EXISTS idx_email_folders_ws ON email_folders(workspace_id, account_id);

		CREATE TABLE IF NOT EXISTS email_ops_log (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT '',
			workspace_id TEXT NOT NULL DEFAULT 'default',
			account_id TEXT NOT NULL,
			email_id TEXT NOT NULL DEFAULT '',
			uid BIGINT NOT NULL DEFAULT 0,
			action TEXT NOT NULL CHECK(action IN ('move','delete')),
			target_folder TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL CHECK(status IN ('pending','applied','failed','skipped')) DEFAULT 'pending',
			error TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			applied_at BIGINT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_ops_log_idem ON email_ops_log(idempotency_key) WHERE idempotency_key IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_ops_log_pending ON email_ops_log(status, created_at) WHERE status = 'pending';
		CREATE INDEX IF NOT EXISTS idx_ops_log_ws ON email_ops_log(workspace_id, created_at DESC);
	`)
	return err
}

// --- Folders ---

// UpsertFolderScoped 登记一个目录。(account_id, name) 冲突时视为同一条
// 目录：刷新展示名/特殊标记/synced，不动创建时间。
func (s *Store) UpsertFolderScoped(ctx context.Context, f *MailFolder, userID, workspaceID string) error {
	if f == nil || f.AccountID == "" || strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("folder missing account/name")
	}
	if f.ID == "" {
		f.ID = randomID("fld-")
	}
	if f.CreatedAt == 0 {
		f.CreatedAt = time.Now().Unix()
	}
	f.UpdatedAt = time.Now().Unix()
	if workspaceID != "" {
		f.WorkspaceID = workspaceID
	}
	if f.UserID == "" {
		f.UserID = userID
	}
	if f.DisplayName == "" {
		f.DisplayName = baseMailboxName(f.Name)
	}
	if f.Source == "" {
		f.Source = "user"
	}
	// 归属校验：account_id 来自请求体，必须先确认它确实在调用者的 scope 内。
	// 与 UpsertVacationReplyScoped 用的是同一套守卫（那边注释写明是"阻止
	// 创建 vacation 后修改 accountID 指向他人账户的越权"）。少了这一步，
	// POST /api/email/folders 就能在别人的账户上凭空登记目录；再加上
	// email_folders 的 UNIQUE(account_id, name)，攻击者还能抢注目录名，
	// 受害者自建同名目录时 ON CONFLICT 会去改攻击者那行。
	owned, err := s.AccountOwnedBy(ctx, f.AccountID, userID, workspaceID)
	if err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO email_folders (id, account_id, workspace_id, user_id, name, display_name, special, source, server_synced, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (account_id, name) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			special = EXCLUDED.special,
			server_synced = email_folders.server_synced OR EXCLUDED.server_synced,
			updated_at = EXCLUDED.updated_at`,
		f.ID, f.AccountID, nullStr(f.WorkspaceID), nullStr(f.UserID), f.Name, f.DisplayName,
		f.Special, f.Source, f.ServerSynced, f.CreatedAt, f.UpdatedAt)
	return err
}

// ListFoldersScoped 列出工作区可见目录（可按账户过滤）。
func (s *Store) ListFoldersScoped(ctx context.Context, userID, workspaceID, accountID string) ([]MailFolder, error) {
	q := `
		SELECT f.id, f.account_id, f.workspace_id, f.user_id, f.name, f.display_name,
		       f.special, f.source, f.server_synced, f.created_at, f.updated_at,
		       (SELECT COUNT(*) FROM emails e
		         WHERE e.folder_name = f.name AND e.account_id = f.account_id
		           AND COALESCE(e.deleted_at, 0) = 0) AS email_count
		FROM email_folders f
		JOIN email_accounts a ON a.id = f.account_id
		WHERE a.user_id = $1 AND a.workspace_id = $2`
	args := []any{userID, workspaceID}
	if accountID != "" {
		q += fmt.Sprintf(" AND f.account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	q += " ORDER BY f.created_at, f.name"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MailFolder, 0)
	for rows.Next() {
		var f MailFolder
		var wsID, usrID, disp, special, src sql.NullString
		var count int64
		if err := rows.Scan(&f.ID, &f.AccountID, &wsID, &usrID, &f.Name, &disp,
			&special, &src, &f.ServerSynced, &f.CreatedAt, &f.UpdatedAt, &count); err != nil {
			return nil, err
		}
		f.WorkspaceID, f.UserID = wsID.String, usrID.String
		f.DisplayName, f.Special, f.Source = disp.String, special.String, src.String
		f.Extra = map[string]any{"emailCount": count}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFolderScoped 删除目录登记行（只删登记，不动服务器目录；
// IMAP DELETE 由 handler 显式走 Fetcher 且单独鉴权）。
func (s *Store) DeleteFolderScoped(ctx context.Context, id, userID, workspaceID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM email_folders f USING email_accounts a
		WHERE f.account_id = a.id AND f.id = $1 AND a.user_id = $2 AND a.workspace_id = $3`,
		id, userID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetFolderByNameScoped 取工作区内指定账户的某个目录名登记（没有则 nil）。
func (s *Store) GetFolderByNameScoped(ctx context.Context, accountID, userID, workspaceID, name string) (*MailFolder, error) {
	var f MailFolder
	var wsID, usrID, disp, special, src sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT f.id, f.account_id, f.workspace_id, f.user_id, f.name, f.display_name,
		       f.special, f.source, f.server_synced, f.created_at, f.updated_at
		FROM email_folders f JOIN email_accounts a ON a.id = f.account_id
		WHERE f.account_id = $1 AND a.user_id = $2 AND a.workspace_id = $3 AND f.name = $4`,
		accountID, userID, workspaceID, name).
		Scan(&f.ID, &f.AccountID, &wsID, &usrID, &f.Name, &disp, &special, &src, &f.ServerSynced, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.WorkspaceID, f.UserID = wsID.String, usrID.String
	f.DisplayName, f.Special, f.Source = disp.String, special.String, src.String
	return &f, nil
}

// SetFolderServerSynced 把目录标记为已在服务器上创建成功。
func (s *Store) SetFolderServerSynced(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE email_folders SET server_synced = TRUE, updated_at = $2 WHERE id = $1`, id, time.Now().Unix())
	return err
}

// EmailRef 是移动操作命中的邮件的最小投影，供 IMAP 执行与日志记录。
type EmailRef struct {
	ID        string
	AccountID string
	UID       int64
	Subject   string
}

// SetEmailsFolderScoped 把一批邮件的目录改为 folder（空串 = 移回 INBOX）。
// 返回实际命中的邮件引用（带 account/uid，供 IMAP MOVE 与日志）。
// 已软删除的邮件不参与移动。
func (s *Store) SetEmailsFolderScoped(ctx context.Context, ids []string, userID, workspaceID, folder string) ([]EmailRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 200 {
		return nil, fmt.Errorf("too many ids (max 200)")
	}
	rows, err := s.pool.Query(ctx, `
		UPDATE emails e SET folder_name = $4, updated_at = $5
		FROM email_accounts a
		WHERE e.account_id = a.id AND a.user_id = $1 AND a.workspace_id = $2
		  AND e.id = ANY($3) AND COALESCE(e.deleted_at, 0) = 0
		RETURNING e.id, e.account_id, COALESCE(e.uid, 0), COALESCE(e.subject, '')`,
		userID, workspaceID, ids, folder, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EmailRef, 0, len(ids))
	for rows.Next() {
		var r EmailRef
		if err := rows.Scan(&r.ID, &r.AccountID, &r.UID, &r.Subject); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CleanFolderName 把工作区内登记在指定目录（按名字）的邮件全部退回收件箱
// 视图（folder_name 清空）。删除目录登记时调用：目录没了，邮件回到收件箱，
// 数据本体不受影响。
func (s *Store) CleanFolderName(ctx context.Context, name, userID, workspaceID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE emails e SET folder_name = '', updated_at = $4
		FROM email_accounts a
		WHERE e.account_id = a.id AND a.user_id = $1 AND a.workspace_id = $2 AND e.folder_name = $3`,
		userID, workspaceID, name, time.Now().Unix())
	return tag.RowsAffected(), err
}

// ListPendingOpsByIdemKeys 按幂等键取 pending 操作（可选同步：用户/客户端
// 勾选要执行哪些）。客户端只持有幂等键（服务端日志行 id 是服务端生成的），
// 所以匹配键是 idempotency_key。非 pending 的静默忽略——applied 的操作重放
// 没有意义。
func (s *Store) ListPendingOpsByIdemKeys(ctx context.Context, keys []string, userID, workspaceID string) ([]OpsLogEntry, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if len(keys) > 500 {
		return nil, fmt.Errorf("too many keys (max 500)")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, email_id, uid, action, COALESCE(target_folder,''), COALESCE(subject,'')
		FROM email_ops_log
		WHERE idempotency_key = ANY($1) AND user_id = $2 AND workspace_id = $3 AND status = 'pending'`,
		keys, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OpsLogEntry, 0, len(keys))
	for rows.Next() {
		var e OpsLogEntry
		var target, subject sql.NullString
		if err := rows.Scan(&e.ID, &e.AccountID, &e.EmailID, &e.UID, &e.Action, &target, &subject); err != nil {
			return nil, err
		}
		e.TargetFolder, e.Subject = target.String, subject.String
		e.Status = "pending"
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountPendingOps 返回工作区剩余 pending 操作数（同步按钮的角标）。
func (s *Store) CountPendingOps(ctx context.Context, userID, workspaceID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM email_ops_log
		WHERE user_id = $1 AND workspace_id = $2 AND status = 'pending'`,
		userID, workspaceID).Scan(&n)
	return n, err
}

// GetEmailsRefsScoped 按 id 批量取邮件引用（account/uid/subject），不改任何行。
// 删除操作记日志时用：purge 先软删（正文清理），服务器侧移动需要 UID。
func (s *Store) GetEmailsRefsScoped(ctx context.Context, ids []string, userID, workspaceID string) ([]EmailRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("too many ids (max 500)")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.account_id, COALESCE(e.uid, 0), COALESCE(e.subject, '')
		FROM emails e JOIN email_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1 AND a.workspace_id = $2 AND e.id = ANY($3)`,
		userID, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EmailRef, 0, len(ids))
	for rows.Next() {
		var r EmailRef
		if err := rows.Scan(&r.ID, &r.AccountID, &r.UID, &r.Subject); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Ops log ---

// InsertOpsLogScoped 批量记录本地迁移操作。idempotency_key 冲突的行静默跳过
// （离线队列重放同一操作不产生重复日志），返回实际新增条数。
func (s *Store) InsertOpsLogScoped(ctx context.Context, entries []OpsLogEntry, userID, workspaceID string) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	if len(entries) > 200 {
		return 0, fmt.Errorf("too many ops entries (max 200)")
	}
	inserted := 0
	now := time.Now().Unix()
	for i := range entries {
		e := &entries[i]
		if e.EmailID == "" || e.AccountID == "" || e.Action == "" {
			return inserted, fmt.Errorf("ops entry %d missing email/account/action", i)
		}
		if e.Action != "move" && e.Action != "delete" {
			return inserted, fmt.Errorf("ops entry %d unsupported action %q", i, e.Action)
		}
		if e.Action == "move" && strings.TrimSpace(e.TargetFolder) == "" {
			return inserted, fmt.Errorf("ops entry %d move requires targetFolder", i)
		}
		if e.ID == "" {
			e.ID = randomID("ops-")
		}
		if e.IdempotencyKey == "" {
			// 无客户端幂等键时按 (email, action, target) 派生，同操作重放仍去重。
			e.IdempotencyKey = fmt.Sprintf("ops:%s:%s:%s", e.EmailID, e.Action, e.TargetFolder)
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = now
		}
		e.UpdatedAt = now
		if e.Status == "" {
			e.Status = "pending"
		}
		if workspaceID != "" {
			e.WorkspaceID = workspaceID
		}
		if e.UserID == "" {
			e.UserID = userID
		}
		// 归属校验：account_id 来自请求体。
		//
		// 这条比目录登记那条更危险：ops 行不只是"写一行脏数据"，它会被
		// /api/emails/ops/sync **执行**。ClaimPendingOpsScoped 按调用者的
		// user/workspace 取 pending，攻击者自己种下的行因此会回到他自己手里，
		// 然后 executeOpsEntries 调 Fetcher.MoveUIDsToMailbox(ctx, accountID, ...)。
		// 而 dialAndLogin → GetAccountByID 不带用户维度，会直接取出**受害者**
		// 解密后的 IMAP 凭据去连服务器 —— 等于可以用别人的账户移信。
		owned, oerr := s.AccountOwnedBy(ctx, e.AccountID, userID, workspaceID)
		if oerr != nil {
			return inserted, oerr
		}
		if !owned {
			return inserted, ErrNotFound
		}
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO email_ops_log (id, user_id, workspace_id, account_id, email_id, uid, action,
				target_folder, subject, status, idempotency_key, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
			e.ID, nullStr(e.UserID), nullStr(e.WorkspaceID), e.AccountID, e.EmailID, e.UID,
			e.Action, e.TargetFolder, e.Subject, e.Status, nullStr(e.IdempotencyKey), e.CreatedAt, e.UpdatedAt)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// ListOpsLogScoped 列出操作日志（status 过滤：pending/applied/failed/空=全部）。
func (s *Store) ListOpsLogScoped(ctx context.Context, userID, workspaceID, status string, limit int) ([]OpsLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `
		SELECT id, user_id, workspace_id, account_id, email_id, uid, action,
		       target_folder, subject, status, error, idempotency_key, created_at, updated_at, applied_at
		FROM email_ops_log WHERE user_id = $1 AND workspace_id = $2`
	args := []any{userID, workspaceID}
	if status != "" {
		q += fmt.Sprintf(" AND status = $%d", len(args)+1)
		args = append(args, status)
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OpsLogEntry, 0)
	for rows.Next() {
		var e OpsLogEntry
		var usrID, wsID, target, subject, errMsg, idem sql.NullString
		var uid sql.NullInt64
		var appliedAt sql.NullInt64
		if err := rows.Scan(&e.ID, &usrID, &wsID, &e.AccountID, &e.EmailID, &uid, &e.Action,
			&target, &subject, &e.Status, &errMsg, &idem, &e.CreatedAt, &e.UpdatedAt, &appliedAt); err != nil {
			return nil, err
		}
		e.UserID, e.WorkspaceID = usrID.String, wsID.String
		e.TargetFolder, e.Subject, e.Error, e.IdempotencyKey = target.String, subject.String, errMsg.String, idem.String
		if uid.Valid {
			e.UID = uid.Int64
		}
		if appliedAt.Valid {
			v := appliedAt.Int64
			e.AppliedAt = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClaimPendingOpsScoped 原子认领一批 pending 操作（置 applied 前的执行队列）。
// 用 FOR UPDATE SKIP LOCKED 防止并发同步按钮重复执行同一批。
func (s *Store) ClaimPendingOpsScoped(ctx context.Context, userID, workspaceID string, limit int) ([]OpsLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, email_id, uid, action, target_folder, subject
		FROM email_ops_log
		WHERE user_id = $1 AND workspace_id = $2 AND status = 'pending'
		ORDER BY created_at
		LIMIT $3
		FOR UPDATE SKIP LOCKED`,
		userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OpsLogEntry, 0)
	for rows.Next() {
		var e OpsLogEntry
		var target, subject sql.NullString
		if err := rows.Scan(&e.ID, &e.AccountID, &e.EmailID, &e.UID, &e.Action, &target, &subject); err != nil {
			return nil, err
		}
		e.TargetFolder, e.Subject = target.String, subject.String
		e.Status = "pending"
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateOpsLogStatusScoped 回写执行结果。status: applied | failed | skipped。
func (s *Store) UpdateOpsLogStatusScoped(ctx context.Context, id, userID, workspaceID, status, errMsg string, appliedAt int64) error {
	if appliedAt == 0 {
		appliedAt = time.Now().Unix()
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE email_ops_log SET status = $4, error = $5, updated_at = $6, applied_at = $7
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3`,
		id, userID, workspaceID, status, errMsg, time.Now().Unix(), appliedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOpsLogScoped 清理已终结（applied/failed/skipped）的日志行。
// pending 行不允许清——那是还没同步到服务器的操作。
func (s *Store) DeleteOpsLogScoped(ctx context.Context, ids []string, userID, workspaceID string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM email_ops_log
		WHERE id = ANY($1) AND user_id = $2 AND workspace_id = $3 AND status <> 'pending'`,
		ids, userID, workspaceID)
	return tag.RowsAffected(), err
}
