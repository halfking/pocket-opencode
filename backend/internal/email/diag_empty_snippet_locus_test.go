package email

// 这条诊断定「32 行为什么永远是空摘要」。
//
// ## 为什么空 snippet 比脏 snippet 更难修
//
// store.go 的 upsert 是
//
//	snippet = CASE WHEN EXCLUDED.snippet <> '' THEN EXCLUDED.snippet ELSE emails.snippet END
//
// 空串 = **不覆盖**。于是「DeriveSnippet 判定为空」这条设计决策（宁可空着，
// 也不把 MIME 转储还给用户）在落库这一层变成了**永久冻结**：一旦某封邮件
// 落过一次空摘要，之后无论重跑多少次 backfill，那一格永远是空的，
// 邮件列表显示空白预览。
//
// 2026-10-04 真库读数（scripts/sql/diag-empty-snippets.sql）：
//
//	kimmy.huang@163.com   31 行空（2026-09-03 ~ 09-28）
//	feikemanager@163.com    1 行空
//	另两个账户              0 行
//	其中 parse-error 占位    0 行  ⇒ 不是解析失败占位，是净化器主动返回空
//
// 主体看得出全是**正常业务邮件**（GitHub 通知、GitLab MR 讨论、Basecamp
// 邀请、营销邮件）—— 一封 GitHub 纯文本通知触发「疑似 MIME 转储」这条闸，
// 本身就是判据失灵，而不是「安全起见」。
//
// ## 它必须区分的五种落空（输出都是空串，修法完全不同）
//
//	P1 snippetFromMIMEParts 返回空     —— 分片形态，非 multipart 就正常
//	P2 mimeCandidates 全部解析失败     —— 报文头畸形
//	P3 looksLikeHTML 分支：h 非空但 containsMIMESource(h) 为真
//	P4 纯文本分支：trimmed 非空但 containsMIMESource(trimmed) 为真
//	P5 前面全部落空，末尾 return ""
//
// 只读：连真库取「哪一行空」+ 连真 IMAP 取原文，不写库、不改邮箱状态。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagWhyEmptySnippet(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
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
	var resolved string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolved); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolved != schema {
		t.Fatalf("search_path 未生效：期望 %q 实际 %q", schema, resolved)
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

	type emptyRow struct {
		acctID, addr, subj string
		uid                int64
	}
	var rows []emptyRow
	for i := range enabled {
		r, qerr := pool.Query(ctx, `
			SELECT uid, left(subject, 50) FROM emails
			 WHERE account_id=$1 AND deleted_at=0 AND snippet='' AND uid IS NOT NULL
			 ORDER BY uid DESC LIMIT 3`, enabled[i].ID)
		if qerr != nil {
			continue
		}
		for r.Next() {
			var e emptyRow
			if err := r.Scan(&e.uid, &e.subj); err != nil {
				continue
			}
			e.acctID = enabled[i].ID
			e.addr = enabled[i].EmailAddress
			rows = append(rows, e)
		}
		r.Close()
	}
	if len(rows) == 0 {
		t.Fatalf("真库里 0 行空 snippet —— 判据在空集上转绿是假绿。" +
			"（若刚跑过清理，这是真结论；否则先确认 snippet='' 这个口径没失效。）")
	}
	t.Logf("抽样 %d 行空 snippet（共每账户取 3 行）", len(rows))

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

	verdict := map[string]int{}
	for _, row := range rows {
		acc, enc, err := store.GetAccountByID(ctx, row.acctID)
		if err != nil || enc == "" || acc.IMAPHost == "" {
			continue
		}
		plain, derr := realCrypto.DecryptString(enc)
		if derr != nil || plain == "" {
			continue
		}
		textSec, terr := fetchBodyTextForDiag(ctx, acc.IMAPHost, acc.IMAPPort,
			acc.EmailAddress, plain, imap.UID(row.uid))
		if terr != nil {
			t.Logf("[%s uid=%d] BODY[TEXT] 抓取失败：%v", row.addr, row.uid, terr)
			continue
		}
		v := classifyEmpty(t, row.addr, row.uid, textSec)
		for k, n := range v {
			verdict[k] += n
		}
	}
	if len(verdict) == 0 {
		t.Fatalf("一封都没抓下来 —— 「无结论」不是「无问题」")
	}
	t.Logf("=== 落空点归类（可一封多计）===")
	for k, n := range verdict {
		t.Logf("  %-28s %d", k, n)
	}
	if verdict["P0_拿到非空摘要"] > 0 {
		t.Logf("  ⇒ **判据失灵**：%d 封当前代码能解出摘要，但库里是空的。"+
			" 说明这批空值是旧代码留下的，而现在的 upsert 因为「空串=不覆盖」"+
			" 无法把它们补上 —— 自愈链路断在这里。", verdict["P0_拿到非空摘要"])
	}
}

// classifyEmpty 复刻 DeriveSnippet 的分支顺序，报告它在哪一步落空。
func classifyEmpty(t *testing.T, addr string, uid int64, raw []byte) map[string]int {
	out := map[string]int{}
	// ⚠️ 首尾各若干行用 %q 打印（**保留 \n**），不做 replaceAll。
	// 上一轮 logMIMEHit 把换行压成空格，于是
	// 「boundary 行 + 内层 Content-Type 头是同一行」这个假象成立了一个小时 ——
	// 真实形态是它们分属两行。压平换行的日志在判断结构形态时是有害的。
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	head := len(lines)
	if head > 4 {
		head = 4
	}
	tail := len(lines) - 3
	if tail < 0 {
		tail = 0
	}
	t.Logf("[%s uid=%d] BODY[TEXT] %d 字节 / %d 行", addr, uid, len(raw), len(lines))
	for i := 0; i < head; i++ {
		t.Logf("    首[%d] %q", i, diagTrunc(lines[i]))
	}
	for i := tail; i < len(lines); i++ {
		t.Logf("    尾[%d] %q", i-len(lines), diagTrunc(lines[i]))
	}

	if s := snippetFromMIMEParts(raw); s != "" {
		out["P1_mimeParts给出版本"]++
		return out
	}

	cands := mimeCandidates(raw)
	if len(cands) == 0 {
		out["P2_无候选可解析"]++
	}
	for i, cand := range cands {
		msg, perr := ParseMIMEMessage(cand)
		if perr != nil {
			t.Logf("    cand[%d] 解析失败：%v", i, perr)
			continue
		}
		tLog := normalizeWhitespace(msg.TextBody)
		t.Logf("    cand[%d] TextBody=%d HTMLBody=%d", i, len(msg.TextBody), len(msg.HTMLBody))
		if tLog != "" {
			t.Logf("      containsMIMESource(TextBody)=%v", containsMIMESource(msg.TextBody))
		}
		if h := htmlToText(msg.HTMLBody); h != "" {
			t.Logf("      containsMIMESource(HTMLBody)=%v", containsMIMESource(msg.HTMLBody))
		}
	}

	rawDerived := DeriveSnippet(raw, 500)
	if rawDerived != "" {
		out["P0_拿到非空摘要"]++
		t.Logf("    ✅ DeriveSnippet(BODY[TEXT]) 非空：%d 字节 %q", len(rawDerived), headOf(rawDerived, 80))
		return out
	}
	// 落空了：**必须指出是哪条子判据命中、在哪命中**。只报 containsMIMESource=true
	// 等于只说「被判脏了」，而这批邮件是普通业务邮件（GitHub 通知、GitLab MR），
	// 判据失灵的可能性与判据正确同样大 —— 两者在输出上一模一样。
	logMIMEHit(t, string(raw))
	out["P6_落空且已定位命中token"]++
	return out
}

// logMIMEHit 逐条打印 containsMIMESource 的三个子判据各自是否命中，命中时
// 给出上下文窗口，让「正文里恰好提到 Content-Type」和「整段都是 MIME 头」
// 在输出上就分得开 —— 这两者的处置完全相反。
func logMIMEHit(t *testing.T, s string) {
	flat := normalizeWhitespace(s)
	for _, seg := range []struct {
		name string
		text string
	}{{"原文(带换行)", s}, {"压平后", flat}} {
		if seg.text == "" {
			continue
		}
		t.Logf("    [%s] looksLikeMIMEStructure=%v", seg.name, looksLikeMIMEStructure(seg.text))
		if m := reMIMEHeaderToken.FindString(seg.text); m != "" {
			i := strings.Index(seg.text, m)
			t.Logf("      reMIMEHeaderToken 命中 %q @%d，上下文：%q",
				m, i, diagWindow(seg.text, i, len(m)))
		} else {
			t.Logf("      reMIMEHeaderToken 未命中")
		}
		if m := reBoundaryToken.FindString(seg.text); m != "" {
			i := strings.Index(seg.text, m)
			t.Logf("      reBoundaryToken 命中 %q @%d，上下文：%q",
				m, i, diagWindow(seg.text, i, len(m)))
		} else {
			t.Logf("      reBoundaryToken 未命中")
		}
	}
}

func diagWindow(s string, at, w int) string {
	lo := at - 60
	if lo < 0 {
		lo = 0
	}
	hi := at + w + 60
	if hi > len(s) {
		hi = len(s)
	}
	out := s[lo:hi]
	out = strings.ReplaceAll(out, "\r", "")
	out = strings.ReplaceAll(out, "\n", "⏎") // ⏎ 保留「这里换行了」这个信息
	if len(out) > 220 {
		out = out[:220] + "…"
	}
	return out
}

// diagTrunc 截长行，保留 %q 的换行可见性。
func diagTrunc(s string) string {
	if len(s) > 110 {
		return s[:110] + "…"
	}
	return s
}

var _ = fmt.Sprintf
