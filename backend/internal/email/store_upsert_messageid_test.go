package email

// store_upsert_messageid_test.go — 护栏：`ON CONFLICT (id)` 消解不了
// `emails_account_id_message_id_key`，冲突时整条 INSERT 报错。
//
// ## 缺陷（2026-10-03 22:02 真库 backfill 实测）
//
// 本表两条唯一约束：
//
//	emails_pkey                      PRIMARY KEY (id)
//	emails_account_id_message_id_key  UNIQUE (account_id, message_id)
//
// 而写入语句只写了 `ON CONFLICT (id)`。PG 的 ON CONFLICT **只消解写明的那一条**，
// 另一条冲突时整条语句直接报 23505 —— 于是：
//
//   · 已存在的邮件：摘要刷新整个丢掉。存量里 3 行原始 MIME 摘要
//     （em-6 / em-11 / em-1669791317）就是这么被永久冻住的；
//   · 新的邮件：**整封根本没有入库**。2026-10-03 22:02 的 backfill 出现 33 次。
//
// ## 为什么这不是理论边界
//
// 两条入库路径在结构上就会撞：POP3 的 id 是 `em-pop3-<acct>-<uidl>`，
// IMAP 的是 `em-<uid>-<acct>`，但两者的 message_id 取的是**同一封邮件的
// 真实 Message-ID**。同账户同邮件经两条路径各落一行 ⇒ id 不同、
// (account_id, message_id) 相同。仓库自己的注释就记着「QQ 信箱 284/444 封
// 走 POP3 路径，47 组是重复副本」。
//
// ## 隔离：2026-10-04 审计重写（本文件此前有三个可证伪的缺陷）
//
// 初版把隔离寄托在**运维手工指定** `POCKET_DIAG_SCHEMA` 上：
//
//  1. **main 的 CI 是红的。** 规则 2（打开 PG 必须钉自建 `*_test_` schema）
//     判 `TestPGTestsNeverTargetTheProductionSchema` 失败，而
//     `.github/workflows/backend.yml` 跑的就是 `go test -race ./... -count=1`
//     —— 于是一个测试文件把整条主干判红。
//  2. **这个用例在 CI 里恒 skip，永远不执行。** 闸门是
//     `POCKET_DIAG_SCHEMA` 非空，而 CI 只设 `POCKET_TEST_POSTGRES_DSN`。
//     它在本地/真库上「跑过」，在 CI 上从不跑。
//  3. **它结构上跑不起来。** 初版借真实账户
//     （`SELECT id FROM email_accounts LIMIT 1`），而干净的自建 schema 里
//     `email_accounts` 是空的 ⇒ 落到 `t.Skipf("库里没有可用账户")`。
//     能让它真正断言的场景，恰好是它拒绝的那个（生产库）。
//
// 修法：按本包 fetcher_greenmail_test.go 的既有形态在本文件内自建
// `email_upsertguard_test_<hex>` schema（守卫的 isolatedSchemaRe 认得的
// 正是这个字面量）、用 RuntimeParams 把 search_path 钉上去、
// `NewStore` 跑真迁移 ⇒ 两条唯一约束是真的、账户自建解开外键、
// 收尾整条 schema DROP。⇒ 不再需要 `POCKET_REAL_MAIL_DSN` /
// `POCKET_DIAG_SCHEMA`，也不再需要在 PG 隔离守卫的豁免表里登记。
//
// ## 为什么隔离写在本文件里，而不是调 newWorkspaceTestStore
//
// 试过复用 `newWorkspaceTestStore`，能过守卫——**但过得没有道理**：
// 规则 2 的 `isolatedSchemaRe` 是词法判据（要求文件里出现 `"..._test_"`
// 字面量），看不见「连接由 helper 提供」。负控实测：把一个真·不隔离的
// `pgxpool.New` + `DELETE FROM emails` 塞进那个版本，守卫**依然绿**。
// 也就是说隔离由别处兜着、而这条判据在本文件上已经没有牙齿。
// 自己建 schema 才能让「守卫绿」与「确实隔离」是同一件事。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newUpsertGuardStore 自建隔离 schema + Store，并返回收尾函数。
//
// 形态照抄 fetcher_greenmail_test.go（那个文件是守卫注释里点名的
// 「已正确隔离」范例），差别只有 schema 前缀。
func newUpsertGuardStore(t *testing.T) (*Store, *string, func()) {
	t.Helper()
	dsn := greenmailDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping upsert-conflict guard")
	}
	ctx := context.Background()

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "email_upsertguard_test_" + hex.EncodeToString(buf)

	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach postgres: %v", err)
	}
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	// 清理在这里就注册：下面还有 t.Fatalf，而 t.Fatalf 走 Goexit，
	// 栈上 defer 会跑但尚未返回的 cleanup 永远不会执行。
	pool, err := newScopedPool(ctx, dsn, schema)
	if err != nil {
		rootPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	cleanup := func() {
		pool.Close()
		if _, err := rootPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
		rootPool.Close()
	}
	t.Cleanup(cleanup)

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore (migrate): %v", err)
	}
	return store, &schema, cleanup
}

// seedGuardAccount 建一个本用例自有的账户，解开 emails.account_id 的外键。
//
// 初版是借生产库里已存在的账户（`SELECT id FROM email_accounts LIMIT 1`），
// 理由写的是「不往业务表塞测试行」。在自建 schema 里这个理由不再成立：
// 这里**就是**本用例自己的表，自建一行反而是唯一不碰真实库的做法。
func seedGuardAccount(t *testing.T, s *Store, id string) {
	t.Helper()
	acc := &Account{
		ID: id, UserID: "user-" + id, WorkspaceID: "ws-" + id,
		DisplayName: "guard " + id, EmailAddress: id + "@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password",
		SyncIntervalMin: 15, Enabled: true, CreatedAt: time.Now().Unix(),
	}
	if err := s.InsertAccount(context.Background(), acc, "enc-cred"); err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

// requireIsolatedSchema 在**运行期**自证确实落在 `*_test_` schema 上。
//
// 为什么在守卫之外还要这一层：词法判据只能证明「文件里写了自建 schema」，
// 证明不了「连接真的用它」。而本用例会真的 INSERT/DELETE `emails` 行；
// 隔离若失效，它会安静地改到真实库、并且**报告 ok**。
func requireIsolatedSchema(t *testing.T, s *Store, want *string) {
	t.Helper()
	var cur string
	if err := s.pool.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&cur); err != nil {
		t.Fatalf("read current_schema: %v", err)
	}
	if cur != *want {
		t.Fatalf("current_schema()=%q，应为本用例自建的 %q：隔离没生效，"+
			"下面的写入会落到真实库", cur, *want)
	}
	if !strings.Contains(cur, "_test") {
		t.Fatalf("current_schema()=%q 不含 _test_", cur)
	}
}

// TestStoreUpsertSurvivesMessageIDConflict 用**真 PG**跑这条冲突。
//
// 为什么不拿 mock 代替：缺陷本体就是 PG 对 ON CONFLICT 目标与唯一约束的匹配
// 规则，mock 复现不了它 —— 而这个缺陷的整个教训就是「以为构造上不可能」。
func TestStoreUpsertSurvivesMessageIDConflict(t *testing.T) {
	store, schema, cleanup := newUpsertGuardStore(t)
	defer cleanup()
	ctx := context.Background()
	requireIsolatedSchema(t, store, schema)

	const acctID = "acct-guard-upsert"
	seedGuardAccount(t, store, acctID)

	const msgID = "guard-upsert-msgid-20261003"
	const idA = "em-guard-A-upsert"
	const idB = "em-guard-B-upsert"

	// 第 1 行：POP3 形态的 id。
	ins1, err := store.InsertEmailIfNew(ctx, Email{
		ID: idA, AccountID: acctID, MessageID: msgID, UID: 1,
		FromAddress: "a@example.com", Subject: "第一行", Snippet: "旧摘要", Date: 1,
	})
	if err != nil {
		t.Fatalf("第一次插入失败（本用例前提就是它能成功）：%v", err)
	}
	if !ins1 {
		t.Fatal("第一次插入应报告 inserted=true")
	}

	// 第 2 行：**同账户、同 message_id、不同 id** —— 精确构造出那条冲突。
	ins2, err := store.InsertEmailIfNew(ctx, Email{
		ID: idB, AccountID: acctID, MessageID: msgID, UID: 2,
		FromAddress: "a@example.com", Subject: "第二行", Snippet: "新摘要", Date: 2,
	})
	if err != nil {
		t.Fatalf("message_id 唯一约束冲突应当被兜住，实际整条 INSERT 失败：%v", err)
	}
	if ins2 {
		t.Fatal("冲突分支必须报告 inserted=false —— 它确实不是新插入，" +
			"否则 fetcher 的「新邮件 N」会被虚增")
	}

	var rows int
	// 按 message_id 数，**不能**按 account_id 数：账户名下的其它邮件会被算进来。
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM emails WHERE message_id = $1`, msgID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("兜底后应仍只有 1 行（同 message_id 不允许并存两行），实际 %d 行", rows)
	}

	var gotSnippet string
	if err := store.pool.QueryRow(ctx,
		`SELECT snippet FROM emails WHERE account_id = $1 AND message_id = $2`,
		acctID, msgID).Scan(&gotSnippet); err != nil {
		t.Fatalf("select snippet: %v", err)
	}
	if gotSnippet != "新摘要" {
		t.Fatalf("兜底分支必须按与 ON CONFLICT 分支相同的口径刷新摘要，实际 %q", gotSnippet)
	}
}

// TestStoreUpsertMessageIDConflictKeepsOldSnippetOnEmpty 钉住「空串不覆盖」
// 这条既有约定在**兜底分支**上同样成立。
//
// 这条不能省：兜底是新增的第二条写入路径，若只复制了「刷新」而漏了
// 「空值保留旧值」，一次抓不到正文的重跑就会把正常摘要刷成空白 ——
// 那是用新缺陷换旧缺陷。
func TestStoreUpsertMessageIDConflictKeepsOldSnippetOnEmpty(t *testing.T) {
	store, schema, cleanup := newUpsertGuardStore(t)
	defer cleanup()
	ctx := context.Background()
	requireIsolatedSchema(t, store, schema)

	const acctID = "acct-guard-empty"
	seedGuardAccount(t, store, acctID)

	const msgID = "guard-upsert-empty-20261003"
	if _, err := store.InsertEmailIfNew(ctx, Email{
		ID: "em-guard-A-empty-upsert", AccountID: acctID, MessageID: msgID, UID: 1,
		Subject: "保留", Snippet: "正常摘要", Date: 1,
	}); err != nil {
		t.Fatalf("第一次插入失败：%v", err)
	}

	// 第二行摘要为空 —— 必须保留旧值。
	if _, err := store.InsertEmailIfNew(ctx, Email{
		ID: "em-guard-B-empty-upsert", AccountID: acctID, MessageID: msgID, UID: 2,
		Subject: "保留", Snippet: "", Date: 2,
	}); err != nil {
		t.Fatalf("空摘要的冲突兜底失败：%v", err)
	}

	var got string
	if err := store.pool.QueryRow(ctx,
		`SELECT snippet FROM emails WHERE account_id = $1 AND message_id = $2`,
		acctID, msgID).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "正常摘要" {
		t.Fatalf("空摘要把旧值刷掉了：实际 %q，期望保留 %q", got, "正常摘要")
	}
}

// TestIsUniqueViolationIsNotTooLoose 钉住判据**不会过宽**。
//
// 两条唯一约束共用同一个 SQLSTATE 23505。只比状态码、不比约束名的话，
// id 冲突也会被当成 message_id 冲突吞掉 —— 而 id 冲突本来就已经被
// `ON CONFLICT (id)` 处理掉了，那条路径压根走不到这里。放宽的代价是
// 「一条本该硬失败的写入被静默改写」。
func TestIsUniqueViolationIsNotTooLoose(t *testing.T) {
	other := &pgconn.PgError{Code: "23505", ConstraintName: "emails_pkey"}
	if isUniqueViolation(other, "emails_account_id_message_id_key") {
		t.Error("把 emails_pkey 的冲突认成 message_id 冲突了 —— 判据过宽，" +
			"会让本该硬失败的写入被静默改写")
	}
	want := &pgconn.PgError{Code: "23505", ConstraintName: "emails_account_id_message_id_key"}
	if !isUniqueViolation(want, "emails_account_id_message_id_key") {
		t.Error("真正的 message_id 冲突没被认出来 —— 判据过严，兜底不会触发")
	}
	notUnique := &pgconn.PgError{Code: "22021", ConstraintName: "emails_account_id_message_id_key"}
	if isUniqueViolation(notUnique, "emails_account_id_message_id_key") {
		t.Error("非 23505 的错误被当成唯一冲突了")
	}
	if isUniqueViolation(nil, "emails_account_id_message_id_key") {
		t.Error("nil 被当成唯一冲突了")
	}
}
