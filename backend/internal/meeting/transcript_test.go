// internal/meeting/transcript_test.go —— §937：转写**尾读**与**定向更新**的判据。
//
// 本文件守的是**两件**此前没人守过的事：
//
//	D1（读放大）：/api/meetings/{id}/analyze 每轮轮询都把**整份转写**从库里
//	  搬进进程 ⇒ 每轮搬运量 ∝ 全文 ⇒ 累计 ∝ 时长²。
//	  ⇒ 尾读的口径必须钉死：**按字符**切、越界返回空串、两个 store 逐字一致。
//
//	D2（写放大 + 丢失更新窗口）：只改 title/status 的写入点原先走
//	  「读整行 + 写整行」⇒ 每改一次标题就把**整份转写**重写一遍；
//	  而且「读-改-写整行」会把并发写入的新转写覆盖回旧值。
//	  ⇒ 定向更新的 SQL 里**绝不出现 transcript**（形状守卫），
//	  且它**不覆盖**并发写入的转写。
//
// ⚠ 本文件分两类用例，**能力不同，不能互相顶替**：
//
//	① 无条件跑的（口径 / SQL 文本守卫 / 内存版行为）—— 任何环境都跑；
//	② 需要真 PG 的（跨 store 逐字一致 / DB 侧字节数）—— 无 PG 时 **skip**。
//	   ⇒ ⇒ 「无 PG 时这些用例被跳过」这件事必须**如实登记**，不能当成「测过了」。
package meeting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ── ① 无条件跑的用例 ───────────────────────────────────────────────────

// TestTranscriptTail 钉住尾读的口径。
//
// ⚠ 为什么要**逐个切点**钉：尾读是滚动分析的地基（游标就是它给的）⇒
//
//	切错一个字符 ⇒ 新增段里多/少一个字 ⇒ 上游看到的上下文就不对。
//	而「切点落在字符中间」这件事在中文上**不会报错**，只会静默产出乱码。
// pgTimeEqual 判断两个时刻在 **PG 的微秒精度**下是否相等。
//
// ⚠ PG 的 timestamptz 只存**微秒**，而 Go 的 time.Time 是**纳秒**
//   ⇒ 「内存里的原值」与「PG 往返后的值」永远差几百纳秒
//   ⇒ 直接 Equal 在 ubuntu CI 上恒红（实测差 1.46ns / 999ns / 7198ns）。
//   ⇒ 容差取 1 微秒 = **PG 的精度单位**（不是「随便放宽一点」）。
func pgTimeEqual(a, b time.Time) bool {
	d := a.Sub(b)
	return d <= time.Microsecond && d >= -time.Microsecond
}

// TestPGTimeEqualTolerance 钉住上面那个容差口径。
//
// ★ **不需要 PG** ⇒ 本机就能验（PG 路径的那两条用例本机是 skip 的，
//   而 ubuntu CI 上它们红的就是这个精度差 ⇒ 不抽成可单测的纯函数，
//   这条缺陷在本机**永远无法复现、也无法验证修好了**）。
func TestPGTimeEqualTolerance(t *testing.T) {
	base := time.Date(2026, 10, 21, 19, 14, 55, 346017826, time.UTC)
	// ① PG 截断到微秒后必须判相等。
	if !pgTimeEqual(base.Truncate(time.Microsecond), base) {
		t.Errorf("PG 截断后的微秒值必须与纳秒原值判等（差 %v）", base.Truncate(time.Microsecond).Sub(base))
	}
	// ② PG 四舍五入到微秒后也必须判相等（两种实现都覆盖）。
	if !pgTimeEqual(base.Round(time.Microsecond), base) {
		t.Errorf("PG 四舍五入后的微秒值必须与纳秒原值判等（差 %v）", base.Round(time.Microsecond).Sub(base))
	}
	// ③ 负控：真的差 1 毫秒 ⇒ 必须判**不等**（否则容差把牙拆了）。
	if pgTimeEqual(base.Add(time.Millisecond), base) {
		t.Error("负控：差 1 毫秒必须判不等 —— 容差被放得太大")
	}
	// ④ 负控：负方向同样。
	if pgTimeEqual(base.Add(-time.Millisecond), base) {
		t.Error("负控：负方向差 1 毫秒必须判不等")
	}
}

func TestTranscriptTail(t *testing.T) {
	// ⚠ 语料刻意混了中文（3 字节）、ASCII 与 emoji（4 字节）⇒
	//   按字节切与按字符切在这里给出**不同**的结果 ⇒ 判据才有牙。
	const text = "张三: 讨论二季度预算\n李四: 排班系统下周定 🎯"

	for _, tc := range []struct {
		name string
		from int
	}{
		{"负游标当 0", -5},
		{"0 是整份", 0},
		{"1 去掉首字", 1},
		{"等于长度是空", len([]rune(text))},
		{"超过长度是空", len([]rune(text)) + 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail, total := TranscriptTail(text, tc.from)
			wantFrom := tc.from
			if wantFrom < 0 {
				wantFrom = 0
			}
			runes := []rune(text)
			want := ""
			if wantFrom < len(runes) {
				want = string(runes[wantFrom:])
			}
			if tail != want {
				t.Fatalf("TranscriptTail(from=%d) = %q, want %q", tc.from, tail, want)
			}
			if total != len(runes) {
				t.Fatalf("TranscriptTail(from=%d) total = %d, want %d", tc.from, total, len(runes))
			}
			if !utf8.ValidString(tail) {
				t.Fatalf("尾读切出了非法 UTF-8：%q", tail)
			}
		})
	}

	// ⭐ 逐个切点全检一遍（不是抽样）：任何一个切点切坏 ⇒ 立刻红。
	runes := []rune(text)
	for i := 0; i <= len(runes); i++ {
		tail, total := TranscriptTail(text, i)
		if total != len(runes) {
			t.Fatalf("切点 %d 的 total = %d, want %d", i, total, len(runes))
		}
		if want := string(runes[i:]); tail != want {
			t.Fatalf("切点 %d 切错：got %q, want %q", i, tail, want)
		}
	}
}

// TestBuildMetaUpdateSQLNeverTouchesTranscript 钉住「定向更新的 SQL 里
// 绝不出现 transcript」这条不变式（§937 的 D2）。
//
// ⚠ 为什么用**形状守卫**而不是行为断言：行为断言（「转写没被改」）在内存版上
//
//	天然成立（内存版的 map 赋值就是改那两列）⇒ ⇒ 它**看不见** PG 版那
//	「UPDATE … transcript=$7」整列重写 ⇒ ⇒ 必须直接钉 SQL 文本。
//
// ⚠ 判据对**排版**免疫：只查「有没有 transcript」与「SET 里有哪几列」，
//
//	不查空格与换行（§936 的 normalizeSQL 同一思路）。
func TestBuildMetaUpdateSQLNeverTouchesTranscript(t *testing.T) {
	now := mustTime(t)
	title := "新标题"
	status := "refined"

	for _, tc := range []struct {
		name      string
		title     *string
		status    *string
		wantCols  []string
		notWanted []string
	}{
		{"只改标题", &title, nil, []string{"title"}, []string{"status"}},
		{"只改状态", nil, &status, []string{"status"}, []string{"title"}},
		{"两个都改", &title, &status, []string{"title", "status"}, nil},
		{"都不改", nil, nil, []string{"updated_at"}, []string{"title", "status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, args := buildMetaUpdateSQL("mtg_1", "u1", "ws-a", tc.title, tc.status, now)
			norm := normalizeSQLText(q)
			if strings.Contains(norm, "transcript") {
				t.Fatalf("定向更新的 SQL 里出现了 transcript ⇒ 整份转写会被重写：\n%s", q)
			}
			for _, col := range tc.wantCols {
				if !strings.Contains(norm, col+"=") {
					t.Fatalf("SET 里缺 %s 列：\n%s", col, q)
				}
			}
			for _, col := range tc.notWanted {
				if strings.Contains(norm, col+"=") {
					t.Fatalf("SET 里出现了不该有的 %s 列：\n%s", col, q)
				}
			}
			// 作用域谓词必须齐全（少一条 ⇒ 能改到别人的会议）。
			for _, frag := range []string{"id=$1", "owner_id=$2", "workspace_id=$3"} {
				if !strings.Contains(norm, frag) {
					t.Fatalf("SQL 缺作用域谓词 %q：\n%s", frag, q)
				}
			}
			// 参数个数必须与占位符个数一致（少一个 ⇒ PG 报参数类型错）。
			if len(args) != strings.Count(norm, "$") {
				t.Fatalf("参数个数 %d 与占位符个数不一致：\n%s\nargs=%v", len(args), q, args)
			}
		})
	}
}

// TestUpdateMetaScopedLeavesTranscriptUntouched 钉住内存版的 D2 行为：
// 定向更新**不碰**转写，且**不覆盖**并发写入的新转写。
//
// ⚠ 第二条才是这条修法的**真正价值**：修复前的「读-改-写整行」会把
//
//	两步之间并发写入的新转写覆盖回旧值（lost update）⇒ ⇒ 一场长会议
//	正在被追加转写时，一次精校收尾就能把新转写**抹掉**。
func TestUpdateMetaScopedLeavesTranscriptUntouched(t *testing.T) {
	s := NewStore()
	m, err := s.CreateScoped(CreateMeetingRequest{
		Title:             "原标题",
		RefinedTranscript: "第一版转写",
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 并发写入：另一条路径把转写追加了。
	live, err := s.GetScoped(m.ID, "u1", "ws-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	live.Transcript = "第一版转写\n第二版（并发写入）"
	if err := s.UpdateScoped(live, "u1", "ws-a"); err != nil {
		t.Fatalf("append: %v", err)
	}

	// 定向更新：只改标题与状态。
	newTitle := "新标题"
	newStatus := "refined"
	updatedAt, err := s.UpdateMetaScoped(m.ID, "u1", "ws-a", &newTitle, &newStatus)
	if err != nil {
		t.Fatalf("update meta: %v", err)
	}
	if updatedAt.IsZero() {
		t.Fatal("UpdateMetaScoped 必须返回盖上的 updated_at")
	}

	after, err := s.GetScoped(m.ID, "u1", "ws-a")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Title != newTitle || after.Status != newStatus {
		t.Fatalf("title/status 没改：title=%q status=%q", after.Title, after.Status)
	}
	if after.Transcript != live.Transcript {
		t.Fatalf("定向更新覆盖了转写：\n got %q\nwant %q", after.Transcript, live.Transcript)
	}
	if !after.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at 不一致：row=%v returned=%v", after.UpdatedAt, updatedAt)
	}
}

// TestTranscriptTailScopedScopeIsolation 钉住作用域隔离。
//
// ⚠ 尾读是**新的**读路径 ⇒ 它自己必须带作用域过滤 ⇒ 否则「换个 workspace
//
//	的 id 就能读到别人的转写尾部」。
func TestTranscriptTailScopedScopeIsolation(t *testing.T) {
	s := NewStore()
	m, err := s.CreateScoped(CreateMeetingRequest{
		Title:             "隔离",
		RefinedTranscript: "机密转写",
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, err := s.TranscriptTailScoped(m.ID, "u1", "ws-b", 0); !errors.Is(err, ErrMeetingNotFound) {
		t.Fatalf("跨 workspace 读尾段应返回 ErrMeetingNotFound，实际 %v", err)
	}
	if _, _, err := s.TranscriptTailScoped(m.ID, "u2", "ws-a", 0); !errors.Is(err, ErrMeetingNotFound) {
		t.Fatalf("跨 owner 读尾段应返回 ErrMeetingNotFound，实际 %v", err)
	}
	if _, err := s.UpdateMetaScoped(m.ID, "u1", "ws-b", nil, nil); !errors.Is(err, ErrMeetingNotFound) {
		t.Fatalf("跨 workspace 定向更新应返回 ErrMeetingNotFound，实际 %v", err)
	}
}

// normalizeSQLText 把 SQL 压成「便于逐字比较」的单行小写形态
// （对排版免疫）。判据只查语义片段，不查空格与换行。
//
// ⚠ 连「= 与 , 周围的空格」也要抹掉 ⇒ ⇒ 「`status=$5` 改成 `status = $5`」
//
//	这类**纯排版**改动不会误判红（负控 N2 就在验这条）。
func normalizeSQLText(q string) string {
	q = strings.ToLower(q)
	q = strings.Join(strings.Fields(q), " ")
	q = strings.ReplaceAll(q, " = ", "=")
	q = strings.ReplaceAll(q, " , ", ",")
	return q
}

// mustTime 给 SQL 形状守卫一个**固定**的时间戳（判据要可重复）。
func mustTime(t *testing.T) time.Time {
	t.Helper()
	return time.Unix(1700000000, 0).UTC()
}

// ── ② 需要真 PG 的用例 ───────────────────────────────────────────────
//
// ⚠ 无 PG 时这些用例会 **skip** ⇒ 退出码仍是 0 ⇒ 「跑了个寂寞」与「全过」
//   完全同形（§935.4.2）⇒ ⇒ 变异批次在缺 PG 时**拒绝跑**，而不是让它绿。

// TestPGTranscriptTailScoped 钉住 PG 版的尾读：内容对、越界不炸。
func TestPGTranscriptTailScoped(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	const head = "第一段：讨论二季度预算。"
	const added = "第二段：排班系统下周定。"
	full := head + added
	m, err := store.CreateScoped(CreateMeetingRequest{
		Title:             "尾读",
		RefinedTranscript: full,
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// ① 游标从头 ⇒ 整份。
	tail, total, err := store.TranscriptTailScoped(m.ID, "u1", "ws-a", 0)
	if err != nil {
		t.Fatalf("tail(0): %v", err)
	}
	if tail != full {
		t.Fatalf("tail(0) = %q, want %q", tail, full)
	}
	if total != len([]rune(full)) {
		t.Fatalf("tail(0) total = %d, want %d", total, len([]rune(full)))
	}

	// ② 游标在第一段末尾 ⇒ 只要新增段。
	from := len([]rune(head))
	tail, total, err = store.TranscriptTailScoped(m.ID, "u1", "ws-a", from)
	if err != nil {
		t.Fatalf("tail(%d): %v", from, err)
	}
	if tail != added {
		t.Fatalf("tail(%d) = %q, want %q", from, tail, added)
	}
	if total != len([]rune(full)) {
		t.Fatalf("tail(%d) total = %d, want %d", from, total, len([]rune(full)))
	}

	// ③ 游标越过末尾 ⇒ 空串（**不是**报错）。
	tail, total, err = store.TranscriptTailScoped(m.ID, "u1", "ws-a", total)
	if err != nil {
		t.Fatalf("tail(越界) 不该报错：%v", err)
	}
	if tail != "" {
		t.Fatalf("tail(越界) = %q, want 空串", tail)
	}
}

// TestPGTranscriptTailMovesFewBytes 量「**DB 交到客户端的字节数**」。
//
// ⚠ 这是 D1 的**直接读数**，不是代理指标：驱动从 PG 收到的就是 substr 的结果
//
//	⇒ ⇒ 尾读交出的字节数与「新增段」成正比，而整读与「全文」成正比。
//
// ⚠ 语料按 §901 实测的 271 字/分钟合成（1 小时 ≈ 16k 字 ≈ 48 KB）⇒
//
//	⇒ 数字量级与一场真实长会议同阶，不是玩具数据。
func TestPGTranscriptTailMovesFewBytes(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	// 1 小时量级（按 271 字/分钟 ⇒ 约 16,260 字）。
	long := strings.Repeat("张三: 讨论二季度预算与排班系统的对接细节。", 600)
	m, err := store.CreateScoped(CreateMeetingRequest{
		Title:             "长会议",
		RefinedTranscript: long,
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ctx := context.Background()
	// 整读：DB 交出多少字节。
	var full string
	if err := store.pool.QueryRow(ctx,
		`SELECT transcript FROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`,
		m.ID, "u1", "ws-a").Scan(&full); err != nil {
		t.Fatalf("select full transcript: %v", err)
	}
	// 尾读：只要最后 100 个字。
	from := len([]rune(long)) - 100
	tail, _, err := store.TranscriptTailScoped(m.ID, "u1", "ws-a", from)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}

	if len(tail) > 512 {
		t.Fatalf("尾读仍搬了 %d 字节（新增段只有 100 字 ⇒ 应在几百字节量级）", len(tail))
	}
	if len(full) < 10*len(tail) {
		t.Fatalf("语料太小，量不出差别：整读 %d 字节 vs 尾读 %d 字节", len(full), len(tail))
	}
	t.Logf("§937 D1 实测：整读 %d 字节 → 尾读 %d 字节（%.1f×↓）",
		len(full), len(tail), float64(len(full))/float64(maxInt(len(tail), 1)))
}

// TestPGUpdateMetaScopedLeavesTranscriptUntouched 钉住 PG 版的 D2：
// 定向更新**不碰**转写，且**不覆盖**并发写入的新转写。
//
// ⚠ 这条在 PG 上才真正有意义：PG 版的 UpdateScoped 是
//
//	`UPDATE meetings SET … transcript=$7 …` ⇒ 不做定向更新就一定会重写整列。
func TestPGUpdateMetaScopedLeavesTranscriptUntouched(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	m, err := store.CreateScoped(CreateMeetingRequest{
		Title:             "原标题",
		RefinedTranscript: "第一版转写",
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 并发写入：另一条路径追加了转写。
	live, err := store.GetScoped(m.ID, "u1", "ws-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	live.Transcript = "第一版转写\n第二版（并发写入）"
	if err := store.UpdateScoped(live, "u1", "ws-a"); err != nil {
		t.Fatalf("append: %v", err)
	}

	newTitle := "新标题"
	newStatus := "refined"
	updatedAt, err := store.UpdateMetaScoped(m.ID, "u1", "ws-a", &newTitle, &newStatus)
	if err != nil {
		t.Fatalf("update meta: %v", err)
	}

	after, err := store.GetScoped(m.ID, "u1", "ws-a")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Title != newTitle || after.Status != newStatus {
		t.Fatalf("title/status 没改：title=%q status=%q", after.Title, after.Status)
	}
	if after.Transcript != live.Transcript {
		t.Fatalf("定向更新覆盖了转写：\n got %q\nwant %q", after.Transcript, live.Transcript)
	}
	// ⚠ PG 的 timestamptz 精度是**微秒**，而 UpdateMetaScoped 返回的是内存里的
	//   **纳秒**值 ⇒ 直接比字面必然差几百纳秒（CI 实测差 1.46ns / 999ns）
	//   ⇒ 这条判据要比的是「同一个时刻在 PG 精度下是否相等」，
	//   不是两个不同精度的字面相等（直接 Equal 在 ubuntu CI 上恒红）。
	//   ⚠ 内存版那条（上面那个函数）**保持严格相等**：内存 store 不丢精度，
	//   放宽它等于自己拆自己的牙。
	if !pgTimeEqual(after.UpdatedAt, updatedAt) {
		t.Fatalf("updated_at 不一致：row=%v returned=%v（差 %v，超过 PG 的微秒精度）", after.UpdatedAt, updatedAt, after.UpdatedAt.Sub(updatedAt))
	}
}

// TestTranscriptTailStoresAgreeExactly 钉住**跨 store 逐字一致**。
//
// ⚠ 为什么必须钉（§936 的 D3 同族）：PG 版用 SQL（substr 按字符定位）、
//
//	内存版用 Go 切片 ⇒ 两边的「字符」只要差一个定义 ⇒ dev 环境（内存版）
//	就测不出生产（PG 版）的偏差 ⇒ ⇒ 同一个 fromRune 必须给出同一个尾部。
func TestTranscriptTailStoresAgreeExactly(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()
	mem := NewStore()

	// ⚠ 语料混中文/ASCII/emoji ⇒ 字符边界在三种编码宽度下都被压到。
	text := "张三: 讨论二季度预算\n李四: 排班系统下周定 🎯\n王五: 那就按这个来"
	pm, err := store.CreateScoped(CreateMeetingRequest{
		Title: "一致", RefinedTranscript: text,
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create pg: %v", err)
	}
	mm, err := mem.CreateScoped(CreateMeetingRequest{
		Title: "一致", RefinedTranscript: text,
	}, "u1", "ws-a")
	if err != nil {
		t.Fatalf("create mem: %v", err)
	}

	total := len([]rune(text))
	for _, from := range []int{0, 1, 5, 17, total - 1, total, total + 9} {
		pt, ptotal, perr := store.TranscriptTailScoped(pm.ID, "u1", "ws-a", from)
		mt, mtotal, merr := mem.TranscriptTailScoped(mm.ID, "u1", "ws-a", from)
		if perr != nil || merr != nil {
			t.Fatalf("from=%d: pg err=%v mem err=%v", from, perr, merr)
		}
		if pt != mt {
			t.Fatalf("from=%d 两个 store 的尾部不同：\n pg  %q\n mem %q", from, pt, mt)
		}
		if ptotal != mtotal {
			t.Fatalf("from=%d 两个 store 的字符数不同：pg=%d mem=%d", from, ptotal, mtotal)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
