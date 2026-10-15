package scheduledtask

// §253 同族：`truncateError` 原来按字节切（`msg[:max] + "..."`），
// 中文下产出**非法 UTF-8**。它的产物 `errMsg` 会**落库**
// （定时任务执行记录）并在移动端界面展示。
//
// ⚠ 与 wecom / kxmemory 那两道门是**三条独立的门**，
//   各自钉自己那个 helper —— 避免「一道绿了就以为三处都绿」。

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateErrorNeverSplitsCharacter(t *testing.T) {
	// ⚠⚠ 必须扫**多个内容长度**，不能只撞一个。
	//
	//   `truncateError` 的上限 `max` 是**编译期常量 4000**，
	//   而 `len(msg) > 4000` 这个判断让**切点固定落在 4000** ——
	//   内容变了，长度变了，**切点没变**。
	//   实测 7 种语料（纯中文/emoji/龘/甲乙丙…）在切点 4000 处
	//   **全都只需 0 或 1 字节回退** ⇒ 变异「回退上限 1 字节」碰巧够用，
	//   判据因此**不红** —— 它量的是「切点 4000 恰好只需 1 字节」这个
	//   **偶然事实**，不是「回退是否正确工作」这件事。
	//   （同族：前端 §245.5 的 N1 —— 断言钉在坐标上而不是性质上。）
	//
	//   ★★ pad 必须用**单字节 ASCII**：用汉字当 pad 时每次右移整数个 3 字节，
	//   切点形态只在同一相位里循环（实测 12 个汉字 pad 全落在「首字节」一种）。
	//   用 1 字节 pad 才能让切点逐字节滑动，扫遍「首字节 / 续字节 / 完整」三种形态。
	//
	//   ★★ 且**追加到尾部是无效的**：`msg[:4000]` 只看前 4000 字节，
	//   尾部追加的内容压根不在切片范围内（实测 8 个 pad 的切点末尾字节
	//   全是 `96b9e6`，形态一模一样）⇒ 扫了等于没扫。
	//   正解：**前缀 pad** —— 让内容整体右移，切点相对内容的落点随之变化。
	//   实测前缀 pad 会让切点在「首字节」与「续字节(切在中间)」之间摆动，
	//   而**「续字节」正是单字节回退救不回来的那种**（需要回退 2 字节）。
	for _, extra := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		msg := strings.Repeat("a", extra) + strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 1000)
		got := truncateError(errors.New(msg))
		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d 产物是非法 UTF-8（切在字符中间）\n  got 末尾=%q", extra, tail(got, 24))
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("pad=%d 产物含 U+FFFD\n  got 末尾=%q", extra, tail(got, 24))
		}
	}

	// emoji 同样扫一遍（星平面字符占 4 字节，切点情形与汉字不同）。
	for _, extra := range []int{0, 1, 2, 3, 4, 5} {
		msg := strings.Repeat("!", extra) + strings.Repeat("转写完成 ✅ 进度 OK🔥 下一步跟进联调。", 1000)
		got := truncateError(errors.New(msg))
		if !utf8.ValidString(got) {
			t.Fatalf("emoji pad=%d 产物是非法 UTF-8\n  got 末尾=%q", extra, tail(got, 24))
		}
	}
}

// 【量具自证 · 负控】ASCII 下**本来就不会**切坏；短串也根本不截断。
// 这两条证明门绿不是因为它对一切输入都绿，而是因为它在真正会切坏的输入上才红。
func TestTruncateErrorAsciiAndShort(t *testing.T) {
	ascii := strings.Repeat("upstream returned 500 ", 1000)
	got := truncateError(errors.New(ascii))
	if !utf8.ValidString(got) {
		t.Fatal("ASCII 产物竟非法 UTF-8 ⇒ 判据逻辑本身有问题")
	}
	if len(got) <= 4000 {
		t.Fatalf("ASCII 产物只有 %d 字节 ⇒ 截断没发生", len(got))
	}
	if short := truncateError(errors.New("短错误")); short != "短错误" {
		t.Fatalf("短于上限应原样返回，得到 %q", short)
	}
}

// 【量具自证】★ 必须证明**上面那组 pad 真的覆盖了不同切点情形**，
// 否则「扫了 12 个」只是看起来更全。
//
// 做法：数一数「不截断时原文本身在切点处是否合法」的分布 ——
// 若 12 个 pad 全都落在同一种情形上，这道门仍然只量了一个坐标。
func TestTruncateErrorPadsCoverDistinctCutShapes(t *testing.T) {
	// 直接对**无回退**的切点形态分类：切点处末尾字节属于哪一类。
	shapes := map[string]int{}
	for _, extra := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		msg := strings.Repeat("a", extra) + strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 1000)
		b := []byte(msg)[:4000]
		shapes[tailShape(b)]++
	}
	if len(shapes) < 2 {
		t.Fatalf("12 个 pad 只落在 %d 种切点形态上（%v）⇒ 这道门仍只量了一个偶然坐标，"+
			"变异「回退上限 1 字节」会碰巧够用", len(shapes), shapes)
	}
	t.Logf("12 个 pad 覆盖 %d 种切点形态：%v", len(shapes), shapes)
}

// tailShape 给切点处的字节形态分类：
// "完整"（切在字符边界）/ "首字节"（切掉后续字节）/ "续字节"（切在字符中间）。
func tailShape(b []byte) string {
	last := b[len(b)-1]
	if last < 0x80 {
		return "完整(ASCII)"
	}
	if last&0xC0 == 0x80 {
		return "续字节(切在中间)"
	}
	return "首字节(切掉后续)"
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// 【量具自证】语料必须真的超过上限 4000，否则门量不到截断。
func TestTruncateErrorCorpusActuallyTruncates(t *testing.T) {
	msg := strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 1000)
	if len(msg) <= 4000 {
		t.Fatalf("语料只有 %d 字节，没超过上限 4000 ⇒ 门量不到截断", len(msg))
	}
	got := truncateError(errors.New(msg))
	if len(got) <= 4000 {
		t.Fatalf("产物只有 %d 字节 ⇒ 截断没发生", len(got))
	}
	t.Logf("语料 %d 字节 → 产物 %d 字节（真的截断了）", len(msg), len(got))
}

// nil 必须仍返回空串（这条是行为，不是本次新增的）。
func TestTruncateErrorNil(t *testing.T) {
	if got := truncateError(nil); got != "" {
		t.Fatalf("nil 应返回空串，得到 %q", got)
	}
}
