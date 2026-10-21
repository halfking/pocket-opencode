package rss

// read_limited_test.go — §952：把「上限由**调用方**给」这件事**钉成可执行的证明**。
//
// ⚠ 为什么这条测试存在：
//
//	§952 的卡口把「上限不是编译期常量」判成**无界**，于是
//	`readLimited(r, max)`（上限是形参）必须登记在台账里。
//	⇒⇒⇒ 但台账说「上限由调用方给」只是一句**约定**
//	⇒⇒⇒ ⇒ 必须有**行为**证明「上限 ≤ 0 时**失败关闭**」，
//	　　 而且「调用方给的确实 > 0」。
//
// ⚠ 三条边界（§951 的教训：时间/大小这类上界，断言必须**双边**）：
//
//	① 恰好等于上限 ⇒ **成功**（不许误拒正常载荷）；
//	② 超过一个字节 ⇒ **失败**（不许放过超限载荷）；
//	③ 上限 ≤ 0 ⇒ **失败**（不许「读 0 字节也算成功」）。

import (
	"strings"
	"testing"
)

// TestReadLimitedAcceptsExactlyAtLimit 钉住①：恰好等于上限必须成功。
func TestReadLimitedAcceptsExactlyAtLimit(t *testing.T) {
	for _, n := range []int{0, 1, 10, 4096} {
		got, err := readLimited(strings.NewReader(strings.Repeat("a", n)), int64(n))
		if err != nil {
			t.Errorf("readLimited(%d 字节, max=%d) 返回错误 %v\n"+
				"⇒ 载荷**恰好等于**上限时必须成功 ⇒ 否则正常载荷被误拒", n, n, err)
			continue
		}
		if len(got) != n {
			t.Errorf("readLimited(%d 字节, max=%d) 读到 %d 字节（期望 %d）", n, n, len(got), n)
		}
	}
}

// TestReadLimitedRejectsOverLimit 钉住②：超过上限必须失败。
func TestReadLimitedRejectsOverLimit(t *testing.T) {
	for _, n := range []int{1, 10, 4096} {
		_, err := readLimited(strings.NewReader(strings.Repeat("a", n+1)), int64(n))
		if err == nil {
			t.Errorf("readLimited(%d 字节, max=%d) 成功了\n"+
				"⇒ 超限必须失败 ⇒ 否则「整个读进内存」这件事没被上界管住", n+1, n)
		}
	}
}

// TestReadLimitedFailsClosedOnNonPositiveLimit 钉住③：上限 ≤ 0 必须**失败关闭**。
//
// ⚠ 这是台账里「上限由调用方给」那条的**可执行证明**：
//
//	若上限 ≤ 0 也能「成功读回 0 字节」，那么「调用方忘了配上限」
//	就会变成**静默地什么都不读** ⇒⇒⇒ ⇒ 必须在台账里钉死「失败关闭」。
func TestReadLimitedFailsClosedOnNonPositiveLimit(t *testing.T) {
	for _, max := range []int64{0, -1, -4096} {
		got, err := readLimited(strings.NewReader("abc"), max)
		if err == nil {
			t.Errorf("readLimited(3 字节, max=%d) 成功了（读到 %d 字节）\n"+
				"⇒ 上限 ≤ 0 必须**失败关闭** ⇒ 否则「调用方忘了配上限」会变成"+
				"「静默地什么都不读」", max, len(got))
		}
	}
}

// TestSetMaxBodyBytesIgnoresNonPositive 钉住「可配置上限恒为正」。
//
// ⚠ §952 台账里 `fetcher.go:91` 的上限来自 `f.maxBodyBytes`（不是常量）
// ⇒⇒⇒ ⇒ 它的「恒为正」必须由**写入侧**保证。
func TestSetMaxBodyBytesIgnoresNonPositive(t *testing.T) {
	f := NewFetcher(nil, nil)
	if f.maxBodyBytes != DefaultMaxBodyBytes {
		t.Fatalf("NewFetcher 的 maxBodyBytes = %d（期望 %d）", f.maxBodyBytes, DefaultMaxBodyBytes)
	}
	for _, n := range []int64{0, -1} {
		f.SetMaxBodyBytes(n)
		if f.maxBodyBytes != DefaultMaxBodyBytes {
			t.Errorf("SetMaxBodyBytes(%d) 之后 maxBodyBytes = %d（期望保持 %d）\n"+
				"⇒ 非正的上限必须被**忽略** ⇒ 否则「上限恒为正」这个台账理由就不成立",
				n, f.maxBodyBytes, DefaultMaxBodyBytes)
		}
	}
	f.SetMaxBodyBytes(1024)
	if f.maxBodyBytes != 1024 {
		t.Errorf("SetMaxBodyBytes(1024) 之后 maxBodyBytes = %d（期望 1024）", f.maxBodyBytes)
	}
}
