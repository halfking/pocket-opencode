package email

// diag_cleanup_preview_test.go — **只读**：把「剔除 58000 行」与「删 5 个发票孤儿」
// 这两件待授权的清理，展开成**逐条可核对**的清单。
//
// ## 为什么要有这个
//
// 这两件都是不可逆操作（删库行 / 删文件），需要用户显式授权。
// 但让用户基于 agent 的散文描述去决定删除，判据就落在「你信不信这段话」上。
// 本诊断把决策依据变成**可以直接核对的输出**：每一行的当前内容、将要执行的
// SQL、每个文件的大小/SHA/分类理由。
//
// ## 结构上不可能删任何东西
//
// 连接在 \`AfterConnect\` 里执行 \`SET default_transaction_read_only = on\`
// ⇒ 任何写语句（含 DELETE）会被 PostgreSQL **直接报错**。
// 所以这不是「我保证不删」，而是**数据库保证删不了**。
//
// ## 只读
//
// 不写文件、不写库、不删文件。门控 POCKET_DIAG_CLEANUP_PREVIEW=1
// + 显式 DSN/SCHEMA/DATA_DIR。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagCleanupPreview(t *testing.T) {
	if os.Getenv("POCKET_DIAG_CLEANUP_PREVIEW") != "1" {
		t.Skip("set POCKET_DIAG_CLEANUP_PREVIEW=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dsn == "" || schema == "" || dataDir == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN / SCHEMA / DATA_DIR 必须显式给全")
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

	// ── 0. 证明这份连接确实删不掉东西（不是口头保证）──
	if _, derr := pool.Exec(ctx,
		`DELETE FROM email_invoices WHERE id='__preview_probe_must_fail__'`); derr == nil {
		t.Fatal("只读连接竟然接受了 DELETE —— 本诊断的前提失效，立刻停止")
	} else {
		t.Logf("只读门禁自检：DELETE 被数据库拒绝（%v）", derr)
	}

	// ── 1. 待剔除的台账行 ──
	t.Log("=== A. 待剔除的台账行 ===")
	type row struct {
		id, userID, ws, seller, kind, status, ino, idate, fpath, fname string
		amt                                                            float64
	}
	rows, err := pool.Query(ctx,
		`SELECT id, user_id, workspace_id, seller, kind, status,
		        COALESCE(invoice_no,''), COALESCE(invoice_date,''),
		        COALESCE(file_path,''), COALESCE(file_name,''), amount
		   FROM email_invoices
		  WHERE status <> 'downloaded' AND status <> 'filed'
		  ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	nRow := 0
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.userID, &r.ws, &r.seller, &r.kind, &r.status,
			&r.ino, &r.idate, &r.fpath, &r.fname, &r.amt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		nRow++
		t.Logf("  id=%s", r.id)
		t.Logf("    seller=%s  kind=%s  status=%s  amount=%.2f", r.seller, r.kind, r.status, r.amt)
		t.Logf("    invoice_no=%q  invoice_date=%q", r.ino, r.idate)
		t.Logf("    file_path=%q  file_name=%q", r.fpath, r.fname)
		t.Logf("    将执行：DeleteInvoiceScoped(ctx, %q, %q, %q)", r.id, r.userID, r.ws)
		t.Logf("    等价 SQL：DELETE FROM email_invoices WHERE id='%s' AND workspace_id='%s' AND user_id='%s'",
			r.id, r.ws, r.userID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if nRow == 0 {
		t.Log("  （无未下载/未归档行）")
	}

	// ── 2. 台账引用 vs 磁盘实际 ──
	refd, err := pool.Query(ctx,
		`SELECT COALESCE(file_name,'') FROM email_invoices
		  WHERE file_path IS NOT NULL AND file_path <> ''`)
	if err != nil {
		t.Fatalf("query refs: %v", err)
	}
	defer refd.Close()
	referenced := map[string]bool{}
	for refd.Next() {
		var n string
		if err := refd.Scan(&n); err != nil {
			t.Fatalf("scan refs: %v", err)
		}
		referenced[n] = true
	}
	if err := refd.Err(); err != nil {
		t.Fatalf("refs rows: %v", err)
	}

	dir := filepath.Join(dataDir, "email-invoices", "ws_user-admin")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read invoice dir: %v", err)
	}
	type orphan struct{ name, kind, sum, full string }
	var orphans []orphan
	byHash := map[string][]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pdf") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Errorf("read %s: %v", e.Name(), rerr)
			continue
		}
		h := sha256.Sum256(b)
		hs := hex.EncodeToString(h[:])
		byHash[hs] = append(byHash[hs], e.Name())
		if !referenced[e.Name()] {
			orphans = append(orphans, orphan{
				name: e.Name(),
				kind: classifyOrphanPDF(filepath.Join(dir, e.Name()), int64(len(b))),
				sum:  hs[:16] + fmt.Sprintf(" (%d 字节)", len(b)),
				full: hs,
			})
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].name < orphans[j].name })

	t.Logf("=== B. 无台账行引用的文件（孤儿）共 %d 个 ===", len(orphans))
	// 重复组：同 SHA 的多个文件里，只有「有台账行引用的那个」可留。
	// ⚠ 键必须是**完整哈希**——第一版用 16 位前缀去查 64 位键，永远查不中，
	// 于是「与已建档文件字节相同、删掉不丢票」这条最关键的信息被静默吞掉。
	referencedHashes := map[string]bool{}
	for name := range referenced {
		if b, rerr := os.ReadFile(filepath.Join(dir, name)); rerr == nil {
			h := sha256.Sum256(b)
			referencedHashes[hex.EncodeToString(h[:])] = true
		}
	}
	for _, o := range orphans {
		dupNote := ""
		if referencedHashes[o.full] {
			dupNote = "  ← 与某个**已建档**文件字节相同，删掉不丢票"
		} else if n := len(byHash[o.full]); n > 1 {
			dupNote = fmt.Sprintf("  ← 同组共 %d 个副本，全是孤儿", n)
		}
		t.Logf("  [%s] %s  sha=%s%s", o.kind, o.name, o.sum, dupNote)
	}

	t.Logf("小结：待剔除台账行 %d 条；待删孤儿文件 %d 个。以上均**未执行任何删除**。", nRow, len(orphans))
}
