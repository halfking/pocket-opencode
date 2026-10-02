package email

// diag_rest_dupes_test.go — 给**未合并**的重复候选定性（2026-10-01）。
//
// 背景：17 组合并执行完（447 -> 430 活 + 17 墓碑）后，库里仍有重复候选
// （脚本口径 40 - 17 = 23）。合并时闸 2 判它们 not-dup / unfetchable，
// 但那是「回源取不到」或「回源 Message-ID 不等」的**过程描述**，不是定性。
//
// 这里的判据只有一条，且是唯一可靠的：回源取真实原文，比对 Message-ID。
// 库内 message_id 字段不可信——实测这批组里 equal=0 / differ=7 /
// missing=3，没有一组库内记录相等，若拿库内字段判重会把它们全判成
// 「不是重复」，而事实可能相反（POP3 侧入库时 message_id 落空）。
//
// 三类结果：
//   REAL_DUP     真实 Message-ID 相等 -> 真重复，可以合并
//   NOT_DUP      真实 Message-ID 不等 -> 同主体同主题同日期的**不同邮件**，不能碰
//   UNFETCHABLE  取不回来            -> 状态未知，保持现状
//
// 门禁：POCKET_DIAG_REST_DUPES=1。
//
// **只读**：这里只做 POP3 UIDL 拉取 + 解析，不跑 IMAP MOVE / DELE，
// 不写库。真实邮箱上任何写操作都留到定性之后单独授权。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagRestDupes(t *testing.T) {
	if os.Getenv("POCKET_DIAG_REST_DUPES") != "1" {
		t.Skip("read-only diagnostic; set POCKET_DIAG_REST_DUPES=1 to run")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" || schema == "" {
		t.Skip("POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// 【2026-10-02 修正】原先这里是
	//     if !hasSearchPath(dsn) { dsn = appendSearchPath(dsn, schema) }
	//     cfg, cerr := pgxpool.ParseConfig(dsn)
	// 那个拼接在 DSN 已带**不同** search_path 时会静默打错库——本文件要查的
	// 是目标 schema 里的重复候选，打到别处会输出「重复很少/没有」的假结论。
	// 与 diag_merge_exec_test.go 是同一个缺陷（那两个辅助函数已随那次修正删除）。
	// 改用覆盖式设置：pgx 走 url.Values.Get 取 query 里**第一个** search_path，
	// 拼接产生的第二个会被忽略（实测 2026-10-02）。
	// MaxConns=1：这台机器上便携 PG 的后端进程会间歇性以 0xC0000142
	// (STATUS_DLL_INIT_FAILED) 崩溃，并把整个实例带下去。实测对照：
	// psql 单连接连打 5 次全 OK，而 pgxpool 默认 4 条并发连接几乎必崩——
	// 崩溃的是**新建的子进程**，本机装有金山毒霸+Defender 并存，外加
	// 一批银行安全控件（gdca_cgb_*/D4Svr_CCB/certd_*），任一做进程注入
	// 都可能拦死新起的 backend。诊断只需要一条连接，收敛并发即可绕开。
	cfg, cerr := pgxpool.ParseConfig(dsn)
	if cerr != nil {
		t.Fatalf("parse dsn: %v", cerr)
	}
	// 覆盖而非追加。"+public" 让 pg_catalog 之外的内置函数与类型可解析。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 与合并执行器同口径：HAVING count(*)>1 再筛 imap_n=1 AND pop3_n=1。
	rows, err := pool.Query(ctx, `
		WITH g AS (
		  SELECT account_id, from_address, subject, date
		  FROM `+schema+`.emails
		  WHERE COALESCE(deleted_at,0)=0
		  GROUP BY 1,2,3,4 HAVING count(*)>1
		)
		SELECT g.account_id, g.subject,
		       max(e.id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')      AS imap_id,
		       max(e.uid) FILTER (WHERE e.id NOT LIKE 'em-pop3-%')     AS imap_uid,
		       coalesce(max(e.message_id) FILTER (WHERE e.id NOT LIKE 'em-pop3-%'),'') AS imap_msg,
		       max(e.id) FILTER (WHERE e.id LIKE 'em-pop3-%')         AS pop3_id,
		       max(e.uid) FILTER (WHERE e.id LIKE 'em-pop3-%')        AS pop3_uid,
		       coalesce(max(e.message_id) FILTER (WHERE e.id LIKE 'em-pop3-%'),'') AS pop3_msg
		FROM g JOIN `+schema+`.emails e
		  ON e.account_id=g.account_id AND e.subject=g.subject AND e.date=g.date
		 AND lower(e.from_address)=lower(g.from_address)
		WHERE COALESCE(e.deleted_at,0)=0
		  AND (SELECT count(*) FROM `+schema+`.emails x
		       WHERE x.account_id=g.account_id AND x.subject=g.subject
		         AND x.date=g.date AND lower(x.from_address)=lower(g.from_address)
		         AND x.id NOT LIKE 'em-pop3-%' AND COALESCE(x.deleted_at,0)=0)=1
		  AND (SELECT count(*) FROM `+schema+`.emails y
		       WHERE y.account_id=g.account_id AND y.subject=g.subject
		         AND y.date=g.date AND lower(y.from_address)=lower(g.from_address)
		         AND y.id LIKE 'em-pop3-%' AND COALESCE(y.deleted_at,0)=0)=1
		GROUP BY 1,2 ORDER BY 2`)
	if err != nil {
		t.Fatalf("candidate query: %v", err)
	}
	type grp struct {
		acct, subj       string
		imapID, pop3ID   string
		imapUID, pop3UID int64
		imapMsg, pop3Msg string
	}
	var groups []grp
	for rows.Next() {
		var g grp
		if err := rows.Scan(&g.acct, &g.subj, &g.imapID, &g.imapUID, &g.imapMsg,
			&g.pop3ID, &g.pop3UID, &g.pop3Msg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		groups = append(groups, g)
	}
	rows.Close()
	t.Logf("remaining candidate groups (imap_n=1 AND pop3_n=1, live only): %d", len(groups))

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

	// 库内 message_id 的一致性先记一笔：它们不可信，但值得知道差在哪。
	libEqual, libDiffer, libMissing := 0, 0, 0
	for _, g := range groups {
		switch {
		case g.imapMsg == "" || g.pop3Msg == "":
			libMissing++
		case g.imapMsg == g.pop3Msg:
			libEqual++
		default:
			libDiffer++
		}
	}
	t.Logf("库内 message_id: equal=%d differ=%d missing=%d（仅供参考，判重不靠它）",
		libEqual, libDiffer, libMissing)

	byAcct := map[string][]int{}
	for i, g := range groups {
		byAcct[g.acct] = append(byAcct[g.acct], i)
	}
	realMsgByPop3 := map[string]string{}
	for acct, idxs := range byAcct {
		var addr, enc string
		if err := pool.QueryRow(ctx, `SELECT email_address, credential_encrypted
			FROM `+schema+`.email_accounts WHERE id=$1`, acct).Scan(&addr, &enc); err != nil {
			t.Logf("acct %s: %v", acct, err)
			continue
		}
		cred, derr := crypto.DecryptString(enc)
		if derr != nil || cred == "" {
			t.Logf("acct %s: decrypt failed", acct)
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
			t.Logf("fetch %s: %v", addr, rerr)
			continue
		}
		for u, raw := range raws {
			if p, perr := ParseMIMEMessage(raw); perr == nil {
				realMsgByPop3[byUIDL[u]] = p.MessageID
			}
		}
	}

	var realDup, notDup, unfetchable int
	for _, g := range groups {
		real := realMsgByPop3[g.pop3ID]
		switch {
		case real == "":
			unfetchable++
			t.Logf("UNFETCHABLE %-40.40s imap=%s pop3=%s", g.subj, g.imapID, g.pop3ID)
		case g.imapMsg != "" && g.imapMsg == real:
			realDup++
			t.Logf("REAL_DUP     %-40.40s imap=%s pop3=%s msgid=%s", g.subj, g.imapID, g.pop3ID, real)
		default:
			notDup++
			t.Logf("NOT_DUP      %-40.40s imap=%s pop3=%s\n             imap_msgid=%q\n             real_pop3_msgid=%q",
				g.subj, g.imapID, g.pop3ID, g.imapMsg, real)
		}
	}
	t.Logf("=== 定性结果（只读，未改库）===")
	t.Logf("REAL_DUP(真重复,可合并): %d", realDup)
	t.Logf("NOT_DUP(不同邮件,勿动) : %d", notDup)
	t.Logf("UNFETCHABLE(状态未知)  : %d", unfetchable)
}
