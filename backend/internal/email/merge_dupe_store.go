package email

// merge_dupe_store.go — 合并重复副本的 DB 写操作（受门禁，默认不跑）。
//
// **刻意不复用 SoftDeleteEmailsScoped**：那个是「用户删除」语义，会顺手
//   body_purged=TRUE / snippet='' / body_path=NULL / ai_summary 降级，
// 把 POP3 侧刚回填的**原文缓存**和摘要一起抹掉。合并去重要的是「这行不再
// 参与业务」，但**数据要留着**以便回溯与回滚。
//
// 所以这里只写 deleted_at（毫秒，墓碑语义与 ListDeletedEmailIDsScoped 一致）。
//
// 门禁：调用方必须显式传 confirm=true，否则一律拒绝——这是不可逆的写操作。
// 事务内执行：墓碑与 invoice 改指要么都成功要么都回滚。

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TombstoneDupeEmails 给重复副本打墓碑，并把挂在被墓碑行上的发票改指到保留行。
//
// plans 必须已通过 planMergeDupes 的三道闸门（本函数**再校验一次**两侧 ID
// 与保留关系，不信任调用方）。返回实际处理的组数与改指的发票行数。
//
// confirm=false 时**不执行任何写操作**，只返回将要执行的内容（预演）。
func (s *Store) TombstoneDupeEmails(ctx context.Context, plans []MergePlan, confirm bool) (merged int, invoicesMoved int, err error) {
	if len(plans) == 0 {
		return 0, 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 回滚在 commit 后是 no-op

	now := time.Now().UnixMilli()
	for _, p := range plans {
		if p.KeepEmailID == "" || p.TombstoneID == "" || p.KeepEmailID == p.TombstoneID {
			return 0, 0, fmt.Errorf("merge: invalid plan keep=%q tombstone=%q",
				p.KeepEmailID, p.TombstoneID)
		}
		if !confirm {
			continue
		}
		// 再校验保留侧确实存在、且不是 em-pop3-（IMAP 侧才是保留侧）。
		// 防止调用方传反了把 IMAP 行墓碑掉——那会丢掉可再 FETCH 的唯一记录。
		var keepID string
		if err := tx.QueryRow(ctx, `SELECT id FROM emails WHERE id=$1`, p.KeepEmailID).Scan(&keepID); err != nil {
			return 0, 0, fmt.Errorf("merge: keep side %s not found: %w", p.KeepEmailID, err)
		}
		if strings.HasPrefix(p.KeepEmailID, "em-pop3-") {
			return 0, 0, fmt.Errorf("merge: keep side must not be a POP3 row: %s", p.KeepEmailID)
		}
		if !strings.HasPrefix(p.TombstoneID, "em-pop3-") {
			return 0, 0, fmt.Errorf("merge: tombstone side must be a POP3 row: %s", p.TombstoneID)
		}
		// 发票改指：被墓碑行上的 invoice 转到保留行。
		ct, err := tx.Exec(ctx, `
			UPDATE email_invoices SET email_id = $2 WHERE email_id = $1`,
			p.TombstoneID, p.KeepEmailID)
		if err != nil {
			return 0, 0, fmt.Errorf("merge: repoint invoices for %s: %w", p.TombstoneID, err)
		}
		if n := ct.RowsAffected(); n > 0 {
			invoicesMoved += int(n)
		}
		// 只打墓碑，**保留** body_path / snippet / ai_summary 以便回滚与回溯。
		ct2, err := tx.Exec(ctx, `
			UPDATE emails SET deleted_at = $2 WHERE id = $1`, p.TombstoneID, now)
		if err != nil {
			return 0, 0, fmt.Errorf("merge: tombstone %s: %w", p.TombstoneID, err)
		}
		if ct2.RowsAffected() == 0 {
			return 0, 0, fmt.Errorf("merge: tombstone %s affected 0 rows", p.TombstoneID)
		}
		merged++
	}
	if !confirm {
		return len(plans), 0, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return merged, invoicesMoved, nil
}
