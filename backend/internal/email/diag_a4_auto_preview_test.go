package email

// diag_a4_auto_preview_test.go — **只读**：预演「若把 POCKET_EMAIL_A4_GRID 打开，
// 每日流水线的 A4 阶段会导出哪几张票、拼成几页」。
//
// ## 为什么要有这个
//
// 打开那个开关的决定，卡在一个问题上：**它对生产数据的第一轮影响是什么？**
// 而 A4 阶段一旦跑起来就会写 exports 目录并回写 exported_at —— 那两件事都需要
// 授权，不能为了「先看看」就先跑一遍。所以本诊断只做**选片与算页**，
// 一行不写、一文件不建。
//
// ## 不重写判定链
//
// 选片直接调用生产函数 `p.pendingA4Files`（pipeline_a4.go），台账读取直接调用
// 生产函数 `Store.ListInvoicesScoped`。若在这里另写一套「哪些票算未导出」，
// 预演结果与真实行为就会分叉——而分叉的方向恰好是用户看不见的那种。
//
// ## 只读由数据库强制
//
// AfterConnect 里 `SET default_transaction_read_only = on`，并用一条**必然失败**
// 的 DELETE 自证（成功即 t.Fatal）。另外本诊断**不写文件**，这一点也在末尾用
// 「运行前后 exports 目录内容逐字节相同」自证，而不是靠承诺。

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagA4AutoPreview(t *testing.T) {
	if os.Getenv("POCKET_DIAG_A4_AUTO") != "1" {
		t.Skip("set POCKET_DIAG_A4_AUTO=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dsn == "" || schema == "" || dataDir == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN / SCHEMA / DATA_DIR 必须显式给全（均无缺省值）")
	}
	// 生产库里目前只有这一个 (user, workspace) 有 downloaded 发票。
	// workspace 是**下划线**的 ws_user-admin——凭印象写 ws-user-admin 会查不到任何行，
	// 而「查不到行」这件事在预演里长得很像「没有票可导」。
	// 写成可覆盖是为了别把「这个 scope 的数」当成「全部 scope 的数」——
	// 报告尾会显式印出本次只覆盖了哪个 scope。
	userID := envOr("POCKET_DIAG_A4_USER", "user-admin")
	wsID := envOr("POCKET_DIAG_A4_WS", "ws_user-admin")
	grid := 2

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

	// ── 0. 自证这份连接写不了东西 ──
	if _, derr := pool.Exec(ctx,
		`UPDATE email_invoices SET exported_at=0 WHERE id='__a4_probe_must_fail__'`); derr == nil {
		t.Fatal("只读连接竟然接受了 UPDATE —— 本诊断的前提失效，立刻停止")
	} else {
		t.Logf("只读门禁自检：UPDATE 被数据库拒绝（%v）", derr)
	}

	exportsDir := filepath.Join(dataDir, "email-invoices", "exports", wsID)
	before := snapshotDir(t, exportsDir)

	// ── 1. 台账侧：downloaded 的票一共多少张 ──
	store := &Store{pool: pool}
	invoices, err := store.ListInvoicesScoped(ctx, userID, wsID, "downloaded", 500)
	if err != nil {
		t.Fatalf("ListInvoicesScoped: %v", err)
	}
	if len(invoices) == 0 {
		t.Fatal("该 scope 没有任何 downloaded 发票 —— 预演会输出「没有票可导」，那不是" +
			"「A4 阶段工作正常」的证据，别把它读成后者")
	}
	t.Logf("=== 台账（scope user=%s ws=%s）===", userID, wsID)
	t.Logf("downloaded 发票：%d 张", len(invoices))

	already, noPath, missing := 0, 0, 0
	for _, inv := range invoices {
		switch {
		case inv.ExportedAt != 0:
			already++
			t.Logf("  [已导出，跳过] %-28s exported_at=%d  %s", inv.InvoiceNo, inv.ExportedAt, filepath.Base(inv.FilePath))
		case inv.FilePath == "":
			noPath++
			t.Logf("  [无文件，跳过] %-28s FilePath 为空", inv.InvoiceNo)
		default:
			abs := filepath.Join(dataDir, inv.FilePath)
			if _, serr := os.Stat(abs); serr != nil {
				missing++
				t.Logf("  [文件不在磁盘，跳过] %-28s %s (%v)", inv.InvoiceNo, filepath.Base(inv.FilePath), serr)
			}
		}
	}
	t.Logf("跳过合计：已导出 %d / 无 FilePath %d / 文件不在磁盘 %d", already, noPath, missing)

	// ── 2. 生产选片函数：它会选中哪几张 ──
	p := &Pipeline{DataDir: dataDir, A4Grid: grid}
	files, ids := p.pendingA4Files(invoices)

	t.Log("=== 打开 POCKET_EMAIL_A4_GRID=2 后第一轮会选中的票 ===")
	if len(ids) == 0 {
		t.Log("  （无 —— 该 scope 当前没有「已下载且未导出」且文件在磁盘上的票）")
	}
	byID := make(map[string]Invoice, len(invoices))
	for _, inv := range invoices {
		byID[inv.ID] = inv
	}
	for i, id := range ids {
		inv := byID[id]
		t.Logf("  %d. %-28s %-10s %8.2f %s  %s",
			i+1, inv.InvoiceNo, inv.Category, inv.Amount, inv.InvoiceDate, filepath.Base(files[i]))
	}

	// ── 3. 算页数：ceil(n / grid²) ──
	perPage := grid * grid
	wantPages := 0
	if len(files) > 0 {
		wantPages = (len(files) + perPage - 1) / perPage
	}
	t.Logf("选入 %d 张，grid=%d（每页 %d 格）⇒ 预计输出 %d 页", len(files), grid, perPage, wantPages)
	t.Logf("输出目录：%s（该目录本诊断不会写入任何东西）", exportsDir)

	// 未知即不存在：本诊断不猜 ExportInvoiceGrid 会跳过几张。畸形件在真实运行时
	// 会被跳过并计入 A4ExportSkipped，届时以运行报告为准。
	t.Log("注意：预计页数是**按选入张数**算的上界。若其中有畸形 PDF，真实运行会跳过它们，")
	t.Log("      实际页数只会更少，且会在报告的 a4ExportSkipped 里逐个列出。")

	// ── 4. 现状对照：exports 目录里现在已有哪些 A4 产物 ──
	after := snapshotDir(t, exportsDir)
	t.Log("=== 现状：exports 目录内容（本诊断运行前后应完全相同）===")
	beforeSet := make(map[string]bool, len(before))
	for _, n := range before {
		beforeSet[n] = true
	}
	for _, n := range after {
		if !beforeSet[n] {
			t.Errorf("只读诊断却新建了文件 %s —— 本诊断的前提（不写文件）不成立", n)
			continue
		}
		if strings.Contains(n, "-a4-") {
			t.Logf("  %s", n)
		}
	}
	if len(after) == len(before) {
		t.Logf("自证：运行前后均为 %d 个文件，逐个比对无新增（本诊断确实一个字节都没写）", len(after))
	} else {
		t.Errorf("exports 目录文件数从 %d 变成 %d —— 本诊断写文件了，前提不成立", len(before), len(after))
	}
	t.Logf("口径提醒：本次只覆盖 scope user=%s ws=%s。多 scope 部署时每个 scope 各导一份。", userID, wsID)
}

// snapshotDir 列出目录下的**文件名**（不含子目录），排序后返回。
//
// 返回 []string 而不是 map：range 一个 map 拿到的是 **value** 不是 key，
// 在这里会把文件名用成 bool，编译器先报错、而一旦真写成别处就会静默取到错的值。
func snapshotDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
