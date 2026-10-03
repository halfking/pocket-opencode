package email

// diag_merge_plan_test.go — **只读**迁移预演：29 组 CONFIRMED 合并后各字段会变成什么。
//
// §7w 已确认 29 组是同一封（两侧真实 message_id 相等）。合并方案待用户拍板，
// 但「按建议方案（保留 IMAP 侧 + 迁移 POP3 侧可继承状态）执行后会发生什么」
// **不需要写库就能算出来**——本诊断输出逐组迁移计划，供用户判断。
//
// 迁移规则（与建议方案一致）：
//   - 保留：IMAP 侧那一条（真实 IMAP UID、可再 FETCH、message_id 已真实）
//   - 迁移：POP3 侧的 notified_at / is_read / is_starred / importance /
//     category（取「POP3 有而 IMAP 空」，即 or 语义，不覆盖 IMAP 已有值）
//   - 改指向：email_invoices.email_id 从 POP3 侧改指 IMAP 侧
//   - 软删：POP3 侧置 deleted_at（可逆，不物理删除）
//
// **只 SELECT，不 UPDATE/DELETE。** 门禁 POCKET_DIAG_MERGE_PLAN=1。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagMergePlan(t *testing.T) {
	if os.Getenv("POCKET_DIAG_MERGE_PLAN") != "1" {
		t.Skip("set POCKET_DIAG_MERGE_PLAN=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 与 §7w 相同的 40 组候选，但这次把两侧的完整可继承状态取出来。
	rows, err := pool.Query(ctx, `
		WITH g AS (
		  SELECT account_id, from_address, subject, date,
		         count(*) FILTER (WHERE id NOT LIKE 'em-pop3-%') AS imap_n,
		         count(*) FILTER (WHERE id LIKE 'em-pop3-%') AS pop3_n
		  FROM `+schema+`.emails
		  WHERE COALESCE(deleted_at,0)=0
		  GROUP BY 1,2,3,4 HAVING count(*)>1
		)
		SELECT g.account_id, g.subject,
		       max(e.id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')          AS imap_id,
		       max(e.uid) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')         AS imap_uid,
		       max(e.notified_at) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_notif,
		       coalesce(max(e.importance) FILTER (WHERE e.id NOT LIKE 'em-pop3-%'),'') AS imap_imp,
		       coalesce(max(e.category) FILTER (WHERE e.id NOT LIKE 'em-pop3-%'),'')     AS imap_cat,
		       bool_or(e.is_read) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')    AS imap_read,
		       bool_or(e.is_starred) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_star,
		       (SELECT count(*) FROM `+schema+`.email_invoices i WHERE i.email_id =
		          max(e.id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%'))      AS imap_inv,

		       max(e.id) FILTER (WHERE e.id LIKE 'em-pop3-%')             AS pop3_id,
		       max(e.uid) FILTER (WHERE e.id LIKE 'em-pop3-%')            AS pop3_uid,
		       max(e.notified_at) FILTER (WHERE e.id LIKE 'em-pop3-%')    AS pop3_notif,
		       coalesce(max(e.importance) FILTER (WHERE e.id LIKE 'em-pop3-%'),'') AS pop3_imp,
		       coalesce(max(e.category) FILTER (WHERE e.id LIKE 'em-pop3-%'),'')     AS pop3_cat,
		       bool_or(e.is_read) FILTER (WHERE e.id LIKE 'em-pop3-%')       AS pop3_read,
		       bool_or(e.is_starred) FILTER (WHERE e.id LIKE 'em-pop3-%')    AS pop3_star,
		       (SELECT count(*) FROM `+schema+`.email_invoices i WHERE i.email_id =
		          max(e.id) FILTER (WHERE e.id LIKE 'em-pop3-%'))        AS pop3_inv
		FROM g JOIN `+schema+`.emails e
		  ON e.account_id=g.account_id AND e.subject=g.subject AND e.date=g.date
		 AND lower(e.from_address)=lower(g.from_address)
		WHERE g.imap_n=1 AND g.pop3_n=1
		GROUP BY 1,2 ORDER BY 2`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type grp struct {
		acct, subj                             string
		imapID, pop3ID                         string
		imapUID, pop3UID                       int64
		imapNotif, pop3Notif                   *int64
		imapImp, pop3Imp, imapCat, pop3Cat     string
		imapRead, imapStar, pop3Read, pop3Star bool
		imapInv, pop3Inv                       int
	}
	var groups []grp
	for rows.Next() {
		var g grp
		if err := rows.Scan(&g.acct, &g.subj, &g.imapID, &g.imapUID, &g.imapNotif, &g.imapImp, &g.imapCat,
			&g.imapRead, &g.imapStar, &g.imapInv,
			&g.pop3ID, &g.pop3UID, &g.pop3Notif, &g.pop3Imp, &g.pop3Cat,
			&g.pop3Read, &g.pop3Star, &g.pop3Inv); err != nil {
			t.Fatalf("scan: %v", err)
		}
		groups = append(groups, g)
	}
	rows.Close()
	t.Logf("candidate groups: %d (合并前需先用 §7w 的 message_id 确认筛出 29 组)", len(groups))

	// 只用真实 POP3 侧原文确认过的组做迁移预演——其它组不动。
	// 这里复用 §7w 的确认结果：重新拉一次只为筛出 CONFIRMED 集合。
	realMsgByPop3 := map[string]string{}
	byAcct := map[string][]int{}
	for i, g := range groups {
		byAcct[g.acct] = append(byAcct[g.acct], i)
	}
	for acct, idxs := range byAcct {
		var addr, enc string
		if err := pool.QueryRow(ctx, `SELECT email_address, credential_encrypted
			FROM `+schema+`.email_accounts WHERE id=$1`, acct).Scan(&addr, &enc); err != nil {
			continue
		}
		dataDir := os.Getenv("POCKET_DIAG_DATA_DIR")
		if dataDir == "" {
			dataDir = "C:/workspace/openpocket/data"
		}
		key, kerr := EnsureMasterKey("", dataDir)
		if kerr != nil {
			t.Skipf("master key unavailable: %v", kerr)
		}
		crypto, cerr := NewCrypto(key)
		if cerr != nil {
			t.Fatalf("crypto: %v", cerr)
		}
		cred, derr := crypto.DecryptString(enc)
		if derr != nil || cred == "" {
			continue
		}
		host, port, tlsFlag := pop3EndpointFor(&Account{EmailAddress: addr})
		var uidls []string
		byUIDL := map[string]string{}
		for _, i := range idxs {
			u := uidlFromEmailID(groups[i].pop3ID, acct)
			if u == "" {
				continue
			}
			uidls = append(uidls, u)
			byUIDL[u] = groups[i].pop3ID
		}
		raws, rerr := FetchPOP3MessagesByUIDLs(ctx, hostPort(host, port), tlsFlag, addr, cred, uidls, 180*time.Second)
		if rerr != nil {
			continue
		}
		for u, raw := range raws {
			p, perr := ParseMIMEMessage(raw)
			if perr != nil {
				continue
			}
			realMsgByPop3[byUIDL[u]] = p.MessageID
		}
	}

	var willMerge, notifMigrated, invRePointed, impGained, catGained, readGained, starGained int
	const show = 20
	shown := 0
	for _, g := range groups {
		real := realMsgByPop3[g.pop3ID]
		if real == "" {
			continue // 取不回来 -> 不动
		}
		var imapMsg string
		_ = pool.QueryRow(ctx, `SELECT message_id FROM `+schema+`.emails WHERE id=$1`, g.imapID).Scan(&imapMsg)
		if imapMsg == "" || imapMsg != real {
			continue // NOT-DUP -> 不动
		}
		willMerge++
		ops := []string{}
		if g.pop3Notif != nil && g.imapNotif == nil {
			notifMigrated++
			ops = append(ops, "notified_at")
		}
		if g.pop3Inv > 0 {
			invRePointed += g.pop3Inv
			ops = append(ops, "invoice→imap")
		}
		if g.imapImp == "" && g.pop3Imp != "" {
			impGained++
			ops = append(ops, "importance="+g.pop3Imp)
		}
		if g.imapCat == "" && g.pop3Cat != "" {
			catGained++
			ops = append(ops, "category="+g.pop3Cat)
		}
		if !g.imapRead && g.pop3Read {
			readGained++
			ops = append(ops, "is_read")
		}
		if !g.imapStar && g.pop3Star {
			starGained++
			ops = append(ops, "is_starred")
		}
		if shown < show {
			// 必须打印**完整 id**。旧写法 g.pop3ID[len-16:] 只输出 UIDL 尾段，
			// 看着像 id，实际拿去查库会 0 行——2026-10-01 执行合并前备份时
			// 就因此以为「POP3 侧已被物理删除」，实际 285 封 em-pop3-* 都在。
			// 截断后的值不能用于任何写操作。
			t.Logf("MERGE %-42.42s imap=%s(uid=%d) <- pop3=%s(uid=%d) ops=[%s]",
				g.subj, g.imapID, g.imapUID, g.pop3ID, g.pop3UID, joinOps(ops))
			shown++
		}
	}
	t.Logf("=== 迁移预演结果（保留 IMAP 侧）===")
	t.Logf("将合并的组数:            %d", willMerge)
	t.Logf("  其中迁移 notified_at:  %d", notifMigrated)
	t.Logf("  其中改指发票关联:      %d 条 invoice 行", invRePointed)
	t.Logf("  其中补上 importance:   %d", impGained)
	t.Logf("  其中补上 category:     %d", catGained)
	t.Logf("  其中补上 is_read:      %d", readGained)
	t.Logf("  其中补上 is_starred:   %d", starGained)
}

func joinOps(ops []string) string {
	if len(ops) == 0 {
		return "(无字段需迁移, 纯删重复行)"
	}
	out := ""
	for i, o := range ops {
		if i > 0 {
			out += ", "
		}
		out += o
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
