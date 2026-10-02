package email

// diag_spam_preview_test.go — 需求 1「清理广告与垃圾邮件，将它们移到垃圾邮件箱」
// 的**只读判定预演**。
//
// ## 为什么不直接跑一次流水线
//
// 要拿到判定结果，最直白的做法是手动触发一轮 pipeline。但那会连带跑
// 第 3 步 notifyImportant，把那 34 条积压提醒**当场推掉**——而那正是
// 用户还没拍板的事。所以这里不能碰流水线。
//
// ## 这个文件做什么
//
// cleanSpam（pipeline.go:832）里判垃圾用的是**纯函数** LooksLikeSpam，
// 入参全部可以从库里算出来。所以可以在**完全不碰 IMAP、不发一条 MOVE、
// 不推一条通知**的前提下，复刻出与真实 dryRun 报告**同一套数字**。
//
// 严格镜像的代码依据（改 SQL 之前先对代码，改了就对不上了）：
//
//	pipeline.go:833-837  lookback 默认 7 天；since = now - lookback
//	pipeline.go:838      ListEmailsSince(since, 1000)
//	store_pipeline.go:101
//	    WHERE date >= $1 AND COALESCE(deleted_at,0)=0 ORDER BY date DESC LIMIT 1000
//	pipeline.go:862      跳过 category IN ('spam','archived')
//	pipeline.go:856-859  senderVolume = 同发件人在本批里的封数
//	pipeline.go:865-867  LooksLikeSpam(from, subject, snippet,
//	                                    InvoiceCandidate(e), importance=='high',
//	                                    senderVolume[from])
//
// ## 与真实 run 的差异（必须说清，别把它当成真跑过）
//
//	· 真实 run 的 emails[i].UID 参与 MOVE；这里只判定，不 MOVE。
//	· 真实 run 走 Store；这里用只读 SQL 自己扫 —— Store 的 NewStore 会跑
//	  migrate()，那是**写操作**，只读诊断不能用。
//	· 时间基准不同：这是快照，08:00 真正跑时库里的邮件已经变了。
//
// 用法：
//	POCKET_DIAG_SPAM_PREVIEW=1
//	POCKET_DIAG_PG=<dsn>   go test ./internal/email/ -run TestDiagSpamPreview -v

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagSpamPreview(t *testing.T) {
	if os.Getenv("POCKET_DIAG_SPAM_PREVIEW") != "1" {
		t.Skip("set POCKET_DIAG_SPAM_PREVIEW=1 and POCKET_DIAG_PG=<dsn> to run (read-only)")
	}
	dsn := os.Getenv("POCKET_DIAG_PG")
	if dsn == "" {
		t.Fatal("POCKET_DIAG_PG 未设置")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()

	// 与 pipeline.go:833-837 一致：默认 7 天回看。
	const lookback = 7
	since := time.Now().AddDate(0, 0, -lookback).Unix()

	// 列集与 store_pipeline.go:101 的 pipelineEmailCols 对齐（只取本判据用到的）。
	// COALESCE 对齐 Go 侧 scanPipelineEmail 的「NULL 落成空串」语义。
	rows, err := pool.Query(ctx, `
		SELECT id, account_id, COALESCE(uid,0), COALESCE(from_address,''),
		       COALESCE(subject,''), COALESCE(snippet,''),
		       COALESCE(category,''), COALESCE(importance,''), date
		FROM opencode_pocket.emails
		WHERE date >= $1 AND COALESCE(deleted_at, 0) = 0
		ORDER BY date DESC
		LIMIT 1000`, since)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var emails []Email
	for rows.Next() {
		var e Email
		if err := rows.Scan(&e.ID, &e.AccountID, &e.UID, &e.FromAddress, &e.Subject,
			&e.Snippet, &e.Category, &e.Importance, &e.Date); err != nil {
			t.Fatalf("scan: %v", err)
		}
		emails = append(emails, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(emails) == 0 {
		t.Fatal("窗口内 0 封邮件 —— 预演不成立，别据此判断规则")
	}

	// pipeline.go:856-859：同发件人在本批里的封数。
	senderVolume := map[string]int{}
	for i := range emails {
		senderVolume[lowerTrim(emails[i].FromAddress)]++
	}

	type hit struct {
		acct, subject, why string
		score              int
	}
	var hits []hit
	var near []hit
	skipped := 0
	for i := range emails {
		e := emails[i]
		// pipeline.go:862
		if e.Category == "spam" || e.Category == "archived" {
			skipped++
			continue
		}
		inv := InvoiceCandidate(e)
		v := LooksLikeSpam(e.FromAddress, e.Subject, e.Snippet, inv, e.Importance == "high",
			senderVolume[lowerTrim(e.FromAddress)])
		row := hit{acct: e.AccountID, subject: e.Subject, why: v.Why, score: v.Score}
		if v.Spam {
			hits = append(hits, row)
			continue
		}
		if v.Score > 0 {
			near = append(near, row)
		}
	}
	sort.Slice(near, func(i, j int) bool { return near[i].score > near[j].score })

	t.Logf("窗口 = 最近 %d 天；扫描 %d 封（跳过已 spam/archived %d 封）", lookback, len(emails), skipped)
	t.Logf("SpamDryRun 命中 = %d 封（真实 dryRun 报告里会出现在 SpamDryRun / SpamDryRunSamples）", len(hits))
	for _, h := range hits {
		t.Logf("  HIT  [%d 分] %s | %s", h.score, h.acct, truncRunes(h.subject, 60))
		t.Logf("       why: %s", h.why)
	}
	// near-miss 必须输出：命中 0 时，「规则很准」和「规则形同虚设」在报告上
	// 长得一模一样（pipeline.go:902-903 记的就是这个坑）。
	t.Logf("SpamNearMiss = %d 封（有分但未过阈值）", len(near))
	for i, n := range near {
		if i >= 15 {
			t.Logf("  … 另有 %d 封", len(near)-15)
			break
		}
		t.Logf("  NEAR [%d 分] %s | %s", n.score, n.acct, truncRunes(n.subject, 60))
	}
	if len(hits) == 0 && len(near) == 0 {
		t.Log("提示：命中 0 且 near-miss 也 0。可能是判据在这批数据上完全不生效，" +
			"也可能是数据形态单一——别把 0 直接读成「没有垃圾邮件」")
	}
	if len(hits) > 0 {
		t.Logf("⇒ 若把 POCKET_EMAIL_SPAM_DRYRUN 改为 false，**这 %d 封会被 IMAP MOVE 到垃圾箱**（不可逆）", len(hits))
	} else {
		t.Log("⇒ 本批数据下即便关掉 dryRun 也不会移任何邮件；改不改配置都无实际差别")
	}
}

func lowerTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// truncRunes 按 rune 截断。刻意**不**复用 diag_dup_report_test.go 里的 trunc：
// 那个按字节切（`s[:n]`），对中文主题会劈出非法 UTF-8 —— 而本预演的全部价值
// 就是让人读那些中文主题，按字节切等于把要看的证据弄坏。
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
