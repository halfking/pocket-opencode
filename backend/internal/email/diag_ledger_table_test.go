package email

// diag_ledger_table_test.go — **只读**诊断：把飞书共享文档那张「发票台账」表
// 用**真实台账行**打出来，按人读的方式逐列核对。
//
// ## 为什么需要它
//
// 需求写的是「建立共享文档及文件，进行整理，需要整理一个列表，记录必要信息
// 并汇总金额」。那张表由 `LedgerRows` 生成、投到飞书电子表格。
// 上一轮在**本地汇总 Markdown** 上发现的正是这类缺陷：表头最后一列叫「文件」，
// 内容却是「已核验/未核验」——**表头与内容对不上**。
//
// 只读代码不够（表头字面量、列数、单元格内容这些「拼装」环节不在任何口径判据的
// 覆盖范围内），所以这里把真实数据渲染出来直接看。
//
// ## 只读
//
// 连接上 `SET default_transaction_read_only = on`；输出只写 t.TempDir()。
// 门控 POCKET_DIAG_LEDGER_TABLE=1 + 显式 DSN/SCHEMA（无缺省值）。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagLedgerTable(t *testing.T) {
	if os.Getenv("POCKET_DIAG_LEDGER_TABLE") != "1" {
		t.Skip("set POCKET_DIAG_LEDGER_TABLE=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" || schema == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN / SCHEMA 必须显式给全，不设缺省值")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	safe := pgx.Identifier{schema}.Sanitize()
	cfg.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(c, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := conn.Exec(c, "SET search_path TO "+safe)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT category, seller, amount, currency, invoice_no, invoice_date,
		        status, file_path, file_name, subject
		   FROM email_invoices ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var invs []Invoice
	for rows.Next() {
		var inv Invoice
		var cur, fp, fn *string
		if err := rows.Scan(&inv.Category, &inv.Seller, &inv.Amount, &cur,
			&inv.InvoiceNo, &inv.InvoiceDate, &inv.Status, &fp, &fn, &inv.Subject); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cur != nil {
			inv.Currency = *cur
		}
		if fp != nil {
			inv.FilePath = *fp
		}
		if fn != nil {
			inv.FileName = *fn
		}
		invs = append(invs, inv)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(invs) == 0 {
		t.Fatal("台账没有任何行：先确认数据，别把空表当成「台账正常」")
	}

	// 主动关掉游标再渲染表格：诊断只需要这批内存里的数据，
	// 不必让连接陪着整个用例。
	rows.Close()

	table, totals := LedgerRows(invs)

	// —— 按人读的方式把表打出来 ——
	t.Logf("=== 飞书台账表（真实 %d 行发票）===", len(invs))
	for i, r := range table {
		cells := make([]string, len(r))
		for j, c := range r {
			cells[j] = fmt.Sprint(c)
		}
		tag := "  "
		if i == 0 {
			tag = "H "
		} else if i == len(table)-1 {
			tag = "Σ "
		}
		t.Logf("%s%s", tag, strings.Join(cells, " | "))
	}
	for _, tt := range totals {
		t.Logf("  totals: %s %.2f × %d 张", tt.Currency, tt.Amount, tt.Count)
	}

	// —— 护栏：所有行列数必须与表头一致 ——
	// 这正是 MD 上踩到的缺陷类别（表头说一套、内容是另一套 / 列数漂移）。
	// CSV/MD 侧已有各自的口径判据，但**列拼装**这一层一直没有被覆盖。
	want := len(table[0])
	if want != 10 {
		t.Errorf("表头列数=%d，want 10。LedgerCellRange 按固定 10 列算写入范围，"+
			"表头宽度变了写入范围就会错位", want)
	}
	for i, r := range table {
		if len(r) != want {
			tag := "明细"
			if i == 0 {
				tag = "表头"
			} else if i == len(table)-1 {
				tag = "合计"
			}
			t.Errorf("%s行(%d)列数=%d，表头=%d。飞书按列位写入，列数不一致会"+
				"把值写进相邻列：%v", tag, i, len(r), want, r)
		}
	}

	// 写入范围必须覆盖全部行。
	rng := LedgerCellRange("sheetTEST", table)
	t.Logf("  写入范围 %s（表头+明细+合计 = %d 行）", rng, len(table))
	if !strings.HasSuffix(rng, fmt.Sprint(len(table))) {
		t.Errorf("写入范围 %s 未覆盖到第 %d 行", rng, len(table))
	}
}
