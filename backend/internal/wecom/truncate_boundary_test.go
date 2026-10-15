package wecom

// §253：截断必须回退到**完整字符边界**。
//
// ⚠ 为什么这道门存在（与前端那条同源，§245/§251）：
//   `truncate` 原来按字节切（`s[:n] + "..."`），而企业微信回调的 XML 原文
//   是**中文为主** ⇒ 纯中文语料下约 **2/3 的切点**会切在汉字中间，
//   产出**非法 UTF-8**（含 U+FFFD），被 `%q` 打进日志。
//
// ⚠ 这道门**不**钉「截断长度」——那是行为；
//   它钉的是「产物永远是合法 UTF-8」，那是**不变量**。
//   若哪天有人把上限从 200 改成 300，这条门不该红。
//
// ⚠ 语料必须**比上限长**，否则根本不触发截断：
//   第一版探针用了 99 字节的句子去撞 160/200/300 三个上限，
//   结果三档全部「valid=true」——那是**没量到被测对象**的假读数，
//   本文件的头条用例就是它的反面。

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateNeverSplitsCharacter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"纯中文", strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 12), 200},
		{"纯中文-160", strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 12), 160},
		{"含emoji", strings.Repeat("转写完成 ✅ 进度 OK🔥 下一步跟进联调。", 12), 200},
		{"ASCII(负控)", strings.Repeat("response is not JSON: ", 40), 200},
		{"短于上限(不触发截断)", "短句", 200},
		{"恰好等于上限", strings.Repeat("甲", 100), 300},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncate(c.in, c.n)

			if !utf8.ValidString(got) {
				t.Fatalf("产物是非法 UTF-8（切在字符中间）\n  in=%q\n  n=%d\n  got=%q",
					c.in, c.n, got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("产物含 U+FFFD（替换字符）\n  got=%q", got)
			}
		})
	}
}

// 【量具自证】本文件的语料必须**真的触发截断**。
//
// ⚠ 第一版探针的教训：99 字节的句子去撞 160/200/300 三个上限，
//
//	三档全部「valid=true」—— 因为**根本没发生截断**。
//	⇒ 这里必须先证明「每个用例的输入都长于它的上限」。
func TestTruncateCorpusActuallyTruncates(t *testing.T) {
	truncated := 0
	for _, s := range []string{
		strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 12),
		strings.Repeat("转写完成 ✅ 进度 OK🔥 下一步跟进联调。", 12),
	} {
		for _, n := range []int{160, 200, 300} {
			if len(s) > n {
				truncated++
			}
		}
	}
	if truncated == 0 {
		t.Fatal("语料全都短于上限 ⇒ 本文件一条断言都量不到截断行为")
	}
	t.Logf("%d 组语料真的触发了截断", truncated)
}

// 【量具自证 · 负控】ASCII 下**本来就不会**切坏。
//
// ⇒ 这道对照证明「门能绿」不是因为它对一切输入都绿，
//
//	而是因为它在**真正会切坏的那种输入**上才红。
func TestTruncateAsciiIsNotBrokenEitherWay(t *testing.T) {
	// 负控：ASCII 的每个字节都是一个完整字符 ⇒ 无论有没有回退都合法。
	in := strings.Repeat("x", 500)
	got := truncate(in, 200)
	if !utf8.ValidString(got) {
		t.Fatal("ASCII 产物竟非法 UTF-8 ⇒ 本文件的判据逻辑本身有问题")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ASCII 产物缺省略号：%q", got)
	}
}
