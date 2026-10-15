package notes

import (
	"strings"
	"testing"
)

// TestNoteBodyForLLM 钉住「完整正文真的拿得到」这条能力。
//
// 背景（2026-10-06）：DB 的 `content` 列一直是 TEXT 且无长度限制，
// 但 Note 结构里**没有**对应字段，GetByIDScoped 读出来两行都往
// n.Snippet 写 —— content 被 snippet 覆盖掉。
// ⇒ 全仓拿不到超过 200 字的笔记正文，两处功能被削到只剩开头：
//  1. handleNoteSummarize 把它当 LLM 输入 ⇒ 长语音笔记的行动项
//     只能从前 200 字抽；
//  2. meetingagent 的 search_notes ⇒ agent 检索不到笔记正文。
func TestNoteBodyForLLM(t *testing.T) {
	long := strings.Repeat("结算流程的讨论。", 200) // 1400 字，远超 200
	n := Note{Content: long, Snippet: long[:100]}

	got := n.BodyForLLM(0)
	if got != long {
		t.Fatalf("maxRunes=0 应返回完整正文，实际长度 %d（期望 %d）", len([]rune(got)), len([]rune(long)))
	}
	if len([]rune(got)) <= 200 {
		t.Fatalf("拿到的正文只有 %d 字 —— 这正是本轮要修的那个上限", len([]rune(got)))
	}
}

func TestNoteBodyForLLM_TruncatesByRuneNotByte(t *testing.T) {
	// 纯中文：一字 3 字节。按字节切会切在字的中间 → 非法 UTF-8。
	n := Note{Content: strings.Repeat("中", 500)}
	got := n.BodyForLLM(100)
	if !isValidUTF8(got) {
		t.Fatal("截断结果非法 UTF-8（按字节切了）")
	}
	if len([]rune(got)) != 100 {
		t.Fatalf("期望 100 个字，实际 %d", len([]rune(got)))
	}
}

// content 为空时必须回落到 snippet —— 存量行里 content 可能仍是空的。
func TestNoteBodyForLLM_FallsBackToSnippet(t *testing.T) {
	n := Note{Content: "", Snippet: "只有摘要"}
	if got := n.BodyForLLM(0); got != "只有摘要" {
		t.Fatalf("content 为空应回落到 snippet，实际 %q", got)
	}
	// 空白串也算空：否则会拿一堆空格去喂 LLM。
	n2 := Note{Content: "   \n ", Snippet: "真摘要"}
	if got := n2.BodyForLLM(0); got != "真摘要" {
		t.Fatalf("纯空白 content 应回落到 snippet，实际 %q", got)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
