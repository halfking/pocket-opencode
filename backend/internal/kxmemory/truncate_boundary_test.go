package kxmemory

// §253 同族：`truncate` 原来按字节切（`s[:maxLen-3] + "..."`），
// 中文下产出**非法 UTF-8**（实测纯中文语料约 2/3 的切点会切在汉字中间）。
// 它的产物是 kxmemory 错误日志的一部分。
//
// ⚠ 与 `internal/wecom/truncate_boundary_test.go` 是**两条独立的门**，
//   不是同一份 —— 每个包各自钉自己那个 helper，
//   避免「一道门绿了就以为三处都绿」。

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
		{"纯中文-200", strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 12), 200},
		{"含emoji", strings.Repeat("转写完成 ✅ 进度 OK🔥 下一步跟进联调。", 12), 200},
		{"纯中文-40", strings.Repeat("会议纪要：请确认下周三的方案。", 12), 40},
		{"ASCII(负控)", strings.Repeat("upstream returned 500 ", 40), 200},
		{"短于上限", "短句", 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncate(c.in, c.n)
			if !utf8.ValidString(got) {
				t.Fatalf("产物是非法 UTF-8\n  n=%d\n  got=%q", c.n, got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("产物含 U+FFFD\n  got=%q", got)
			}
		})
	}
}

// 【量具自证】语料必须真的触发截断，否则本文件量不到任何东西。
func TestTruncateCorpusActuallyTruncates(t *testing.T) {
	n := 0
	for _, s := range []string{
		strings.Repeat("会议纪要：请确认下周三的方案与预算缺口。", 12),
		strings.Repeat("转写完成 ✅ 进度 OK🔥 下一步跟进联调。", 12),
	} {
		for _, lim := range []int{40, 200} {
			if len(s) > lim {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("语料全短于上限 ⇒ 门量不到截断行为")
	}
	t.Logf("%d 组语料真的触发了截断", n)
}
