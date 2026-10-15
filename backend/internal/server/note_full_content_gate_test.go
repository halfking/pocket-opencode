package server

import (
	"os"
	"strings"
	"testing"
)

// TestNoteContentNotDiscarded 钉住「content 列不许再被丢弃」。
//
// 2026-10-06 的缺陷：GetByIDScoped 里
//
//	if content.Valid { n.Snippet = content.String }
//	if snippet.Valid { n.Snippet = snippet.String }   ← 覆盖了上一行
//
// DB 的 content 列是 TEXT 无长度限制，却永远读不出来 ⇒ 长语音笔记的
// 行动项与 agent 检索都只剩开头 200 字。
//
// 这道门用**源码扫描**钉住：content 与 snippet 是**两列**，
// 任何一处再出现 `n.Snippet = content.String` 就是回退。
func TestNoteContentNotDiscarded(t *testing.T) {
	src, err := os.ReadFile("../notes/store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	if strings.Contains(s, "n.Snippet = content.String") {
		t.Error("store.go 仍在把 content 列写进 Snippet ⇒ 正文被丢弃（回退到 2026-10-06 之前）")
	}
	if !strings.Contains(s, "n.Content = content.String") {
		t.Error("store.go 没有把 content 列读进 n.Content")
	}
	// Upsert 必须真的写正文，而不是继续写 200 字的 snippet。
	if !strings.Contains(s, "content := n.Content") {
		t.Error("Upsert 没有用 n.Content 写 content 列（仍在写截断后的内容）")
	}
	if !strings.Contains(s, "content = n.Snippet") {
		t.Error("Upsert 缺少「Content 为空则回落到 Snippet」—— 老调用方只填 Snippet 时会撞 NOT NULL")
	}
}

// TestSummarizeUsesFullBody 钉住「总结必须喂完整正文」。
//
// ⚠ 2026-10-06 改过判据形态，理由记在这里免得日后又被改回去：
// 原判据是在 handleNoteSummarize 里扫「prompt 字面量 + BodyForLLM」，
// 前提是 prompt 在 handler 内联拼接。后来 prompt 抽成了
// buildNoteSummaryPrompt（为了让真网关探针复用**同一份**提示词，
// 而不是探针里手抄一份会漂移的副本），原判据就必然假红。
//
// 只把锚点挪个位置是不够的——那仍然是纯源码扫描，只证明「代码写在那」。
// 现在拆成两层：
//
//	(1) 接线断言：handler 确实把 found.BodyForLLM(...) 传进去了；
//	(2) 行为断言：真的调用 buildNoteSummaryPrompt，验证 >200 字之后的
//	    正文尾部逐字出现在提示词里，且 JSON 契约没丢。
//
// (2) 是变异测试真正能打红的那一层：把 body 参数换成 Snippet 语义
// （截断到 200 字），(1) 仍绿，(2) 转红。
func TestSummarizeUsesFullBody(t *testing.T) {
	src, err := os.ReadFile("server_assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	i := strings.Index(s, "func (s *Server) handleNoteSummarize")
	if i < 0 {
		t.Fatal("找不到 handleNoteSummarize")
	}
	fn := s[i:]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}

	// ---- (1) 接线 ----
	if !strings.Contains(fn, "buildNoteSummaryPrompt(found.BodyForLLM(") {
		t.Error("handleNoteSummarize 没有把 found.BodyForLLM(...) 传给 buildNoteSummaryPrompt ⇒ 仍只喂前 200 字")
	}
	// 负控：prompt 实参不许是 Snippet。
	//
	// 用调用形态而不是「窗口里有没有 Snippet」：同一个 handler 里
	// finance.NewRecognizer() 仍在用 found.Snippet，那是**自动记账**的
	// 独立关注点（金额识别要短文本，不该喂整篇）。窗口式扫描会把它误判红。
	if strings.Contains(fn, "buildNoteSummaryPrompt(found.Snippet") {
		t.Error("笔记总结的 prompt 实参仍是 found.Snippet ⇒ 只喂前 200 字")
	}

	// ---- (2) 行为：提示词真的带上了正文尾部 ----
	const tail = "正文尾部唯一标记ZZ7749"
	body := strings.Repeat("甲", 260) + tail // 260 > 200：落在 Snippet 截断之外
	got := buildNoteSummaryPrompt(body)

	if !strings.Contains(got, tail) {
		t.Error("buildNoteSummaryPrompt 的输出里没有正文尾部（>200 字部分被截断了）")
	}
	if !strings.Contains(got, "action_items") {
		t.Error("buildNoteSummaryPrompt 丢了 action_items 契约 ⇒ 随手记侧又拿不到期限")
	}
}

// TestAgentSearchesFullContent 钉住「agent 搜的是正文不是 200 字摘要」。
func TestAgentSearchesFullContent(t *testing.T) {
	src, err := os.ReadFile("../meetingagent/tools.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	if !strings.Contains(s, "n.BodyForLLM(0)") {
		t.Error("search_notes 没用 BodyForLLM ⇒ agent 搜不到笔记正文")
	}
	// 负控：命中判定必须至少看 Content，而不是只有 Snippet。
	score := s[strings.Index(s, "func scoreNotes"):]
	if !strings.Contains(score, "BodyForLLM") {
		t.Error("scoreNotes 没有基于正文打分")
	}
	// snippet 单独命中仍要保留（老行只有 snippet 有值）。
	if !strings.Contains(score, "n.Snippet") {
		t.Error("scoreNotes 完全不看 Snippet ⇒ 只有 snippet 的老行永远搜不到")
	}
}

// TestClassifyNoteUsesFullBody 钉住「分类也送完整正文」。
//
// 2026-10-06 顺带修的：classifyNoteAsync 原先送 note.Snippet，注释写着
// 「Note 只有 Snippet，完整内容在客户端」——那句话当时为真，本轮加了
// Content 字段后就是**过期事实**。注释与代码不一致本身就是缺陷：
// 日后有人照着它判断「正文拿不到」就不会再想着修。
func TestClassifyNoteUsesFullBody(t *testing.T) {
	src, err := os.ReadFile("server_assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	i := strings.Index(s, "func (s *Server) classifyNoteAsync")
	if i < 0 {
		t.Fatal("找不到 classifyNoteAsync")
	}
	fn := s[i : i+1200]
	if !strings.Contains(fn, "note.BodyForLLM(") {
		t.Error("classifyNoteAsync 没有用 BodyForLLM ⇒ kxmemory 只拿到 200 字")
	}
	// 负控：那条过期注释必须一起消失。
	if strings.Contains(fn, "Note 只有 Snippet，完整内容在客户端") {
		t.Error("classifyNoteAsync 里还留着「完整内容在客户端」这条**已过期**的事实性注释")
	}
}
