// spamdryprobe — 用**真实库里的真实邮件**离线跑一遍需求 1 的垃圾判定规则。
//
// ## 为什么需要它
//
// `POCKET_EMAIL_SPAM_DRYRUN` 默认 true，`Pipeline.cleanSpam` 在预演模式下
// 只判定不 MOVE，并把「会移哪些、为什么」写进报告。代码注释里明确写着：
// **这个预演报告是「要不要开真实 MOVE」的唯一依据**。
//
// 但报告只在流水线真跑起来时才有，而跑流水线要连真实 IMAP、调计费 LLM。
// 于是「规则在真实数据上表现如何」这件事一直只能靠推断。
//
// 本探针把这件事拆出来：`LooksLikeSpam` / `InvoiceCandidate` 都是**纯函数**，
// 不碰网络、不碰 IMAP、不调 LLM。只要把 emails 表的行读进内存，就能得到
// 和 cleanSpam 完全相同的判定结果（判定逻辑是同一份代码，不是复刻）。
//
// ## 只读保证
//
// 连接串强制 `default_transaction_read_only = on`，所以这个程序在数据库
// 层面**写不了任何东西** —— 不是「我们没写写操作」，是「写了会被 PG 拒绝」。
// 同理它**不发任何 IMAP 命令**、不连任何外部服务。
//
// 用法：
//
//	POCKET_PROBE_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
//	POCKET_PROBE_PG_SCHEMA=opencode_pocket \
//	go run ./cmd/spamdryprobe/
//
// 另有 -top N 控制列出多少条 near-miss 明细（默认 20）。
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
	date       int64
}

type scored struct {
	row
	verdict email.SpamVerdict
	invoice bool
	volume  int
}

func main() {
	top := flag.Int("top", 20, "列出多少条 near-miss 明细")
	dumpFrom := flag.String("dump-from", "", "只打印该发件人（子串匹配）名下邮件的完整 snippet，用来人工核对判定依据")
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
	// 只读保证：这一行是本程序「不会动生产数据」的硬凭据，不是承诺。
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.MaxConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// 主动验证只读真的生效，而不是假设它生效。
	if err := assertReadOnly(ctx, pool); err != nil {
		log.Fatalf("read-only guard: %v", err)
	}

	rows, err := load(ctx, pool)
	if err != nil {
		log.Fatalf("load emails: %v", err)
	}
	if len(rows) == 0 {
		fmt.Printf("[probe] schema=%s 里没有邮件行（deleted_at=0）。先跑一次同步或客户端推送。\n", schema)
		return
	}

	// -dump-from：只看某个发件人，把正文完整打出来。
	// 「该留还是该移」最终只能人看，而人要看的是**原文里到底有什么**，
	// 不是规则自报的 Why 字符串。
	if *dumpFrom != "" {
		dump(rows, *dumpFrom)
		return
	}

	report(rows, *top)
}

func dump(rows []row, needle string) {
	vol := map[string]int{}
	for _, r := range rows {
		vol[strings.ToLower(strings.TrimSpace(r.from))]++
	}
	n := 0
	for _, r := range rows {
		if !strings.Contains(strings.ToLower(r.from), strings.ToLower(needle)) {
			continue
		}
		n++
		e := email.Email{Subject: r.subject, Snippet: r.snippet}
		v := email.LooksLikeSpam(r.from, r.subject, r.snippet, email.InvoiceCandidate(e), r.importance == "high",
			vol[strings.ToLower(strings.TrimSpace(r.from))])
		fmt.Printf("\n=== [%d] %s\n    from=%s  vol=%d  判定=%v score=%d\n    why=%s\n--- snippet (%d 字符) ---\n%s\n",
			n, r.subject, r.from, vol[strings.ToLower(strings.TrimSpace(r.from))],
			v.Spam, v.Score, v.Why, len([]rune(r.snippet)), r.snippet)
	}
	if n == 0 {
		fmt.Printf("[probe] 没有发件人匹配 %q 的邮件\n", needle)
	}
}

// assertReadOnly 用一次真实的写尝试来证明连接是只读的。
// 失败会被 PG 拒绝（这是预期），成功才说明 guard 没生效 —— 那就必须停。
func assertReadOnly(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TEMP TABLE probe_write_guard(x int)`)
	if err == nil {
		return fmt.Errorf("连接竟然可写，只读保护没生效，拒绝继续")
	}
	return nil
}

func load(ctx context.Context, pool *pgxpool.Pool) ([]row, error) {
	// 列集与 cleanSpam 实际用到的字段一致：from/subject/snippet 供判定，
	// category/importance 供短路，date/account_id 供分组。
	q := `
		SELECT id, account_id, COALESCE(from_address,''), COALESCE(subject,''),
		       COALESCE(snippet,''), COALESCE(category,''), COALESCE(importance,''), date
		  FROM emails
		 WHERE COALESCE(deleted_at, 0) = 0
		 ORDER BY date DESC`
	rs, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.accountID, &r.from, &r.subject, &r.snippet, &r.category, &r.importance, &r.date); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func report(rows []row, top int) {
	// senderVolume 与 cleanSpam 同样口径：同一发件人在本批里的封数。
	// 这是 LooksLikeSpam 唯一拿不到的跨封信息，必须由调用方统计。
	vol := map[string]int{}
	for _, r := range rows {
		vol[strings.ToLower(strings.TrimSpace(r.from))]++
	}

	var hits, invoiceCands, unclassified []scored
	scoreHist := map[int]int{}
	byAccount := map[string]int{}
	impDist := map[string]int{}

	for _, r := range rows {
		e := email.Email{
			FromAddress: r.from, Subject: r.subject, Snippet: r.snippet,
			Category: r.category, Importance: r.importance,
		}
		inv := email.InvoiceCandidate(e)
		v := email.LooksLikeSpam(r.from, r.subject, r.snippet, inv, r.importance == "high",
			vol[strings.ToLower(strings.TrimSpace(r.from))])
		s := scored{row: r, verdict: v, invoice: inv, volume: vol[strings.ToLower(strings.TrimSpace(r.from))]}

		if v.Spam {
			hits = append(hits, s)
			byAccount[r.accountID]++
		} else if v.Score > 0 {
			scoreHist[v.Score] += 1
			unclassified = append(unclassified, s)
		}
		if inv {
			invoiceCands = append(invoiceCands, s)
		}
		impDist[orNone(r.importance)]++
	}

	fmt.Printf(`
[probe] 只读连接 schema 校验通过（写尝试已被 PG 拒绝）
[probe] 邮件 %d 封（deleted_at=0）

── 需求 1 垃圾判定 ──────────────────────────────────────────
  会判为垃圾（score>=100）        %d 封
  发票候选（判定时短路，不参与）  %d 封
  importance=high（判定时短路）   %d 封
  未判垃圾但有分（near-miss）     %d 封
`, len(rows), len(hits), len(invoiceCands), countHigh(impDist), len(unclassified))

	if len(hits) == 0 && len(unclassified) == 0 {
		fmt.Println("\n  ⚠ 0 命中且 0 near-miss —— 规则在这批数据上完全没给出任何信号。")
		fmt.Println("    这和「规则失灵」在数字上长得一样，必须看下面的分布再判断。")
	}

	if len(hits) > 0 {
		fmt.Println("\n  【会移入垃圾箱的邮件】")
		for _, s := range hits {
			fmt.Printf("    %-46s %-28s %d分 %s\n", trunc(s.subject, 46), trunc(s.from, 28), s.verdict.Score, s.verdict.Why)
		}
		fmt.Println("\n  按账户：")
		for id, n := range byAccount {
			fmt.Printf("    %-46s %d 封\n", trunc(id, 46), n)
		}
	}

	if len(unclassified) > 0 {
		fmt.Println("\n  near-miss 分数分布（离阈值 100 还差多少）：")
		keys := make([]int, 0, len(scoreHist))
		for k := range scoreHist {
			keys = append(keys, k)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(keys)))
		for _, k := range keys {
			fmt.Printf("    %3d 分  %3d 封   距阈值还差 %3d\n", k, scoreHist[k], 100-k)
		}
		sort.Slice(unclassified, func(i, j int) bool { return unclassified[i].verdict.Score > unclassified[j].verdict.Score })
		if top > 0 && top < len(unclassified) {
			unclassified = unclassified[:top]
		}
		fmt.Println("\n  near-miss 明细（该留还是该移，只能人看）：")
		for _, s := range unclassified {
			fmt.Printf("    %-46s %-26s %3d分 %s\n", trunc(s.subject, 46), trunc(s.from, 26), s.verdict.Score, s.verdict.Why)
		}
	}

	fmt.Printf("\n── importance 分布（决定需求 4 能否触发）──────────────\n")
	keys := []string{"high", "medium", "low", "normal", "(空=未分类)"}
	for _, k := range keys {
		if n := impDist[k]; n > 0 {
			fmt.Printf("    %-14s %d 封\n", k, n)
		}
	}
	if impDist["(空=未分类)"] > 0 {
		fmt.Println("    ⚠ 未分类的邮件不会进入重要提醒（pipeline.go splitReminderCandidates）。")
	}
	fmt.Println()
}

func countHigh(m map[string]int) int { return m["high"] }

func orNone(s string) string {
	if s == "" {
		return "(空=未分类)"
	}
	return s
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
