package email

// diag_invoice_handoff_integrity_test.go —— **只读**诊断：交给财务之前，
// data/email-invoices 目录与台账是否对得上？
//
// ## 为什么需要它
//
// 2026-10-03 23:0x 实测（schema opencode_pocket，data 目录 11 个 PDF）：
//
//   · 台账 6 行 downloaded（各有 file_path）+ 1 行 pending；
//   · 磁盘 11 个 PDF ⇒ **5 个没有任何台账行引用**；
//   · 且这 5 个构成 3 组**字节完全相同**的重复：
//
//       其他-杭州创客家…-3500.00-2026-09-24.pdf  157615 B  sha 0E331BF151CBFEA9
//       其他-杭州创客家…-3500.00-2026-10-01.pdf  157615 B  sha 0E331BF151CBFEA9  ← 日期是采集当天
//       其他-财务部-0.00-2026-09-30.pdf              69 B  sha CFA3181C1EE36E8B  ← 空壳
//       其他-财务部-0.00-2026-10-01.pdf              69 B  sha CFA3181C1EE36E8B  ← 空壳
//       其他-云服务开票中心-1280.00-2026-09-28.pdf     1537 B  sha 7AB3033721A51A78
//       其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf 1537 B  sha 7AB3033721A51A78
//
// 第一组是**同一张票的两份拷贝**，第二份的日期不是票面开票日期（2026-09-24）
// 而是采集当天（2026-10-01）—— 即 `InvoiceFileName` 在 `invoice_date` 为空时
// 用 `time.Now()` 兜底（invoice_harvest.go:765）那个行为，**在生产上已经发生过
// 一次**，不是理论风险。第二组是 0.00 元的空壳凭证，已经出现过两次。
//
// ## 这个诊断**不能**证明什么（先写清楚）
//
// 产品的导出路径是**按 id 取**的（`POST /api/emails/invoices/export {ids,grid}`），
// 汇总文档也只从 DB 行生成（`BuildInvoiceSummaryDocs`），所以**产品自己不会**
// 把这些孤儿拼进 A4 或台账。本诊断针对的是另一条现实路径：**人（或 agent）
// 直接翻目录挑文件**交给财务——那会把 69 字节空壳和错日期的重复票递出去。
//
// 它也**不判断**哪一份才是对的：重复组里谁对、谁错，只有对照台账行的
// `file_path` 才能定，而本诊断正是用台账行做这个判定的。
//
// ## 只读与隔离
//
// 全文件 0 写语句；只读由**数据库强制**（default_transaction_read_only = on）。
// DSN 与 schema 必须**显式**从 POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA
// 传入且**无缺省值**；发票目录必须由 POCKET_DIAG_INVOICE_DIR 显式传入
// （不设就用 t.TempDir()，那样只会扫到空目录、输出「一切干净」的假结论，
// 所以这里改成必须显式给）。门禁 POCKET_DIAG_HANDOFF=1。
//
// 已登记在 internal/server/pg_test_isolation_guard_test.go 的
// pgSafeWithoutIsolation。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type handoffFile struct {
	name string
	size int64
	sha  string
}

// reFileNameDate 抓文件名末尾的 -YYYY-MM-DD.pdf。
// 必须锚在末尾：发票文件名形如「其他-<单位>-<金额>-<日期>[-<发票号>].pdf」，
// 发票号本身还可能带连字符，按最后一个 "-" 切会切出发票号片段。
var reFileNameDate = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2})\.pdf$`)

func TestDiagInvoiceHandoffIntegrity(t *testing.T) {
	if os.Getenv("POCKET_DIAG_HANDOFF") != "1" {
		t.Skip("set POCKET_DIAG_HANDOFF=1 (+ POCKET_REAL_MAIL_DSN / POCKET_REAL_MAIL_SCHEMA / " +
			"POCKET_DIAG_INVOICE_DIR) for the read-only pre-handoff integrity diagnostic")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN 未设置（本诊断无缺省值：缺省会误打生产库）")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		t.Fatal("POCKET_REAL_MAIL_SCHEMA 未设置（本诊断无缺省值）")
	}
	dir := os.Getenv("POCKET_DIAG_INVOICE_DIR")
	if dir == "" {
		t.Fatal("POCKET_DIAG_INVOICE_DIR 未设置。" +
			"\n不设就没有「扫到空目录 ⇒ 一切干净」这个假结论可用——本诊断的价值全在扫到真实文件。")
	}

	// ---- 磁盘侧：只列 PDF，按内容哈希分组（不靠文件名判断重复） ----
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读发票目录 %s 失败：%v", dir, err)
	}
	var files []handoffFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Errorf("读 %s 失败：%v", e.Name(), rerr)
			continue
		}
		sum := sha256.Sum256(b)
		files = append(files, handoffFile{name: e.Name(), size: int64(len(b)), sha: hex.EncodeToString(sum[:])})
	}
	if len(files) == 0 {
		t.Fatalf("%s 里一个 PDF 都没有——目录指错了，本诊断会输出「一切干净」的假结论", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	t.Logf("[diag] 发票目录 %s：%d 个 PDF（测量时刻 %s）", dir, len(files), time.Now().Format("15:04:05"))

	// ---- 台账侧：downloaded 行的 file_name / file_path / invoice_date ----
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TEMP TABLE handoff_write_guard(x int)`); err == nil {
		t.Fatal("连接竟然可写，只读保护没生效，拒绝继续")
	}

	rows, err := pool.Query(ctx, `
		SELECT COALESCE(file_name,''), COALESCE(file_path,''), COALESCE(invoice_date,''),
		       COALESCE(amount::text,'0'), status
		  FROM email_invoices
		 WHERE COALESCE(status,'') = 'downloaded'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type inv struct{ fileName, filePath, date, amount, status string }
	var claimed []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.fileName, &i.filePath, &i.date, &i.amount, &i.status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		claimed = append(claimed, i)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatalf("台账里 downloaded 行数为 0——要么真没有可交付的票，要么口径变了；" +
			"两种都不该被本诊断读成「目录干净」（dir 里还有 %d 个 PDF）", len(files))
	}
	t.Logf("[diag] 台账 downloaded 行：%d", len(claimed))

	// ---- 判据 1：台账声称的文件是否真在磁盘上 ----
	byName := map[string]handoffFile{}
	for _, f := range files {
		byName[f.name] = f
	}
	missing := 0
	for _, c := range claimed {
		if _, ok := byName[filepath.Base(c.fileName)]; !ok {
			missing++
			t.Errorf("[台账有·磁盘无] %s（file_path=%s）—— 交付时会被财务追问" +
				"「凭证在哪」，而目录里根本没有它", c.fileName, c.filePath)
		}
	}

	// ---- 判据 2：磁盘上多出来的文件（没有任何台账行引用） ----
	claimedNames := map[string]bool{}
	for _, c := range claimed {
		claimedNames[filepath.Base(c.fileName)] = true
	}
	orphans := 0
	for _, f := range files {
		if claimedNames[f.name] {
			continue
		}
		orphans++
		flag := ""
		if f.size < 1024 {
			flag = "  ← 不足 1KB，几乎可以肯定是空壳"
		}
		t.Logf("[磁盘有·台账无] %-58s %7d B%s", f.name, f.size, flag)
	}

	// ---- 判据 3：字节相同的重复组（不靠文件名猜） ----
	bySHA := map[string][]string{}
	for _, f := range files {
		bySHA[f.sha] = append(bySHA[f.sha], f.name)
	}
	dupGroups := 0
	for sha, names := range bySHA {
		if len(names) < 2 {
			continue
		}
		dupGroups++
		sort.Strings(names)
		t.Logf("[内容重复] sha=%s… 共 %d 份：", sha[:16], len(names))
		for _, n := range names {
			tag := ""
			if !claimedNames[n] {
				tag = "  （台账未引用）"
			}
			t.Logf("            %s%s", n, tag)
		}
	}

	// ---- 判据 4：文件名里的日期与台账 invoice_date 不一致 ----
	// 这是「日期兜底用采集当天」留下的痕迹：同一张票在磁盘上出现两份、
	// 一份日期对、一份是采集当天，靠这一条能直接点名。
	mismatch := 0
	for sha, names := range bySHA {
		if len(names) < 2 {
			continue
		}
		var dates []string
		for _, n := range names {
			// 锚在**文件名末尾**的 -YYYY-MM-DD.pdf。
			// 第一版按最后一个 "-" 切分，于是
			// 「…-3500.00-2026-09-24.pdf」切出 "24"、长度 2 被跳过，
			// 该判据在真实数据上恒不触发（恒暗）——而它本该正好命中
			// 09-24 / 10-01 这一组。装饰性判据比没有更坏。
			m := reFileNameDate.FindStringSubmatch(n)
			if m == nil {
				t.Logf("[判据自检] %s 末尾没有 -YYYY-MM-DD.pdf，该文件不参与日期比对", n)
				continue
			}
			dates = append(dates, m[1])
		}
		if len(dates) < 2 {
			continue
		}
		sort.Strings(dates)
		if dates[0] != dates[len(dates)-1] {
			mismatch++
			t.Logf("[日期打架] sha=%s… 文件名日期 %v 不一致；"+
				"多半是同一张票被写了两遍，其中一份的日期来自采集当天"+
				"（invoice_harvest.go:765 的 time.Now() 兜底）", sha[:16], dates)
		}
	}

	t.Logf("[diag] 汇总：台账缺文件 %d / 磁盘孤儿 %d / 内容重复组 %d / 日期打架组 %d"+
		"（目录 %d 个 PDF，台账 %d 行）", missing, orphans, dupGroups, mismatch, len(files), len(claimed))
	if orphans == 0 && missing == 0 && dupGroups == 0 {
		t.Log("[diag] 目录与台账一致。**这只覆盖这一个目录**——" +
			"A4 拼版产物在 exports 子目录、且由 export 端点按 id 生成，不在本诊断范围内。")
	}
}
