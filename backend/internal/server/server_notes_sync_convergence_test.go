package server

// §940 判据：笔记的**增量同步必须收敛**。
//
// ⚠⚠ 这是 §940 的 D1 在**用户可见层面**的形态：
//
// `GET /api/notes?since=X` 的契约是「只返回 updated_at 晚于 X 的行」
// （docs/2026-09-09-list-sync-rules.md §3.2）。客户端拿响应里的
// `serverTimeMs`（或自己 max(updated_at)）当下一轮的 `since`
// ⇒ ⇒ **拉完一轮之后，第二轮必须是空的** —— 这才叫「增量」。
//
// ⇒ ⇒⇒ 修复前**不成立**：notes 表的时间列是 `timestamp without time zone`
// （裸墙钟）⇒ ⇒ 写入按会话时区渲染、读回按 UTC 解释 ⇒ ⇒ **往返偏一个
// 会话时区差**（实测 Asia/Shanghai ⇒ **+8 小时**）⇒ ⇒ 笔记的 updated_at
// 一律落在**未来 8 小时** ⇒ ⇒ `since` 过滤**永远命中** ⇒ ⇒⇒
// **每一轮轮询都把同一批改动重推一遍** ⇒ ⇒⇒ 增量**永不收敛**。
//
// ⇒ ⇒⇒ 后果直接违背属主要求（「成本只取决于存储/新增量」）：
// 一次轮询的成本 ∝ **最近 8 小时的改动量**，而不是 ∝ **本轮的增量**。
//
// ⚠ 本判据量的是**端到端行为**（HTTP 响应里的 notes 条数），不是源码形状；
// 也**不是**时间戳的数值 ⇒ ⇒ 时间戳那一侧由 notes 包的
// store_timestamp_test.go 负责（§940 的 D1 判据在那边）。
//
// ⚠ 需要真 PG：notes.Store 只有 PostgreSQL 一个实现 ⇒ 没有内存版可测。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/jackc/pgx/v5/pgxpool"
)

// s940DowngradeToNaive 把 notes 的三个时间列退回「裸墙钟」形态，
// 用来复现**存量库**（生产上那张表就是这个样子：裸墙钟列 + 墙钟值）。
//
// ⚠⚠ **为什么用「降级」而不是「自己造一张旧表」**（★ §80 的形状）：
//
//	旧表的 DDL 只有 **notes 包**知道 ⇒ ⇒ 在 server 包里抄一份就是
//	⇒ ⇒「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」⇒ ⇒⇒ 而降级用的是
//	⇒ ⇒⇒ **同一张表**、**一行 DDL 都不抄** ⇒ ⇒⇒ 降级后的状态与生产旧表
//	⇒ ⇒⇒ **等价**。
//
// ⚠ 降级用 `ALTER ... TYPE timestamp`（**不带 USING**）⇒ ⇒ PG 按**会话时区**
// ⇒ ⇒⇒ 把 instant **渲染成墙钟**存进去 ⇒ ⇒⇒ 正是旧代码写进去的那种值。
func s940DowngradeToNaive(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, col := range []string{"created_at", "updated_at", "deleted_at"} {
		if _, err := pool.Exec(ctx,
			"ALTER TABLE notes ALTER COLUMN "+col+" TYPE timestamp"); err != nil {
			t.Fatalf("downgrade %s: %v", col, err)
		}
	}
}

// s940ServerFixture 造一个「服务端 + notes store」的夹具。
//
// ⚠ legacy=true ⇒ 先把三列**降级**成裸墙钟 ⇒ ⇒ 再建一次 store ⇒ ⇒
// ⇒ ⇒ `migrate()` 的 ALTER **真的发生** ⇒ ⇒ ⇒ 而 server 包里那份「新库」
// ⇒ ⇒⇒ **测的是 DDL 路径**，不是转换路径。
func s940ServerFixture(t *testing.T, legacy bool) (*Server, string) {
	t.Helper()
	pool := mustTestPool(t)
	store, err := notes.NewStore(pool)
	if err != nil {
		t.Fatalf("notes.NewStore: %v", err)
	}
	if legacy {
		s940DowngradeToNaive(t, pool)
		store, err = notes.NewStore(pool) // 这一次 migrate() 会做类型转换
		if err != nil {
			t.Fatalf("notes.NewStore（降级后）: %v", err)
		}
	}
	srv, token := newTestServerWithAuth(t)
	srv.notesStore = store
	return srv, token
}

// s940NotesEnvelope 是 GET /api/notes 的响应信封。
type s940NotesEnvelope struct {
	Notes        []notes.Note `json:"notes"`
	ServerTimeMs int64        `json:"serverTimeMs"`
	DeletedIds   []string     `json:"deletedIds"`
}

func s940GetNotes(t *testing.T, srv *Server, token, target string) s940NotesEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, rr.Code, rr.Body.String())
	}
	var env s940NotesEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET %s: %v body=%q", target, err, rr.Body.String())
	}
	return env
}

// TestS940NotesIncrementalSyncConverges 拉完一轮之后，第二轮增量必须是空的。
//
// ⚠ 为什么这条是**端到端**的：它是客户端真正做的事（拿 serverTimeMs 当
// 下一轮的 since）。只测 store 的时间戳不够 —— 时间戳对了但 handler 的
// since 口径错了，同样不收敛。
//
// ⚠⚠ **两种夹具各跑一遍**（新库 / 存量库）：新库那一臂测的是 **DDL 路径**，
// 存量库那一臂才真的在测「**转换**做对没有」⇒ ⇒ ⇒ 而生产上的主形态是
// **存量库** ⇒ ⇒ ⇒ 只跑新库 ⇒ ⇒ ⇒ 「转换整个坏掉」时这条**仍然绿**
// ⇒ ⇒ ⇒ ⇒ 那个绿是**假的**（★ 本轮由变异 M5 暴露出来）。
func TestS940NotesIncrementalSyncConverges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
	}{
		{"新库（DDL 直接建表 ⇒ 不触发转换）", false},
		{"存量库（裸墙钟列 ⇒ 靠 ALTER 转换）", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testS940NotesIncrementalSyncConverges(t, tc.legacy)
		})
	}
}

func testS940NotesIncrementalSyncConverges(t *testing.T, legacy bool) {
	srv, token := s940ServerFixture(t, legacy)
	ctx := context.Background()
	store := srv.notesStore

	// ⚠ 三条笔记的 updated_at 都在**过去**（不是 now）⇒ ⇒ 第一轮全量能拿到它们；
	// 而第二轮的 since = serverTimeMs ≈ now ⇒ ⇒ 正确实现下必然为空。
	base := time.Now().Unix() - 10
	for i, ts := range []int64{base, base - 60, base - 120} {
		if err := store.Upsert(ctx, &notes.Note{
			ID:          fmt.Sprintf("s940_sync_%d", i),
			UserID:      "test-user",
			WorkspaceID: "test-workspace",
			Title:       "s940",
			Snippet:     "s",
			ContentType: "text",
			CreatedAt:   ts,
			UpdatedAt:   ts,
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	first := s940GetNotes(t, srv, token, "/api/notes")
	if len(first.Notes) != 3 {
		t.Fatalf("第 1 轮全量应返回 3 条，实际 %d 条 —— 夹具没造对，不是被测代码的问题",
			len(first.Notes))
	}
	if first.ServerTimeMs <= 0 {
		t.Fatalf("响应里没有 serverTimeMs：%+v", first)
	}

	// 第二轮：客户端拿 serverTimeMs 当 since（文档 §3.2 的标准做法）。
	second := s940GetNotes(t, srv, token, fmt.Sprintf("/api/notes?since=%d", first.ServerTimeMs))
	if len(second.Notes) != 0 {
		t.Errorf("增量没有收敛：第 2 轮（since=serverTimeMs=%d）又返回了 %d 条 —— "+
			"同一批改动被反复推送 ⇒ 一次轮询的成本 ∝ 最近一个时间窗的改动量，"+
			"而不是 ∝ 本轮增量", first.ServerTimeMs, len(second.Notes))
		for _, n := range second.Notes {
			t.Errorf("  被重复推送：id=%s updatedAt=%d（serverTimeMs=%d ⇒ 偏 %+d 秒）",
				n.ID, n.UpdatedAt, first.ServerTimeMs, n.UpdatedAt*1000-first.ServerTimeMs)
		}
	}
}

// TestS940NotesIncrementalSyncConvergesWithMaxWatermark 客户端改用
// 「本地 max(updated_at)」当 since（文档 §4 的另一种做法）⇒ ⇒ 也必须收敛。
//
// ⚠ 两种水位线口径都要钉：客户端用哪一种是**仓外**的选择（移动端在仓外）
// ⇒ ⇒ 服务端不能只对其中一种成立。
//
// ⚠ 这条**只跑新库夹具** ⇒ ⇒ 因为它量的是「**另一种水位线**的语义」
// ⇒ ⇒⇒ 转换路径的覆盖由上一条（两种夹具都跑）承担 ⇒ ⇒⇒⇒ 不必在这里
// ⇒ ⇒⇒ 再重复一次同样的覆盖。
func TestS940NotesIncrementalSyncConvergesWithMaxWatermark(t *testing.T) {
	srv, token := s940ServerFixture(t, false)
	ctx := context.Background()
	store := srv.notesStore

	base := time.Now().Unix() - 10
	for i, ts := range []int64{base, base - 60, base - 120} {
		if err := store.Upsert(ctx, &notes.Note{
			ID:          fmt.Sprintf("s940_max_%d", i),
			UserID:      "test-user",
			WorkspaceID: "test-workspace",
			Title:       "s940",
			Snippet:     "s",
			ContentType: "text",
			CreatedAt:   ts,
			UpdatedAt:   ts,
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	first := s940GetNotes(t, srv, token, "/api/notes")
	if len(first.Notes) != 3 {
		t.Fatalf("第 1 轮全量应返回 3 条，实际 %d 条", len(first.Notes))
	}
	var watermark int64
	for _, n := range first.Notes {
		if n.UpdatedAt > watermark {
			watermark = n.UpdatedAt
		}
	}

	second := s940GetNotes(t, srv, token, fmt.Sprintf("/api/notes?since=%d", watermark))
	if len(second.Notes) != 0 {
		t.Errorf("增量没有收敛：第 2 轮（since=max(updated_at)=%d）又返回了 %d 条",
			watermark, len(second.Notes))
	}
}
