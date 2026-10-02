package email

// diag_invoice_backlog_test.go — **只读**诊断：需求 3 的发票积压到底有多大。
//
// ## 为什么需要它
//
// 真实库 2026-10-02 只有 2 张发票（3500 downloaded+有文件 / 58000 new+无文件），
// 128 封邮件。但「只有 2 张」这个数**分不清**下面两种：
//
//	「邮箱里就只收到过 2 张发票」——功能完成了；
//	「还有 N 张没被发现」——功能存在但没跑出来。
//
// 而「跑一次流水线会不会自动补上」是需求 3 的核心承诺，判据不该是
// 「代码看着像是对的」。这个诊断把第 1.5 步的判定链在真实数据上跑一遍，
// 直接给出待建档的封数、以及其中多少封需要拉 IMAP 原文（每封一次完整
// IMAP 会话，受 maxInvoiceBodyFetches 预算限制）。
//
// ## 复用生产判据，不重抄
//
// 判定链与 extractInvoiceCandidates 完全同源：
//
//	ListEmailsSince(90d, 2000) → 跳过 GetInvoiceByEmailID 无错的（已建档）
//	→ ExtractInvoice(e, "") → invoiceBodyReason(hit, inv, e)
//
// 抄一份判定的话，规则一改诊断就悄悄说谎——而它全部的价值就在于它说的
// 就是线上会做的事。
//
// ## 只读
//
// 连接上 `SET default_transaction_read_only = on`，写尝试直接报错。
// 绕开 NewStore（它会 migrate() 建表，那是写）。门禁
// POCKET_DIAG_INVOICE_BACKLOG=1。

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagInvoiceBacklog(t *testing.T) {
	if os.Getenv("POCKET_DIAG_INVOICE_BACKLOG") != "1" {
		t.Skip("set POCKET_DIAG_INVOICE_BACKLOG=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	if schema == "" {
		t.Skip("POCKET_REAL_MAIL_SCHEMA not set")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	safeSchema := pgx.Identifier{schema}.Sanitize()
	cfg.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(c, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := conn.Exec(c, "SET search_path TO "+safeSchema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	store := &Store{pool: pool}

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	scope := make(map[string][2]string, len(accounts))
	for _, a := range accounts {
		if a.UserID != "" {
			scope[a.ID] = [2]string{a.UserID, defaultWorkspace(a.WorkspaceID)}
		}
	}

	// 与第 1.5 步同一窗口与同一行数上限。
	emails, _, err := store.ListEmailsSince(ctx,
		time.Now().AddDate(0, 0, -invoiceCandidateLookbackDays).Unix(), invoiceCandidateScanLimit)
	if err != nil {
		t.Fatalf("ListEmailsSince: %v", err)
	}
	if len(emails) == 0 {
		t.Fatal("窗口内一封邮件都没有：先修连接或窗口，别把空结果当成「没有发票」")
	}

	var filed, inScope, pending int
	reasonCount := map[string]int{}
	type cand struct {
		reason, subject, from string
		date                  time.Time
	}
	var cands []cand

	for i := range emails {
		e := emails[i]
		if _, err := store.GetInvoiceByEmailID(ctx, e.ID); err == nil {
			filed++
			continue // 已建档，幂等跳过（与生产同一条）
		}
		if _, ok := scope[e.AccountID]; !ok {
			continue
		}
		inScope++
		inv, hit := ExtractInvoice(e, "")
		reason := invoiceBodyReason(hit, inv, e)
		if reason != "" {
			reasonCount[reason]++
			cands = append(cands, cand{reason, e.Subject, e.FromAddress,
				time.Unix(e.Date, 0)})
		}
		pending++
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].date.After(cands[j].date) })

	t.Logf("=== 需求 3 发票积压（%d 天窗口，与第 1.5 步同参数）===",
		invoiceCandidateLookbackDays)
	t.Logf("窗口内邮件 %d 封；其中已建档发票 %d 封", len(emails), filed)
	t.Logf("**未建档**（幂等跳过后剩下的）: %d 封", pending)
	t.Logf("其中 envelope 已命中（hit）: 见下表；需要拉 IMAP 原文的按 reason 分：")
	for _, k := range []string{"date", "candidate"} {
		t.Logf("  reason=%-9s %d 封", k, reasonCount[k])
	}
	t.Logf("拉原文预算 maxInvoiceBodyFetches=%d/轮 → 本轮最多处理 %d 封，"+
		"其余顺延下一轮（顺延不是丢弃）",
		maxInvoiceBodyFetches, maxInvoiceBodyFetches)
	if len(reasonCount) == 0 {
		t.Logf("没有任何邮件被判为需要拉原文：未建档的 %d 封都在 envelope 阶段就能定案，"+
			"或根本不是发票候选", pending)
	}
	t.Logf("--- 待拉原文的邮件（最多列 30 封）---")
	shown := 0
	for _, c := range cands {
		if shown >= 30 {
			t.Logf("  ...（还有 %d 封未列）", len(cands)-shown)
			break
		}
		shown++
		t.Logf("  [%-9s] %s  %.40s  %.60s", c.reason, c.date.Format("MM-DD 15:04"), c.from, c.subject)
	}

	reportOrphanInvoiceFiles(t, pool, safeSchema)
}

// reportOrphanInvoiceFiles 对账「磁盘上的发票 PDF」与「库里有 file_name 的行」。
//
// ## 为什么单独拎出来
//
// 2026-10-02 手工对账时，磁盘上有 6 个 PDF、库里只有 1 行有 file_name，看着像
// 「台账漏了 5 张发票」。逐个打开才发现：其中 2 个是 **IMAP 测试夹具残留**
// （PDF 正文自称 "VAT E-INVOICE (IMAP fixture)" / "Fixture Cloud Services Co.,
// Ltd."，见 gen_fixture_invoice_test.go），2 个是 69 字节、0 页的退化件，
// 真正无主且是真实发票的只有 1 个。**「5 个孤儿」这个数会让人得出完全错误的
// 结论**——它把测试数据和退化件算成了业务损失。
//
// 所以这里按性质分类，而不是只报个数：只有「无对应行 + 不像夹具 + 不是退化件」
// 才值得人看一眼。
func reportOrphanInvoiceFiles(t *testing.T, pool *pgxpool.Pool, safeSchema string) {
	t.Helper()
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dataDir == "" {
		t.Logf("POCKET_REAL_DATA_DIR 未设置，跳过磁盘/库对账")
		return
	}
	ctx := context.Background()

	// 库里有 file_name 的（用 hex 取，绕开库中个别行的非 UTF-8 字节）。
	rows, err := pool.Query(ctx,
		`SELECT encode(convert_to(coalesce(file_name,''),'UTF8'),'hex') FROM email_invoices
		 WHERE coalesce(file_name,'') <> ''`)
	if err != nil {
		t.Logf("读 file_name 失败: %v", err)
		return
	}
	known := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			continue
		}
		raw := make([]byte, len(h)/2)
		for i := 0; i < len(raw); i++ {
			if b, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8); err == nil {
				raw[i] = byte(b)
			}
		}
		known[string(raw)] = true
	}
	rows.Close()

	base := filepath.Join(dataDir, "email-invoices")
	var wsDirs []string
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Logf("读 %s 失败: %v", base, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != "exports" {
			wsDirs = append(wsDirs, e.Name())
		}
	}
	if len(wsDirs) == 0 {
		t.Logf("%s 下没有 workspace 子目录（无票据可对账）", base)
		return
	}

	t.Logf("--- 磁盘/库对账（只读，不删任何文件）---")
	var orphanReal, orphanFixture, orphanDegenerate int
	for _, ws := range wsDirs {
		dir := filepath.Join(base, ws)
		files, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
		if err != nil {
			continue
		}
		for _, f := range files {
			baseName := filepath.Base(f)
			if known[baseName] {
				t.Logf("  [有对应行]   %s/%s", ws, baseName)
				continue
			}
			st, err := os.Stat(f)
			if err != nil {
				continue
			}
			kind := classifyOrphanPDF(f, st.Size())
			switch kind {
			case "夹具":
				orphanFixture++
			case "退化件":
				orphanDegenerate++
			default:
				orphanReal++
				kind = "**疑似真实、值得看一眼**"
			}
			t.Logf("  [无对应行/%s] %s/%s  %d 字节  mtime=%s",
				kind, ws, baseName, st.Size(),
				st.ModTime().Format("2006-01-02 15:04"))
		}
	}
	t.Logf("  小结：无对应行中 疑似真实=%d  测试夹具=%d  退化件=%d"+
		"（夹具与退化件不需要当业务损失报）",
		orphanReal, orphanFixture, orphanDegenerate)
}

// classifyOrphanPDF 只读地判断一个无主 PDF 是什么。
//
// 判据来自实测（2026-10-02 真实库 data/email-invoices/ws_user-admin/）：
//   - IMAP 夹具：正文含 "IMAP fixture" / "Fixture Cloud" / "Demo Co"（gen_fixture_invoice_test.go 生成）
//   - 退化件：字节数极小（69 字节）、且无 /Type/Page（只有 Catalog，0 页）
//
// 其余归为「疑似真实」——**宁可多报给人看，也不要把测试数据算成业务损失**。
//
// ## 必须解压：判据要指向 PDF 的**内容**，不是它的字节
//
// 第一版只扫裸字节，结果把两份夹具报成「疑似真实」——因为它们的内容流是
// /FlateDecode 压缩的，裸字节里根本没有 "IMAP fixture" 这几个字（人手工解压
// 才看得到）。这正是「判据指向的对象错了」的形态：扫文件内容却只看了容器。
// 代价很具体：这份诊断的唯一作用就是防止「台账漏了 1280」这种假结论，
// 而它自己就产出了那个假结论。
func classifyOrphanPDF(path string, size int64) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "读取失败"
	}
	raw := latin1(b)
	hay := strings.ToUpper(raw + "\n" + inflateAllStreams(b))
	if strings.Contains(hay, "IMAP FIXTURE") || strings.Contains(hay, "FIXTURE CLOUD") ||
		strings.Contains(hay, "DEMO CO") {
		return "夹具"
	}
	pages := strings.Count(raw, "/Type/Page") + strings.Count(raw, "/Type /Page")
	if pages == 0 && size < 1024 {
		return "退化件"
	}
	return "真实候选"
}

// inflateAllStreams 把文件里所有 FlateDecode 流解开并拼起来。
// 解不开就跳过那一个——畸形流不该让整份诊断失败。
func inflateAllStreams(b []byte) string {
	var sb strings.Builder
	s := latin1(b)
	for {
		i := strings.Index(s, "stream")
		if i < 0 {
			break
		}
		rest := s[i+len("stream"):]
		rest = strings.TrimLeft(rest, "\r\n")
		j := strings.Index(rest, "endstream")
		if j < 0 {
			break
		}
		seg := b[len(b)-len(rest) : len(b)-len(rest)+j]
		if zr, err := zlib.NewReader(bytes.NewReader(seg)); err == nil {
			io.Copy(&sb, zr)
			zr.Close()
		}
		s = rest[j+len("endstream"):]
	}
	return sb.String()
}

func latin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteByte(c)
	}
	return sb.String()
}
