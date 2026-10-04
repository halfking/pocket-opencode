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

// isInvoiceDiskFile 判断一个磁盘文件是不是「凭证」，**PDF 与图片都算**。
//
// ## 为什么必须包含图片（2026-10-04 真实数据上的假警报）
//
// 需求原文写的是「形成 pdf 文件或**其它相关文件**」，而 `saveInvoiceFile`
// 用 `DetectInvoiceMedia` 决定扩展名——拍照发票落盘就是 `.jpg`（见
// `invoice_attachment_harvest_wiring_test.go` 的图片分支用例）。
//
// 而这个诊断第一版磁盘侧**只列 `.pdf`**（`strings.HasSuffix(name, ".pdf")`），
// 于是「台账声称的文件在不在磁盘上」这条判据拿 PDF 集合去比所有台账行，
// **每一张图片发票都被判成「台账有·磁盘无」**。真实数据上当场误报 2 行：
//
//	[台账有·磁盘无] 其他-系统服务-6071.00-….jpg —— 交付时会被财务追问「凭证在哪」，而目录里根本没有它
//	[台账有·磁盘无] 其他-系统服务-283.20-….jpg  —— 同上
//
// 而这两个文件**明明在磁盘上**（把 jpg 打开看得到内容）。
// 更坏的是这条判据用 `t.Errorf`，于是**诊断整体变红**——
// 一个专门用来回答「凭证齐不齐」的诊断，在凭证其实齐的时候报警。
//
// ⇒ 这与「判据只覆盖了一种可能来源」同族：不是判据太松，是**覆盖面**有洞。
func isInvoiceDiskFile(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range []string{".pdf", ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff"} {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}

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

	// ---- 磁盘侧：只列发票文件（PDF **与图片**），按内容哈希分组（不靠文件名判断重复） ----
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读发票目录 %s 失败：%v", dir, err)
	}
	var files []handoffFile
	for _, e := range entries {
		if e.IsDir() || !isInvoiceDiskFile(e.Name()) {
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
		t.Fatalf("%s 里一个发票文件都没有——目录指错了，本诊断会输出「一切干净」的假结论", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	t.Logf("[diag] 发票目录 %s：%d 个发票文件（PDF + 图片，测量时刻 %s）",
		dir, len(files), time.Now().Format("15:04:05"))

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

	// 带上「这行是从哪封邮件来的、发件人是谁、那个发件人是不是就是收件邮箱本身」。
	// 判据 0a 要用到后两个字段——没有它们就只能靠 PDF 正文里的英文字符串猜，
	// 那判据只认一种写法（与「grep 不到 ≠ 不存在」同源）。
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(ci.file_name,''), COALESCE(ci.file_path,''),
		       COALESCE(ci.invoice_date::text,''),
		       COALESCE(ci.amount::float8, 0), ci.status,
		       COALESCE(ci.email_id,''),
		       COALESCE(e.from_address,''), COALESCE(e.from_name,''),
		       COALESCE(ac.email_address,''),
		       (ci.invoice_date IS NULL) AS date_is_null
		  FROM email_invoices ci
		  LEFT JOIN emails e ON e.id = ci.email_id
		  LEFT JOIN email_accounts ac ON ac.id = e.account_id
		 WHERE COALESCE(ci.status,'') = 'downloaded'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type inv struct {
		fileName, filePath, date, status string
		amount                           float64
		emailID, fromAddr, fromName      string
		mailbox                          string
		dateIsNull                       bool
	}
	var claimed []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.fileName, &i.filePath, &i.date, &i.amount, &i.status,
			&i.emailID, &i.fromAddr, &i.fromName, &i.mailbox, &i.dateIsNull); err != nil {
			t.Fatalf("scan: %v", err)
		}
		claimed = append(claimed, i)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatalf("台账里 downloaded 行数为 0——要么真没有可交付的票，要么口径变了；"+
			"两种都不该被本诊断读成「目录干净」（dir 里还有 %d 个 PDF）", len(files))
	}
	t.Logf("[diag] 台账 downloaded 行：%d", len(claimed))

	// ---- 判据 0：**台账引用**的行里，有多少其实不该当凭证交出去 ----
	//
	// 为什么排在最前：台账的 `status=downloaded` 只表示「拿到过某个 PDF」，
	// 它**不表示**那个 PDF 是对方开来的票面。2026-10-03 深夜实测 3 张
	// 「腾讯云发票」（合计 513.40 CNY）整条链路是自造的：
	//
	//   · 来源邮件 from_address == 收件邮箱地址本身（56551681@qq.com 发给
	//     56551681@qq.com），from_name 伪装成 `[QQ Wallet] …`；
	//   · 正文是英文 `Dear user, your electronic invoice has been issued.`；
	//   · 附件是 973 字节的 Helvetica 纯文本 7 行 PDF，无内嵌 CJK 字体、
	//     无发票代码/密码区/校验码，结构上不可能是增值税电子普通发票票面；
	//   · 同一个邮箱里另有 `[urgent-e2e]` 前缀的自注入邮件（round27/28
	//     文档已记录），说明这个真实邮箱**本来就是 E2E 注入靶子**。
	//
	// 判据拆成三条，因为它们各自会失效：
	//   0a 自发自收 —— 纯 DB 事实，最硬，不依赖任何文案字面量；
	//   0b 夹具/退化件 —— 复用 classifyOrphanPDF（会解压 Flate 流），
	//      但它只看**内容**，自造的英文摘要页不含它的关键词，会漏；
	//   0c 摘要页 —— 只认 `system-generated` 这一种写法，改个文案就失效，
	//      所以它只是 0a 的补充佐证，**不能单独作为结论**。
	var selfSent, fixture, degenerate, sysSummary []inv
	var selfSentAmt, sysSummaryAmt float64
	for _, c := range claimed {
		if c.mailbox != "" && strings.EqualFold(c.fromAddr, c.mailbox) {
			selfSent = append(selfSent, c)
			selfSentAmt += c.amount
		}
		p := filepath.Join(dir, filepath.Base(c.fileName))
		info, serr := os.Stat(p)
		if serr != nil {
			continue // 判据 1 会报它
		}
		switch classifyOrphanPDF(p, info.Size()) {
		case "夹具":
			fixture = append(fixture, c)
			continue
		case "退化件":
			degenerate = append(degenerate, c)
			continue
		}
		if isSystemSummaryPDF(p) {
			sysSummary = append(sysSummary, c)
			sysSummaryAmt += c.amount
		}
	}
	// 计入合计的口径只认 InvoiceCountsTowardTotal；这里只用 downloaded 行
	// 求和，是为了让「剔除可疑后还剩多少」这句话有个可复算的底数。
	var total float64
	for _, c := range claimed {
		total += c.amount
	}
	nullDate := 0
	for _, c := range claimed {
		if c.dateIsNull {
			nullDate++
		}
	}
	if len(selfSent) > 0 {
		t.Logf("[⚠ 台账行来自自发自收] %d 行，合计 %.2f —— 发件人地址就是收件邮箱本身，"+
			"这些**不是任何一家供应商开来的票**，是本系统自己的测试注入"+
			"（同邮箱另有 [urgent-e2e] 前缀注入记录，见 round27/28 文档）",
			len(selfSent), selfSentAmt)
		for _, c := range selfSent {
			t.Logf("     自发  %10.2f  发件=%s  伪装名=%q  %s",
				c.amount, c.fromAddr, c.fromName, c.fileName)
		}
	}
	if len(fixture)+len(degenerate) > 0 {
		t.Logf("[⚠ 台账引用了非票面文件] 夹具 %d 个、退化件 %d 个", len(fixture), len(degenerate))
		for _, c := range fixture {
			t.Logf("     夹具    %s", c.fileName)
		}
		for _, c := range degenerate {
			t.Logf("     退化件  %s", c.fileName)
		}
	}
	if len(sysSummary) > 0 {
		t.Logf("[佐证·不单独作结论] 台账引用了 %d 个自称 system-generated 的摘要页，合计 %.2f；"+
			"该判据只认这一种文案，改文案即失效，结论请以 0a 为准",
			len(sysSummary), sysSummaryAmt)
		for _, c := range sysSummary {
			t.Logf("     摘要页  %10.2f  %s", c.amount, c.fileName)
		}
	}
	t.Logf("[⚠ 口径] downloaded 行合计 %.2f；剔除自发自收的 %.2f 后 = %.2f；"+
		"其中 invoice_date 为空的 %d 行（文件名日期是采集当天兜底的，见 invoice_harvest.go:765）",
		total, selfSentAmt, total-selfSentAmt, nullDate)

	// ---- 判据 1：台账声称的文件是否真在磁盘上 ----
	byName := map[string]handoffFile{}
	for _, f := range files {
		byName[f.name] = f
	}
	missing := 0
	for _, c := range claimed {
		if _, ok := byName[filepath.Base(c.fileName)]; !ok {
			missing++
			t.Errorf("[台账有·磁盘无] %s（file_path=%s）—— 交付时会被财务追问"+
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

	// 汇总时要按**文件**去重：自发自收与摘要页在真实数据上命中的是同一批行
	// （那 3 张既是自注入、正文也自称 system-generated）。第一版直接把三个
	// 计数相加报成「6 行要先定性」，而肉眼看目录只有 3 个文件——
	// 判据自报的统计量必须和肉眼可数的事实对得上，否则读者会照着 6 去核对，
	// 核不出来就再也不信这条诊断。
	flagged := map[string]bool{}
	for _, c := range claimed {
		for _, group := range [][]inv{selfSent, fixture, degenerate, sysSummary} {
			for _, f := range group {
				if f.fileName == c.fileName {
					flagged[c.fileName] = true
				}
			}
		}
	}
	t.Logf("[diag] 汇总：台账缺文件 %d / 磁盘孤儿 %d / 内容重复组 %d / 日期打架组 %d"+
		"（目录 %d 个发票文件，台账 %d 行）", missing, orphans, dupGroups, mismatch, len(files), len(claimed))
	t.Logf("[diag] 判据 0：自发自收 %d 行 / 夹具 %d / 退化件 %d / 摘要页 %d"+
		"；按文件去重后实际待定性 **%d** 个（多条判据可能命中同一文件）",
		len(selfSent), len(fixture), len(degenerate), len(sysSummary), len(flagged))
	if orphans == 0 && missing == 0 && dupGroups == 0 && len(flagged) == 0 {
		t.Log("[diag] 目录与台账一致。**这只覆盖这一个目录**——" +
			"A4 拼版产物在 exports 子目录、且由 export 端点按 id 生成，不在本诊断范围内。")
	}
}

// isSystemSummaryPDF 判断一个 PDF 正文是否自称「系统生成的电子发票」。
//
// ## 它只是一条佐证，不是结论
//
// 第一版把它当成「不是官方票面」的判据。错在两点：
//
//  1. 它只认 `system-generated` **这一种英文写法**。上游把文案改成
//     "auto-generated" / "电子发票（系统开具）" 就完全漏判——
//     与「grep 不到 ≠ 不存在」同源：文本搜索只认你会写的那一种。
//  2. 更糟的是它**恒亮**：只要有一张这样的页被引用就报出来，于是
//     读日志的人会以为「抓到证据了」，而真正硬的证据（发件人==收件人）
//     反而被这条弱判据盖住。
//
// 所以现在它的定位是「补充佐证」，主判据是 DB 侧的
// `from_address = email_accounts.email_address`。
func isSystemSummaryPDF(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	hay := strings.ToUpper(latin1(b) + "\n" + inflateAllStreams(b))
	return strings.Contains(hay, "SYSTEM-GENERATED")
}
