// invoiceprobe — 用**真实库里的真实邮件**离线跑一遍需求 2/3 的发票提取，
// 并算出「真跑流水线时会产出什么文件名」。
//
// ## 为什么需要它
//
// 需求 2「收取发票类邮件并解析整理」此前只有夹具测试。真实数据上的执行
// 证据是 0：email_invoices 表只有 1 行、磁盘上有 6 个文件对应 3 份内容
// （§7cs）。而跑流水线要连真实 IMAP、调计费 LLM。
//
// `InvoiceCandidate` / `ExtractInvoiceLoose` / `InvoiceFileName` 全是**纯函数**，
// 不碰网络、不碰 IMAP、不写磁盘。把 emails 表读进内存就能得到和
// `extractInvoiceCandidates`（pipeline.go 步骤 1.5）**完全相同**的结果。
//
// ## 顺带量一个真缺陷：has_attachments 在生产里恒为 false
//
// `ExtractInvoiceLoose` 的第三个参数 hasInvoiceAttachment 决定要不要走
// 「放宽建档」——那条路径当初就是为「主题写 9 月度对账单、金额只印在附件
// PDF 里」加的，硬门槛会在采集器看到附件之前就把邮件扔掉。
//
// 结构上这个信号确实拿不到：
//   · POP3 路径正确置位（fetcher.go:974 `em.HasAttachments = len(parsed.Attachments) > 0`）
//   · IMAP 路径只有 envelope，插入时未知；事后唯一有机会的
//     `MarkEmailBodyCached` 写的是 `has_attachments = COALESCE(has_attachments, FALSE)`
//     —— 恒等操作，把机会丢掉了
//   · harvest 回填（invoice_harvest.go:317）压根不调 MarkEmailBodyCached
//   · 全树 `HasAttachments: true` 只出现在**测试文件**里
// 实测 opencode_pocket.emails 120 封 has_attachments 为真的 0 封。
//
// **但别把它当成「丢了 5 张发票」**：本探针两种取值都跑，实测结果是
// 6 封候选里 false 命中 1 封、true 命中 6 封，差出来的 5 封全是
// AWS/Amazon 账户提醒、扣款成功通知这类**金额根本不在邮件正文里**的通知
// （amount=0.00、无发票号）。放宽建档对它们救不回任何金额，只会在需求 3 的
// 汇总文档里建出 5 条零金额记录 —— 那比丢弃更糟。
//
// 所以准确的说法是：has_attachments 恒 false 是**真实的结构缺陷**，放宽路径
// 因此从不执行；但在**当前这批数据**上，它单独并不是需求 2 的瓶颈 ——
// 瓶颈是这批邮件的金额压根不在邮件里（在门户/附件里）。放宽建档只是让
// harvestOne 有机会去取附件的**必要条件**，单独打开它并不能解决。
//
// ## 只读保证
//
// 连接串强制 `default_transaction_read_only = on`，并且启动时主动做一次
// 写尝试来证明它生效（写成功了反而说明保护没生效，直接退出）。
// 不发任何 IMAP 命令、不写任何文件、不调任何外部服务。
//
// 用法：
//
//	POCKET_PROBE_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
//	POCKET_PROBE_PG_SCHEMA=opencode_pocket \
//	go run ./cmd/invoiceprobe/
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

type row struct {
	id         string
	accountID  string
	from       string
	subject    string
	snippet    string
	category   string
	importance string
	hasAtt     bool
	date       int64
}

func main() {
	flag.Parse()

	dsn := os.Getenv("POCKET_PROBE_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("POCKET_TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		log.Fatal("set POCKET_PROBE_POSTGRES_DSN (or POCKET_TEST_POSTGRES_DSN)")
	}
	schema := os.Getenv("POCKET_PROBE_PG_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.MaxConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TEMP TABLE probe_write_guard(x int)`); err == nil {
		log.Fatal("连接竟然可写，只读保护没生效，拒绝继续")
	}

	rows, err := load(ctx, pool)
	if err != nil {
		log.Fatalf("load: %v", err)
	}
	if len(rows) == 0 {
		fmt.Printf("[probe] schema=%s 没有邮件行\n", schema)
		return
	}
	report(rows)
}

func load(ctx context.Context, pool *pgxpool.Pool) ([]row, error) {
	rs, err := pool.Query(ctx, `
		SELECT id, account_id, COALESCE(from_address,''), COALESCE(subject,''),
		       COALESCE(snippet,''), COALESCE(category,''), COALESCE(importance,''),
		       COALESCE(has_attachments, FALSE), date
		  FROM emails
		 WHERE COALESCE(deleted_at, 0) = 0
		 ORDER BY date DESC`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.accountID, &r.from, &r.subject, &r.snippet,
			&r.category, &r.importance, &r.hasAtt, &r.date); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func report(rows []row) {
	var cands []row
	attTrue := 0
	for _, r := range rows {
		if r.hasAtt {
			attTrue++
		}
		if email.InvoiceCandidate(email.Email{Subject: r.subject, Snippet: r.snippet}) {
			cands = append(cands, r)
		}
	}

	var strict, loose []email.Invoice
	onlyLoose := 0
	for _, r := range cands {
		e := email.Email{ID: r.id, AccountID: r.accountID, Subject: r.subject, Snippet: r.snippet}
		// hasInvoiceAttachment=false —— 这是当前生产里**实际发生**的取值，
		// 因为库里 has_attachments 一封都没置真。
		if inv, ok := email.ExtractInvoiceLoose(e, "", false); ok {
			strict = append(strict, *inv)
		}
		// true —— 只有在 has_attachments 正确置位时才会发生。
		if inv, ok := email.ExtractInvoiceLoose(e, "", true); ok {
			loose = append(loose, *inv)
		}
	}
	for _, l := range loose {
		found := false
		for _, s := range strict {
			if s.EmailID == l.EmailID {
				found = true
				break
			}
		}
		if !found {
			onlyLoose++
		}
	}

	fmt.Printf(`
[probe] 只读连接校验通过（写尝试已被 PG 拒绝）
[probe] 邮件 %d 封，其中 has_attachments=true 的 %d 封

── 需求 2 发票提取 ────────────────────────────────────────
  发票候选（命中关键词）          %d 封
  ExtractInvoiceLoose(false) 命中 %d 封  ← 当前生产实际取值
  ExtractInvoiceLoose(true)  命中 %d 封
  **只因为 has_attachments 为真才被建档的**  %d 封
`, len(rows), attTrue, len(cands), len(strict), len(loose), onlyLoose)

	if onlyLoose > 0 {
		fmt.Println("\n  ⚠ 上面这批邮件在真实数据上会被**丢弃** —— 它们命中了发票关键词，")
		fmt.Println("    但抽不到金额/发票号，而 has_attachments 恒为 false 让放宽路径永不触发。")
		fmt.Println("    这正是当初加 ExtractInvoiceLoose 要解决的场景。")
		fmt.Println("\n  【会被丢弃的候选明细】")
		strictIDs := map[string]bool{}
		for _, s := range strict {
			strictIDs[s.EmailID] = true
		}
		for _, l := range loose {
			if strictIDs[l.EmailID] {
				continue
			}
			fmt.Printf("    %-52s %-28s amount=%.2f no=%s\n",
				trunc(l.Subject, 52), trunc(l.Seller, 28), l.Amount, orNone(l.InvoiceNo))
		}
	}

	if len(strict) == 0 {
		fmt.Println("\n  没有一封能建档 —— 需求 2 在这批真实数据上产出为 0。")
		return
	}

	sort.Slice(strict, func(i, j int) bool { return strict[i].EmailID < strict[j].EmailID })
	fmt.Println("\n  【会建档的发票 + 真跑流水线时的文件名】")
	for i := range strict {
		inv := strict[i]
		name := email.InvoiceFileName(&inv)
		fmt.Printf("\n    [%d/%d] %s\n", i+1, len(strict), trunc(inv.Subject, 50))
		fmt.Printf("          kind=%s category=%s seller=%s\n", inv.Kind, inv.Category, orNone(inv.Seller))
		fmt.Printf("          amount=%.2f %s  invoiceNo=%s  invoiceDate=%s\n",
			inv.Amount, orCNY(inv.Currency), orNone(inv.InvoiceNo), orNone(inv.InvoiceDate))
		if inv.InvoiceDate == "" {
			fmt.Printf("          ⚠ 无开票日期 -> 文件名日期会退化成**采集当天**（InvoiceFileName:602 time.Now）\n")
		}
		fmt.Printf("          文件名: %s\n", name)
	}

	fmt.Println("\n── 金额汇总（按币种分组，不跨币种相加）──")
	// LedgerRows 的第二个返回值是 []CurrencyTotal 而不是 map，且刻意不提供
	// 一个「总额」标量 —— USD 与 CNY 直接相加不是金额（ledger.go:32-58）。
	_, totals := email.LedgerRows(strict)
	if len(totals) == 0 {
		fmt.Println("    (无)")
	}
	for _, t := range totals {
		fmt.Printf("    %-6s %10.2f  (%d 张)\n", t.Currency, t.Amount, t.Count)
	}
	fmt.Println()
}

func orNone(s string) string {
	if s == "" {
		return "(空)"
	}
	return s
}

func orCNY(c string) string {
	if c == "" {
		return "CNY"
	}
	return c
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
