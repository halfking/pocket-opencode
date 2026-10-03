package email

// 这条诊断定「落库时那 25 条为什么修不掉」。
//
// ## 它接的是哪一棒
//
// 2026-10-04 03:4x 的实验链：
//
//	1. 单元层面：diag_unsanitized_stock_replay_test.go 用**当前** DeriveSnippet
//	   重放库里导出的 24 条原文 → 全部产出干净文本（map[C_正常解码:24]）。
//	2. 负控：把 snippet.go 回退到 origin/main → 同样 24 条全部仍脏（24/24）。
//	   ⇒ 判据有鉴别力，snippet.go 的 QP 修复在**它自己那条腿上**确实生效。
//	3. 真库层面：用**含修复**的 pocketd 重跑 backfill（totalSaved=856）
//	   → opencode_pocket.emails 未净化行 **25 → 25，一条没少**。
//
// 1 与 3 直接矛盾。而矛盾只可能来自一件事：**判据钉的对象不是落库对象。**
// 落库主路径是 fetcher.go 的
//
//	em.Snippet = SnippetFromParsed(parsed, 500)
//	if em.Snippet == "" { em.Snippet = DeriveSnippet([]byte(parsed.HTMLBody), 500) }
//
// 这条链上**没有任何一步调用 decodeWholeQuotedPrintable** —— 我修的那个函数
// 只有在 SnippetFromParsed 返回空、且 HTMLBody 恰好还是 QP 载荷时才会被走到。
// 静态读代码到此只能给出「假设」，下面这条把它变成读数。
//
// ## 它必须区分的三种落空（输出上一模一样，但修法完全不同）
//
//	P. ParseMIMEMessage 失败         → 走 em.Snippet = "parse error: …"
//	M. TextBody/HTMLBody 为空         → SnippetFromParsed 空 → DeriveSnippet 空
//	                                   → store 的 CASE WHEN EXCLUDED.snippet <> ''
//	                                     不覆盖 ⇒ **旧脏值永久冻结**
//	Q. 正文非空但仍是 QP 源码        → 净化器压根没被调用，是判据问题
//
// M 与 Q 是本条要判开的那个岔口：M 意味着「空串被当成不覆盖」的自愈链路问题，
// Q 意味着「正文解码根本没跑」。两者的修法分别在 store.go 和 mime.go。
//
// 只读：不写库、不改邮箱状态（UID FETCH 只读，与 realprobe 同一纪律）。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

// diagStage 一句话说明「这一步的输出是什么形态」。
func diagStage(t *testing.T, label, v string) {
	t.Logf("  %-28s len=%-6d head=%q", label, len(v), headOf(v, 90))
}

func TestDiagRealFetchSnippetStages(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set")
	}
	schema, serr := dsnSearchPathFromDSN(dsn)
	if serr != nil {
		t.Skipf("PG_DSN 未带 search_path：%v", serr)
	}
	keyPaths := strings.Split(os.Getenv("POCKET_REAL_KEYS"), ";")
	if len(keyPaths) == 1 && keyPaths[0] == "" {
		t.Skip("POCKET_REAL_KEYS not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	cfg, cerr := pgxpool.ParseConfig(dsn)
	if cerr != nil {
		t.Fatalf("parse dsn: %v", cerr)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	// 「以为钉住了」是这个缺陷家族的标志，所以当场验一次。
	var resolved string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolved); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolved != schema {
		t.Fatalf("search_path 未生效：期望 %q，实际 %q", schema, resolved)
	}
	t.Logf("search_path verified: %q", resolved)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	enabled, err := store.ListEnabledAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}

	// 目标：仍然带 QP 源码的那个账户（kimmy.huang@163.com）。
	// 用「库里的 snippet 仍被判为脏」来定位账户，而不是硬编码 id ——
	// 硬编码的 id 在换库/重灌后会静默扫到空集，读数与「已修复」无法区分。
	type dirtyAcct struct {
		id, addr string
		uid      int64
		rows     int
	}
	var targets []dirtyAcct
	for i := range enabled {
		var rows int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM emails
			 WHERE account_id=$1 AND deleted_at = 0
			   AND ( (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
			      OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' )`,
			enabled[i].ID).Scan(&rows)
		if err != nil {
			continue
		}
		if rows == 0 {
			continue
		}
		var uid int64
		if err := pool.QueryRow(ctx, `
			SELECT uid FROM emails
			 WHERE account_id=$1 AND deleted_at = 0 AND uid IS NOT NULL
			   AND snippet ~ '=(0D|E5|E7|E4|B8)'
			 ORDER BY uid DESC LIMIT 1`, enabled[i].ID).Scan(&uid); err != nil {
			continue
		}
		targets = append(targets, dirtyAcct{enabled[i].ID, enabled[i].EmailAddress, uid, rows})
	}
	if len(targets) == 0 {
		t.Fatalf("真实库里 0 个账户还有未净化行 —— 判据在空集上转绿是假绿。" +
			"（如果刚跑过清理，这是真结论；如果没有，先确认判据本身没失效。）")
	}

	// 找一把能解开凭据的 key。
	var working []byte
	for _, p := range keyPaths {
		key, rerr := os.ReadFile(p)
		if rerr != nil || len(key) != 32 {
			continue
		}
		c, cerr2 := NewCrypto(key)
		if cerr2 != nil {
			continue
		}
		for i := range enabled {
			_, enc, gerr := store.GetAccountByID(ctx, enabled[i].ID)
			if gerr != nil || enc == "" {
				continue
			}
			if plain, derr := c.DecryptString(enc); derr == nil && plain != "" &&
				plain != "oauth-pending-no-credential" {
				working = key
				break
			}
		}
		if working != nil {
			t.Logf("命中可用 key: %s", p)
			break
		}
	}
	if working == nil {
		t.Skip("POCKET_REAL_KEYS 里没有可用 key（安全结果，不是失败）")
	}

	realCrypto, cerr := NewCrypto(working)
	if cerr != nil {
		t.Fatalf("NewCrypto: %v", cerr)
	}
	fetcher := NewFetcherWithOptions(store, realCrypto, false, false)

	for _, tgt := range targets {
		acc, enc, err := store.GetAccountByID(ctx, tgt.id)
		if err != nil || enc == "" || acc.IMAPHost == "" {
			t.Logf("ACCT %s: 跳过（拿不到凭据/IMAP 主机）", tgt.id)
			continue
		}
		plain, derr := realCrypto.DecryptString(enc)
		if derr != nil || plain == "" || plain == "oauth-pending-no-credential" {
			t.Logf("ACCT %s (%s): 凭据解不开", tgt.id, tgt.addr)
			continue
		}
		t.Logf("=== ACCT %s (%s) 库中未净化行 %d，取 uid=%d ===",
			tgt.id, tgt.addr, tgt.rows, tgt.uid)

		fctx, fcancel := context.WithTimeout(ctx, 120*time.Second)
		raw, ferr := fetcher.FetchMessageRaw(fctx, tgt.id, tgt.uid)
		fcancel()
		if ferr != nil {
			t.Errorf("ACCT %s: FetchMessageRaw: %v", tgt.id, ferr)
			continue
		}
		t.Logf("  原始报文 %d 字节", len(raw))
		diagStage(t, "报文头", string(raw[:minInt(len(raw), 400)]))

		parsed, perr := ParseMIMEMessage(raw)
		if perr != nil {
			t.Logf("  ⇒ P. ParseMIMEMessage 失败：%v（落库会写 parse error 占位）", perr)
			continue
		}
		t.Logf("  parsed: subject=%q from=%q", headOf(parsed.Subject, 40), headOf(parsed.From, 40))
		// charset / CTE 直接从原始报文头读：**ParsedMessage 不带这两个字段**
		// （解码失败时它们就丢失了），而它们恰恰是本条要判的关键。
		t.Logf("  报文里的 Content-Type 声明：%v", diagCTypes(string(raw)))
		t.Logf("  报文里的 Content-Transfer-Encoding 声明：%v", diagCTEs(string(raw)))
		diagStage(t, "parsed.TextBody", parsed.TextBody)
		diagStage(t, "parsed.HTMLBody", parsed.HTMLBody)

		sfp := SnippetFromParsed(parsed, 500)
		diagStage(t, "SnippetFromParsed(非backfill)", sfp)

		// ⭐⭐ backfill.go:270 / fetcher.go:519 fetchSnippetOnConnected 的**真实**形态：
		// UID FETCH BODY[TEXT] —— **不带头**（PartSpecifierText），Peek。
		//
		// 这是本条探针存在的全部理由。上面那两个读数（FetchMessageRaw 的带报文形态、
		// ParseMIMEMessage 的解析形态）**都不是 backfill 用的输入**：
		//   · backfill 批量 fetch 只带 envelope/flags/bodystructure ⇒ m.BodySection 为空
		//     ⇒ 第 270 行的 for 循环一次都不进 ⇒ snippet 保持 ""
		//   ⇒ 落到第 273 行改走 fetchSnippetOnConnected，即下面这一腿。
		//
		// BODY[TEXT] 与 BODY[] 的差别不是大小，是**有没有 MIME 头**：
		// 判据 looksLikeMIMEStructure / containsMIMESource 的行为在带头/不带头
		// 两种形态下完全不同。同一个 DeriveSnippet，两种输入可以给出
		// 「干净文本」与「空串」两种截然不同的输出。
		textSec, terr := fetchBodyTextForDiag(ctx, acc.IMAPHost, acc.IMAPPort,
			acc.EmailAddress, plain, imap.UID(tgt.uid))
		if terr != nil {
			t.Errorf("ACCT %s: BODY[TEXT] 抓取失败：%v", tgt.id, terr)
			continue
		}
		t.Logf("  BODY[TEXT] 取到 %d 字节（前 120 字符：%q）", len(textSec), headOf(string(textSec), 120))
		bsDerived := DeriveSnippet(textSec, 500)
		diagStage(t, "DeriveSnippet(BODY[TEXT])", bsDerived)
		t.Logf("  ⇒ backfill 真正落库值 = %d 字节；isUnsanitized=%v；空串=%v",
			len(bsDerived), isUnsanitized(bsDerived), bsDerived == "")

		switch {
		case bsDerived == "":
			t.Logf("  ⇒ **M. backfill 的 DeriveSnippet(BODY[TEXT]) 返回空串** ⇒ " +
				"store.go 的 `CASE WHEN EXCLUDED.snippet <> ''` 不覆盖 ⇒ 旧脏值被永久冻结。" +
				" 净化器本身没坏，坏的是「空串 = 不覆盖」这条自愈语义。")
		case isUnsanitized(bsDerived):
			t.Logf("  ⇒ **Q. backfill 落库值仍是 QP 源码** ⇒ 净化器在这条真实输入上失效。")
		default:
			t.Logf("  ⇒ backfill 落库值干净。库里仍是脏值 ⇒ upsert 根本没被调用到这些行。")
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fetchBodyTextForDiag 复刻 fetcher.go:519 fetchSnippetOnConnected 的取件形态：
// UID FETCH BODY[TEXT]（PartSpecifierText，Peek），返回第一段非空字节。
// 只读（Peek 不置 \Seen），与 realprobe 同一纪律。
func fetchBodyTextForDiag(ctx context.Context, host string, port int,
	user, pass string, uid imap.UID) ([]byte, error) {
	c, err := imapclient.DialTLS(fmt.Sprintf("%s:%d", host, port), nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer c.Close()
	if err := c.Login(user, pass).Wait(); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	// 163 必须先发 ID，否则回 NO SELECT Unsafe Login。
	sendClientID(c, user)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	uidSet := imap.UIDSet{}
	uidSet.AddNum(uid)
	msgs, err := c.Fetch(uidSet, &imap.FetchOptions{
		UID: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierText,
			Peek:      true,
		}},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("fetch returned 0 messages for uid=%d", uid)
	}
	for _, bs := range msgs[0].BodySection {
		if len(bs.Bytes) > 0 {
			return bs.Bytes, nil
		}
	}
	return nil, fmt.Errorf("uid=%d has no non-empty BODY[TEXT] section", uid)
}

// diagCTypes / diagCTEs 从原始报文里抽出所有 Content-Type / CTE 声明（含去重的）。
// 只读，不改报文。
func diagCTypes(raw string) []string { return diagHeaderValues(raw, "content-type") }

func diagCTEs(raw string) []string { return diagHeaderValues(raw, "content-transfer-encoding") }

func diagHeaderValues(raw, want string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(line[:i]), want) {
			continue
		}
		v := strings.TrimSpace(line[i+1:])
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
