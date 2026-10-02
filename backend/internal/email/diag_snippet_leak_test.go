package email

// diag_snippet_leak_test.go — 诊断「收件箱摘要显示成原始 MIME 编码」。
//
// 2026-10-02 在模拟器收件箱上看到：列表里的摘要不是正文，而是
//   ------=_Part_8505717_93977514.1790821420306 Content-Type: text/html; …=E5=B0=8A…
//   PHN0eWxlPgogICAgLmVtbC13IHsK…            （base64 正文）
// 用户看到的是 MIME boundary、Content-* 头和 quoted-printable / base64 串。
// 120 封里 83 封是这种形态。
//
// DeriveSnippet 的**单元测试是绿的**，但用的是「头与正文之间有空行」的完整
// MIME 形态（snippet_test.go 的 realMIMEDump）。真实 IMAP 的 BODY[TEXT] 在
// 某些邮件上连头都取不到（只剩 base64 正文），那一条会漏过去：
//   ParseMIMEMessage 失败 → 不是 HTML → looksLikeMIMEStructure 也判不出
//   （没有 Content-* 头行、没有 boundary 行、第一行也没冒号）
//   → 落到「确认不是 MIME 源码才当纯文本用」，把 base64 原文当正文。
//
// 这个诊断只回答：真库里有多少行摘要仍是原始 MIME，并把某个账户的
// last_synced_uid 归零，好让下一次 sync 用**当前**二进制重写它们，
// 从而区分「历史脏数据」与「当前代码仍在产出脏数据」。
//
// 跑法：
//
//	POCKET_REAL_MAIL_DSN='postgresql://...' POCKET_DIAG_SCHEMA='opencode_pocket' \
//	go test ./internal/email/ -run TestDiagnoseSnippetLeak -v
//
// 需要重置某个账户的同步进度时再加 POCKET_DIAG_RESET_ACCOUNT=<id or email>
// **并**加 POCKET_DIAG_ALLOW_RESET=1 **且显式**写 POCKET_DIAG_SCHEMA=<schema>。
// 不设 DSN 就跳过。

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var snippetLeakMarkers = []string{"Content-Type:", "=_Part_", "=E5=", "PHN0eWxlP", "--_000_"}

func looksLikeRawMIME(s string) bool {
	for _, m := range snippetLeakMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func TestDiagnoseSnippetLeak(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	schema := os.Getenv("POCKET_DIAG_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// 真库诊断必须显式钉 search_path：pocketd 用 db.New 把连接钉在
	// POCKET_PG_SCHEMA 上，不钉的话未限定表名会打到 public 上 42P01。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var total, leak, empty int
	rows, err := pool.Query(ctx, `SELECT id, subject, snippet FROM emails`)
	if err != nil {
		t.Fatalf("query emails: %v", err)
	}
	type row struct{ id, subj, snip string }
	var samples []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.subj, &r.snip); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total++
		if r.snip == "" {
			empty++
			continue
		}
		if looksLikeRawMIME(r.snip) {
			leak++
			if len(samples) < 3 {
				samples = append(samples, r)
			}
		}
	}
	rows.Close()
	t.Logf("emails total=%d  rawMIME=%d  empty=%d  clean=%d", total, leak, empty, total-leak-empty)
	for _, s := range samples {
		t.Logf("  LEAK %s | %s | %s", s.id, s.snip, truncateStr(s.snip, 120))
	}

	// 可选：把某个账户的同步进度归零，好让下一次 sync 用当前代码重写摘要。
	//
	// 这一段是本文件**唯一**的写语句，也是全文件唯一有破坏性的操作：
	// 把 last_synced_uid 归零会让该账户下一次 sync 从头重拉全部邮件。
	// 因此它需要**三个**显式开关，而读路径只要一个：
	//   POCKET_REAL_MAIL_DSN   —— 连哪个库
	//   POCKET_DIAG_SCHEMA      —— 显式写清是哪个 schema
	//   POCKET_DIAG_RESET_ACCOUNT + POCKET_DIAG_ALLOW_RESET=1 —— 确认要写
	//
	// 为什么不沿用读路径的 schema 缺省值：本文件的 schema 缺省是
	// `opencode_pocket`，也就是**生产 schema**。若允许「设了 DSN + 设了
	// RESET_ACCOUNT」就写，那么一条看起来无害的诊断命令
	//   POCKET_REAL_MAIL_DSN=... POCKET_DIAG_RESET_ACCOUNT=ALL go test ...
	// 会把生产库里**每一个**账户的同步进度归零，触发全量重拉 —— 而 SQL 里
	// `who = 'ALL'` 正是支持的形态。写路径因此拒绝使用缺省 schema：
	// 必须显式写出 POCKET_DIAG_SCHEMA，让「我要动的是哪个库」出现在命令里。
	if who := os.Getenv("POCKET_DIAG_RESET_ACCOUNT"); who != "" {
		if os.Getenv("POCKET_DIAG_ALLOW_RESET") != "1" {
			t.Fatalf("POCKET_DIAG_RESET_ACCOUNT=%q 已设置但 POCKET_DIAG_ALLOW_RESET!=1。"+
				"这一步会把 %s.email_accounts.last_synced_uid/last_synced_at 归零，"+
				"该账户下一次 sync 会从头重拉全部邮件。确认要写就补上 POCKET_DIAG_ALLOW_RESET=1。",
				who, schema)
		}
		if os.Getenv("POCKET_DIAG_SCHEMA") == "" {
			t.Fatalf("写路径不接受 schema 缺省值。POCKET_DIAG_SCHEMA 未设置时本文件默认指向 "+
				"%s（生产 schema），而 RESET_ACCOUNT=%q 会改写它的 email_accounts。"+
				"请显式写明 POCKET_DIAG_SCHEMA=<要动的 schema> 再跑。", schema, who)
		}
		tag, err := pool.Exec(ctx,
			`UPDATE email_accounts SET last_synced_uid = 0, last_synced_at = 0
			 WHERE $1 = 'ALL' OR id = $1 OR email_address = $1`, who)
		if err != nil {
			t.Fatalf("reset account: %v", err)
		}
		t.Logf("RESET last_synced_uid for %q -> %d row(s)", who, tag.RowsAffected())
	}
}
