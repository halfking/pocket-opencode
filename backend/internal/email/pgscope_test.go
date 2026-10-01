package email

// pgscope_test.go — PG 测试的 schema 隔离助手。
//
// ## 为什么需要它
//
// 本包的 greenmail 用例（`fetcher_greenmail_test.go`、`junk_greenmail_test.go`）
// 以前是 `pgxpool.New(ctx, os.Getenv("PG_DSN"))` —— **原样吃调用方的 DSN**。
// 而本仓库的惯例是同一个 DSN 既喂服务也喂测试，所以 `PG_DSN` 的 search_path
// 完全可能就是**生产 schema**。那样跑一次，就等于往生产表里插测试账户：
// §7bh 记录的那次污染（线上 pocketd 每 60 秒对「用临时 master key 加密的测试
// 账户」报一次 `decrypt credential`，把真实故障埋进日志）就是这么来的。
//
// 写操作本身是收敛的（`DELETE ... WHERE account_id=$1` / `WHERE id=$1`），
// 所以**没有删到别人的行**；但「测试账户进了生产表」这件事本身就是缺陷 ——
// 它会污染线上日志、并且在账户 id 撞车时删掉别人的行。
//
// ## 关键细节：search_path **只**指向自己的 schema
//
// 不能写 `schema + ",public"`。PG 的规则是当前 schema 找不到就**回落到
// public**，于是「引用一张不存在的表」会静悄悄变成「读写的��生产表」——
// §7bm 里 chatagent 的 `got 278` 就是这个形态。宁可让它报错。
//
// `store_workspace_test.go` 里那份先例是 `schema + ",public"`；这里刻意不同，
// 两者都对各自的用例成立，但新写的助手应当取更严的那一个。

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// greenmailDSN 是 greenmail / realprobe 一类集成测试取 DSN 的**唯一**入口。
//
// ## 为什么统一到这里
//
// 此前这三个文件各自硬编码 `os.Getenv("PG_DSN")`：
// fetcher_greenmail_test.go / junk_greenmail_test.go / realprobe_test.go。
// 而本包其余二十多处走的是 `testDSN()`（认 POCKET_TEST_POSTGRES_DSN，
// 回退 POCKET_POSTGRES_DSN）。两套变量名并存的后果：
//
//  1. 设了标准变量、这些用例**静默 skip** —— 需求 2「移到垃圾邮件箱」那条
//     不可逆链路（junk.go 的 69 条语句）于是在 CI 与本地都从未执行，
//     而报告是「ok」；
//  2. 反过来设 PG_DSN 时，它们又真的连上去，而 PG_DSN 极可能就是生产
//     DSN（本仓库惯例是同一个 DSN 既喂服务也喂测试）。
//
// ## 这里的取法比 testDSN 更严
//
// **不**回退 POCKET_POSTGRES_DSN。只认 POCKET_TEST_POSTGRES_DSN：
// 这几个用例会**写**库（建账户、插邮件、标记 spam），让「忘了设测试变量」
// 的后果是 skip，而不是连上生产库。
//
// 用绿色目录放 PG_DSN 的风险由 pgisolation_guard_test.go 的护栏兜底
// （源码里再出现 `os.Getenv("PG_DSN")` 会让那条护栏转红）。
func greenmailDSN() string { return os.Getenv("POCKET_TEST_POSTGRES_DSN") }

// newScopedPool 建一个 search_path 只指向 schema 的连接池。
// schema 必须已经由调用方 CREATE 过。
func newScopedPool(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	return pgxpool.NewWithConfig(ctx, cfg)
}

// dropScopedSchema 收尾用。**必须用一条独立连接**：测试里通常是
// `defer pool.Close()`，而 defer 在函数返回时先于 t.Cleanup 执行 ——
// 复用那个 pool 会拿到 `closed pool`，清理**静默**失败。
// 那是 §7bh 真踩过的：日志里三条 `cleanup ...: closed pool`，账户照样留着。
func dropScopedSchema(ctx context.Context, dsn, schema string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	return err
}
