package pgtime

// pgtime_test.go —— 「时刻刻度」这条纪律的**纯函数**判据（不需要 PG）。
//
// ⚠ 为什么这些是**纯函数**判据：它们量的是「截断的方向」与「截断的粒度」，
//	这两件事**与 PG 无关** ⇒ ⇒ 任何平台、任何时刻都能跑 ⇒ 成本极低。
//	而「驱动到底怎么编码 timestamptz」那条**必须连真 PG**，在
//	pg_driver_test.go 里（那是本纪律的**前提**，不能靠纸面推断）。

import (
	"testing"
	"time"
)

// TestTruncateFloorsToMicrosecond 钉住两件事：
// ① 它是**向下**截断（floor），不是四舍五入（round）；
// ② 它**只动亚微秒部分**，微秒及以上一字未改。
//
// ⚠ 为什么「向下」这件事必须有判据（这是 §964 的核心，§966 把理由换了）：
//
//	**驱动（pgx）在编码时就会向下截断一次**
//	（实测见 pgtime 包的 pg_driver_test.go 与包注释的读数表）
//	⇒ 若写入侧用 round，落盘值仍可能带亚微秒部分
//	⇒ ⇒ 驱动再向下截断 ⇒ 「内存值 == 库值」这条严格等值就不成立。
//	⇒ ⇒ 必须**向下**截断（floor）。
//
// ⚠ §964 的注释在这里写的是「PG 用 round-half-to-even ⇒ PG 存它时还会再动一次」
//
//	⇒ **那句话的前提被 §966 的真机实测证伪了**（对参数绑定路径而言）
//	⇒ ⇒ 但**结论不变**，只是理由从「PG 会再动一次」换成「驱动会再动一次」。
func TestTruncateFloorsToMicrosecond(t *testing.T) {
	base := time.Date(2026, 10, 21, 19, 14, 55, 346017826, time.UTC)
	want := time.Date(2026, 10, 21, 19, 14, 55, 346017000, time.UTC)

	// ① 向下截断：.826µs ⇒ 舍去成 .000µs。
	got := Truncate(base)
	if !got.Equal(want) {
		t.Fatalf("Truncate 必须向下截断到微秒：got %v want %v", got, want)
	}
	// ② 负控：若有人把它写成 Round，.826µs 会**进位**成 .346018 ⇒ 上面那条会红。
	//    这条断言「Round 的结果与 want 不同」⇒ 证明 ① 的取值确实区分了
	//    「向下」与「四舍五入」（否则 ① 就没有区分度）。
	if base.Round(time.Microsecond).Equal(want) {
		t.Fatal("负控：Round 会把 .826µs 进位 ⇒ ① 的 want 取值本身就错了")
	}
	// ③ 微秒及以上部分必须**一字未动**（不得截到秒/毫秒去）。
	if got.Unix() != base.Unix() {
		t.Fatalf("Truncate 不得改动秒级部分：got %v want %v", got.Unix(), base.Unix())
	}
	if got.Nanosecond()%1000 != 0 {
		t.Fatalf("Truncate 后仍带亚微秒部分：%v", got.Nanosecond()%1000)
	}
	// ④ 负控：截到**毫秒**去是另一种结果 ⇒ 证明 ③ 的两个取值确实不同。
	if base.Truncate(time.Millisecond).Equal(got) {
		t.Error("负控：Truncate(毫秒) 必须与 Truncate(微秒) 不同 —— 否则 ③ 没有区分度")
	}
}

// TestTruncateIsTimezoneIndependent 钉住「截断与时区无关」。
//
// ⚠ 为什么这条重要：PG 的 `timestamptz` 存的是**绝对时刻**，
//
//	而 Go 的 `time.Time` 带一个 Location ⇒ 若截断依赖 Location，
//	同一绝对时刻在本地时区与 UTC 下会得到**不同的结果**
//	⇒ ⇒ 而仓库里到处都在用本地时区构造时刻
//	⇒ ⇒ ⇒ 这条一旦破了，「同一个时刻」会在两处变成两个值。
func TestTruncateIsTimezoneIndependent(t *testing.T) {
	utc := time.Date(2026, 10, 21, 19, 14, 55, 346017826, time.UTC)
	loc := time.FixedZone("X", 8*3600)
	sameInstantElsewhere := utc.In(loc)

	if !Truncate(utc).Equal(Truncate(sameInstantElsewhere)) {
		t.Fatalf("截断必须与时区无关：utc=%v other=%v", Truncate(utc), Truncate(sameInstantElsewhere))
	}
	// 负控：两个 Location 的**表示**确实不同（否则上面那条是恒真的空比较）。
	if utc.Equal(sameInstantElsewhere) && utc.Location() == sameInstantElsewhere.Location() {
		t.Fatal("负控：两个 Location 必须真的不同 ⇒ 否则上一条没有区分度")
	}
	if utc.String() == sameInstantElsewhere.String() {
		t.Fatal("负控：同一绝对时刻在两个时区下的字符串必须不同 ⇒ 否则上一条没有区分度")
	}
}

// TestTruncateKeepsMonotonicity 钉住「截断不破坏先后顺序」。
//
// ⚠ 为什么这条重要：截断被用在**排序键**上（会议列表按 created_at 排序、
//
//	审计导出按 (timestamp, id) 排序）。
//	⇒ 若截断把两个不同的时刻**折叠成同一个**，排序就会不稳定
//	⇒ ⇒ 而 floor 只会让相邻两个时刻**变成相等**（不会倒过来）
//	⇒ ⇒ ⇒ 这条把「不会倒过来」钉死。
func TestTruncateKeepsMonotonicity(t *testing.T) {
	base := time.Date(2026, 10, 21, 19, 14, 55, 0, time.UTC)
	cases := []time.Duration{
		0,
		999 * time.Nanosecond,
		time.Microsecond,
		time.Microsecond + 999*time.Nanosecond,
		1500 * time.Microsecond,
	}
	prev := base
	for _, d := range cases {
		cur := base.Add(d)
		if Truncate(cur).Before(Truncate(prev)) {
			t.Fatalf("截断不得把先后顺序倒过来：%v 截断后早于 %v", cur, prev)
		}
		if Truncate(cur).After(cur) {
			t.Fatalf("截断不得把时刻推到未来：%v ⇒ %v", cur, Truncate(cur))
		}
		prev = cur
	}
}

// TestNowHasNoSubMicrosecondPart 钉住 `Now()` 的产出**没有亚微秒部分**。
//
// ⚠ 为什么这条重要：`Now()` 是「写进 PG 的那一刻」的**唯一**入口
//
//	⇒ ⇒ 若它带亚微秒部分，那么「写进去的值」与「库里的值」必然差一截
//	⇒ ⇒ ⇒ 而这条判据在**任何平台**上都成立
//	（macOS 的 `time.Now()` 本来就是 µs 对齐的 ⇒ 不加这条，
//	  macOS 上 `Now()` 的产出与 `time.Now()` 逐字相同 ⇒ 判据空转）。
func TestNowHasNoSubMicrosecondPart(t *testing.T) {
	before := time.Now()
	got := Now()
	after := time.Now()

	if got.Nanosecond()%1000 != 0 {
		t.Fatalf("Now() 不得带亚微秒部分：%v", got.Nanosecond()%1000)
	}
	// Now() 必须是「刚才那一刻」，不能是别的时间。
	if got.Before(before.Truncate(time.Microsecond)) || got.After(after.Truncate(time.Microsecond)) {
		t.Fatalf("Now() 必须落在 [before, after] 区间内：got=%v before=%v after=%v", got, before, after)
	}
	// 负控：`before` 与 `after` 本身在 macOS 上就可能逐字相同
	// ⇒ 若它们恰好相等，上面那条区间断言就没有区分度 ⇒ 明确记下来。
	if before.Equal(after) {
		t.Log("提示：本机上 before/after 逐字相同（µs 对齐时钟）⇒ 区间断言此刻靠 Now() 自身带亚微秒尾的场合才有区分度")
	}
}
