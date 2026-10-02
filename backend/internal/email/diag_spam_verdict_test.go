package email

// diag_spam_verdict_test.go — **只读**诊断：按需求 2 的真实判定，重跑一遍
// 垃圾邮件扫描，输出「真开 MOVE 会移哪些、依据是什么、近门槛有哪些」。
//
// ## 为什么需要它
//
// 「手动触发一次真实 IMAP MOVE」是待授权项，而授权要看的就是 dry-run 的判定
// 结果。问题是那个结果**不留痕**：`cleanSpam` 的 dry-run 分支只填
// PipelineReport 并打一行日志，报告不落库、没有任何表存它
// （2026-10-02 实测 opencode_pocket 里没有任何 report/pipeline 表）。于是
// 进程一退出，判定就没了——想复核「上次判成什么样」只能去翻当时的日志。
//
// 2026-10-02 实测到的状态：email_action_intents 0 行、5 个账户 rules 全 NULL、
// category='spam' 0 封，也就是**真实 MOVE 从未发生过一次**。那么第一个要回答
// 的问题不是「敢不敢移」，而是「现在到底有没有东西可移」。
//
// ## 怎么做到只读
//
// 1. 连接上直接 `SET default_transaction_read_only = on`，任何写尝试都会报错。
//    「只读」这句话由数据库强制，不是靠读代码保证。
// 2. 绕开 NewStore——它会调 migrate() 建表，那本身就是写。schema 里的表
//    早就建好了，直接 &Store{pool: pool}。
//
// ## 跑生产代码，不重抄判定
//
// 这里调用的是 **Pipeline.cleanSpam 本身**（SpamDryRun=true），不是把
// LooksLikeSpam 的判定抄一份。抄的那份会随规则漂移而不自知——而本诊断的
// 全部价值就在于它说的就是线上会做的事。
//
// **只 SELECT，不 UPDATE/DELETE。** 门禁 POCKET_DIAG_SPAM_VERDICT=1。

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagSpamVerdict(t *testing.T) {
	if os.Getenv("POCKET_DIAG_SPAM_VERDICT") != "1" {
		t.Skip("set POCKET_DIAG_SPAM_VERDICT=1 to run (read-only)")
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
		// 只读由数据库强制：后面任何写尝试都会直接失败，而不是悄悄写进去。
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
	lookback := 7
	p := &Pipeline{
		Store:            store,
		Fetcher:          nil, // 预演分支根本不会碰它；显式留 nil 以证明这一点
		SpamDryRun:       true,
		SpamLookbackDays: lookback,
	}
	rep := &PipelineReport{}
	start := time.Now()
	p.cleanSpam(ctx, rep)

	// 先证明「真的读到了真实数据」——空输入下下面所有结论都不成立。
	scanned, _, err := store.ListEmailsSince(ctx, time.Now().AddDate(0, 0, -lookback).Unix(), 2000)
	if err != nil {
		t.Fatalf("ListEmailsSince: %v", err)
	}
	if len(scanned) == 0 {
		t.Fatal("窗口内一封邮件都没有：真实库连不上，或 lookback 窗口算错了。" +
			"下面不会有任何判定结论可读——先修这个，别把空结果当成「没有垃圾」")
	}

	t.Logf("=== 需求 2 垃圾判定预演（%d 天窗口，耗时 %s）===",
		lookback, time.Since(start).Round(time.Millisecond))
	t.Logf("窗口内邮件数: %d", len(scanned))
	t.Logf("判定为垃圾（真开 MOVE 会移走的）: %d", rep.SpamDryRun)
	for _, s := range rep.SpamDryRunSamples {
		t.Logf("  账户 %s: %d 封  依据=%s", s.AccountID, s.Count, s.Why)
		for i, subj := range s.Subjects {
			t.Logf("      [%d] %.70s", i+1, subj)
		}
	}
	near := 0
	for _, list := range rep.SpamNearMiss {
		near += len(list.Near)
	}
	t.Logf("近门槛（有分但未判垃圾，卡在 100 分线下）: %d", near)
	for _, list := range rep.SpamNearMiss {
		for _, n := range list.Near {
			t.Logf("  账户 %s  分=%3d  发件人=%.34s  主题=%.50s  依据=%s",
				list.AccountID, n.Score, n.From, n.Subject, n.Why)
		}
	}
	if len(rep.Errors) > 0 {
		sort.Strings(rep.Errors)
		t.Logf("本轮错误 %d 条: %v", len(rep.Errors), rep.Errors)
	}
	t.Logf("SpamMoved=%d（dry-run 恒为 0：真 MOVE 没发生）", rep.SpamMoved)
}
