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
// ## 门控
//
//	POCKET_REAL_MAIL_DSN='postgresql://...' POCKET_DIAG_SCHEMA='opencode_pocket'
//
// 只在显式给了 DSN 时跑；会**建自己的临时行**并在结束时删掉，不碰既有数据。

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// upsertGuardSchema 解析目标 schema，并**拒绝**生产库与「没给」。
//
// 2026-10-04 复核：本文件原先把 POCKET_DIAG_SCHEMA 缺省成 `opencode_pocket`，
// 而它下面两个测试是会**真的写** emails 行的（cleanup 就是
// `DELETE FROM emails WHERE message_id = $1`，以及 InsertEmailIfNew）。
// 闸门又只有 `POCKET_REAL_MAIL_DSN` 非空 —— 而本仓跑只读真实库诊断时
// **本来就要**带 POCKET_REAL_MAIL_DSN。两边一撞就是「跑一次全量 go test，
// 往生产 emails 表插两行再删掉」。当时它已被 PG 隔离守卫判红
// （TestPGTestsNeverTargetTheProductionSchema），属真违规而非误报。
//
// 修法沿用本仓既有约定（见 diag_snippet_leak_test.go 的登记理由）：
// **写路径拒绝 schema 缺省值**，并且显式点名生产库时直接 Fatal。
func upsertGuardSchema(t *testing.T) string {
	t.Helper()
	schema := os.Getenv("POCKET_DIAG_SCHEMA")
	if schema == "" {
		t.Skip("POCKET_DIAG_SCHEMA 未设置，故不运行：本文件会真的 INSERT/DELETE emails 行，" +
			"缺省会落到生产库 opencode_pocket。请显式指定一个隔离 schema 后再跑。")
	}
	if schema == "opencode_pocket" {
		t.Fatalf("POCKET_DIAG_SCHEMA=%s 指向生产库，拒绝运行：", schema)
	}
	return schema
}

// TestStoreUpsertSurvivesMessageIDConflict 用**真 PG**跑这条冲突。
//
// 为什么不拿 mock 代替：缺陷本体就是 PG 对 ON CONFLICT 目标与唯一约束的匹配
// 规则，mock 复现不了它 —— 而这个缺陷的整个教训就是「以为构造上不可能」。
func TestStoreUpsertSurvivesMessageIDConflict(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	schema := upsertGuardSchema(t)
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// emails.account_id 有外键指向 email_accounts，所以必须借一个**真实存在**的
	// 账户；不去新建账户，是为了不往业务表塞测试行。message_id 用本次运行独有的
	// 值，清理也只按它删，绝不碰既有数据。
	//
	// （外键是本轮实测才发现的：最初这里写死一个假 account_id，第一次插入就被
	// 23503 挡下。用 mock 或「假设没有外键」都会把这个事实一并掩盖掉。）
	var acct string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_accounts ORDER BY id LIMIT 1`).Scan(&acct); err != nil {
		t.Skipf("库里没有可用账户（需要至少一个 email_accounts 行）：%v", err)
	}
	const msgID = "guard-upsert-msgid-20261003"
	const idA = "em-guard-A-upsert"
	const idB = "em-guard-B-upsert"
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM emails WHERE message_id = $1`, msgID)
	}
	cleanup()
	t.Cleanup(cleanup)

	s := &Store{pool: pool}

	// 第 1 行：POP3 形态的 id。
	ins1, err := s.InsertEmailIfNew(ctx, Email{
		ID: idA, AccountID: acct, MessageID: msgID, UID: 1,
		FromAddress: "a@example.com", Subject: "第一行", Snippet: "旧摘要", Date: 1,
	})
	if err != nil {
		t.Fatalf("第一次插入失败（本用例前提就是它能成功）：%v", err)
	}
	if !ins1 {
		t.Fatal("第一次插入应报告 inserted=true")
	}

	// 第 2 行：**同账户、同 message_id、不同 id** —— 精确构造出那条冲突。
	ins2, err := s.InsertEmailIfNew(ctx, Email{
		ID: idB, AccountID: acct, MessageID: msgID, UID: 2,
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
	// 按 message_id 数，**不能**按 account_id 数：上面借的是一个真实账户，
	// 它本来就有别的邮件行，按账户数会把它们一并算进来（实测 11 行）。
	// 断言的口径必须与「本用例造出来的那几行」一致。
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM emails WHERE message_id = $1`, msgID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("兜底后应仍只有 1 行（同 message_id 不允许并存两行），实际 %d 行", rows)
	}

	var gotSnippet string
	if err := pool.QueryRow(ctx,
		`SELECT snippet FROM emails WHERE account_id = $1 AND message_id = $2`,
		acct, msgID).Scan(&gotSnippet); err != nil {
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
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set")
	}
	schema := upsertGuardSchema(t)
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var acct string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_accounts ORDER BY id LIMIT 1`).Scan(&acct); err != nil {
		t.Skipf("库里没有可用账户：%v", err)
	}
	const msgID = "guard-upsert-empty-20261003"
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM emails WHERE message_id = $1`, msgID)
	}
	cleanup()
	t.Cleanup(cleanup)

	s := &Store{pool: pool}
	if _, err := s.InsertEmailIfNew(ctx, Email{
		ID: "em-guard-A-empty-upsert", AccountID: acct, MessageID: msgID, UID: 1,
		Subject: "保留", Snippet: "正常摘要", Date: 1,
	}); err != nil {
		t.Fatalf("第一次插入失败：%v", err)
	}

	// 第二行摘要为空 —— 必须保留旧值。
	if _, err := s.InsertEmailIfNew(ctx, Email{
		ID: "em-guard-B-empty-upsert", AccountID: acct, MessageID: msgID, UID: 2,
		Subject: "保留", Snippet: "", Date: 2,
	}); err != nil {
		t.Fatalf("空摘要的冲突兜底失败：%v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`SELECT snippet FROM emails WHERE account_id = $1 AND message_id = $2`,
		acct, msgID).Scan(&got); err != nil {
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
	var _ = pgx.ErrNoRows // 保持 import 有用，避免误删后编译失败掩盖真错误
}
