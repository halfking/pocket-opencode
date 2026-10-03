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
// ## 它测的是流水线的**第一趟**，不是整条需求 2
//
// `extractInvoiceCandidates`（pipeline.go 步骤 1.5）分两趟：
//
//	第 1 趟  pipeline.go:412  ExtractInvoice(e, "")            ← 只有 envelope
//	         门控 pipeline.go:414 `p.Fetcher != nil && e.UID > 0`
//	第 2 趟  pipeline.go:469  ExtractInvoiceLoose(e,
//	         b.parsed.TextBody+"\n"+b.parsed.HTMLBody,           ← IMAP 取回的完整正文
//	         HasInvoiceAttachment(b.parsed.Attachments))         ← 现场从 MIME 判定附件
//
// 本探针**只复现第 1 趟**（`ExtractInvoiceLoose(e, "", false)` 与
// `ExtractInvoice(e, "")` 是同一个函数）。第 2 趟需要连真实 IMAP 取原文，
// 离线做不了 —— 所以本探针给出的「会建档 N 封」是**下界**，不是最终答案。
//
// 想要最终答案必须让第 2 趟真的跑一遍（需要授权跑真实 IMAP）。
//
// ## 关于 has_attachments 那一列：它不参与发票判定
//
// 容易误读成「附件标志拿不到所以发票建不了档」。**不是**：
// 第 2 趟的附件判定是 `HasInvoiceAttachment(b.parsed.Attachments)`，从刚解析的
// MIME 现场算，**根本不读 DB 这一列**。全树 `e.HasAttachments` 只被 store 的
// 扫描器读进结构体，没有任何业务逻辑消费它。
//
// 它的消费方只有前端的 📎 标记（emails-store → EmailInboxView）。
//
// ## ⚠ 这一段原本写的「真缺陷」**已经修了，别再照着它去查**
//
// 原文（2026-10-03 08:25 的测量）：真实库 120 封里 has_attachments=true 的
// 0 封，而唯一能置位的 POP3 路径在本部署产出为 0，据此判定为缺陷。
// 修法是给 IMAP 的 `fetchOpts` 加 BodyStructure
// （fetcher_attachment.go，24a8656b，2026-10-02 08:44 落地）。
// 「全树 HasAttachments: true 只出现在测试文件里」这句话**当时就不准确、
// 现在更不准确** —— 它只扫过 fetcher 与 store，没扫 fetcher_attachment.go。
//
// 2026-10-03 19:40 复核（schema opencode_pocket，183 封）：true 的 6 封
// **全部**来自 POP3 路径（id 前缀 em-pop3-），IMAP 来源的 134 封里 0 封为真。
//
// 关键：这个 0 是 **0/0，不是「修复无效」的证据**。真实库里没有一封
// 「已知带附件的 IMAP 邮件」可以当正控 —— 已知带附件的真实邮件是 QQ Wallet
// 电子发票 3 封与通行费电子发票 2 封，而它们**全是 POP3 来源**
// （本部署有账户的 IMAP 每轮 50s 预算耗尽后会回退 POP3，库里 49 封 POP3
// 来源的行（id 前缀 em-pop3-<accountID>-<uidl>，见 pop3_fetcher.go:563）
// 里 45 封属 56551681@qq.com，而带附件的 6 封**全部**属它）。所以真实
// 环境下这一半**仍未验证**，
// 验证条件是：一封走 IMAP 路径、且确实带附件的真实邮件被同步进来。
// 在那之前，说它「好了」和说它「没好」都没有证据。
//
// 它导致/曾导致的是**前端 📎 标记在 IMAP 来源的邮件上可能不显示**，
// 而不是发票建档失败。两件事，别混。
//
// ## 数据是活的
//
// 运行中的 pocketd 每分钟同步真实账户，库的内容会变。实测记录：
// 05:33 时 120/120 封 `uid IS NULL`；06:11 时已变成 120/120 封 `uid > 0`。
// 所以本探针的每个数字都只在**测量时刻**成立，复现时要带时刻。
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
[probe] 测量时刻 %s —— 运行中的 pocketd 每分钟在改库，复现请带时刻

── 需求 2 第 1 趟（只有 envelope，与 pipeline.go:412 的 ExtractInvoice(e,"") 同一函数）──
  发票候选（命中关键词）          %d 封
  ExtractInvoiceLoose(false) 命中 %d 封  ← 第 1 趟的实际取值
  ExtractInvoiceLoose(true)  命中 %d 封  ← 仅作对照：这一列**不是生产取值**
  差值（只因 hasInvoiceAttachment 为真才成立）  %d 封
`, len(rows), attTrue, time.Now().Format("15:04:05"), len(cands), len(strict), len(loose), onlyLoose)

	fmt.Println("\n  注意：第 1 趟只是**下界**。第 2 趟（pipeline.go:469）会 IMAP 取回完整正文，")
	fmt.Println("  用 ExtractInvoiceLoose(email, 正文, HasInvoiceAttachment(附件)) 重跑一次，")
	fmt.Println("  上面那批候选能不能建档，取决于它们正文/附件里到底有没有金额 —— 离线测不了。")
	fmt.Println("  另外第 2 趟的门控是 `e.UID > 0`（pipeline.go:414），不是 has_attachments。")

	if onlyLoose > 0 {
		fmt.Println("\n  【第 1 趟建不了、需靠第 2 趟的候选】")
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
		fmt.Println("    （amount=0.00 表示正文里没有金额，不代表它们是废邮件 ——")
		fmt.Println("      金额可能在附件 PDF 或门户页里，那要第 2 趟 + 采集器才知道。）")
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
