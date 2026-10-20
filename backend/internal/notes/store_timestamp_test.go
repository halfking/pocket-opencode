package notes

// §940 判据：notes 的时间戳必须是**绝对时刻**（timestamptz）。
//
// ⚠⚠ 本节要钉的缺陷（§940 的 D1，实测）：
//
// `notes` 表的 created_at / updated_at / deleted_at 原本是
// `timestamp without time zone` —— PG 里的**裸墙钟**（naive wall clock），
// 它**不携带时区**。于是同一列上出现了两套不一致的约定：
//
//	写：参数是 timestamptz（instant）⇒ PG 用**会话时区**把这个 instant
//	    **渲染成墙钟**再存；`CURRENT_TIMESTAMP` 同样是会话时区的墙钟。
//	读：pgx 把裸墙钟**按 UTC 解释**。
//
// ⇒ ⇒ **往返误差 = 会话时区差**。实测（本机 PG 会话时区 Asia/Shanghai）：
// 写进去再读回来，epoch 偏 **+28800 秒（+8 小时）**。
//
// ⇒ ⇒ 后果不是「时间显示得不准」，而是**增量同步被平移**：
// 笔记的 updated_at 一律落在**未来 8 小时** ⇒ ⇒ `GET /api/notes?since=`
// 的水位线过滤**永远命中** ⇒ ⇒ **每一轮增量轮询都把最近 8 小时的改动
// 重推一遍** ⇒ ⇒ 成本 ∝ **8 小时的改动量**，而不是 ∝ **新增量**
// ⇒ ⇒⇒ 这正是属主要求（「成本只取决于存储/新增量」）的反面。
//
// ⇒ ⇒⇒ 修法：三个时间列统一成 **timestamptz**（全仓其余 store 都是它，
// notes 是唯一的例外）⇒ ⇒ 绝对时刻与会话时区无关 ⇒ ⇒ 读写两侧**零改动**
// 就都精确了。存量行用 `AT TIME ZONE current_setting('TimeZone')` 按
// **写它那一刻的会话时区**还原成真正的 instant（不硬编码 '-8 hours'）。
//
// ⚠⚠ 本文件要量的是**量**（epoch 偏差、列类型），不是「看起来对不对」；
// 而且**量的是两处**：① DB 里存的 epoch（写对了吗）② Go 读回来的 epoch
// （读对了吗）—— ⚠ 修复前的偏差**同时**来自这两侧，只量一侧会漏。
//
// ⚠ 需要真 PG：notes.Store 只有 PostgreSQL 一个实现 ⇒ 没有内存版可测。
// ⚠ §940 的 D2：本包的 PG 夹具原本恒返回 nil ⇒ 全部用例恒 skip ⇒
// 本文件能存在的前提是夹具已经接上真 PG（见 store_test.go 的 getTestPool）。

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// s940DriftTolerance 是允许的 epoch 偏差（秒）。
//
// ⚠ 为什么是 2 秒：列是 timestamptz ⇒ 存的是 instant ⇒ 往返**精确**；
// 留 2 秒只是为了让「跨秒边界写、跨秒边界读」这种时序抖动不误报。
// ⚠ 它**不能**放大到分钟级：修复前的偏差是 **+28800 秒**（8 小时）⇒
// 任何小于 1 小时的容差都足以把那个缺陷判红。
const s940DriftTolerance = 2

// s940RawEpoch 直接从 DB 读某一行的 updated_at 的 epoch 秒。
//
// ⚠ 为什么要绕开 store 读：store 的读路径（pgx → scanNoteRows → .Unix()）
// 本身就是缺陷的一侧 ⇒ ⇒ 只量 Go 侧会漏掉「写歪了」的可能。
func s940RawEpoch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) int64 {
	t.Helper()
	var epoch int64
	err := pool.QueryRow(ctx,
		"SELECT EXTRACT(EPOCH FROM updated_at)::bigint FROM notes WHERE id = $1", id).Scan(&epoch)
	if err != nil {
		t.Fatalf("read raw epoch for %s: %v", id, err)
	}
	return epoch
}

// s940NewFixture 造一个夹具（pool + 已经 migrate 完的 store）。
//
// ⚠⚠ **legacy 这个参数是本节判据的关键**（★ 本轮修掉的一个真盲区）：
//
//	`legacy=false` ⇒ 新库 ⇒ `migrate()` 的 CREATE TABLE 直接把三列声明成
//	  **TIMESTAMPTZ** ⇒ ⇒ **转换根本没发生** ⇒ ⇒ 凡是「结果」类的判据
//	  （列类型 / 往返精度）在这种夹具上**测的是 DDL，不是转换**。
//	`legacy=true`  ⇒ 先造一张**裸墙钟**的旧表 ⇒ ⇒ `migrate()` 的 ALTER
//	  **真的发生** ⇒ ⇒ 判据这才真的在测「转换」这件事。
//
// ⇒ ⇒ ⇒ 修复前（§940.7 的 M4/M5 变异暴露出来的）：**只有旧行还原那一条**
// ⇒ ⇒ ⇒ 在测转换 ⇒ ⇒ ⇒ 其余判据在「转换整个坏掉」时**仍然绿** ⇒ ⇒ ⇒
// ⇒ ⇒ ⇒ ⇒ 那个绿是**假的** ⇒ ⇒⇒ ⇒ 所以每一条「结果类」判据都**两种夹具各跑一遍**。
//
// ⚠ 为什么「旧表」是生产形态：生产上那张表**就是**裸墙钟的（它是
// `appendix-a-pg-migration.sql` 建的，从没被改过类型）⇒ ⇒ ⇒ 存量库**才是**
// ⇒ ⇒ ⇒ 生产的主路径 ⇒ ⇒ ⇒ 「只测新库」等于**只测了不存在的形态**。
func s940NewFixture(t *testing.T, runtimeParams map[string]string, legacy bool) (*pgxpool.Pool, *Store) {
	t.Helper()
	pool := newNotesTestPool(t, runtimeParams)
	if legacy {
		createLegacyNotesTable(t, pool)
	}
	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return pool, store
}

// s940FixtureCases 是「两种夹具」的枚举 ⇒ ⇒ 判据在两种形态上**各跑一遍**。
//
// ⚠ 名字会出现在子测试名里 ⇒ ⇒ 红了的时候能一眼看出「是新库臂还是旧库臂」。
var s940FixtureCases = []struct {
	name   string
	legacy bool
}{
	{"新库（DDL 直接建表 ⇒ 不触发转换）", false},
	{"旧库（裸墙钟列 ⇒ 靠 ALTER 转换）", true},
}

// ── 1. 往返精确：三条写入路径 ─────────────────────────────────────

// TestS940TimestampRoundTripIsExact 三条写入路径写进去、读回来，epoch 必须一致。
//
// ⚠ 三条路径缺一不可（它们是同一列的三个入口）：
//
//	① Upsert 显式传 CreatedAt/UpdatedAt ⇒ 走 Go 侧的 time.Unix(x).UTC()
//	② Upsert 不传时间戳（0）⇒ 走 DB 侧 CURRENT_TIMESTAMP
//	③ UpdateNoteScoped ⇒ 走 DB 侧 CURRENT_TIMESTAMP（更新路径）
//
// ⇒ ⇒ 修复前三条**全部**偏 +8h（实测）⇒ ⇒ 三条都必须是判据。
//
// ⚠⚠ **两种夹具各跑一遍**（新库 / 旧库）：只跑新库的话，这条判据**测的是
// DDL 而不是转换** ⇒ ⇒ ⇒ 而生产的主形态是**旧库** ⇒ ⇒ ⇒ 两种都跑。
func TestS940TimestampRoundTripIsExact(t *testing.T) {
	for _, tc := range s940FixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			testS940RoundTripIsExact(t, tc.legacy)
		})
	}
}

func testS940RoundTripIsExact(t *testing.T, legacy bool) {
	pool, store := s940NewFixture(t, nil, legacy)
	ctx := context.Background()

	base := time.Now().Unix()
	base -= base % 60 // 对齐到整分 ⇒ 跨秒边界不误报

	// ① 显式时间戳
	if err := store.Upsert(ctx, &Note{
		ID: "s940_a", UserID: "u", WorkspaceID: "default",
		Snippet: "s", ContentType: "text",
		CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	// ② 不传时间戳 ⇒ DB 默认
	if err := store.Upsert(ctx, &Note{
		ID: "s940_b", UserID: "u", WorkspaceID: "default",
		Snippet: "s", ContentType: "text",
	}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	// ③ 更新路径
	if err := store.Upsert(ctx, &Note{
		ID: "s940_c", UserID: "u", WorkspaceID: "default",
		Snippet: "s", ContentType: "text",
		CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("upsert c: %v", err)
	}
	title := "c2"
	if _, err := store.UpdateNoteScoped(ctx, "s940_c", "u", "default", NotePatch{Title: &title}); err != nil {
		t.Fatalf("update c: %v", err)
	}

	list, err := store.ListScoped(ctx, "u", "default", "")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	got := map[string]Note{}
	for _, n := range list {
		got[n.ID] = n
	}

	// ① 显式路径：必须**逐秒**对上写入的那个 epoch。
	a, ok := got["s940_a"]
	if !ok {
		t.Fatalf("s940_a missing from list")
	}
	if drift := a.UpdatedAt - base; drift > s940DriftTolerance || drift < -s940DriftTolerance {
		t.Errorf("① Upsert 显式时间戳：读回 epoch 偏 %+d 秒（写的是 %d，读到 %d）—— "+
			"期望 |偏差| <= %d", drift, base, a.UpdatedAt, s940DriftTolerance)
	}
	if raw := s940RawEpoch(t, ctx, pool, "s940_a"); raw-base > s940DriftTolerance || raw-base < -s940DriftTolerance {
		t.Errorf("① Upsert 显式时间戳：DB 里存的 epoch 偏 %+d 秒（写的是 %d，存的是 %d）",
			raw-base, base, raw)
	}

	// ② / ③：DB 默认与更新路径 ⇒ 允许「现在」，但**不得偏一个时区差**。
	for _, id := range []string{"s940_b", "s940_c"} {
		n, ok := got[id]
		if !ok {
			t.Fatalf("%s missing from list", id)
		}
		now := time.Now().Unix()
		if n.UpdatedAt > now+s940DriftTolerance {
			t.Errorf("%s：updated_at 落在**未来**（%d > now %d，偏 %+d 秒）—— "+
				"这正是「增量窗口被平移」的症状：水位线永远追不上它，每轮都被重推",
				id, n.UpdatedAt, now, n.UpdatedAt-now)
		}
		if n.UpdatedAt < now-3600 {
			t.Errorf("%s：updated_at 偏到过去太多（%d vs now %d，偏 %+d 秒）",
				id, n.UpdatedAt, now, n.UpdatedAt-now)
		}
	}
}

// ── 2. 列类型必须是绝对时刻 ───────────────────────────────────────

// TestS940TimestampColumnsAreAbsolute 三个时间列必须都是 timestamptz。
//
// ⚠ 为什么单独钉一条：往返精确是**结果**，列类型是**原因**。
// ⇒ ⇒ 只钉结果的话，将来有人「为了兼容」把列改回裸墙钟，只要恰好在
// UTC 部署上，往返仍然是精确的 ⇒ ⇒ 门禁不会红 ⇒ ⇒ ⇒ 必须把**口径**本身
// 钉住（house 口径：全仓其余 store 用的都是 timestamptz）。
//
// ⚠⚠ **两种夹具各跑一遍**：旧库那一臂才是**真在测转换**（新库那一臂测的是
// DDL）⇒ ⇒ 「只转第一列」这种缺陷只会在旧库臂现形 ⇒ ⇒ ⇒ 漏掉旧库臂 ⇒ ⇒ ⇒
// ⇒ 这条判据就会在**转换整个坏掉**时仍然绿 ⇒ ⇒ ⇒ 那个绿是**假的**。
func TestS940TimestampColumnsAreAbsolute(t *testing.T) {
	for _, tc := range s940FixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			testS940ColumnsAreAbsolute(t, tc.legacy)
		})
	}
}

func testS940ColumnsAreAbsolute(t *testing.T, legacy bool) {
	pool, _ := s940NewFixture(t, nil, legacy)
	ctx := context.Background()

	for _, col := range notesTimestampColumns {
		var typ string
		err := pool.QueryRow(ctx, `
			SELECT format_type(atttypid, atttypmod)
			FROM pg_attribute
			WHERE attrelid = to_regclass('notes') AND attname = $1`, col).Scan(&typ)
		if err != nil {
			t.Fatalf("read type of notes.%s: %v", col, err)
		}
		if typ != "timestamp with time zone" {
			t.Errorf("notes.%s 的类型是 %q，期望 %q —— 裸墙钟（timestamp without time zone）"+
				"在「写按会话时区渲染 / 读按 UTC 解释」下往返会偏一个时区差",
				col, typ, "timestamp with time zone")
		}
	}
}

// ── 3. 旧库存量行必须被还原 ───────────────────────────────────────

// createLegacyNotesTable 造一张**旧形态**的 notes 表（三个时间列都是
// `timestamp without time zone`）—— 也就是 §940 修复之前线上那张表。
//
// ⚠⚠ 为什么抽成一份（§80 的形状）：这段 DDL 有**两个**用例要用
//
//	（旧行还原判据 + 「不得重写表」判据）⇒ ⇒ 抄两遍 ⇒ ⇒ 同一处改动
//	（比如漏加一列）只会在其中一处暴露 ⇒ ⇒ ⇒ 必须共用一份。
func createLegacyNotesTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE notes (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT DEFAULT 'default',
			title TEXT, content TEXT NOT NULL DEFAULT '', snippet TEXT, content_type TEXT DEFAULT 'voice',
			domain TEXT, tags JSONB DEFAULT '[]'::jsonb, audio_path TEXT, audio_duration INTEGER DEFAULT 0,
			created_by_voice BOOLEAN DEFAULT TRUE, ai_summary TEXT, confidence_score REAL,
			created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP WITHOUT TIME ZONE)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
}

// s940RelFileNode 读 notes 表当前的文件节点号。
//
// ⚠ 为什么用它当「表有没有被重写」的探针：PG 改写一张表时会把它指向一个
// **新的 relfilenode**（旧的留在磁盘上等 vacuum）⇒ ⇒ relfilenode 变了
// ⇒ ⇒ 就说明这张表**刚刚被整表拷贝过一次**。
func s940RelFileNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var rn int64
	if err := pool.QueryRow(ctx,
		"SELECT relfilenode::bigint FROM pg_class WHERE oid = to_regclass('notes')").Scan(&rn); err != nil {
		t.Fatalf("read relfilenode: %v", err)
	}
	return rn
}

// TestS940LegacyTableMigrationRestoresInstants 旧库（裸墙钟列）+ 旧行
// ⇒ migrate() 之后必须还原成**真正的 instant**。
//
// ⚠ 这是本节最容易被忽略的一半：只保证「以后写的不再歪」是不够的 ——
// 存量行已经歪了 ⇒ ⇒ 如果不还原，用户的**历史**笔记仍然带着 +8h 的
// 时间戳 ⇒ ⇒ 增量窗口仍然被平移 ⇒ ⇒ 缺陷只修了一半。
//
// ⚠ 还原必须用**写它那一刻的会话时区**去解释墙钟 ⇒ ⇒ 这里显式把会话
// 时区设成 America/New_York 来构造旧行 ⇒ ⇒ 若实现硬编码 '-8 hours'
// ⇒ ⇒ 还原值会偏 3 小时 ⇒ ⇒ 判据立刻红。
func TestS940LegacyTableMigrationRestoresInstants(t *testing.T) {
	pool := newNotesTestPool(t, map[string]string{"TimeZone": "America/New_York"})
	ctx := context.Background()
	createLegacyNotesTable(t, pool)

	epoch := time.Now().Unix()
	epoch -= epoch % 60
	// ⚠ 旧行必须**按旧代码的写法**造出来 ⇒ ⇒ 即「pgx 送一个 instant，
	// PG 用会话时区把它渲染成墙钟再存」⇒ ⇒ 用 `to_timestamp($1) AT TIME ZONE
	// current_setting('TimeZone')` 逐字复现那个写法。
	// ⚠⚠ 千万别在这里硬编码某个时区偏移（比如 '-5 hours'）：测试机上
	// America/New_York 在 10 月是 **EDT(-4h)** 而不是 EST(-5h) ⇒ ⇒
	// 硬编码会把**夹具**写歪 ⇒ ⇒ 判据红的是夹具不是被测代码。
	if _, err := pool.Exec(ctx, `
		INSERT INTO notes (id,user_id,workspace_id,title,content,snippet,content_type,domain,tags,
			audio_path,audio_duration,created_by_voice,created_at,updated_at)
		VALUES ('s940_legacy','u','default','old','c','s','voice',NULL,'[]'::jsonb,NULL,0,TRUE,
			to_timestamp($1) AT TIME ZONE current_setting('TimeZone'),
			to_timestamp($1) AT TIME ZONE current_setting('TimeZone'))`,
		epoch); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	var wall string
	if err := pool.QueryRow(ctx,
		"SELECT to_char(updated_at, 'YYYY-MM-DD HH24:MI:SS') FROM notes WHERE id = 's940_legacy'",
	).Scan(&wall); err != nil {
		t.Fatalf("read legacy wall clock: %v", err)
	}

	// 迁移
	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore (migrate): %v", err)
	}

	got, err := store.GetByIDScoped(ctx, "s940_legacy", "u", "default")
	if err != nil {
		t.Fatalf("GetByIDScoped: %v", err)
	}
	if drift := got.UpdatedAt - epoch; drift > s940DriftTolerance || drift < -s940DriftTolerance {
		t.Errorf("旧行没有被还原成真正的 instant：期望 %d，读到 %d（偏 %+d 秒）—— "+
			"墙钟 %s 必须按**写它那一刻的会话时区**解释",
			epoch, got.UpdatedAt, drift, wall)
	}
}

// ── 4. 精度与会话时区无关 ─────────────────────────────────────────

// TestS940TimestampIsSessionTimeZoneIndependent 换一个会话时区，往返仍然精确。
//
// ⚠ 这是「不是恰好在本机时区对」的唯一证据：本机是 Asia/Shanghai，
// 换到 America/New_York 后若实现里残留了「按某个固定时区换算」的逻辑
// ⇒ ⇒ 偏差会立刻现形。
//
// ⚠⚠ 同样**两种夹具各跑一遍**：旧库那一臂才真的在测「转换在别的会话时区
// 下也做对了」（生产库换个时区跑，就会走到那一条）。
func TestS940TimestampIsSessionTimeZoneIndependent(t *testing.T) {
	for _, tc := range s940FixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			_, store := s940NewFixture(t, map[string]string{"TimeZone": "America/New_York"}, tc.legacy)
			ctx := context.Background()

			base := time.Now().Unix()
			base -= base % 60
			if err := store.Upsert(ctx, &Note{
				ID: "s940_tz", UserID: "u", WorkspaceID: "default",
				Snippet: "s", ContentType: "text",
				CreatedAt: base, UpdatedAt: base,
			}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			got, err := store.GetByIDScoped(ctx, "s940_tz", "u", "default")
			if err != nil {
				t.Fatalf("GetByIDScoped: %v", err)
			}
			if drift := got.UpdatedAt - base; drift > s940DriftTolerance || drift < -s940DriftTolerance {
				t.Errorf("会话时区换成 America/New_York 后往返偏 %+d 秒 —— "+
					"说明精度仍然依赖会话时区（期望 |偏差| <= %d）", drift, s940DriftTolerance)
			}
		})
	}
}

// ── 5. 机制负控：裸墙钟必然漂移 ───────────────────────────────────

// TestS940NegativeControl_NaiveColumnDrifts 负控：**证明机制**。
//
// ⚠ 这条用例**明知它绿**（它测的是缺陷本身，不是被测代码）⇒ 它的作用是
// 让「往返精确」那条判据**有解释力**：如果哪天有人把列改回裸墙钟，
// 本条会显示出偏一个时区差 ⇒ ⇒ 读者立刻知道那条判据为什么红。
//
// ⚠ 用的是**另一个表**（notes_naive_probe），不动 notes 本表 ⇒ ⇒
// 本条不会污染被测对象。
func TestS940NegativeControl_NaiveColumnDrifts(t *testing.T) {
	pool := getTestPool(t)
	ctx := context.Background()

	var tz string
	if err := pool.QueryRow(ctx, "SHOW TimeZone").Scan(&tz); err != nil {
		t.Fatalf("SHOW TimeZone: %v", err)
	}
	// 取会话时区的当前 UTC 偏移（秒）。
	var offsetSec int64
	if err := pool.QueryRow(ctx,
		"SELECT EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')))::bigint",
	).Scan(&offsetSec); err != nil {
		t.Fatalf("read session tz offset: %v", err)
	}

	epoch := time.Now().Unix()
	epoch -= epoch % 60
	if _, err := pool.Exec(ctx, `
		CREATE TABLE notes_naive_probe (
			id TEXT PRIMARY KEY,
			updated_at TIMESTAMP WITHOUT TIME ZONE)`); err != nil {
		t.Fatalf("create naive probe: %v", err)
	}
	// 旧写法：instant 按会话时区渲染成墙钟再存。
	if _, err := pool.Exec(ctx,
		"INSERT INTO notes_naive_probe (id, updated_at) VALUES ($1, to_timestamp($2) AT TIME ZONE current_setting('TimeZone'))",
		"probe", epoch); err != nil {
		t.Fatalf("insert naive probe: %v", err)
	}
	var readBack int64
	if err := pool.QueryRow(ctx,
		"SELECT EXTRACT(EPOCH FROM updated_at)::bigint FROM notes_naive_probe WHERE id = $1",
		"probe").Scan(&readBack); err != nil {
		t.Fatalf("read naive probe: %v", err)
	}

	if readBack-epoch != offsetSec {
		t.Errorf("负控本身坏了：裸墙钟的偏差应为会话时区偏移 %+d 秒，实测 %+d 秒"+
			"（会话时区 %s）", offsetSec, readBack-epoch, tz)
	}
	if offsetSec == 0 {
		t.Logf("注意：本机会话时区是 UTC ⇒ 裸墙钟的偏差恰为 0 ⇒ " +
			"这条负控在本机不具区分力（往返精确那条判据在本机也抓不到缺陷）")
	}
}

// ── 6. 启动不得重写 notes 表 ───────────────────────────────────────

// TestS940StoreCreationDoesNotRewriteNotesTable 第二次创建 store 不得
// **重写** notes 表（relfilenode 必须不变）。
//
// ⚠⚠ 这条判据量的是**成本**，不是精度 —— 它是本节里唯一一条「不查时间戳」的
// 判据，但它守着属主要求的另一半（「不增加大量存储或资源」）。
//
// ⚠ 它的由来是 §940 的一次**自我推翻**：
//
//	我原本给 advisory lock 写的理由是「类型转换不是幂等的 ⇒ 双实例重复转换
//	⇒ 数据被静默平移」⇒ ⇒ **实测否掉了**：对已是 timestamptz 的列再转一次，
//	epoch 一字不变（换会话时区也不变）⇒ ⇒⇒ **值上是幂等的**。
//	⇒ ⇒ 但同一批实测又量出另一半：**每一次 ALTER COLUMN ... TYPE 都会重写
//	整张表**（relfilenode 变）⇒ ⇒⇒ ACCESS EXCLUSIVE + 全表拷贝 + WAL
//	⇒ ⇒⇒ 成本 ∝ 表大小。
//
// ⇒ ⇒⇒ 所以「按列类型决定转不转」这一步的真实价值 = **每次进程启动对
// notes 表零 DDL**（既不重写表，也不拿 ACCESS EXCLUSIVE）。
// ⇒ ⇒⇒ 而这条判据就是那句话的**可执行**形式：把「按类型跳过」拆掉，
// 第二次 NewStore 就会再重写一次 ⇒ ⇒ relfilenode 变 ⇒ ⇒ 判据立刻红。
//
// ⚠ 为什么要先造**旧表**：新库（DDL 直接声明 TIMESTAMPTZ）压根不会触发
// 转换 ⇒ ⇒ 在新库上这条判据**恒绿** ⇒ ⇒ 测不出任何东西。
func TestS940StoreCreationDoesNotRewriteNotesTable(t *testing.T) {
	pool := getTestPool(t)
	ctx := context.Background()
	createLegacyNotesTable(t, pool)

	// 第 1 次创建：会做类型转换 ⇒ 预期**会**重写一次（这是必要的、也是允许的）。
	if _, err := NewStore(pool); err != nil {
		t.Fatalf("NewStore（第 1 次）: %v", err)
	}
	before := s940RelFileNode(t, ctx, pool)
	if before == 0 {
		t.Fatalf("读不到 notes 的 relfilenode（表没建起来？夹具错了，不是被测代码的问题）")
	}

	// 第 2 次创建：列已经是 timestamptz ⇒ 必须**零 DDL**。
	if _, err := NewStore(pool); err != nil {
		t.Fatalf("NewStore（第 2 次）: %v", err)
	}
	after := s940RelFileNode(t, ctx, pool)
	if after != before {
		t.Errorf("第 2 次创建 store 又重写了 notes 表（relfilenode %d → %d）—— "+
			"「按列类型决定转不转」失效 ⇒ ⇒ 每次进程启动都会 ACCESS EXCLUSIVE + "+
			"全表拷贝 + WAL ⇒ ⇒ 成本 ∝ 表大小 × 启动次数（而不是 ∝ 一次）",
			before, after)
	}
}

// ── 7. 「这一列要不要转」这个判定本身 ─────────────────────────────────

// TestS940ColumnTypeDecision 「按列类型决定转不转」这个判定的**纯函数**判据。
//
// ⚠⚠ 为什么它**不需要 PG**：这一段是「值上幂等 / 成本上不幂等」那整个结论的
// **支点** ⇒ ⇒ 如果它只能靠真 PG 才验得了 ⇒ ⇒ ⇒ CI 那个**没有 PG** 的
// backend job 里，这段逻辑就是**空气门** ⇒ ⇒ ⇒ 抽成纯函数就是为了让它
// 在**任何**机器上都有门。
//
// ⚠ 第四条断言最要紧：PG 的 `format_type` 输出的是 **"timestamp with time zone"**
// （实测），**不是** "timestamptz" ⇒ ⇒⇒ 所以 "timestamptz" 这个拼法必须落进
// 「**不猜、直接报错**」分支 ⇒ ⇒⇒ 这条断言是变异 **M1** 的地基。
func TestS940ColumnTypeDecision(t *testing.T) {
	if needs, err := notesColumnNeedsConversion("timestamp with time zone"); err != nil || needs {
		t.Errorf("已经是绝对时刻的列应判「不需要转换」，实测 needs=%v err=%v", needs, err)
	}
	if needs, err := notesColumnNeedsConversion("timestamp without time zone"); err != nil || !needs {
		t.Errorf("裸墙钟的列应判「需要转换」，实测 needs=%v err=%v", needs, err)
	}
	if _, err := notesColumnNeedsConversion("timestamp"); err == nil {
		t.Errorf("别的类型必须**报错**（不猜），实测 err=nil")
	}
	if _, err := notesColumnNeedsConversion("timestamptz"); err == nil {
		t.Errorf("\"timestamptz\" 不是 PG format_type 的输出形态（实测是 " +
			"\"timestamp with time zone\"）⇒ ⇒ 必须落进「报错」分支，实测 err=nil")
	}
}
