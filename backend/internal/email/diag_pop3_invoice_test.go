package email

// diag_pop3_invoice_test.go — **只读**诊断：用真实 POP3 位置序号补取发票原文。
//
// 目的（2026-10-01 授权：只读 RETR，不删除、不移动、不标记已读）：
// 验证 §7n 的自愈路径在真实 QQ 邮箱上能不能取回那两张 failed 发票的原文。
// 两张发票的位置序号是 134/135，库里的 em-pop3-…-ZL0007_*。
//
// 门禁：必须显式设 POCKET_DIAG_POP3=1，且只做 STAT/UIDL/RETR/QUIT ——
// 不发 DELE（真删），不发任何 IMAP 写命令。
//
// 安全性：拿到原文后用 sameEmailMessage 校验「就是那一封」，位置序号若已
// 漂移会取到**另一封**邮件 —— 那种情况必须丢弃而不是当成发票。

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagPOP3RefetchInvoice(t *testing.T) {
	if os.Getenv("POCKET_DIAG_POP3") != "1" {
		t.Skip("set POCKET_DIAG_POP3=1 to run (read-only POP3 RETR)")
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

	// 取那两张 failed 发票对应的 POP3 邮件行。
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.account_id, e.uid, e.from_address, e.subject, e.date, e.message_id
		FROM `+schema+`.emails e
		JOIN `+schema+`.email_invoices i ON i.email_id = e.id
		WHERE e.id LIKE 'em-pop3-%' AND i.status = 'failed'
		ORDER BY e.uid`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type emRow struct {
		id, acct, from, subj, msgID string
		uid, date                   int64
	}
	var ems []emRow
	for rows.Next() {
		var e emRow
		if err := rows.Scan(&e.id, &e.acct, &e.uid, &e.from, &e.subj, &e.date, &e.msgID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ems = append(ems, e)
	}
	rows.Close()
	if len(ems) == 0 {
		t.Skip("no failed POP3 invoices found")
	}
	t.Logf("failed POP3 invoices to probe: %d", len(ems))

	var accRow struct {
		addr, enc string
	}
	// email_accounts 没有 pop3_host 列——POP3 端点由域名推导（见 pop3EndpointFor）。
	if err := pool.QueryRow(ctx, `
		SELECT email_address, credential_encrypted
		FROM `+schema+`.email_accounts WHERE id=$1`, ems[0].acct).
		Scan(&accRow.addr, &accRow.enc); err != nil {
		t.Fatalf("load account: %v", err)
	}

	dataDir := os.Getenv("POCKET_DIAG_DATA_DIR")
	if dataDir == "" {
		dataDir = "C:/workspace/openpocket/data" // master key 只在主仓
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Skipf("master key unavailable under %s: %v", dataDir, err)
	}
	crypto, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	cred, err := crypto.DecryptString(accRow.enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if cred == "" {
		t.Skip("empty credential")
	}
	// POP3 端点由域名推导（库里没有 pop3_host 列，见 pop3EndpointFor）。
	host, port, tlsFlag := pop3EndpointFor(&Account{EmailAddress: accRow.addr})
	t.Logf("pop3 endpoint derived: %s:%d tls=%v", host, port, tlsFlag)

	_ = time.Second
	for _, e := range ems {
		em := Email{ID: e.id, AccountID: e.acct, UID: e.uid, FromAddress: e.from,
			Subject: e.subj, Date: e.date, MessageID: e.msgID}
		raw, err := FetchPOP3MessageByIndex(ctx, hostPort(host, port), tlsFlag, accRow.addr, cred, int(e.uid), "", 45*time.Second)
		if err != nil {
			t.Logf("FAIL index=%d subject=%.40q: %v", e.uid, e.subj, err)
			continue
		}
		same := sameEmailMessage(&em, raw)
		parsed, perr := ParseMIMEMessage(raw)
		atts := 0
		if perr == nil {
			atts = len(parsed.Attachments)
		}
		// 打印实际取回邮件的头部，用来判断「位置漂移」还是「同一封但解析差异」。
		gotFrom, gotSubj, gotMsgID, gotDate := "", "", "", int64(0)
		if perr == nil {
			gotFrom, gotSubj, gotMsgID = parsed.From, parsed.Subject, parsed.MessageID
			if parsed.Date.Unix() > 0 {
				gotDate = parsed.Date.Unix()
			}
		}
		t.Logf("index=%d bytes=%d sameMessage=%v attachments=%d", e.uid, len(raw), same, atts)
		t.Logf("   db:   from=%q subj=%q msgID=%q date=%d", e.from, e.subj, e.msgID, e.date)
		t.Logf("   pop3: from=%q subj=%q msgID=%q date=%d", gotFrom, gotSubj, gotMsgID, gotDate)
		if !same {
			t.Errorf("index=%d returned a DIFFERENT message — must be discarded, not used as this invoice", e.uid)
			continue
		}
		if perr == nil && atts > 0 {
			for _, a := range parsed.Attachments {
				t.Logf("   attachment: %s contentType=%s bytes=%d isPDF=%v isImage=%v",
					a.Filename, a.ContentType, len(a.Data), isPDFBytes(a.Data), isImageBytes(a.Data))
			}
		}
	}
}

func hostPort(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}
