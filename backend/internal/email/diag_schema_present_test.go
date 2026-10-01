package email

// diag_schema_present_test.go — 诊断「后端所有端点 500 / 数据凭空消失」
// （2026-10-01 第二次发生，根因终于定位）。
//
// 症状：模拟器上「邮箱设置」报 Could not load mail settings；宿主打
// GET /api/email/accounts 得到
//     500 {"error":"ERROR: relation \"email_accounts\" does not exist (SQLSTATE 42P01)"}
// 而同一台机器、同一个 DSN 直连，`public.email_accounts` **存在**。
//
// 真因：pocketd 用 db.New 把每条连接的 search_path 钉在 POCKET_PG_SCHEMA
// （本机是 opencode_pocket）。那个 schema 被**别人删掉了**（本会话两次都是），
// 而 db.New 的 CREATE SCHEMA IF NOT EXISTS 只在**启动时**跑一次。于是：
//   · 池还在、healthz 还是 200、/api/auth/login 也正常；
//   · 任何未限定表名都 42P01；
//   · 显式 writeError 的端点（邮箱）→ 500 + 裸 SQL 错误；
//   · 吞掉错误的端点（/api/tasks，server.go:1316 只有 err==nil 分支）→ 200 + 空列表，
//     看起来像「本来就没数据」。
// 重启 pocketd 即恢复（schema 与表被重新建出来），但数据要重新灌。
//
// 排查路上三个假象，写在这里免得下次重走：
//
//  1. pg_stat_activity 里那条跑着 ListAccountsScoped 的连接，**是诊断自己**建的，
//     不是后端的。差点据此断言「后端 search_path 是默认值」。
//  2. `SELECT current_setting('search_path') FROM pg_stat_activity` —— 这个
//     函数是**本会话**的设置，对每一行返回的都是查询者自己的值，看起来
//     「所有连接都是默认 search_path」，于是 public 里的表「明明看得见」。
//     要看别人连接的 search_path 没有直接办法，只能靠「它那条查询能不能跑通」反推。
//  3. `SET search_path TO <不存在的 schema>` **不报错**，后续未限定查询才 42P01。
//     所以「SET 成功」不能当「schema 存在」的证据。
//
// 这个诊断只回答一个问题：**POCKET_PG_SCHEMA 指向的 schema 现在还在吗？**
//
// 跑法：
//
//	POCKET_REAL_MAIL_DSN='postgresql://...' POCKET_DIAG_SCHEMA='opencode_pocket' \
//	go test ./internal/email/ -run TestDiagnosePocketdSchema -v
//
// 不设 DSN 就跳过，不影响常规测试。

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagnosePocketdSchema(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	want := os.Getenv("POCKET_DIAG_SCHEMA")
	if want == "" {
		want = "opencode_pocket"
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// 刻意**不**钉 search_path：要站在「默认视角」看这个 schema 还在不在。
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = $1)`, want).Scan(&exists); err != nil {
		t.Fatalf("probe namespace: %v", err)
	}
	if !exists {
		t.Errorf("schema %q 不存在 —— 这就是所有未限定查询 42P01 的原因。"+
			"重启 pocketd 会重新 CREATE SCHEMA 并跑迁移（数据需重新灌）。", want)
	}

	t.Logf("---- non-system schemas ----")
	for _, s := range diagSchemas(ctx, pool) {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.tables WHERE table_schema=$1`, s).Scan(&n); err != nil {
			t.Logf("  %-45s (count failed: %v)", s, err)
			continue
		}
		t.Logf("  %-45s tables=%d", s, n)
	}

	// 后端真正会问的那几张表，逐个报存在性 + 行数。
	for _, tbl := range []string{"email_accounts", "emails", "email_invoices", "tasks"} {
		var n int
		q := fmt.Sprintf(`SELECT count(*) FROM %s.%s`, diagIdent(want), diagIdent(tbl))
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Logf("  %-18s -> %v", tbl, err)
			continue
		}
		t.Logf("  %-18s -> rows=%d", tbl, n)
	}
}

func diagSchemas(ctx context.Context, pool *pgxpool.Pool) []string {
	rows, err := pool.Query(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema' ORDER BY nspname`)
	if err != nil {
		return []string{"ERR:" + err.Error()}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// diagIdent 给标识符加双引号，避免拼接 SQL 时被奇怪名字带偏。
func diagIdent(s string) string {
	out := []rune{'"'}
	for _, r := range s {
		if r == '"' {
			out = append(out, '"', '"')
			continue
		}
		out = append(out, r)
	}
	return string(append(out, '"'))
}
