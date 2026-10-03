package email

// updated_at_guard_test.go — 钉住两个关于 emails.updated_at 的**结构性**断点。
//
// 这两个都不是「数据不对」，而是**代码与 DDL 不一致**，且在**全新部署**上
// 必然发生（真库碰巧有这一列，只是因为历史上有人手工加过）。
//
// ## 断点 1（严重）：全新 schema 里 emails 没有 updated_at 列
//
// `migrate` 的 `CREATE TABLE emails`（store.go:63-88）与它后面的
// `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 补丁里**都没有** updated_at
// ——补丁只加了 notified_at。
//
// 但 `SetSummaryScoped`（store.go:531）写的是：
//
//	UPDATE emails e SET ai_summary = $1, updated_at = $2 ...
//
// 实测（全新 schema，本仓库自己的 migrate 建出来，2026-10-02）：
//
//	列数=26  has_updated_at=false
//	SetSummaryScoped ERR = ERROR: column "updated_at" of relation "emails"
//	                      does not exist (SQLSTATE 42703)
//
// 即**手动总结在全新部署上直接失败**。真库 opencode_pocket 之所以有这一列
// （28 列，多出 updated_at 与 folder_name），是因为历史上有人手工加过——
// 那不是本仓库的 migrate 产物，不能当成「已经有」。
//
// ## 断点 2（较轻）：InsertEmail 的列清单里没有 updated_at
//
// 即使补上 DDL，`InsertEmail`（store.go:560）的 19 列 INSERT 也不含它。
// 于是刚同步进来、未被总结/分类的邮件 `updated_at IS NULL`，
// 而 SQL 里 `NULL > 任何值` 的结果是 NULL（不是 false），
// 这些行在增量请求（`updated_at > since`）里**永不返回**。
//
// 真库实测（2026-10-02 11:4x）：`emails_with_updated_at = 122/124`，
// 缺的两行正是当时最新的两封，`rows_visible_to_incremental = 0`。
//
// ## 为什么这个文件报告而不失败
//
// 两处修法都**改变产品行为**，属用户拍板范围，本轮不擅自决定：
//
//	A. DDL 补 `ALTER TABLE emails ADD COLUMN IF NOT EXISTS updated_at BIGINT`
//	   + InsertEmail 列清单加它、与 created_at 同填 time.Now().Unix()
//	   → 新邮件**立即**参与增量同步（不等被总结后才可见）
//	B. 只给列加 `NOT NULL DEFAULT 0`
//	   → 只消除 NULL，但 `0 > since` 仍为假，**断点 2 失明依旧**，治标
//
// 所以这里先把两个断点**量化并钉住**，让任何一次改动都能立刻看到
// 状态变化。改成 t.Error 的那天，就是行为已定、修法已落地的那天。
//
// 判据是**真跑一次并回读数据库**，不是匹配源码字面量，
// 所以它对「换个写法写这一列」也成立。
//
// 负控：给 migrate 补上 ADD COLUMN、并在 InsertEmail 的列清单里加上
// updated_at → 本文件应从 DEFECT 变成 FIXED。

import (
	"context"
	"testing"
	"time"
)

func TestGuard_EmailsUpdatedAtConsistency(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t) // 全新 schema，走本仓库自己的 migrate
	defer cleanup()
	ctx := context.Background()

	var schema string
	if err := store.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}

	var hasUpdatedAt bool
	if err := store.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM information_schema.columns
		              WHERE table_schema=$1 AND table_name='emails' AND column_name='updated_at')`,
		schema).Scan(&hasUpdatedAt); err != nil {
		t.Fatalf("column probe: %v", err)
	}
	t.Logf("SCHEMA %s: emails.updated_at exists = %v", schema, hasUpdatedAt)

	seedAccount(t, store, "acct-upd", "u", "ws-upd")

	// **两处判定必须用两行独立数据。**
	// 第一版这里只插一行 em-upd-1，然后既用它验断点 1（SetSummaryScoped 会
	// 写 updated_at），又用它验断点 2（InsertEmail 有没有写）。
	// 于是断点 1 一旦被修好（SetSummaryScoped 成功执行），
	// 那一行的 updated_at 就有值了 —— 断点 2 于是**假报** FIXED-2，
	// 而 InsertEmail 其实一个字都没改。
	// 这是「变异对所选用例是 no-op」的近亲：
	// **两处断言共用一份可变状态，后执行的那处会继承前一处的作用。**
	const rowForSummary = "em-upd-summary" // 只给断点 1 用，会被 SetSummaryScoped 写
	const rowForInsert = "em-upd-insert"   // 只给断点 2 用，不许任何别的写路径碰

	insert := func(id, subject string) {
		t.Helper()
		if err := store.InsertEmail(ctx, Email{
			ID: id, AccountID: "acct-upd", UID: 4242,
			FromAddress: "s@example.com", Subject: subject,
			// 两行的 date 必须不同：表上有 (account_id, subject, date) 唯一索引
			// （message_id IS NULL 时的兜底约束），相同会直接撞 23505。
			Snippet: "正文", Date: time.Now().Unix(),
		}); err != nil {
			t.Fatalf("InsertEmail(%s): %v", id, err)
		}
	}
	insert(rowForSummary, "断点1 专用行")
	insert(rowForInsert, "断点2 专用行")

	// ---- 断点 1：SetSummaryScoped 能否在全新 schema 上跑通 ----
	sumErr := store.SetSummaryScoped(ctx, rowForSummary, "u", "ws-upd", "摘要")
	switch {
	case sumErr != nil:
		t.Logf("DEFECT-1 CONFIRMED: SetSummaryScoped 在全新 schema 上失败 —— %v", sumErr)
		t.Logf("  手动总结（POST /api/emails/{id}/summarize）在**新部署**上直接不可用。")
		t.Logf("  根因：migrate 的 CREATE TABLE 与 ALTER 补丁里都没有这一列，")
		t.Logf("        而真库的那一列是历史手工迁移加的，不属于本仓库的 migrate 产物。")
	case !hasUpdatedAt:
		t.Logf("INCONSISTENT: SetSummaryScoped 通过了但列不存在（不该发生，请查 search_path）")
	default:
		t.Logf("FIXED-1: SetSummaryScoped 在全新 schema 上通过")
	}

	// ---- 断点 2：InsertEmail 是否给这一列赋了值 ----
	if !hasUpdatedAt {
		t.Logf("DEFECT-2 SKIPPED: 列不存在，断点 2 无从判定（先修断点 1）")
		return
	}
	// 用 *int64 显式区分 NULL 与 0：§7ei 记录过我把 NULL 误读成 0 的那一次，
	// 而两者在这里推理链不同（NULL=列无默认值；0=有 DEFAULT 0）。
	var updatedRaw *int64
	if err := store.pool.QueryRow(ctx,
		`SELECT updated_at FROM emails WHERE id=$1`, rowForInsert).Scan(&updatedRaw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	// 对照断言：另一行**确实**被 SetSummaryScoped 写过（有值）。
	// 若它也是 NULL，说明 SetSummaryScoped 名义上成功但没写进去，
	// 那样 FIXED-1 就是假绿。
	var summaryRow *int64
	if err := store.pool.QueryRow(ctx,
		`SELECT updated_at FROM emails WHERE id=$1`, rowForSummary).Scan(&summaryRow); err != nil {
		t.Fatalf("read summary row: %v", err)
	}
	t.Logf("CONTROL summary-row updated_at=%v (SetSummaryScoped 写的那一行)", summaryRow)
	switch {
	case updatedRaw == nil:
		t.Logf("DEFECT-2 CONFIRMED: InsertEmail 后 updated_at IS NULL —— " +
			"该行在 `updated_at > since` 的增量请求里**永不返回**（NULL 比较结果为 NULL）")
	case *updatedRaw == 0:
		t.Logf("PARTIAL-2: updated_at=0。`0 > since` 仍为假，失明**依旧**，只是从 NULL 变成 0")
	default:
		t.Logf("FIXED-2: InsertEmail 后 updated_at=%d（非 NULL、> 0），该行能参与增量同步", *updatedRaw)
	}
}
