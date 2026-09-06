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

	// 触发发票提取，断言 harvester 处理真实邮件（不一定 Downloaded，因为
	// Greenmail 测试邮件的附件是占位 %PDF 内容，harvester 会落到 failed 但
	// processed>0；这是预期行为——测试的是「真实邮件被识别成发票/账单」）。
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