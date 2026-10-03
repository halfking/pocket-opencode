package email

// diag_backfill_align_test.go — **只读**预演：回填真实 Message-ID 后能对齐多少组。
//
// 这是写库前的最后一步确认。§7v 已确定 40 组是「IMAP 1 条 + POP3 1 条」的
// 候选，但同主题同时刻仍可能是两封不同邮件——**只有真实 Message-ID 相等
// 才是同一封**。本诊断按 UIDL 回填（§7u，已实测 216/279 可取回）拿到 POP3
// 侧的真实 Message-ID，然后与 IMAP 侧逐组比对，报告：
//   - 真副本（两侧真实 message_id 相等）→ 可安全合并
//   - 非副本（不相等）→ **两封不同的邮件，绝不能合并**
//
// **只 SELECT，不 UPDATE/DELETE。** 门禁 POCKET_DIAG_ALIGN=1。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagBackfillAlignment(t *testing.T) {
	if os.Getenv("POCKET_DIAG_ALIGN") != "1" {
		t.Skip("set POCKET_DIAG_ALIGN=1 to run (read-only)")
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

	// 候选组：同一 (account, from, subject, date) 下恰好 IMAP 1 条 + POP3 1 条。
	rows, err := pool.Query(ctx, `
		WITH g AS (
		  SELECT account_id, from_address, subject, date,
		         count(*) FILTER (WHERE id NOT LIKE 'em-pop3-%') AS imap_n,
		         count(*) FILTER (WHERE id LIKE 'em-pop3-%') AS pop3_n
		  FROM `+schema+`.emails
		  WHERE COALESCE(deleted_at,0)=0
		  GROUP BY 1,2,3,4 HAVING count(*)>1
		)
		SELECT g.account_id, g.subject, g.date,
		       max(e.id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')         AS imap_id,
		       max(e.message_id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%') AS imap_msgid,
		       max(e.id) FILTER (WHERE e.id LIKE 'em-pop3-%')             AS pop3_id,
		       max(e.message_id) FILTER (WHERE e.id LIKE 'em-pop3-%')     AS pop3_msgid
		FROM g JOIN `+schema+`.emails e
		  ON e.account_id=g.account_id AND e.subject=g.subject AND e.date=g.date
		 AND lower(e.from_address)=lower(g.from_address)
		WHERE g.imap_n=1 AND g.pop3_n=1
		GROUP BY 1,2,3 ORDER BY 2`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type cand struct {
		acct, subj      string
		date            int64
		imapID, imapMsg string
		pop3ID, pop3Msg string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.acct, &c.subj, &c.date, &c.imapID, &c.imapMsg, &c.pop3ID, &c.pop3Msg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cands = append(cands, c)
	}
	rows.Close()
	t.Logf("candidate groups (IMAP1+POP31): %d", len(cands))

	// 按账户分组批量回填 POP3 侧真实 Message-ID。
	byAcct := map[string][]int{}
	for i, c := range cands {
		byAcct[c.acct] = append(byAcct[c.acct], i)
	}
	realByIdx := map[int]string{} // 候选下标 -> POP3 侧真实 message_id
	rawByIdx := map[int][]byte{}  // 候选下标 -> POP3 侧原文（用于写缓存）

	for acct, idxs := range byAcct {
		var addr, enc string
		if err := pool.QueryRow(ctx, `SELECT email_address, credential_encrypted
			FROM `+schema+`.email_accounts WHERE id=$1`, acct).Scan(&addr, &enc); err != nil {
			t.Logf("SKIP acct=%s: %v", acct, err)
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
			t.Logf("SKIP acct=%s: decrypt: %v", acct, derr)
			continue
		}
		host, port, tlsFlag := pop3EndpointFor(&Account{EmailAddress: addr})
		uidls := make([]string, 0, len(idxs))
		byUIDL := map[string]int{}
		for _, i := range idxs {
			u := uidlFromEmailID(cands[i].pop3ID, acct)
			if u == "" {
				continue
			}
			uidls = append(uidls, u)
			byUIDL[u] = i
		}
		raws, rerr := FetchPOP3MessagesByUIDLs(ctx, hostPort(host, port), tlsFlag, addr, cred, uidls, 180*time.Second)
		if rerr != nil {
			t.Logf("FAIL acct=%s batch: %v", acct, rerr)
			continue
		}
		t.Logf("acct=%s fetched %d/%d", acct, len(raws), len(uidls))
		for u, raw := range raws {
			p, perr := ParseMIMEMessage(raw)
			if perr != nil || p.MessageID == "" {
				continue
			}
			i := byUIDL[u]
			realByIdx[i] = p.MessageID
			rawByIdx[i] = raw
		}
	}

	// 逐组比对。
	confirmed, rejected, unresolved := 0, 0, 0
	const show = 15
	for i, c := range cands {
		real := realByIdx[i]
		switch {
		case real == "":
			unresolved++
			if i < show {
				t.Logf("UNRESOLVED %-45.45s pop3=%s (邮件已删除或取回失败)", c.subj, c.pop3ID)
			}
		case real == c.imapMsg:
			confirmed++
			if i < show {
				t.Logf("CONFIRMED  %-45.45s 两侧 message_id 相同 = %s", c.subj, truncStr(real, 40))
			}
		default:
			rejected++
			if i < show {
				t.Logf("NOT-DUP    %-45.45s imap=%s pop3=%s", c.subj, truncStr(c.imapMsg, 28), truncStr(real, 28))
			}
		}
	}
	t.Logf("=== 回填后判定结果 ===")
	t.Logf("CONFIRMED (同一封, 可合并):        %d", confirmed)
	t.Logf("NOT-DUP (两封不同邮件, 不可合并):  %d", rejected)
	t.Logf("UNRESOLVED (取不回来, 需人工):     %d", unresolved)
	t.Logf("可回填原文缓存的邮件数:            %d", len(rawByIdx))
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
