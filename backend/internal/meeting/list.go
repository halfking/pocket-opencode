// internal/meeting/list.go
package meeting

import (
	"sort"
	"strconv"
	"time"
)

// 会议列表的**共用口径**（§936）。
//
// 为什么要有这个文件：§936 之前，PG 版与内存版各自实现了一份「取列表」，
// 两者**形状不同**：
//
//	PG 版   —— `ORDER BY created_at DESC LIMIT 500`（有上限、有排序）
//	内存版  —— 既无上限、也不排序（map 遍历 ⇒ 随机顺序）
//
// ⇒ ⇒ 同一个端点在两种 store 下返回的**顺序与数量都不同**
// ⇒ ⇒ **dev 环境（内存版）根本测不出生产（PG 版）的问题**
// ⇒ ⇒ 而 CI 的 backend job 没有 PG ⇒ 那条路径**平时根本没人跑**。
//
// ⭐ 纪律：**同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处**（§80 的形状）。
// 所以「排序 / 过滤 / 分页 / 计数」这四件事**只允许有一份口径**：
// 两个 store 都调本文件，语义由 §936 的**跨 store 逐字一致**判据保证。
//
// ⚠ 注意本文件**不**规定「过滤与分页谁先」以外的东西：
// 内存版把整套做在 Go 里，PG 版把它下推成 SQL —— 两者的**顺序**必须一致，
// 否则同一份数据在两种 store 下会给出不同的结果集（这正是 §936 的 D2/D3）。

const (
	// ListDefaultLimit 是列表默认返回条数。
	//
	// 为什么是 500：这是 §936 修复前 PG 版 SQL 里写死的 `LIMIT 500`。
	// 保留它作默认值 ⇒ **默认行为不变**，只是让它变成**可覆盖**的参数。
	ListDefaultLimit = 500

	// ListMaxLimit 是单页条数上限。
	//
	// 为什么要有上限：列表响应带整份 transcript（§936 登记的 D4），
	// 所以 limit 越大响应体越大 ⇒ 必须封顶，否则一个 `?limit=100000`
	// 就能把整个作用域的转写全拉出来。
	ListMaxLimit = 500

	// ListMaxOffset 是翻页深度上限。
	//
	// 为什么要有上限：offset 是**深翻页**（OFFSET 越大，数据库扫过的行越多），
	// 而会议条数由存储大小决定（属主诉求：时长/条数只取决于存储）⇒
	// 没有上限时，`offset=1000000000` 会让 DB 扫过十亿行才返回空。
	ListMaxOffset = 100000
)

// ListOptions 是列表查询的**唯一**入参形态。
//
// ⚠ 三个字段的口径必须与 PG 版的 SQL 一一对应（见 pg_store.go 的
// listScopedQuery）：Since ⇒ `updated_at > $3`；Limit/Offset ⇒ `LIMIT/OFFSET`。
type ListOptions struct {
	// Since 是增量同步的水位线：只返回 UpdatedAt **严格晚于**它的行。
	// 零值 ⇒ 不过滤。
	Since time.Time

	// Limit 是单页条数。<=0 ⇒ ListDefaultLimit；> ListMaxLimit ⇒ 夹到上限。
	Limit int

	// Offset 是跳过的条数。<0 ⇒ 0；> ListMaxOffset ⇒ 夹到上限。
	Offset int
}

// NormalizeListOptions 把入参夹到合法区间。
//
// ⚠ **只有这一处**做夹取（handler 与两个 store 都调它）⇒ ⇒
// 「非法参数怎么处理」这件事在仓里**只有一份口径**。
func NormalizeListOptions(opts ListOptions) ListOptions {
	if opts.Limit <= 0 {
		opts.Limit = ListDefaultLimit
	}
	if opts.Limit > ListMaxLimit {
		opts.Limit = ListMaxLimit
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	if opts.Offset > ListMaxOffset {
		opts.Offset = ListMaxOffset
	}
	return opts
}

// ParseListSince 解析 since 参数（Unix 秒或毫秒）。
//
// ⚠ 口径与 §936 修复前**逐字一致**（>1e12 当毫秒，否则当秒）⇒ ⇒
// 移动端已有的增量同步请求**行为不变**。
// ⚠ 解析失败 / 非正数 ⇒ 零值（**不过滤**）⇒ 与修复前一致。
//
// ⚠ 为什么住在 meeting 包而不是 handler 里：§936 修的正是「口径分叉」
// ⇒ ⇒ 解析口径也只能有一份 ⇒ handler 直接调它。
func ParseListSince(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	if n > 1_000_000_000_000 {
		return time.UnixMilli(n)
	}
	return time.Unix(n, 0)
}

// ParseListInt 解析分页参数（limit / offset）。
//
// ⚠ **不**因为参数非法而 400：客户端把 `limit` 写成 `""` / `"abc"` 是很
// 常见的手滑 ⇒ 夹取比报错更符合「分页是可选增强」的定位。
// ⚠ 真正的夹取（上限 / 下限）**不在这里**做 ⇒ ⇒ 那是 NormalizeListOptions
// 的事（**全仓只有那一份口径**）。
func ParseListInt(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// SortMeetings 按 (created_at DESC, id DESC) 排序。
//
// ⚠ 为什么 id 也要参与排序：created_at 的精度有限（PG 的 timestamptz 是
// **微秒**），同一批写入的会议会撞成同一时刻 ⇒ 只按 created_at 排的话
// **顺序不确定** ⇒ 两个 store 的同一次查询可能给出不同顺序 ⇒
// 跨 store 逐字一致就永远做不到。id DESC 是**稳定的** tie-break。
//
// ⚠ nil 排最后（不 panic）：调用方传进来的切片理论上不该含 nil，
// 但排序器不该因为一个脏元素就整段 panic。
func SortMeetings(list []*Meeting) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a == nil || b == nil {
			if a == nil {
				return false
			}
			return true
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
}

// ApplyListOptions 是内存版的列表实现：**先过滤 → 再排序 → 再分页**，
// 并返回「过滤后的总条数」。
//
// ⚠ **顺序是语义的一部分**：§936 的 D2 就是「先 LIMIT 再过滤」造成的 ——
// 一场**创建于 500 名之外**但**刚被更新**的会议，永远同步不到它的更新
// ⇒ ⇒ 过滤必须在分页**之前**。
//
// ⚠ 返回的 total 是**过滤后、分页前**的条数（**不是**本页行数）：
// 有上限时「本页有几行」是**谎话**（§936 的 D1）。
func ApplyListOptions(all []*Meeting, opts ListOptions) ([]*Meeting, int) {
	opts = NormalizeListOptions(opts)
	matched := make([]*Meeting, 0, len(all))
	for _, m := range all {
		if m == nil {
			continue
		}
		if !opts.Since.IsZero() && !m.UpdatedAt.After(opts.Since) {
			continue
		}
		matched = append(matched, m)
	}
	SortMeetings(matched)
	total := len(matched)
	start := opts.Offset
	if start > total {
		start = total
	}
	end := start + opts.Limit
	if end > total {
		end = total
	}
	return matched[start:end], total
}
