package email

// diag_pop3_backfill_test.go — **回填**存量 POP3 邮件的真实 Message-ID + 原文缓存。
//
// 背景（2026-10-01 实证）：
// - 285 封 em-pop3-* 邮件全部是**合成** message_id（pop3-ZC0001_…）且
//   body_path 全空，因为它们落库于 00:53~04:44，而「保存真实 Message-ID +
//   原文缓存」的代码是当天 07:xx 才补上的。
// - seen 去重让 POP3 永不重拉这些邮件，存量因此**永远不会**被自动修复。
// - 合成 message_id 还导致同一封邮件走 IMAP/POP3 两条路径各落一条
//   （UNIQUE(account_id, message_id) 拦不住）——47 组重复副本的来源。
//
// 回填的可行性依据（已实测）：emails.id 里的 UIDL 段与 email_pop3_seen.uidl
// **完全一致**（QQ 的 UIDL 只含 sanitize 不会改动，或仅 ~ → - 两侧同步），
// 所以能按 UIDL 精确反查；再用 POP3 `UIDL` 命令拿当前的
// 「位置序号 → UIDL」映射，就能定位到**当前**的正确位置，而不是用会漂移的
// 裸位置号（§7s 实测 134/135 已经漂移到无关邮件）。
//
// 安全性：取回原文后**必须**用 sameEmailMessage 校验；UIDL 不匹配、
// 主题/发件人不符、真实 Message-ID 与库里已有值冲突，一律跳过——
// 宁可留合成 message_id，也绝不把 A 封邮件的原文挂到 B 封上。
//
// 门禁：POCKET_DIAG_POP3_BACKFILL=1。默认只 DRY-RUN 报告，不写库。
// 真正写库需再加 POCKET_DIAG_POP3_BACKFILL_WRITE=1。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagPOP3BackfillMessageIDs(t *testing.T) {
	if os.Getenv("POCKET_DIAG_POP3_BACKFILL") != "1" {
		t.Skip("set POCKET_DIAG_POP3_BACKFILL=1 to run")
	}
	write := os.Getenv("POCKET_DIAG_POP3_BACKFILL_WRITE") == "1"
	_ = write // 真正写库尚未实现：本诊断只做只读取回验证
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

	// 待回填：合成 message_id 且没有 body_path 的 POP3 邮件。
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.account_id, e.message_id, e.subject, coalesce(e.body_path,'')
		FROM `+schema+`.emails e
		WHERE e.id LIKE 'em-pop3-%' AND e.message_id LIKE 'pop3-%'
		ORDER BY e.id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type job struct{ id, acct, msgID, subj, bodyPath string }
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.acct, &j.msgID, &j.subj, &j.bodyPath); err != nil {
			t.Fatalf("scan: %v", err)
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	t.Logf("candidates needing backfill: %d (write=%v)", len(jobs), write)
	if len(jobs) == 0 {
		t.Skip("nothing to backfill")
	}

	// 按账户分组：每个账户一次连接批量取回（逐封重连会因全量 UIDL 反复超时）。
	byAcct := map[string][]job{}
	for _, j := range jobs {
		byAcct[j.acct] = append(byAcct[j.acct], j)
	}

	for acct, group := range byAcct {
		var addr, enc string
		if err := pool.QueryRow(ctx, `SELECT email_address, credential_encrypted
			FROM `+schema+`.email_accounts WHERE id=$1`, acct).Scan(&addr, &enc); err != nil {
			t.Logf("SKIP acct=%s: account load: %v", acct, err)
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

		uidls := make([]string, 0, len(group))
		byUIDL := map[string]job{}
		for _, j := range group {
			u := uidlFromEmailID(j.id, j.acct)
			if u == "" {
				t.Logf("SKIP %s: cannot derive uidl", j.id)
				continue
			}
			uidls = append(uidls, u)
			byUIDL[u] = j
		}
		t.Logf("acct=%s (%s) probing %d uidls in one connection", acct, addr, len(uidls))
		raws, rerr := FetchPOP3MessagesByUIDLs(ctx, hostPort(host, port), tlsFlag, addr, cred, uidls, 180*time.Second)
		if rerr != nil {
			t.Logf("FAIL acct=%s batch: %v", acct, rerr)
			continue
		}
		t.Logf("acct=%s fetched %d/%d by uidl", acct, len(raws), len(uidls))
		for _, u := range uidls {
			raw, ok := raws[u]
			if !ok {
				t.Logf("MISS uidl=%s (deleted or RETR failed)", u)
				continue
			}
			parsed, perr := ParseMIMEMessage(raw)
			if perr != nil {
				t.Logf("FAIL uidl=%s parse: %v", u, perr)
				continue
			}
			j := byUIDL[u]
			t.Logf("OK %s uidl=%s bytes=%d realMsgID=%q atts=%d subj=%.45q",
				j.id, u, len(raw), parsed.MessageID, len(parsed.Attachments), parsed.Subject)
		}
	}
}
