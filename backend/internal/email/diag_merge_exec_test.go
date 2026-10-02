package email

// diag_merge_exec_test.go — **执行**重复副本合并（2026-10-01，经用户授权）。
//
// 与 diag_merge_plan_test.go 共用同一套三道闸，区别只在最后一步：
// 预演 confirm=false 只报告，这里 confirm=true 真打墓碑。
//
// 门禁：POCKET_DIAG_MERGE_EXEC=1，否则整个测试 skip。
//
// 安全设计：
//  1. 备份表必须存在且非空，否则拒绝执行（没有回滚路径就不写）；
//  2. 三道闸任一不过就跳过该组，绝不误删；
//  3. 写操作走 TombstoneDupeEmails——内部是事务 + 前缀二次校验 + confirm 门禁，
//     只写 deleted_at 墓碑、保留 body_path/snippet。
//
// **执行前发现并修正的诊断缺陷**：预演日志把 POP3 侧 id 截断成
// `pop3ID[len-16:]`（只看得到 UIDL 尾段）。我据此建备份时拿尾段当 id 查库，
// 0 行，差点误判成「POP3 侧已被物理删除、合并无需执行」——实际 285 封
// em-pop3-* 全在库里。已把预演改成打印完整 id。
//
// 回滚：
//   UPDATE emails SET deleted_at = 0
//    FROM emails_merge_backup_20261001 b WHERE emails.id = b.id;

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// hasSearchPath 判断 DSN 里是否已带 search_path 参数。
func hasSearchPath(dsn string) bool { return strings.Contains(dsn, "search_path") }

// appendSearchPath 往 DSN 追加 search_path=<schema>。
func appendSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func TestDiagMergeExec(t *testing.T) {
	if os.Getenv("POCKET_DIAG_MERGE_EXEC") != "1" {
		t.Skip("merge exec is a write operation; set POCKET_DIAG_MERGE_EXEC=1 to run")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" || schema == "" {
		t.Skip("POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// schema 必须进 search_path：Store 的 SQL 一律不带 schema 限定符
	// （如 `SELECT id FROM emails`），它靠连接的 search_path 找表。
	// 第一次执行就是栽在这：直接用裸 DSN 建连，search_path=public，
	// 于是「keep side not found」——事务回滚了，库没被改。
	// 证据：public.emails 有 0 行，447 封全在 opencode_pocket.emails。
	if !hasSearchPath(dsn) {
		dsn = appendSearchPath(dsn, schema)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 备份必须先在：没有回滚路径就不执行写操作。
	var backupRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM `+schema+`.emails_merge_backup_20261001`).Scan(&backupRows); err != nil {
		t.Fatalf("backup table missing — refusing to run: %v", err)
	}
	if backupRows == 0 {
		t.Fatal("backup table is empty — refusing to run a write without a rollback path")
	}
	t.Logf("backup rows = %d (rollback path verified)", backupRows)

	// 候选组：与预演同一判定（同一主体+发件人+时刻，一侧 em-pop3- 一侧非）。
	rows, err := pool.Query(ctx, `
		WITH g AS (
		  SELECT account_id, subject, date, lower(from_address) AS fa,
		         count(*) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_n,
		         count(*) FILTER (WHERE e.id LIKE 'em-pop3-%')     AS pop3_n
		  FROM `+schema+`.emails e
		  WHERE COALESCE(e.deleted_at,0)=0
		  GROUP BY 1,2,3,4
		)
		SELECT g.account_id, g.subject,
		       max(e.id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_id,
		       max(e.uid) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_uid,
		       max(e.id) FILTER (WHERE e.id LIKE 'em-pop3-%') AS pop3_id,
		       max(e.uid) FILTER (WHERE e.id LIKE 'em-pop3-%') AS pop3_uid
		FROM g JOIN `+schema+`.emails e
		  ON e.account_id=g.account_id AND e.subject=g.subject AND e.date=g.date
		 AND lower(e.from_address)=g.fa
		WHERE g.imap_n=1 AND g.pop3_n=1 AND COALESCE(e.deleted_at,0)=0
		GROUP BY 1,2 ORDER BY 2`)
	if err != nil {
		t.Fatalf("candidate query: %v", err)
	}
	type grp struct {
		acct, subj       string
		imapID, pop3ID   string
		imapUID, pop3UID int64
	}
	var groups []grp
	for rows.Next() {
		var g grp
		if err := rows.Scan(&g.acct, &g.subj, &g.imapID, &g.imapUID, &g.pop3ID, &g.pop3UID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		groups = append(groups, g)
	}
	rows.Close()
	t.Logf("candidate groups: %d", len(groups))

	// 闸 3：保留侧必须是真实 IMAP UID（可再 FETCH 回原文）。
	var eligible []grp
	for _, g := range groups {
		if g.imapUID > 0 && g.pop3ID != "" && g.imapID != "" {
			eligible = append(eligible, g)
		}
	}
	t.Logf("gate3 (keep side has real IMAP uid>0): %d", len(eligible))

	// 闸 2：真实回源取 Message-ID 比对——唯一可靠判据。
	dataDir := os.Getenv("POCKET_DIAG_DATA_DIR")
	if dataDir == "" {
		dataDir = "C:/workspace/openpocket/data"
	}
	masterKey, kerr := EnsureMasterKey("", dataDir)
	if kerr != nil {
		t.Skipf("master key unavailable: %v", kerr)
	}
	crypto, cerr := NewCrypto(masterKey)
	if cerr != nil {
		t.Fatalf("crypto: %v", cerr)
	}

	byAcct := map[string][]int{}
	for i, g := range eligible {
		byAcct[g.acct] = append(byAcct[g.acct], i)
	}
	realMsgByPop3 := map[string]string{}
	for acct, idxs := range byAcct {
		var addr, enc string
		if err := pool.QueryRow(ctx, `SELECT email_address, credential_encrypted
			FROM `+schema+`.email_accounts WHERE id=$1`, acct).Scan(&addr, &enc); err != nil {
			continue
		}
		cred, derr := crypto.DecryptString(enc)
		if derr != nil || cred == "" {
			continue
		}
		host, port, tlsFlag := pop3EndpointFor(&Account{EmailAddress: addr})
		var uidls []string
		byUIDL := map[string]string{}
		for _, i := range idxs {
			u := uidlFromEmailID(eligible[i].pop3ID, acct)
			if u == "" {
				continue
			}
			uidls = append(uidls, u)
			byUIDL[u] = eligible[i].pop3ID
		}
		raws, rerr := FetchPOP3MessagesByUIDLs(ctx, hostPort(host, port), tlsFlag, addr, cred, uidls, 180*time.Second)
		if rerr != nil {
			t.Logf("fetch %s: %v", addr, rerr)
			continue
		}
		for u, raw := range raws {
			if p, perr := ParseMIMEMessage(raw); perr == nil {
				realMsgByPop3[byUIDL[u]] = p.MessageID
			}
		}
	}

	plans := make([]MergePlan, 0, len(eligible))
	var notDup, unfetchable int
	for _, g := range eligible {
		real := realMsgByPop3[g.pop3ID]
		if real == "" {
			unfetchable++
			continue
		}
		var imapMsg string
		_ = pool.QueryRow(ctx, `SELECT COALESCE(message_id,'') FROM `+schema+`.emails WHERE id=$1`, g.imapID).Scan(&imapMsg)
		if imapMsg == "" || imapMsg != real {
			notDup++
			continue
		}
		plans = append(plans, MergePlan{KeepEmailID: g.imapID, TombstoneID: g.pop3ID, Reason: "same real Message-ID"})
	}
	t.Logf("gate2: confirmed=%d  not-dup=%d  unfetchable=%d", len(plans), notDup, unfetchable)

	for _, p := range plans {
		t.Logf("PLAN keep=%s <- tombstone=%s", p.KeepEmailID, p.TombstoneID)
	}
	if len(plans) != 17 {
		t.Fatalf("expected 17 confirmed groups, got %d — aborting rather than guessing", len(plans))
	}

	store, serr := NewStore(pool)
	if serr != nil {
		t.Fatalf("store: %v", serr)
	}
	merged, invMoved, err := store.TombstoneDupeEmails(ctx, plans, true)
	if err != nil {
		t.Fatalf("merge failed (transaction rolled back): %v", err)
	}
	t.Logf("=== EXECUTED === merged=%d invoices_repointed=%d", merged, invMoved)
	t.Logf("rollback: UPDATE emails SET deleted_at=0 FROM emails_merge_backup_20261001 b WHERE emails.id=b.id")
}
