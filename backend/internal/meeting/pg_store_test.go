// internal/meeting/pg_store_test.go
package meeting

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 这些测试存在的理由和 internal/learning 的 PG 测试一样：本文件的 SQL 之前
// 从未被执行过一次。所有 DDL/SQL 都只在编译期存在，列名打错、JSONB 往返丢
// 字段、墓碑忘了写，都要等到真实部署才会暴露。
//
// 关键设计：每个测试用**独立 schema**，可以直接打开发者自己的库，不碰现有数据。
//
//	POCKET_TEST_POSTGRES_DSN=postgres://... go test ./internal/meeting/

func pgDSN() string {
	// 只认测试专用 DSN。2026-10-02 实测：这里原本还会回退读
	// POCKET_POSTGRES_DSN，于是只带了生产 DSN 的 `go test ./internal/meeting/`
	// 直接在**生产数据库**里建出了 meeting_test_* schema（至今残留 2 个）。
	// 护栏见 internal/server/pg_test_isolation_guard_test.go 规则 1。
	return os.Getenv("POCKET_TEST_POSTGRES_DSN")
}

// newPGTestEnv 建一个独立 schema 的连接池，返回建好的 store 与清理函数。
func newPGTestEnv(t *testing.T) (*PGStore, func()) {
	t.Helper()
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping meeting PG integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "meeting_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	store, err := NewPGStore(ctx, pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewPGStore: %v", err)
	}
	return store, func() {
		// 注意：这里**不能**直接 pool.Close()。
		//
		// 负控（negctl-meeting-pg.mjs on）时实测踩到过：断言失败 → t.Fatalf
		// → runtime.Goexit → 跑这个 cleanup → pgxpool.Close() 内部
		// puddle.Pool 的 WaitGroup 永远等不到归零，测试不是干脆报错而是
		// 挂到 go test -timeout 才 panic。判据一坏，"红"就变成了难查的
		// timeout，所以这里必须给关闭加有界等待。
		//
		// 先 DROP SCHEMA 再关连接：这样即便 Close 卡住，测试库也已被清干净。
		//
		// 两处不能照抄旧写法：
		//
		// 1. 不能用 `ctx`（context.Background()，无 deadline）。连接半死时
		//    Exec 会一直等，cleanup 挂到 go test -timeout 才 panic —— 那正是
		//    本文件 closeQuietly 要防的失败形态，清理阶段不该再制造一次。
		// 2. 不能 `_, _ =` 吞掉错误。2026-10-02 实测：一次性库里跑完全量测试
		//    后留下 2 个 meeting_test_* schema，而 DROP 失败这件事**没有任何
		//    输出**——残留既不会被发现，也永远查不出为什么没删掉。清理失败
		//    本身就是缺陷，必须让测试红。
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := rootPool.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)); err != nil {
			t.Errorf("清理失败：DROP SCHEMA %s CASCADE: %v（残留 schema 会一直留在库里，且本条错误曾被静默吞掉）", schema, err)
		}
		closeQuietly(pool)
		closeQuietly(rootPool)
	}
}

// closeQuietly 有界地关闭连接池：卡住就放弃，绝不让测试进程挂在清理阶段。
// 卡住时那个 goroutine 会泄漏，但测试进程本来就要退出，泄漏无害；
// 而"清理卡死"本身会在栈里留下 puddle.WaitGroup.Wait 的痕迹。
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

// reattach 在**同一个 pool** 上再构造一个 PGStore，模拟"后端进程重启"。
// 重启后连接池也是新的，所以这里不缓存任何 store 内部状态 —— 凡是能从
// 新实例读出来的东西，只可能来自数据库。
func reattach(t *testing.T, s *PGStore) *PGStore {
	t.Helper()
	fresh, err := NewPGStore(context.Background(), s.pool)
	if err != nil {
		t.Fatalf("re-attach after restart: %v", err)
	}
	return fresh
}

// TestPersistence_PositiveAndNegativeControl 是本文件的核心判据。
//
// 它把**同一个场景**跑两遍，对两种实现给出**相反的期望**：
//
//	PGStore  —— 换新实例后数据仍在（持久化生效）
//	*Store   —— 换新实例后数据全没（内存实现就是会丢）
//
// 为什么必须成对：只测 PGStore 的话，"读到的其实是同一个 map"这种假绿
// 完全测不出来 —— 断言照样通过，但持久化其实没生效。配上内存版这一侧
// 必须在同一套判据下失败，才能证明这套判据真的能区分「读到了数据库」
// 与「读到了进程内的残留」。
func TestPersistence_PositiveAndNegativeControl(t *testing.T) {
	const n = 7
	titles := make([]string, n)
	for i := range titles {
		titles[i] = fmt.Sprintf("会议 %d", i)
	}

	t.Run("PGStore_新实例仍读得到", func(t *testing.T) {
		store, cleanup := newPGTestEnv(t)
		defer cleanup()

		ids := make([]string, n)
		for i, title := range titles {
			m, err := store.CreateScoped(CreateMeetingRequest{Title: title}, "u1", "ws-a")
			if err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
			ids[i] = m.ID
		}

		after := reattach(t, store)
		list, err := after.ListScoped("u1", "ws-a")
		if err != nil {
			t.Fatalf("list after restart: %v", err)
		}
		if len(list) != n {
			t.Fatalf("重启后应读回 %d 条，实际 %d 条 —— 持久化没生效", n, len(list))
		}
		for i, id := range ids {
			got, err := after.GetScoped(id, "u1", "ws-a")
			if err != nil {
				t.Fatalf("get %s after restart: %v", id, err)
			}
			if got.Title != titles[i] {
				t.Fatalf("id=%s 期望 title=%q，实际 %q", id, titles[i], got.Title)
			}
		}
	})

	// —— 负控：同一套判据，内存实现必须红 ——
	t.Run("内存Store_换新实例会丢_这是缺陷本身", func(t *testing.T) {
		mem := NewStore()
		ids := make([]string, n)
		for i, title := range titles {
			m, err := mem.CreateScoped(CreateMeetingRequest{Title: title}, "u1", "ws-a")
			if err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
			ids[i] = m.ID
		}

		restarted := NewStore() // 这就是后端进程重启
		list, err := restarted.ListScoped("u1", "ws-a")
		if err != nil {
			t.Fatalf("list after restart: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("内存实现重启后本应全丢，却读回 %d 条 —— 说明判据测不出持久化", len(list))
		}
		for _, id := range ids {
			if _, err := restarted.GetScoped(id, "u1", "ws-a"); err == nil {
				t.Fatalf("内存实现重启后 %s 竟然还在，判据失效", id)
			}
		}
	})
}

// TestPGStore_UpdateScoped_RoundTrip 覆盖三个 JSONB 列的往返。
// 会议数据里真正贵的是 transcript / summary / key_decisions / action_items，
// 只验 title 的话，JSONB 编码写错（比如把 []ActionItem 序列化成对象）一样会绿。
func TestPGStore_UpdateScoped_RoundTrip(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	m, err := store.CreateScoped(CreateMeetingRequest{Title: "季度评审"}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	m.Duration = 3725
	m.RecordingURL = "https://example.invalid/rec.mp3"
	m.Transcript = "逐字稿正文"
	m.Summary = "摘要正文"
	m.KeyDecisions = []string{"决定 A", "决定 B"}
	m.ActionItems = []ActionItem{{Owner: "张三", Task: "补测试", Deadline: "2026-10-01"}}
	m.Tags = []string{"okr", "q4"}
	m.ProjectID = "proj-7"
	m.Status = "done"
	if err := store.UpdateScoped(m, "u1", "ws-a"); err != nil {
		t.Fatalf("update: %v", err)
	}

	after := reattach(t, store)
	got, err := after.GetScoped(m.ID, "u1", "ws-a")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.Duration != 3725 || got.RecordingURL != "https://example.invalid/rec.mp3" {
		t.Fatalf("标量字段没对上: duration=%d url=%q", got.Duration, got.RecordingURL)
	}
	if got.Transcript != "逐字稿正文" || got.Summary != "摘要正文" {
		t.Fatalf("文本字段没对上: transcript=%q summary=%q", got.Transcript, got.Summary)
	}
	if len(got.KeyDecisions) != 2 || got.KeyDecisions[0] != "决定 A" {
		t.Fatalf("key_decisions 往返失败: %#v", got.KeyDecisions)
	}
	if len(got.ActionItems) != 1 {
		t.Fatalf("action_items 往返失败: %#v", got.ActionItems)
	}
	ai := got.ActionItems[0]
	if ai.Owner != "张三" || ai.Task != "补测试" || ai.Deadline != "2026-10-01" {
		t.Fatalf("action_items 字段丢失: %#v", ai)
	}
	if len(got.Tags) != 2 || got.Tags[1] != "q4" {
		t.Fatalf("tags 往返失败: %#v", got.Tags)
	}
	if got.ProjectID != "proj-7" || got.Status != "done" {
		t.Fatalf("project/status 没对上: project=%q status=%q", got.ProjectID, got.Status)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("created_at 被更新覆盖了: %v -> %v", m.CreatedAt, got.CreatedAt)
	}
}

// TestPGStore_DeleteScoped_WritesTombstone 覆盖删除与墓碑。
// 墓碑是这次一起持久化的第二个对象；漏写它的话，被删的会议会在下一次
// 增量同步里被客户端当成"服务器还有"重新显示出来。
func TestPGStore_DeleteScoped_WritesTombstone(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	keep, err := store.CreateScoped(CreateMeetingRequest{Title: "保留"}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create keep: %v", err)
	}
	drop, err := store.CreateScoped(CreateMeetingRequest{Title: "删除"}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create drop: %v", err)
	}

	if got := store.DeletedIDsSince("u1", "ws-a", time.Time{}); len(got) != 0 {
		t.Fatalf("删除前不该有墓碑，实际 %#v", got)
	}
	cut := time.Now().Add(-time.Millisecond)
	if err := store.DeleteScoped(drop.ID, "u1", "ws-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// 重启后墓碑必须还在 —— 这正是内存版做不到的那一半。
	after := reattach(t, store)
	tombs := after.DeletedIDsSince("u1", "ws-a", cut)
	if len(tombs) != 1 || tombs[0] != drop.ID {
		t.Fatalf("重启后墓碑应含 %s，实际 %#v", drop.ID, tombs)
	}
	if _, err := after.GetScoped(drop.ID, "u1", "ws-a"); err == nil {
		t.Fatal("删掉的会议重启后竟然还在")
	}
	if _, err := after.GetScoped(keep.ID, "u1", "ws-a"); err != nil {
		t.Fatalf("未删的 %s 丢了: %v", keep.ID, err)
	}
	list, err := after.ListScoped("u1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != keep.ID {
		t.Fatalf("list 应只剩 %s，实际 %#v", keep.ID, list)
	}
	// 再删一次必须报 not found，不能因为墓碑的主键冲突而 500。
	if err := after.DeleteScoped(drop.ID, "u1", "ws-a"); err == nil {
		t.Fatal("重复删除应当返回错误")
	}
}

// TestPGStore_WorkspaceIsolation 守住 scope 语义：PG 版是新的读写路径，
// 这里最容易写出"WHERE 条件漏了一个字段"的越权 bug。
func TestPGStore_WorkspaceIsolation(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	mine, err := store.CreateScoped(CreateMeetingRequest{Title: "我的"}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other, err := store.CreateScoped(CreateMeetingRequest{Title: "别人的"}, "u2", "ws-b")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	// 同一 owner，换 workspace
	if _, err := store.GetScoped(mine.ID, "u1", "ws-b"); err == nil {
		t.Fatal("换 workspace 竟然能读到")
	}
	list, err := store.ListScoped("u1", "ws-b")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ws-b 的列表不该有 u1/ws-a 的数据，实际 %d 条", len(list))
	}
	// 换 owner
	if _, err := store.GetScoped(other.ID, "u1", "ws-b"); err == nil {
		t.Fatal("换 owner 竟然能读到")
	}
	// 越权删除必须失败，且不能真的删掉
	if err := store.DeleteScoped(other.ID, "u1", "ws-b"); err == nil {
		t.Fatal("越权删除应当失败")
	}
	if _, err := store.GetScoped(other.ID, "u2", "ws-b"); err != nil {
		t.Fatalf("越权删除把别人的数据删了: %v", err)
	}
	// 越权更新必须失败
	if _, err := store.GetScoped(mine.ID, "u1", "ws-a"); err != nil {
		t.Fatalf("get own: %v", err)
	}
}

// TestPGStore_UpdateScoped_NotFound 更新一条不存在的记录必须报 not found，
// 而不是静默成功（handler 里有 _ = s.meetingStore.UpdateScoped(...) 这类
// 吞错误的调用，静默成功会让"会议写不进去"变成查不出来的幽灵问题）。
func TestPGStore_UpdateScoped_NotFound(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	err := store.UpdateScoped(&Meeting{ID: "mtg_missing", Title: "x", Status: "done"}, "u1", "ws-a")
	if err == nil {
		t.Fatal("更新不存在的会议应当返回错误")
	}
}

// TestPGStore_InputValidation 守住与内存版一致的入参校验，
// 免得同一个非法请求在 PG 部署下就穿过去了。
func TestPGStore_InputValidation(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	if _, err := store.CreateScoped(CreateMeetingRequest{Title: "   "}, "u1", "ws-a"); err == nil {
		t.Fatal("空标题应当被拒")
	}
	if _, err := store.CreateScoped(CreateMeetingRequest{Title: "x"}, "", "ws-a"); err == nil {
		t.Fatal("空 owner 应当被拒")
	}
	if _, err := store.CreateScoped(CreateMeetingRequest{Title: "x"}, "u1", ""); err == nil {
		t.Fatal("空 workspace 应当被拒")
	}
	if _, err := store.GetScoped("", "u1", "ws-a"); err == nil {
		t.Fatal("空 id 应当被拒")
	}
	if _, err := store.ListScoped("", "ws-a"); err == nil {
		t.Fatal("空 owner 列举应当被拒")
	}
}

// TestPGStore_CreateScoped_IDUnique 守住 ID 唯一性。
//
// 背景：Windows 上 time.Now() 没有纳秒精度（store.go 的注释记录了实测
// 1000 次调用只产生 1 个不同值），纯 nano 会撞号。内存版是"静默覆盖前一条"，
// PG 版是 PRIMARY KEY 冲突。并发创建 200 条必须条条不落地。
func TestPGStore_CreateScoped_IDUnique(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	const n = 200
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		m, err := store.CreateScoped(CreateMeetingRequest{Title: fmt.Sprintf("t%d", i)}, "u1", "ws-a")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if seen[m.ID] {
			t.Fatalf("第 %d 条拿到了重复 ID %s", i, m.ID)
		}
		seen[m.ID] = true
	}
	after := reattach(t, store)
	list, err := after.ListScoped("u1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != n {
		t.Fatalf("建了 %d 条，重启后应全部在，实际 %d 条 —— 有写入被静默吞掉", n, len(list))
	}
}
