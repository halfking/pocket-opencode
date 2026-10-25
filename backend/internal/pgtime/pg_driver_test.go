package pgtime

// pg_driver_test.go —— 钉住「**驱动（pgx）到底怎么编码 timestamptz**」。
//
// ⚠⚠ 为什么这条判据是本纪律的**前提**，而不是一条普通测试：
//
//	`pgtime` 这族修法（写进 PG 的时刻先向下截断）**完全建立在一条前提上**：
//	「pgx 在编码 timestamptz 时会向下截断到微秒」。
//	⇒ ⇒ ⇒ 而 §964 的注释把这条前提**写错了**（写成了「PG 用 round-half-to-even」）
//	⇒ ⇒ ⇒ 而错误的提会让人依据它做决策 ⇒ ⇒ ⇒ 所以它必须**被断言**，
//	而不是只写在注释里。
//
// ⚠ 为什么必须**连真 PG**：pgx 的编码行为是**驱动**的行为，
//	纸面推断（读 pgx 源码 / 猜 PG 的取整规则）都判不准
//	⇒ ⇒ 且必须**两条路径都跑**：参数绑定 与 SQL 字面量
//	⇒ ⇒ ⇒ 因为它们的行为**不同**（实测），而仓里的写入**只走参数绑定**。
//
// ⚠ 没有 PG 时本用例 skip（退出码仍是 0）⇒ ⇒ 变异批次用 forbid_skip=True
//	把「被 skip」变成失败（见 scripts/mutations/s966.py）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgDriverDSN 只认测试专用 DSN（不读 POCKET_POSTGRES_DSN）——
// 理由见 internal/server/pg_test_isolation_guard_test.go 规则 1。
func pgDriverDSN() string { return os.Getenv("POCKET_TEST_POSTGRES_DSN") }

// newPGDriverEnv 建一个独立 schema 的连接池（照抄 internal/meeting 的形态）。
func newPGDriverEnv(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := pgDriverDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping PG driver precision test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "pgtime_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		closeQuietly(rootPool)
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		closeQuietly(rootPool)
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		closeQuietly(rootPool)
		t.Fatalf("test pool: %v", err)
	}
	return pool, func() {
		// 先 DROP SCHEMA 再关连接：即便 Close 卡住，测试库也已清干净。
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := rootPool.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)); err != nil {
			t.Errorf("清理失败：DROP SCHEMA %s CASCADE: %v（残留 schema 会一直留在库里）", schema, err)
		}
		closeQuietly(pool)
		closeQuietly(rootPool)
	}
}

// closeQuietly 有界地关闭连接池：卡住就放弃，绝不让测试进程挂在清理阶段。
func closeQuietly(p *pgxpool.Pool) {
	done := make(chan struct{})
	go func() {
		p.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// TestDriverEncodesTimestamptzByFlooring 是本包的核心判据。
//
// 它量三件事：
//
//	① **参数绑定路径**（仓里所有写入走的就是它）：写进去的亚微秒尾被
//	   **向下**截断 ⇒ 存回来的值 == floor(写进去的) ⇒ 差恰为 −尾数。
//	② **SQL 字面量路径**：**不是**向下截断（.7µs 会被 PG 进位）。
//	③ ⇒ ⇒ **两条路径行为不同** ⇒ ⇒ 我们的纪律必须按**参数绑定**那条
//	   ⇒ ⇒ ⇒ 而 §964 依据的「PG 会四舍五入」只对字面量路径成立 ⇒ 前提被订正。
//
// ⚠ 负控（防「判据空转」）：① 的差必须**非零**（否则这条只是在比两个相同的值）。
func TestDriverEncodesTimestamptzByFlooring(t *testing.T) {
	pool, cleanup := newPGDriverEnv(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		CREATE TABLE probe (
			label text primary key,
			tail  int  not null,
			ts    timestamptz not null
		)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tails := []int{0, 300, 500, 700, 999999}

	for _, tail := range tails {
		v := base.Add(time.Duration(tail) * time.Nanosecond)
		// 路径 A：参数绑定（pgx 编码）。
		if _, err := pool.Exec(ctx, `INSERT INTO probe (label, tail, ts) VALUES ($1, $2, $3)`,
			fmt.Sprintf("param-%d", tail), tail, v); err != nil {
			t.Fatalf("insert param-%d: %v", tail, err)
		}
		// 路径 B：SQL 字面量（让 PG 自己的输入函数去解析）。
		lit := v.Format("2006-01-02T15:04:05.999999999Z07:00")
		if _, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO probe (label, tail, ts) VALUES ('literal-%d', %d, '%s')`, tail, tail, lit)); err != nil {
			t.Fatalf("insert literal-%d: %v", tail, err)
		}
	}

	rows, err := pool.Query(ctx, `SELECT label, tail, ts FROM probe ORDER BY label`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer rows.Close()

	nonZeroDelta := 0
	paramFloored := 0
	literalNotFloored := 0
	for rows.Next() {
		var label string
		var tail int
		var ts time.Time
		if err := rows.Scan(&label, &tail, &ts); err != nil {
			t.Fatalf("scan: %v", err)
		}
		orig := base.Add(time.Duration(tail) * time.Nanosecond)
		floored := orig.Truncate(time.Microsecond)
		delta := ts.Sub(orig)

		switch {
		case strings.HasPrefix(label, "param-"):
			paramFloored++
			// ① 参数绑定：存回来的值必须**恰是** floor(写进去的)。
			if !ts.Equal(floored) {
				t.Errorf("参数绑定路径：%s 存回来的值必须是 floor(写进去的)：stored=%v want=%v（实测 delta=%v）",
					label, ts.Format(time.RFC3339Nano), floored.Format(time.RFC3339Nano), delta)
			}
			// 差必须恰为 −（尾数的亚微秒部分）。
			//
			// ⚠⚠ 这里量的是**亚微秒那部分**，不是整个尾数：
			//	尾数是**纳秒**，而 floor 只丢**不足 1µs 的那部分**
			//	⇒ 999999ns 的尾数 ⇒ floor 后剩 999µs ⇒ 差是 −999ns
			//	⇒ 而不是 −999999ns（那会把「µs 以上的部分」也算成被丢掉的）。
			//	⚠ 第一版这里写成了 `-tail`，实测红在 param-999999 上
			//	⇒ 那是**量具的期望值写错**，不是驱动行为变了。
			if want := -(time.Duration(tail) % 1000) * time.Nanosecond; delta != want {
				t.Errorf("参数绑定路径：%s 的差必须是 %v（向下截断丢掉亚微秒部分），实际 %v", label, want, delta)
			}
			if delta != 0 {
				nonZeroDelta++
			}
		case strings.HasPrefix(label, "literal-"):
			literalNotFloored++
			// ② 字面量路径：**不是**向下截断（至少 .7µs 那条会进位）。
			if tail == 700 && ts.Equal(floored) {
				t.Errorf("字面量路径：.7µs 应当被 PG 进位（stored=%v）⇒ 它与参数绑定路径**行为不同**这件事变了 ⇒ 前提要重新审视",
					ts.Format(time.RFC3339Nano))
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// ③ 负控：差必须非零（否则「floor 这件事」根本没被量到）。
	if nonZeroDelta == 0 {
		t.Error("负控：所有差都是 0 ⇒ 这条判据没有量到任何东西")
	}
	if paramFloored != len(tails) {
		t.Errorf("参数绑定路径的样本数不对：%d want %d", paramFloored, len(tails))
	}
	if literalNotFloored != len(tails) {
		t.Errorf("字面量路径的样本数不对：%d want %d", literalNotFloored, len(tails))
	}
}

// TestBoundaryParamsShareTheStoredScale 钉住「**查询边界参数**与存储值同刻度」。
//
// ⚠⚠ 为什么这条必须存在（§966.7 那条断言的实测依据）：
//
//	§966.7 写着「驱动在编码时把**边界参数也一并**向下截断 ⇒ 存储值与边界值
//	**天然同刻度** ⇒ ⇒ ⇒ 边界参数**不需要**手工截断」⇒ ⇒ ⇒ 那句话当时**只有推理**。
//	⇒ ⇒ ⇒ 本条把它变成**读数**。
//
// ⚠ 判别手法（**一个比特**就能区分两种行为）：
//
//	写入 t = …700ns（带亚微秒尾）⇒ 库里存的是 floor(t) = …000000；
//	再查 `WHERE ts = $1`，参数传**同一个 t**：
//	  · 驱动若也 floor 参数 ⇒ 参数变成 …000000 ⇒ 与库值相等 ⇒ **查得到**
//	  · 驱动若**不**处理参数 ⇒ 参数仍是 …700ns ⇒ 与库值不等 ⇒ **查不到**
//
// ⇒ ⇒ ⇒ ⇒ **「查得到 / 查不到」这一个比特，就是全部判据**。
//
// ⚠ 负控：把参数换成「库值 + 1µs」⇒ 必须**查不到**
//
//	⇒ ⇒ 否则上面那条可能是因为「等值查询恒真」而过的。
func TestBoundaryParamsShareTheStoredScale(t *testing.T) {
	pool, cleanup := newPGDriverEnv(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		CREATE TABLE probe (
			id int primary key,
			ts timestamptz not null
		)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tsVal := base.Add(700 * time.Nanosecond)

	if _, err := pool.Exec(ctx, `INSERT INTO probe (id, ts) VALUES (1, $1)`, tsVal); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// 库里的值：必须是 floor（驱动在客户端就截断了）。
	var stored time.Time
	if err := pool.QueryRow(ctx, `SELECT ts FROM probe WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("select stored: %v", err)
	}
	if want := base.Truncate(time.Microsecond); !stored.Equal(want) {
		t.Fatalf("库里存的必须是 floor 后的值：stored=%v want=%v", stored, want)
	}

	// ① 关键读数：等值查询，参数 = **写入时那个带 700ns 尾的值**。
	//    ⇒ 查得到 ⇒ **参数也被向下截断** ⇒ 参数与存储值同刻度。
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM probe WHERE ts = $1`, tsVal).Scan(&n); err != nil {
		t.Fatalf("count with equal param: %v", err)
	}
	if n != 1 {
		t.Errorf("边界参数必须与存储值同刻度（等值查询应查到 1 行）：实际 %d 行 ⇒ 参数**没有**被向下截断", n)
	}

	// ② 负控：参数 = 库值 + 1µs ⇒ 必须**查不到**（否则 ① 没有区分度）。
	var k int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM probe WHERE ts = $1`, stored.Add(time.Microsecond)).Scan(&k); err != nil {
		t.Fatalf("count with +1µs param: %v", err)
	}
	if k != 0 {
		t.Errorf("负控：参数比库值大 1µs 时必须查不到，实际 %d 行", k)
	}
}
