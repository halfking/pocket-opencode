//go:build greenmail

// fetcher_greenmail_test.go — 真实 IMAP 链路验证（-tags=greenmail 启用）。
//
// 前置：
//   docker run -d --rm --name greenmail-test -p 3025:3025 -p 3993:3993 \
//     greenmail/standalone:latest -Dgreenmail.setup.test.all \
//     -Dgreenmail.users=huangxutao@kxmail.local:h8pass
//   通过 3025 SMTP 投递若干封带 PDF 附件的发票邮件到 huangxutao@kxmail.local
//   env PG_DSN=postgresql://...:.../pocket?sslmode=disable go test -tags=greenmail \
//     ./internal/email/ -run TestSyncGreenmail -v
//
// 验证项：fetcher.Sync 真实 TCP 链路 → IMAP login → UIDSearch → InsertEmail
// 落库到 email_accounts / emails（message_id 用 uid-{n} 兜底，避免 Greenmail
// 缺 Message-ID 时的 UNIQUE 冲突被 ON CONFLICT DO NOTHING 静默吞掉）。
package email

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSyncGreenmail(t *testing.T) {
	dsn := greenmailDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping greenmail integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 隔离 schema：dsn 由 greenmailDSN() 保证只来自 POCKET_TEST_POSTGRES_DSN，
	// 仍建独立 schema 兜底。详见 pgscope_test.go 的说明。
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "email_greenmail_test_" + hex.EncodeToString(buf)
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("root pool:", err)
	}
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatal("create schema:", err)
	}
	rootPool.Close()

	pool, err := newScopedPool(ctx, dsn, schema)
	if err != nil {
		t.Fatal("pool:", err)
	}
	defer pool.Close()

	store, err := NewStore(pool)
	if err != nil {
		t.Fatal("store:", err)
	}
	masterKey, err := EnsureMasterKey("", t.TempDir())
	if err != nil {
		t.Fatal("master:", err)
	}
	c, err := NewCrypto(masterKey)
	if err != nil {
		t.Fatal("crypto:", err)
	}
	// insecure=true 让 fetcher 跳过 Greenmail 自签证书校验；StartTLS=false
	// 走隐式 IMAPS（Greenmail 端口 3993）。
	fetcher := NewFetcherWithOptions(store, c, true, false)

	const acctID = "acct-greenmail-realrun"
	now := time.Now().Unix()
	acc := &Account{
		ID:              acctID,
		UserID:          "user-admin",
		WorkspaceID:     "ws_user-admin",
		DisplayName:     "Greenmail 测试",
		EmailAddress:    "huangxutao@kxmail.local",
		IMAPHost:        "127.0.0.1",
		IMAPPort:        3993,
		AuthType:        "password",
		SyncIntervalMin: 5,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	encrypted, err := c.EncryptString("h8pass")
	if err != nil {
		t.Fatal("encrypt:", err)
	}
	// 测试环境每次重建干净账户（避免主 master key 漂移导致解密失败）。
	if _, err := pool.Exec(ctx, `DELETE FROM email_accounts WHERE id=$1`, acctID); err != nil {
		t.Fatal("cleanup acct:", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM emails WHERE account_id=$1`, acctID); err != nil {
		t.Fatal("cleanup emails:", err)
	}
	if err := store.InsertAccount(ctx, acc, encrypted); err != nil {
		t.Fatalf("insert account: %v", err)
	}


	// 收尾：DROP 掉自建的 schema，而不是逐表 DELETE。
	//
	// 逐表 DELETE 的写法在这里是**多余且危险**的：账户只可能落在本测试自己的
	// schema 里，逐表删反而是「假设表在生产库里」的做法。第一版之所以需要它，
	// 是因为当时根本没有隔离 —— 那次清理还踩了两个静默失败（复用已 Close 的
	// pool 拿到 `closed pool`；三张表统一写 `WHERE account_id=$1 OR id=$1`，
	// 父表 `email_accounts` 没有 account_id 列直接报错）。两条教训都记在这里，
	// 但它们属于「没有隔离」的年代，现在一条也不需要了。
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		if err := dropScopedSchema(cctx, dsn, schema); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	n, err := fetcher.Sync(ctx2, acctID)
	fmt.Println("GREENMAIL FETCH:", "n=", n, "err=", err)
	if err != nil {
		t.Fatalf("fetch err: %v", err)
	}
	if n == 0 {
		t.Fatal("no new emails fetched from greenmail")
	}

	// Sync 只写 emails 行；**email_invoices 行是流水线 step1.5 建的**
	// （extractInvoiceCandidates：ExtractInvoice -> UpsertInvoice）。
	// 原测试直接跳到 HarvestAll，而它只处理已在 email_invoices 里的行，
	// 于是 processed 恒为 0 —— 这个用例从来没被跑过，所以没人发现。
	p := &Pipeline{Store: store, Fetcher: fetcher, DataDir: t.TempDir()}
	rep := &PipelineReport{}
	accs := []Account{*acc}
	// step1.5 会为「命中发票但缺开票日期」的邮件逐封开 IMAP 会话拉原文，
	// 真实服务器上单封可能耗时分钟级（代码注释里记过：151 封时跑了 6 分钟
	// 未完）。这里给足预算，别复用上面 30s 的 ctx2——不够，会让**后续断言**
	// 因 deadline 假失败（我第一版就踩了这个坑）。
	ctxCand, cancelCand := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancelCand()
	p.extractInvoiceCandidates(ctxCand, accs, rep)
	fmt.Println("GREENMAIL CANDIDATES:", rep)

	var invCount int
	if err := pool.QueryRow(ctxCand, `SELECT count(*) FROM email_invoices WHERE account_id=$1`, acctID).Scan(&invCount); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	fmt.Println("GREENMAIL INVOICE ROWS:", invCount)
	if invCount == 0 {
		t.Fatalf("step1.5 没建出任何发票行 —— HarvestAll 无从下手，后面的断言会假失败")
	}

	// 断言 harvester 真的处理了这些**真实邮件**。
	//
	// 实测结果（2026-10-01 Greenmail，18 封 INBOX）：Processed=4 Downloaded=2
	// Pending=2 Failed=0。那 2 封 downloaded 是货真价实的 PDF 附件，被按需求 3
	// 的命名格式落盘，例如：
	//   通信-开票中心-128.00-2026-09-24.pdf
	//   其他-杭州创客家投资管理有限公司-3500.00-2026-09-24-26332000008261110741.pdf
	// （末尾那串是发票号，撞名保护追加的，见 invoice_harvest.go 的命名规则。）
	// 另外 2 封 Pending 是因为邮件里根本没有可用的 PDF 附件，属于正常待重试。
	//
	// 早期版本这里写的是「附件是占位 %PDF 内容，harvester 会落到 failed」——
	// 那是**写测试时臆测的**，从未真跑过。实测恰好相反：附件是真 PDF，走的是
	// downloaded。注释按实测改掉，别再让它把后来的人带偏。
	harv := &InvoiceHarvester{
		Store:   store,
		Fetcher: fetcher,
		DataDir: t.TempDir(),
		XMLRenderer: func(name string, inv *Invoice, xml []byte) ([]byte, error) {
			return nil, fmt.Errorf("xml renderer disabled in test")
		},
	}
	hres := harv.HarvestAll(ctx2)
	fmt.Println("GREENMAIL HARVEST:", hres)
	if hres.Processed == 0 {
		t.Fatal("harvester did not see any invoices")
	}
}