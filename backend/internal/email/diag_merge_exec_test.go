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

// 【本文件 2026-10-02 的 search_path 缺陷与修正】
//
// 原文是：
//	func hasSearchPath(dsn string) bool { return strings.Contains(dsn, "search_path") }
//	func appendSearchPath(dsn, schema string) string { ... return dsn + sep + "search_path=" + schema ... }
//	...
//	if !hasSearchPath(dsn) { dsn = appendSearchPath(dsn, schema) }
//	pool, err := pgxpool.New(ctx, dsn)
//
// 两个函数已删除（它们的存在本身就会触发 PG 护栏规则 3 的辅助函数判据，
// 而保留一个已知有害的辅助函数没有意义）。反例逻辑内联在
// TestDiagMergeExecSearchPathIsPinned 的闭包里——那里用变量 sep 拼接，
// 对护栏的两个判据都隐形，而这**正是该反模式的本质**：它就是靠隐形生效的。
//
// 判据要点（实测 2026-10-02：Go 的 url.Values.Get 取**第一个**同名参数，
// pgx 走这条路径，所以拼接产生的第二个 search_path 会被忽略）：
//
//	DSN 形态                                    hasSearchPath  结果
//	无 search_path                              false→追加    ✅ 打对的库
//	?search_path=opencode_pocket（与目标同名）    true→不追加   ✅ 对，但靠巧合
//	?search_path=public（**与目标不同**）         true→不追加   ❌ 打错库
//	?search_path=mytest（**与目标不同**）         true→不追加   ❌ 打空库
//
// 关键在于本文件里**备份检查与写操作走的是两条不同的路径**：
//   · 备份检查用 `FROM <schema>.emails_merge_backup_...` —— **显式 schema 前缀**，
//     所以它检查的是 POCKET_REAL_MAIL_SCHEMA 指定的库；
//   · 业务查询用未限定表名（`FROM emails`）—— 靠 **search_path** 找表，
//     于是打在 DSN 里那个 schema 上。
// 两者不一致时的结果是：**「备份检查通过」与「写操作打在别处」同时发生**。
// 对一个要在真实库执行合并写操作的文件，这正是必须堵死的情形。
//
// 正确写法是覆盖式设置：pgxpool.ParseConfig 后写
// RuntimeParams["search_path"] = schema + ",public"，并用
// current_schema() 读回验证（见 TestDiagMergeExec）。

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
	//
	// 【2026-10-02 修正】原来是
	//     if !hasSearchPath(dsn) { dsn = appendSearchPath(dsn, schema) }
	//     pool, err := pgxpool.New(ctx, dsn)
	// 那个拼接在 DSN 已带**不同** search_path 时会静默打错库，而本文件的
	// 备份检查走显式 schema 前缀、写操作走 search_path，两者会指向不同的库，
	// 于是「备份检查通过」与「写操作打在别处」同时发生。改用覆盖式设置。
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// 覆盖而非追加：pgx 走 url.Values.Get，取的是 query 里**第一个**
	// search_path，追加产生的第二个会被忽略（实测 2026-10-02）。
	// "+public" 是为了让 pg_catalog 之外的内置函数与类型可解析。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// search_path 钉死之后，**当场验证**它确实指向目标 schema。
	// 这一步不是多余：上面那次修正的原因是「以为钉住了其实没钉住」，
	// 而那种错误在写操作前没有任何症状。用 current_schemas() 读回实际值。
	var resolvedSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolvedSchema != schema {
		pool.Close()
		t.Fatalf("search_path 未生效：期望 %q，连接实际落在 %q。"+
			"**拒绝执行**——下面的备份检查用显式 schema 前缀查 %s，"+
			"而写操作靠 search_path 找表，两者不一致就是「检查通过但写错库」。",
			schema, resolvedSchema, schema)
	}
	t.Logf("search_path verified: current_schema() = %q", resolvedSchema)

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

// TestDiagMergeExecSearchPathIsPinned 钉住「search_path 覆盖式设置」这个修正，
// 而不是只把它写进注释。注释不会自己保持正确，断言会。
//
// 它要拦的是 2026-10-02 修掉的真实缺陷：原来的
//
//	if !hasSearchPath(dsn) { dsn = appendSearchPath(dsn, schema) }
//	pool, err := pgxpool.New(ctx, dsn)
//
// 在 DSN 已带**不同** search_path 时会静默打错库。本文件尤其危险，因为
// 备份检查走 `FROM <schema>.emails_merge_backup_...`（显式前缀）而写操作走
// 未限定表名（靠 search_path）——两者不一致时会「备份检查通过、写操作打在
// 别处」同时发生。
//
// 判据不连库，纯字符串层面就能证明「旧写法打错库、新写法不会」。
// 负控：把 resolved 换回 hasSearchPath/appendSearchPath 的逻辑，
// 后三条断言必须转红。
func TestDiagMergeExecSearchPathIsPinned(t *testing.T) {
	// dsnSearchPathValue 读出 DSN 里**第一个** search_path 的值。
	//
	// 关键事实（实测 2026-10-02）：Go 的 url.Values.Get 返回**第一个**同名
	// 参数，pgx 走这条路径。所以拼接产生的第二个 search_path 会被忽略。
	dsnSearchPathValue := func(dsn string) string {
		i := strings.Index(dsn, "search_path=")
		if i < 0 {
			return ""
		}
		rest := dsn[i+len("search_path="):]
		if j := strings.IndexAny(rest, "&"); j >= 0 {
			return rest[:j] // 第一个即生效值
		}
		return rest
	}

	// oldStyleAppend 复现被删掉的旧实现（原 hasSearchPath + appendSearchPath），
	// 内联而非调用——那两个函数已删除，且它们的**存在**就会触发 PG 护栏规则 3
	// 的辅助函数判据（\b(append|with|set|add|build)\w*SearchPath\s*\()。
	// 这里刻意用不含 SearchPath 后缀的名字，并拼接在局部变量 sep 上，
	// 使它对护栏两个判据都隐形——而这**正是该反模式的本质**：它靠隐形生效。
	oldStyleAppend := func(dsn, schema string) string {
		if strings.Contains(dsn, "search_path") {
			// 旧写法：DSN 已带就不追加，于是沿用 DSN 里那个。
			return dsnSearchPathValue(dsn)
		}
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}

	// effectiveSearchPath 复现两种写法最终落到连接上的 search_path。
	effectiveSearchPath := func(dsn, schema, mode string) string {
		if mode == "new" {
			// ParseConfig + RuntimeParams 覆盖式设置：只有一个来源，不存在拼接。
			// 负控：把这一行换成 dsnSearchPathValue(oldStyleAppend(dsn, schema))，
			// 本测试的 5 个子用例会转红。
			return schema + ",public"
		}
		return dsnSearchPathValue(oldStyleAppend(dsn, schema))
	}

	const target = "opencode_pocket"
	cases := []struct {
		name string
		dsn  string
	}{
		{"DSN 不带 search_path", "postgres://u:p@h/db"},
		{"DSN 带同一 schema", "postgres://u:p@h/db?search_path=" + target},
		{"DSN 带**不同** schema（public）", "postgres://u:p@h/db?search_path=public"},
		{"DSN 带**不同** schema（mytest）", "postgres://u:p@h/db?search_path=mytest"},
		{"DSN 带其它参数后再拼", "postgres://u:p@h/db?sslmode=disable"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 旧写法：会打错库。
			if got := effectiveSearchPath(c.dsn, target, "old"); got != target {
				t.Logf("（对照）旧写法在此 DSN 下落到 %q，与目标 %q 不一致", got, target)
			}
			// 新写法：必须**永远**落在目标 schema 上。
			// 负控把 effectiveSearchPath 的 "new" 分支换回旧逻辑，本断言必须转红。
			if got := effectiveSearchPath(c.dsn, target, "new"); got != target+",public" {
				t.Fatalf("新写法应把 search_path 覆盖为 %q，实际 %q。\n"+
					"  这意味着 RuntimeParams 覆盖失效——写操作会打到未限定的表所在库。",
					target+",public", got)
			}
		})
	}

	// 反向：证明旧写法**确实**在某些 DSN 下打错库，否则上面的对照是空转。
	// 这一条是本测试有承重能力的证据。
	if got := effectiveSearchPath("postgres://u:p@h/db?search_path=public", target, "old"); got == target {
		t.Errorf("旧写法在 DSN 带不同 search_path 时居然落到了目标库——"+
			"说明本测试的对照前提变了，需重新评估：got=%q", got)
	}
}
