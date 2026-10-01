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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSyncGreenmail(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set; skipping greenmail integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
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

	// 跑完删掉账户，否则线上 pocketd 每 60 秒会对这个「用临时 master key 加密
	// 的测试账户」报一次 decrypt credential，把真实故障埋进日志。
	// 同样用 t.Cleanup，t.Fatal 也会删。详见 junk_greenmail_test.go 的说明。
	//
	// **必须自己开连接**：测试里是 `defer pool.Close()`，而 defer 在函数返回时
	// 先于 t.Cleanup 执行 —— 复用 pool 会拿到 `closed pool`，清理静默失败。
	// 那是第一版真踩的：日志里三条 `cleanup ...: closed pool`，账户照样留着。
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		cpool, cerr := pgxpool.New(cctx, dsn)
		if cerr != nil {
			t.Logf("cleanup pool: %v", cerr)
			return
		}
		defer cpool.Close()
		// 三张表的 WHERE 列不一样：email_accounts 是父表，只有 id，没有
		// account_id。第一版对三张表统一写 `WHERE account_id=$1 OR id=$1`，
		// 父表那句直接报 `column "account_id" does not exist` —— 清理**静默**
		// 失败，账户照样留着。子表先删、父表后删。
		for _, d := range []struct{ table, where string }{
			{"email_invoices", "account_id"},
			{"emails", "account_id"},
			{"email_accounts", "id"},
		} {
			if _, err := cpool.Exec(cctx,
				`DELETE FROM `+d.table+` WHERE `+d.where+`=$1`, acctID); err != nil {
				t.Logf("cleanup %s: %v", d.table, err)
			}
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