// internal/meeting/list_test.go —— §936：会议列表口径的判据。
//
// 本文件守的是**四件**此前没人守过的事：
//
//	D1  total 必须是「作用域内的真实总条数」，不是本页行数
//	    （修复前 total = len(被 LIMIT 500 截断后的列表) ⇒ 用户有 600 场
//	      会议时 total 恒为 500 ⇒ 第 501 场之后永远取不到）
//	D2  since 必须**先过滤后分页**（修复前 PG 版是「先 LIMIT 500，再在 Go 里
//	      过滤」⇒ 一场「创建于 500 名之外、但刚被更新」的会议永远同步不到更新）
//	D3  PG 版与内存版必须**逐字一致**（修复前 PG 有上限有排序、内存版既没有
//	      ⇒ 同一个端点两种形状 ⇒ dev 环境测不出生产的问题）
//	D5  分页参数必须被夹取（不能因为 `?limit=abc` 就 500，也不能因为
//	      `?limit=1000000` 就把整个作用域的转写全拉出来）
//
// ⚠ 本文件分两类用例，**能力不同，不能互相顶替**：
//
//	① 无条件跑的（口径 / SQL 文本守卫）—— 任何环境都跑，不依赖 PG；
//	② 需要真 PG 的（跨 store 逐字一致）—— 无 PG 时 **skip**。
//	   ⇒ ⇒ 「无 PG 时这些用例被跳过」这件事必须**如实登记**，不能当成「测过了」。
package meeting

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// listStore 是本文件要对比的两个 store 的**最小公共面**。
//
// ⚠ 这里只列本文件真正用到的三个方法 ⇒ ⇒ 任何多出来的行为都不该
// 让两个实现「看起来一致」—— 一致性由**同样的输入产出同样的输出**证明，
// 不是由接口长得像证明。
type listStore interface {
	CreateScoped(req CreateMeetingRequest, ownerID, workspaceID string) (*Meeting, error)
	UpdateScoped(m *Meeting, ownerID, workspaceID string) error
	ListScoped(ownerID, workspaceID string, opts ListOptions) ([]*Meeting, int, error)
}

// ── ① 无条件跑的用例 ───────────────────────────────────────────────────

// TestNormalizeListOptions 钉住夹取口径。
//
// ⚠ 为什么要**逐个值**钉：夹取是「非法参数不炸库」的唯一防线，
// 而「不炸库」这件事**在正常路径上完全看不出来**（不越界就永远走不到
// 那几行）⇒ ⇒ 不钉死就等于没钉。
func TestNormalizeListOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        ListOptions
		wantLimit int
		wantOff   int
	}{
		{"零值走默认", ListOptions{}, ListDefaultLimit, 0},
		{"limit=0 走默认", ListOptions{Limit: 0}, ListDefaultLimit, 0},
		{"limit 为负走默认", ListOptions{Limit: -1}, ListDefaultLimit, 0},
		{"limit 超上限被夹", ListOptions{Limit: ListMaxLimit + 1}, ListMaxLimit, 0},
		{"limit 巨大被夹", ListOptions{Limit: 1000000}, ListMaxLimit, 0},
		{"limit 合法值不动", ListOptions{Limit: 20}, 20, 0},
		{"offset 为负夹到 0", ListOptions{Offset: -5}, ListDefaultLimit, 0},
		{"offset 超上限被夹", ListOptions{Offset: ListMaxOffset + 1}, ListDefaultLimit, ListMaxOffset},
		{"offset 合法值不动", ListOptions{Offset: 7}, ListDefaultLimit, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeListOptions(tc.in)
			if got.Limit != tc.wantLimit {
				t.Fatalf("limit 夹取后 = %d，期望 %d", got.Limit, tc.wantLimit)
			}
			if got.Offset != tc.wantOff {
				t.Fatalf("offset 夹取后 = %d，期望 %d", got.Offset, tc.wantOff)
			}
		})
	}
}

// TestApplyListOptions_TotalIsScopeNotPage 钉住 D1 的核心：
// **total 不是本页行数**。
//
// ⚠ 旧实现下这个断言会**红**：total = len(截断后的列表) = 2 ≠ 5。
func TestApplyListOptions_TotalIsScopeNotPage(t *testing.T) {
	all := make([]*Meeting, 0, 5)
	for i := range 5 {
		all = append(all, &Meeting{
			ID:        fmt.Sprintf("m%d", i),
			CreatedAt: time.Unix(int64(1000+i), 0),
			UpdatedAt: time.Unix(int64(1000+i), 0),
		})
	}

	page, total := ApplyListOptions(all, ListOptions{Limit: 2})
	if total != 5 {
		t.Fatalf("total = %d，期望 5（是作用域内的总条数，不是本页行数）", total)
	}
	if len(page) != 2 {
		t.Fatalf("本页 %d 行，期望 2", len(page))
	}
	// 排序口径：created_at DESC ⇒ 新的在前。
	if page[0].ID != "m4" || page[1].ID != "m3" {
		t.Fatalf("排序不是 created_at DESC，实际 %s,%s", page[0].ID, page[1].ID)
	}

	// 翻页：offset 必须真的跳过。
	page2, total2 := ApplyListOptions(all, ListOptions{Limit: 2, Offset: 2})
	if total2 != 5 {
		t.Fatalf("翻页时 total 变成了 %d，期望仍是 5", total2)
	}
	if len(page2) != 2 || page2[0].ID != "m2" || page2[1].ID != "m1" {
		t.Fatalf("offset=2 拿到的不是第 3、4 行，实际 %v", idsOf(page2))
	}

	// offset 越过末尾 ⇒ 空页，但 total 仍是 5。
	page3, total3 := ApplyListOptions(all, ListOptions{Limit: 2, Offset: 99})
	if len(page3) != 0 {
		t.Fatalf("offset 越过末尾应返回空页，实际 %v", idsOf(page3))
	}
	if total3 != 5 {
		t.Fatalf("空页时 total 变成了 %d，期望仍是 5", total3)
	}
}

// TestApplyListOptions_FiltersBeforePaging 钉住 D2 的核心：
// **过滤必须在分页之前**。
//
// ⚠ 为什么这条对**内存版**也有牙：内存版修复前**没有**任何上限
// ⇒ 「先分页后过滤」这个错误在内存版上**看不出来** ⇒ 但现在内存版
// 也有上限了（共用口径）⇒ ⇒ 顺序一旦写反，这条立刻红。
//
// ⚠ 夹具刻意用**很小的 limit**（2）而不是 500 ⇒ 理由：��错的是
// 「过滤与分页的相对顺序」，与 limit 的具体数值无关 ⇒ 用小 limit
// 可以把用例做得又快又不依赖 500 行夹具。
func TestApplyListOptions_FiltersBeforePaging(t *testing.T) {
	old := time.Unix(1000, 0)
	recent := time.Unix(2000, 0)
	all := []*Meeting{
		{ID: "old-but-just-updated", CreatedAt: old, UpdatedAt: recent},
		{ID: "new-1", CreatedAt: time.Unix(1100, 0), UpdatedAt: time.Unix(1100, 0)},
		{ID: "new-2", CreatedAt: time.Unix(1200, 0), UpdatedAt: time.Unix(1200, 0)},
	}
	cut := time.Unix(1500, 0)

	page, total := ApplyListOptions(all, ListOptions{Since: cut, Limit: 2})
	if total != 1 {
		t.Fatalf("total = %d，期望 1（只有那场刚更新的）", total)
	}
	if len(page) != 1 || page[0].ID != "old-but-just-updated" {
		t.Fatalf("「老但刚更新」的那场没被捞出来，实际 %v ⇒ 过滤被排在了分页之后", idsOf(page))
	}

	// ★ 前提自证：这场会议按 created_at 排序**落在 limit 之外** ——
	// 不然上面那条断言就是恒真的（它本来就在第一页里，顺序写反也照样绿）。
	// ⇒ ⇒ 判据红时必须先确认「前提没建立」这件事不会伪装成「缺陷」。
	full, _ := ApplyListOptions(all, ListOptions{Limit: 2})
	for _, m := range full {
		if m.ID == "old-but-just-updated" {
			t.Fatal("前提不成立：那场会议本就在第一页里 ⇒ 上面那条断言量不到「先过滤后分页」")
		}
	}
}

// TestPGListSQLShape 是**源码级**守卫：无 PG 环境也跑。
//
// ⚠ 它守的是 PG 版 SQL 的**子句顺序**：`updated_at > $3` 必须在 `LIMIT` 之前。
//
//	为什么需要它：跨 store 逐字一致那条用例需要真 PG ⇒ 在没有 PG 的
//	环境（CI 的 backend job）它会被 **skip** ⇒ ⇒ 如果只有那一条守卫，
//	「先 LIMIT 后过滤」这个缺陷在 CI 上就是**无人看守**的。
//	⇒ ⇒ 这条文本守卫是那道 skip 用例的**兜底**。
//
// ⚠ 它量的**不是**「源码里出现过 updated_at」⇒ ⇒ 它量的是
// **同一段 SQL 里两个子句的相对位置** ⇒ 这才是语义。
//
// ⚠ 判据**对排版免疫**（normalizeSQL）⇒ ⇒ 只改空白/换行不该判红
// ⇒ ⇒ 这条「负控 1」就写在下面，不靠外部脚本证明。
func TestPGListSQLShape(t *testing.T) {
	if err := listSQLShape(listScopedQuery); err != nil {
		t.Fatalf("列表 SQL 形状不对：%v", err)
	}
	if err := countSQLShape(listScopedCountQuery); err != nil {
		t.Fatalf("计数 SQL 形状不对：%v", err)
	}

	// ★ 负控 1：只改**排版**（不改编义）⇒ 必须仍然通过。
	//   ⇒ 证明这道门量的是**语义**，不是**格式**。
	reformatted := strings.ReplaceAll(listScopedQuery, "updated_at > $3", "updated_at>$3")
	reformatted = strings.ReplaceAll(reformatted, "ORDER BY created_at DESC, id DESC", "ORDER BY  created_at   DESC,id   DESC")
	if err := listSQLShape(reformatted); err != nil {
		t.Fatalf("只改排版就判红 ⇒ 这道门在量格式而不是语义：%v", err)
	}

	// ★ 负控 2：把过滤挪到 LIMIT **之后**（旧实现的形状）⇒ 必须判红，
	//   且必须报出**那条**理由（不能是别的理由碰巧红了）。
	broken := "SELECT x FROM meetings WHERE owner_id=$1 AND workspace_id=$2 " +
		"ORDER BY created_at DESC, id DESC LIMIT $4 OFFSET $5 " +
		"AND ($3::timestamptz IS NULL OR updated_at > $3)"
	err := listSQLShape(broken)
	if err == nil {
		t.Fatal("把过滤排到 LIMIT 之后仍然判绿 ⇒ 这道门没有牙（恒真的空壳）")
	}
	if !strings.Contains(err.Error(), "LIMIT") {
		t.Fatalf("判红的理由不对：%v（期望点名 LIMIT 的位置）", err)
	}

	// ★ 负控 3：计数 SQL 里混进 LIMIT ⇒ 必须判红。
	badCount := listScopedCountQuery + " LIMIT 10"
	if err := countSQLShape(badCount); err == nil {
		t.Fatal("计数 SQL 含 LIMIT 仍然判绿 ⇒ 门没有牙")
	}
}

// listSQLShape 检查「列表 SQL 的形状」，nil = 形状对。
//
// ⚠ 抽成函数是为了让**负控**能直接调它（负控与正控验的是**同一份**判断
// ⇒ ⇒ 负控才不是在验另一件事）。
func listSQLShape(q string) error {
	n := normalizeSQL(q)
	filterIdx := indexOf(n, "updated_at>$3")
	limitIdx := indexOf(n, "LIMIT $4")
	offsetIdx := indexOf(n, "OFFSET $5")
	orderIdx := indexOf(n, "ORDER BY")
	if filterIdx < 0 {
		return fmt.Errorf("列表 SQL 里没有 updated_at>$3 ⇒ 增量过滤根本没下推进 SQL")
	}
	if limitIdx < 0 {
		return fmt.Errorf("列表 SQL 里没有 LIMIT $4 ⇒ 分页上限不见了")
	}
	if offsetIdx < 0 {
		return fmt.Errorf("列表 SQL 里没有 OFFSET $5 ⇒ 翻页不成立")
	}
	if orderIdx < 0 {
		return fmt.Errorf("列表 SQL 里没有 ORDER BY ⇒ 顺序不确定")
	}
	if filterIdx > limitIdx {
		return fmt.Errorf("过滤子句(%d)排在 LIMIT(%d)之后 ⇒ 一场「创建于上限之外、"+
			"但刚被更新」的会议永远同步不到更新（§936 的 D2）", filterIdx, limitIdx)
	}
	if orderIdx > limitIdx {
		return fmt.Errorf("排序子句(%d)排在 LIMIT(%d)之后 ⇒ 翻页拿到的是未排序的行", orderIdx, limitIdx)
	}
	orderSeg := n[orderIdx:limitIdx]
	if !strings.Contains(orderSeg, "created_at DESC") {
		return fmt.Errorf("排序段里没有 created_at DESC：%q", orderSeg)
	}
	if !strings.Contains(orderSeg, "id DESC") {
		return fmt.Errorf("排序段里没有 id DESC 的 tie-break（顺序不确定）：%q", orderSeg)
	}
	return nil
}

// countSQLShape 检查「计数 SQL 的形状」，nil = 形状对。
func countSQLShape(q string) error {
	n := normalizeSQL(q)
	if !strings.Contains(n, "COUNT(*)") {
		return fmt.Errorf("计数 SQL 里没有 COUNT(*)：%q", n)
	}
	if !strings.Contains(n, "owner_id=$1") {
		return fmt.Errorf("计数 SQL 缺 owner 作用域过滤：%q", n)
	}
	if !strings.Contains(n, "workspace_id=$2") {
		return fmt.Errorf("计数 SQL 缺 workspace 作用域过滤：%q", n)
	}
	if !strings.Contains(n, "updated_at>$3") {
		return fmt.Errorf("计数 SQL 里没有 updated_at>$3 ⇒ total 与 meetings 数组的过滤口径不一致：%q", n)
	}
	if strings.Contains(n, "LIMIT") {
		return fmt.Errorf("计数 SQL 里含 LIMIT ⇒ total 不可能是「总条数」：%q", n)
	}
	return nil
}

// normalizeSQL 把 SQL 压成「与排版无关」的形态。
//
// ⚠ 为什么需要它：这道门量的是**语义**（子句的相对位置）⇒ ⇒
// 「`updated_at > $3` 写成 `updated_at>$3`」不该让它变红
// ⇒ ⇒ 否则后人会去「把门改宽」而不是「把门改对」⇒ ⇒ 而「改宽」会让它
// 变成另一道门。
//
// ⚠ 折叠规则：所有空白折成**一个**空格；比较运算符两侧的空格**去掉**。
func normalizeSQL(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		out = strings.ReplaceAll(out, " "+op+" ", op)
	}
	return out
}

// TestParseListSinceSemantics 钉住 since 的**既有**口径不变。
//
// ⚠ 这是**兼容性**门：移动端已经在用 `since` ⇒ §936 改了 SQL 之后，
// since 的**解析口径必须逐字不变**（秒 vs 毫秒的判别阈值是 1e12）。
//
// ⚠ 解析函数住在 meeting 包（ParseListSince）⇒ handler 调的是同一个
// ⇒ ⇒ 这里不需要「复刻一份」⇒ 也就不存在两份口径漂移的可能。
func TestParseListSinceSemantics(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"1700000000", time.Unix(1700000000, 0)},
		{"1700000000000", time.UnixMilli(1700000000000)},
		{"1700000000001", time.UnixMilli(1700000000001)},
	} {
		got := ParseListSince(tc.in)
		if !got.Equal(tc.want) {
			t.Fatalf("since=%q 解析成 %v，期望 %v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "abc", "0", "-1", "1.5", " 12"} {
		if got := ParseListSince(bad); !got.IsZero() {
			t.Fatalf("since=%q 应解析成零值（不过滤），实际 %v", bad, got)
		}
	}
}

// TestParseListIntSemantics 钉住分页参数的解析口径。
//
// ⚠ 解析与夹取是**两件事**：解析只管「能不能读成一个整数」，
// 夹取在 NormalizeListOptions 里 ⇒ ⇒ 这里不测夹取（那边另有判据）。
func TestParseListIntSemantics(t *testing.T) {
	for _, tc := range []struct {
		in   string
		def  int
		want int
	}{
		{"", 500, 500},
		{"abc", 500, 500},
		{"12", 500, 12},
		{"0", 500, 0},
		{"-5", 500, -5},
		{"", 0, 0},
		{"7", 0, 7},
	} {
		if got := ParseListInt(tc.in, tc.def); got != tc.want {
			t.Fatalf("ParseListInt(%q, %d) = %d，期望 %d", tc.in, tc.def, got, tc.want)
		}
	}
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func idsOf(list []*Meeting) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.ID)
	}
	return out
}

// titlesOf 返回标题序列（跨 store 对比用；两个 store 的 ID 各自生成，
// **不可比** ⇒ 比标题）。
func titlesOf(list []*Meeting) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.Title)
	}
	return out
}

// ── ② 需要真 PG 的用例 ─────────────────────────────────────────────────

// TestListScoped_StoresAgreeExactly 是本文件**最贵也最值**的一条：
// 同一份数据、同���组 opts ⇒ PG 版与内存版必须给出**逐字相同**的结果。
//
// ⚠ 为什么它值钱：修复前两个 store 的形状**根本不同**（PG 有 LIMIT 500
// + 排序，内存版没有）⇒ ⇒ 「同一个端点在 dev 与生产返回不同的东西」
// 这类缺陷，**只有**这条判据能抓住。
//
// ⚠ 期望值是**写死的**（不是「两个 store 互相等于」）：
// 「互相等于」在两边都返回空时也成立 ⇒ ⇒ 必须同时钉住「期望是什么」。
//
// ⚠ 无 PG 时本用例 **skip** ⇒ 「这条在 CI 上没跑」必须如实登记。
func TestListScoped_StoresAgreeExactly(t *testing.T) {
	mem := NewStore()
	pg, cleanup := newPGTestEnv(t)
	defer cleanup()

	const n = 8
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	memIDs := make([]string, 0, n)
	pgIDs := make([]string, 0, n)
	created := make([]time.Time, 0, n)
	updated := make([]time.Time, 0, n)
	for i := range n {
		title := fmt.Sprintf("m%d", i)
		mm, err := mem.CreateScoped(CreateMeetingRequest{Title: title}, "u1", "ws-a")
		if err != nil {
			t.Fatalf("内存版 create %d: %v", i, err)
		}
		pm, err := pg.CreateScoped(CreateMeetingRequest{Title: title}, "u1", "ws-a")
		if err != nil {
			t.Fatalf("PG 版 create %d: %v", i, err)
		}
		memIDs = append(memIDs, mm.ID)
		pgIDs = append(pgIDs, pm.ID)
		// 夹具：把两个 store 的时间戳**钉成同一组**（否则 created_at 的
		// 微秒精度差会让排序不可比）。
		created = append(created, base.Add(time.Duration(i)*time.Millisecond))
		updated = append(updated, base.Add(time.Duration(i)*time.Millisecond))
	}
	stampAllForTest(t, mem, memIDs, created, updated)
	stampAllForTest(t, pg, pgIDs, created, updated)

	for _, tc := range []struct {
		name       string
		opts       ListOptions
		wantTitles []string
		wantTotal  int
	}{
		{"默认（全部）", ListOptions{}, []string{"m7", "m6", "m5", "m4", "m3", "m2", "m1", "m0"}, 8},
		{"单页 3 条", ListOptions{Limit: 3}, []string{"m7", "m6", "m5"}, 8},
		{"翻页 offset=2", ListOptions{Limit: 3, Offset: 2}, []string{"m5", "m4", "m3"}, 8},
		{"翻页越过末尾", ListOptions{Limit: 3, Offset: 99}, nil, 8},
		{"增量：只取 i>=4", ListOptions{Since: base.Add(3 * time.Millisecond)}, []string{"m7", "m6", "m5", "m4"}, 4},
		{"增量 + 分页", ListOptions{Since: base.Add(3 * time.Millisecond), Limit: 2}, []string{"m7", "m6"}, 4},
		{"增量：水位在将来 ⇒ 空", ListOptions{Since: base.Add(time.Hour)}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memPage, memTotal, err := mem.ListScoped("u1", "ws-a", tc.opts)
			if err != nil {
				t.Fatalf("内存版 list: %v", err)
			}
			pgPage, pgTotal, err := pg.ListScoped("u1", "ws-a", tc.opts)
			if err != nil {
				t.Fatalf("PG 版 list: %v", err)
			}

			if memTotal != tc.wantTotal || pgTotal != tc.wantTotal {
				t.Fatalf("total 不符：内存版 %d / PG 版 %d，期望都是 %d", memTotal, pgTotal, tc.wantTotal)
			}
			if got := titlesOf(memPage); !sameStrings(got, tc.wantTitles) {
				t.Fatalf("内存版返回 %v，期望 %v", got, tc.wantTitles)
			}
			if got := titlesOf(pgPage); !sameStrings(got, tc.wantTitles) {
				t.Fatalf("PG 版返回 %v，期望 %v", got, tc.wantTitles)
			}
			// ★ 形态分叉的直接断言：两个 store 必须逐字一致。
			if !sameStrings(titlesOf(memPage), titlesOf(pgPage)) {
				t.Fatalf("两个 store 返回了不同的序列：内存版 %v / PG 版 %v ⇒ "+
					"§936 的 D3 复发（形态分叉）", titlesOf(memPage), titlesOf(pgPage))
			}
		})
	}

	// ★ 负控（前提自证）：增量过滤**真的**在起作用 ⇒ 否则上面那条
	// 「增量：只取 i>=4」可能因为「两条路都返回全部」而恒真。
	all, _, _ := mem.ListScoped("u1", "ws-a", ListOptions{})
	inc, _, _ := mem.ListScoped("u1", "ws-a", ListOptions{Since: base.Add(3 * time.Millisecond)})
	if len(inc) >= len(all) {
		t.Fatalf("增量过滤没起作用（全部 %d 行，增量 %d 行）⇒ 增量用例恒真", len(all), len(inc))
	}
}

// TestListScoped_UpdatedOldMeetingSurvivesPaging 是 §936 的 D2 判据。
//
// 场景：**一场创建于 500 名之外、但刚被更新的会议**，增量同步必须能捞到它。
//
// ⚠ 为什么必须造 501 行：缺陷的触发条件是「**先 LIMIT 后过滤**」
// ⇒ 只有行数超过 limit 时，被过滤掉的行才会真的消失
// ⇒ ⇒ 少于 limit 的夹具**测不到这个缺陷**（旧代码也绿）⇒ ⇒ 501 行不是
// 「为了好看」，是**缺陷的最小可复现规模**。
//
// ⚠ 夹具的时间戳是**钉死**的（不靠 sleep、不靠真实时钟分辨率）⇒
// ⇒ 用例在任何机器上都给出同一组读数。
func TestListScoped_UpdatedOldMeetingSurvivesPaging(t *testing.T) {
	for _, tc := range []struct {
		name    string
		newFunc func(t *testing.T) (listStore, func())
	}{
		{"内存版", func(t *testing.T) (listStore, func()) {
			return NewStore(), func() {}
		}},
		{"PG版", func(t *testing.T) (listStore, func()) {
			store, cleanup := newPGTestEnv(t)
			return store, cleanup
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := tc.newFunc(t)
			defer cleanup()

			const n = 501
			base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			var oldest *Meeting
			ids := make([]string, 0, n)
			created := make([]time.Time, 0, n)
			updated := make([]time.Time, 0, n)
			for i := range n {
				m, err := store.CreateScoped(
					CreateMeetingRequest{Title: fmt.Sprintf("m%03d", i)}, "u1", "ws-a")
				if err != nil {
					t.Fatalf("create %d: %v", i, err)
				}
				// 所有行的 updated_at 都钉成同一个值（base）⇒
				// 于是「水位线 = base+1ms」只可能捞到**被更新过**的那一行。
				ids = append(ids, m.ID)
				created = append(created, base.Add(time.Duration(i)*time.Millisecond))
				updated = append(updated, base)
				if i == 0 {
					oldest = m
				}
			}
			stampAllForTest(t, store, ids, created, updated)

			// ★ 前提自证：这场「最老」的会议按 created_at 排序落在
			// newest-500 **之外** ⇒ 不然下面那条断言量不到 D2。
			page, total, err := store.ListScoped("u1", "ws-a", ListOptions{})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if total != n {
				t.Fatalf("夹具不成立：total = %d，期望 %d（%d 行）", total, n, n)
			}
			if len(page) != ListDefaultLimit {
				t.Fatalf("夹具不成立：本页 %d 行，期望 %d（默认上限）", len(page), ListDefaultLimit)
			}
			for _, m := range page {
				if m.ID == oldest.ID {
					t.Fatal("前提不成立：那场最老的会议竟在 newest-500 里 ⇒ 用例量不到「先 LIMIT 后过滤」")
				}
			}

			// 把它更新一次（UpdateScoped 会盖 updated_at = now ⇒ 远大于 base）。
			if err := store.UpdateScoped(oldest, "u1", "ws-a"); err != nil {
				t.Fatalf("update oldest: %v", err)
			}

			// 水位线在 base 与 now 之间 ⇒ 只有被更新的那一行该被捞到。
			got, gotTotal, err := store.ListScoped("u1", "ws-a",
				ListOptions{Since: base.Add(time.Millisecond)})
			if err != nil {
				t.Fatalf("list since: %v", err)
			}
			if gotTotal != 1 {
				t.Fatalf("增量 total = %d，期望 1 ⇒ 过滤口径不对", gotTotal)
			}
			if len(got) != 1 || got[0].ID != oldest.ID {
				t.Fatalf("「创建于 %d 名之外、但刚被更新」的那场没被捞出来，实际 %v ⇒ "+
					"§936 的 D2 仍在（先 LIMIT 后过滤）", n, idsOf(got))
			}
		})
	}
}

// TestListScoped_TotalIsWorkspaceScoped 守住 total 的作用域。
//
// ⚠ 为什么必须单独守：total 来自一条**独立的 COUNT 查询**（§936 新增）
// ⇒ ⇒ 那条查询的 WHERE 少写一个字段就会**跨作用域串数**
// ⇒ ⇒ 而串数在响应里**看不出来**（只有数字变大）⇒ 必须钉。
// ⚠ 本用例跑在**真 PG** 上 ⇒ 它量的是 COUNT 的**行为**，不是 SQL 文本
// ⇒ ⇒ 与 list_test.go 里那条文本守卫（countSQLShape）**互补**。
func TestListScoped_TotalIsWorkspaceScoped(t *testing.T) {
	mem := NewStore()
	pg, cleanup := newPGTestEnv(t)
	defer cleanup()

	for _, tc := range []struct {
		owner, ws, title string
	}{
		{"u1", "ws-a", "A1"},
		{"u1", "ws-a", "A2"},
		{"u1", "ws-b", "B1"},
		{"u2", "ws-a", "别人的"},
	} {
		if _, err := mem.CreateScoped(CreateMeetingRequest{Title: tc.title}, tc.owner, tc.ws); err != nil {
			t.Fatalf("内存版 create %s/%s: %v", tc.owner, tc.ws, err)
		}
		if _, err := pg.CreateScoped(CreateMeetingRequest{Title: tc.title}, tc.owner, tc.ws); err != nil {
			t.Fatalf("PG 版 create %s/%s: %v", tc.owner, tc.ws, err)
		}
	}

	for _, tc := range []struct {
		owner, ws string
		want      int
	}{
		{"u1", "ws-a", 2},
		{"u1", "ws-b", 1},
		{"u2", "ws-a", 1},
		{"u9", "ws-a", 0},
	} {
		memPage, memTotal, err := mem.ListScoped(tc.owner, tc.ws, ListOptions{})
		if err != nil {
			t.Fatalf("内存版 list %s/%s: %v", tc.owner, tc.ws, err)
		}
		pgPage, pgTotal, err := pg.ListScoped(tc.owner, tc.ws, ListOptions{})
		if err != nil {
			t.Fatalf("PG 版 list %s/%s: %v", tc.owner, tc.ws, err)
		}
		if memTotal != tc.want || pgTotal != tc.want {
			t.Fatalf("%s/%s 的 total 应是 %d，实际内存版 %d / PG 版 %d ⇒ COUNT 漏了作用域过滤",
				tc.owner, tc.ws, tc.want, memTotal, pgTotal)
		}
		if len(memPage) != tc.want || len(pgPage) != tc.want {
			t.Fatalf("%s/%s 的行数应是 %d，实际内存版 %d / PG 版 %d",
				tc.owner, tc.ws, tc.want, len(memPage), len(pgPage))
		}
	}
}

// stampAllForTest 直接改存储层的时间戳，用来造**确定性**夹具。
//
// ⚠ 这是**夹具**，不是产品路径：产品的 created_at / updated_at 只能由
// CreateScoped / UpdateScoped 写 ⇒ 这里改的是「时间机器」，不是「业务逻辑」。
// ⚠ 用类型分支而不是给生产类型加方法 ⇒ 不往产品代码里塞测试专用 API。
// ⚠ PG 侧用**一条** unnest 批量 UPDATE（501 行逐条 UPDATE 实测要 12.5s，
//
//	批量后整条用例回到秒级 ⇒ 夹具不该是整包测试的瓶颈）。
func stampAllForTest(t *testing.T, store listStore, ids []string, created, updated []time.Time) {
	t.Helper()
	if len(ids) != len(created) || len(ids) != len(updated) {
		t.Fatalf("夹具长度对不上：ids=%d created=%d updated=%d", len(ids), len(created), len(updated))
	}
	switch s := store.(type) {
	case *Store:
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, id := range ids {
			m, ok := s.meetings[id]
			if !ok {
				t.Fatalf("内存版里没有 %s", id)
			}
			m.CreatedAt = created[i]
			m.UpdatedAt = updated[i]
		}
	case *PGStore:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.pool.Exec(ctx,
			`UPDATE meetings AS m SET created_at = v.c, updated_at = v.u
			 FROM unnest($1::text[], $2::timestamptz[], $3::timestamptz[]) AS v(id, c, u)
			 WHERE m.id = v.id`,
			ids, created, updated); err != nil {
			t.Fatalf("PG 版改时间戳失败: %v", err)
		}
	default:
		t.Fatalf("未知 store 类型 %T", store)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
