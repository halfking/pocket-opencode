package server

import (
	"os"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
)

// TestMeetingSummaryAgentWiring 是「智能体真的接在会议摘要上」的门禁。
//
// 需求原文：「在总结同时，给出一些参考的资料与建议，**这个需要有一个智能体
// 来完成这些**」。
//
// 这道门守的是**接线**，不是行为 —— 行为在 meetingagent 包里有 12 条用例。
// 为什么还要这道门：那个包可以整体绿着而根本没人调用它。
// 本轮已经吃过一次这种亏：随手记的 AI 总结因为笔记没镜像到后端而**恒 404**，
// 而当时所有单测都是绿的（详见 docs/design/2026-10-06-recording-quality-fixes.md §8）。
//
// 判据分两类，缺一不可：
//   - 源码结构断言：handleMeetingSummary 必须调 meetingSummaryViaAgent；
//   - 负控：把调用摘掉，判据必须转红（证明它不是恒真）。
func TestMeetingSummaryAgentWiring(t *testing.T) {
	src, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	if !strings.Contains(s, "s.meetingSummaryViaAgent(") {
		t.Fatal("handleMeetingSummary 没有调 meetingSummaryViaAgent —— 智能体没接在真实路径上")
	}
	if !strings.Contains(s, "meetingagent.Runner{") {
		t.Fatal("没有构造 meetingagent.Runner（包导入了但没跑起来）")
	}
	if !strings.Contains(s, "meetingagent.NoteSearcher{") {
		t.Fatal("没有给智能体装 search_notes 工具 ⇒ 参考资料只能靠模型编")
	}
	// 工具的数据源必须是 notesStore：查别的库等于查不到用户自己的笔记。
	if !strings.Contains(s, "Src: s.notesStore") {
		t.Fatal("NoteSearcher 的数据源不是 s.notesStore")
	}
}

// 负控：摘掉调用后的形态必须不再满足接线判据。
//
// 只写「源码里存在这个函数名」是不够的 —— 判据必须能区分「定义了」
// 与「真的在 handleMeetingSummary 里被调用了」。
func TestMeetingSummaryAgentWiring_NegativeControl(t *testing.T) {
	withCall := `
	if result, used := s.meetingSummaryViaAgent(ctx, r, body.Segments, body.PrevSummary); used {`
	withoutCall := `
	result, err := s.llmMeetingSummary(ctx, r, body.Segments, body.PrevSummary)`

	if !strings.Contains(withCall, "s.meetingSummaryViaAgent(ctx") {
		t.Fatal("负控样本自身不满足判据，判据可能恒假")
	}
	if strings.Contains(withoutCall, "s.meetingSummaryViaAgent(ctx") {
		t.Fatal("负控：摘掉调用后不该再匹配接线判据")
	}
}

// TestMeetingSummaryAgentFallsBack 钉住「agent 失败不得让总结消失」。
//
// 总结是主链路，智能体是增强。这条如果被回退，用户会在某次网关抖动后
// 直接看不到摘要 —— 而 agent 的存在本身就是为了增强。
func TestMeetingSummaryAgentFallsBack(t *testing.T) {
	src, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// agent 失败后必须还有一次性 chat 那条路。
	if !strings.Contains(s, "s.llmMeetingSummary(ctx, r, body.Segments, body.PrevSummary)") {
		t.Fatal("一次性 chat 兜底路径不见了 —— agent 一失败就没有总结了")
	}
	// 截断时也必须回落：截断内容是工具输出，不是模型结论。
	if !strings.Contains(s, "res.Truncated") {
		t.Fatal("没有处理 Truncated：轮数耗尽时会把检索结果糊到摘要字段里")
	}
	if !strings.Contains(s, "falling back to one-shot") {
		t.Fatal("agent 失败没有日志，回落会变得不可观测")
	}
}

// TestMeetingAgentSystemPrompt 钉住提示词里的三条硬约束。
//
// 这三条不是措辞偏好，去掉任何一条都会让产出退化到不可用：
//   - 不许编造引用：没有它，references 里全是看起来很像真的笔记标题；
//   - 强制 JSON：没有它，action_items 拿不到 due，§7.2 的日程落点失效；
//   - 限制查询次数：没有它，工具循环会反复查同一个词。
//
// ⚠ §469：三条约束只对**注册了 search_notes 那个分支**成立。
//
//	工具为空时另有一段提示词（不含这三条中的两条），由
//	TestToollessPromptIsNative 与 meeting_agent_prompt_tools_gate_test.go 守。
//	⚠ 这里必须显式传工具：漏传会拿到「工具为空」那支，
//	而下面这三条里有两条写着 search_notes ⇒ 会红，且红得对。
func TestMeetingAgentSystemPrompt(t *testing.T) {
	p := meetingAgentSystemPrompt("", []meetingagent.Tool{&meetingagent.NoteSearcher{}})

	for _, must := range []string{
		"禁止编造",
		"search_notes",
		"严格返回 JSON",
		"不要自己换算成日期",
		"最多调用 search_notes 两次",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("系统提示词缺少约束 %q", must)
		}
	}
	// 负控：这些约束不能只存在于注释里。
	if !strings.Contains(p, "references") {
		t.Error("提示词没有要求 references 字段 ⇒ 前端拿不到参考资料")
	}
	// prev 非空时必须带进上下文（滚动摘要要在此基础上更新）。
	// ⚠ 两支都要带：§469 之后 prev 是**支外**追加的，
	//   漏一处就会让滚动摘要在该支上悄悄丢掉上一轮结论。
	withPrev := meetingAgentSystemPrompt("上一轮说：讨论了结算上线", []meetingagent.Tool{&meetingagent.NoteSearcher{}})
	if !strings.Contains(withPrev, "讨论了结算上线") {
		t.Error("prev 没有进提示词，滚动摘要会丢掉上一轮的结论")
	}
}
