package meetingagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// fakeChat 按脚本逐轮返回 Delta。它记录了每轮收到的 messages —— 断言
// 「第二轮真的带上了 tool 消息」只能靠这个，不能靠最终 Content 反推。
type fakeChat struct {
	turns [][]llmbff.Delta // 每个元素是一轮要吐出的帧序列
	// seen 记录每轮收到的消息（用于断言协议正确性）
	seen [][]llmbff.Message
	err  error
}

func (f *fakeChat) call(ctx context.Context, req llmbff.ChatRequest, kind string, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.seen = append(f.seen, append([]llmbff.Message(nil), req.Messages...))
	i := len(f.seen) - 1
	if i >= len(f.turns) {
		// 脚本用尽：**继续吐工具调用**，而不是安静地返回空终态。
		//
		// 为什么必须这样：若这里返回无工具的 Done 帧，循环会立刻收尾，
		// 于是「把轮数上限改成 9999」这个变异体也会通过本测试 ——
		// 判据量的其实是「假件自己停了」，不是「实现有界」。
		// ⇒ 让假件**永不满足停止条件**，逼实现自己兜底。
		fn(tcDelta("call_0_pad", "search_notes", `{"query":"pad"}`))
		return &llmbff.Usage{}, nil
	}
	for _, d := range f.turns[i] {
		if !fn(d) {
			break
		}
	}
	return &llmbff.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil
}

func tcDelta(id, name, args string) llmbff.Delta {
	return llmbff.Delta{
		ToolCalls: []llmbff.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: llmbff.ToolCallFunc{Name: name, Arguments: args},
		}},
	}
}

// 用具：记录调用参数的可测工具。
type fakeTool struct {
	name  string
	calls []string
	out   string
	err   error
}

func (f *fakeTool) Definition() llmbff.Tool {
	return llmbff.Tool{Type: "function", Function: llmbff.ToolFunction{Name: f.name}}
}
func (f *fakeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	f.calls = append(f.calls, string(args))
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func newRunner(chat *fakeChat) *Runner {
	return &Runner{Chat: chat.call, Kind: "meeting_agent", MaxTurns: 3}
}

// TestAgent_CallsToolThenSummarizes 是这道门的核心：
// 模型第一轮要求查资料，第二轮才给总结。
func TestAgent_CallsToolThenSummarizes(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{tcDelta("call_0_a", "search_notes", `{"query":"结算"}`)},
		{{Content: `{"summary":"讨论了结算上线"}`, Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", out: "与「结算」相关的笔记 1 条：\n1. [n1] 结算项目"}

	res, err := newRunner(chat).Run(context.Background(), "sys", "转写内容", []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != `{"summary":"讨论了结算上线"}` {
		t.Fatalf("content=%q", res.Content)
	}
	if res.Turns != 2 {
		t.Fatalf("期望 2 轮，实际 %d", res.Turns)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("工具应被调用 1 次，实际 %d", len(tool.calls))
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0] != "search_notes" {
		t.Fatalf("ToolCalls=%v", res.ToolCalls)
	}
	// ★ 协议断言：第二轮的消息里必须带 tool 角色的结果，否则模型看不到
	// 自己查到了什么 —— 这正是「参考资料」成立的前提。
	if len(chat.seen) < 2 {
		t.Fatal("没有第二轮")
	}
	var sawTool, sawAssistant bool
	for _, m := range chat.seen[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "结算项目") {
			sawTool = true
		}
		if m.Role == llmbff.RoleAssistant && len(m.ToolCalls) == 1 {
			sawAssistant = true
		}
	}
	if !sawTool {
		t.Fatalf("第二轮缺少 tool 结果消息：%+v", chat.seen[1])
	}
	if !sawAssistant {
		t.Fatalf("第二轮缺少携带 tool_calls 的 assistant 消息（OpenAI 协议要求）")
	}
}

// TestAgent_MergesIncrementalToolCallFragments 钉住增量分片合并。
//
// OpenAI 的 tool_call 是**分片**下发的：同一个调用被拆成多帧，每帧只有
// index + 一段 arguments。若按「覆盖」而不是「拼接」实现，参数会变成半截
// JSON，工具解析失败 —— 而模型明明给对了。
func TestAgent_MergesIncrementalToolCallFragments(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{
			{Done: false, ToolCalls: []llmbff.ToolCall{{
				ID: "call_0_", Type: "function",
				Function: llmbff.ToolCallFunc{Name: "search_", Arguments: `{"que`},
			}}},
			{Done: false, ToolCalls: []llmbff.ToolCall{{
				ID: "0_abc", Type: "function",
				Function: llmbff.ToolCallFunc{Name: "notes", Arguments: `ry":"结算"}`},
			}}},
		},
		{{Content: "最终结论", Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", out: "命中"}

	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("工具应被调用 1 次，实际 %d", len(tool.calls))
	}
	// 拼接后必须是完整 JSON，且 name 也拼回了 search_notes。
	if tool.calls[0] != `{"query":"结算"}` {
		t.Fatalf("分片未正确拼接：%q", tool.calls[0])
	}
	if res.Content != "最终结论" {
		t.Fatalf("content=%q", res.Content)
	}
}

// TestAgent_KeepsDistinctToolCallsSeparate 钉住多个工具调用不被合并成一条。
func TestAgent_KeepsDistinctToolCallsSeparate(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{
			{Done: false, ToolCalls: []llmbff.ToolCall{{
				Index: 0, ID: "call_5zy4c6vk9eds203sq9miyn97", Type: "function",
				Function: llmbff.ToolCallFunc{Name: "search_notes", Arguments: `{"query":"A"}`},
			}}},
			{Done: false, ToolCalls: []llmbff.ToolCall{{
				Index: 1, ID: "call_gxgypmgx0s5ie8asaf32oqzm", Type: "function",
				Function: llmbff.ToolCallFunc{Name: "search_notes", Arguments: `{"query":"B"}`},
			}}},
		},
		{{Content: "结论", Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", out: "命中"}

	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.calls) != 2 {
		t.Fatalf("两个工具调用应各执行一次，实际 %d：%v", len(tool.calls), tool.calls)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("ToolCalls 记录数=%d", len(res.ToolCalls))
	}
}

// TestAgent_ToolFailureDoesNotKillLoop 工具失败不终止循环。
func TestAgent_ToolFailureDoesNotKillLoop(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{tcDelta("call_0_a", "search_notes", `{"query":"x"}`)},
		{{Content: "仍然给出了总结", Done: true}},
	}}
	tool := &fakeTool{name: "search_notes", err: errors.New("PG 不可达")}

	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool})
	if err != nil {
		t.Fatalf("工具失败不应让整个 Run 报错：%v", err)
	}
	if res.Content != "仍然给出了总结" {
		t.Fatalf("content=%q", res.Content)
	}
	// 失败信息必须回给模型，否则它会以为查到了空结果。
	found := false
	for _, m := range chat.seen[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "PG 不可达") {
			found = true
		}
	}
	if !found {
		t.Fatalf("工具失败原因没有回给模型：%+v", chat.seen[1])
	}
}

// TestAgent_BoundedByMaxTurns 钉住「有界」这条硬约束。
func TestAgent_BoundedByMaxTurns(t *testing.T) {
	// 模型每一轮都调工具，永不收尾。
	var frames [][]llmbff.Delta
	for i := 0; i < 10; i++ {
		frames = append(frames, []llmbff.Delta{tcDelta("call_0_a", "search_notes", `{"query":"q"}`)})
	}
	chat := &fakeChat{turns: frames}
	tool := &fakeTool{name: "search_notes", out: "工具输出内容"}

	r := newRunner(chat)
	r.MaxTurns = 2
	res, err := r.Run(context.Background(), "sys", "转写", []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Fatalf("期望停在 2 轮，实际 %d（无界循环）", res.Turns)
	}
	if !res.Truncated {
		t.Fatal("到达上限必须标记 Truncated，调用方要知道这不是模型结论")
	}
	if res.Content != "工具输出内容" {
		t.Fatalf("截断时应把工具输出当结果，content=%q", res.Content)
	}
}

// TestAgent_NoToolsBehavesLikeOneShot 无工具时退化成一次性 chat。
func TestAgent_NoToolsBehavesLikeOneShot(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{{{Content: "直接总结", Done: true}}}}
	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "直接总结" || res.Turns != 1 {
		t.Fatalf("content=%q turns=%d", res.Content, res.Turns)
	}
}

// TestAgent_UnknownToolIsReportedNotPanic 未知工具名回给模型而不是崩。
func TestAgent_UnknownToolIsReportedNotPanic(t *testing.T) {
	chat := &fakeChat{turns: [][]llmbff.Delta{
		{tcDelta("call_0_a", "不存在的工具", `{}`)},
		{{Content: "收到", Done: true}},
	}}
	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", nil)
	if err != nil {
		t.Fatalf("未知工具不应让 Run 报错：%v", err)
	}
	if res.Content != "收到" {
		t.Fatalf("content=%q", res.Content)
	}
}

// ---- search_notes 工具的行为 ----

type fakeNotes []notes.Note

func (f fakeNotes) ListScoped(_ context.Context, _, _, _ string) ([]notes.Note, error) {
	return f, nil
}

func TestNoteSearcher_RanksTitleAboveSnippet(t *testing.T) {
	src := fakeNotes{
		{ID: "n1", Title: "无关", Snippet: "这里提到结算流程"},
		{ID: "n2", Title: "结算系统重构", Snippet: "正文没有那个词"},
		{ID: "n3", Title: "无关", Snippet: "无关"},
	}
	s := &NoteSearcher{Src: src}
	out, err := s.Execute(context.Background(), json.RawMessage(`{"query":"结算"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "结算系统重构") {
		t.Fatalf("没检索到标题命中的那条：%s", out)
	}
	// 标题命中必须排在摘要命中之前。
	if strings.Index(out, "n2") > strings.Index(out, "n1") {
		t.Fatalf("标题命中应排在摘要命中前：%s", out)
	}
	if strings.Contains(out, "n3") {
		t.Fatalf("零命中的笔记不该出现：%s", out)
	}
}

func TestNoteSearcher_NoMatchSaysSo(t *testing.T) {
	s := &NoteSearcher{Src: fakeNotes{{ID: "n1", Title: "甲", Snippet: "乙"}}}
	out, err := s.Execute(context.Background(), json.RawMessage(`{"query":"完全不存在"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "没有找到") {
		t.Fatalf("无命中时应如实说没有：%s", out)
	}
}

// 负控：空查询不得被硬解成「查全部」。
func TestNoteSearcher_EmptyQueryIsNotEverything(t *testing.T) {
	s := &NoteSearcher{Src: fakeNotes{{ID: "n1", Title: "甲", Snippet: "乙"}}}
	for _, q := range []string{`{"query":""}`, `{"query":"   "}`, `{}`} {
		out, err := s.Execute(context.Background(), json.RawMessage(q))
		if err != nil {
			t.Fatalf("%s 不该报错：%v", q, err)
		}
		if strings.Contains(out, "n1") {
			t.Fatalf("空查询返回了全部笔记（信息量爆炸）：%s", out)
		}
	}
}

func TestNoteSearcher_RespectsResultCap(t *testing.T) {
	var many fakeNotes
	for i := 0; i < 30; i++ {
		many = append(many, notes.Note{ID: string(rune('a' + i%26)), Title: "结算相关", Snippet: "x"})
	}
	s := &NoteSearcher{Src: many}
	out, err := s.Execute(context.Background(), json.RawMessage(`{"query":"结算"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "结算相关") > maxToolResults {
		t.Fatalf("返回条数超过上限 %d", maxToolResults)
	}
}

func TestParseTags_SurvivesBadJSON(t *testing.T) {
	if got := parseTags(`["a","b"]`); len(got) != 2 {
		t.Fatalf("合法 tags 解析错：%v", got)
	}
	// 负控：坏 JSON 必须返回空而不是 panic（一个坏 tag 不该毁掉整次检索）。
	if got := parseTags(`{broken`); got != nil {
		t.Fatalf("坏 JSON 应返回 nil：%v", got)
	}
	if got := parseTags(""); got != nil {
		t.Fatalf("空串应返回 nil：%v", got)
	}
}

// toolCallIndex 直接用协议自带的 index。
//
// ⚠ 夹具形状必须照抄真网关（2026-10-06 实测抓包，llmgo.kxpms.cn）：
// id 是 `call_<hex>`，**没有**数字下标；分组靠 wire 上的 index 字段。
//
// 旧实现从 id 字符串里猜（"call_0_abc" 取中间那段），旧夹具也正好写成
// `call_0_a`/`call_1_b` —— 于是「真实 id 没有数字段」这个事实从未进过
// 任何用例，缺陷在每一个夹具下都是隐形的。
func TestToolCallIndexUsesWireIndex(t *testing.T) {
	cases := []struct {
		name string
		tc   llmbff.ToolCall
		want int
	}{
		{"真网关第 1 个并发调用", llmbff.ToolCall{ID: "call_5zy4c6vk9eds203sq9miyn97", Index: 0}, 0},
		{"真网关第 2 个并发调用", llmbff.ToolCall{ID: "call_gxgypmgx0s5ie8asaf32oqzm", Index: 1}, 1},
		// 非流式响应不带 index，一轮只有一个调用 ⇒ 0 是对的。
		{"非流式单调用缺省 0", llmbff.ToolCall{ID: "call_abc"}, 0},
	}
	for _, c := range cases {
		if got := toolCallIndex(c.tc); got != c.want {
			t.Errorf("%s：toolCallIndex=%d，期望 %d", c.name, got, c.want)
		}
	}
}

// TestAgent_ReplaysRealGatewayParallelToolCalls 回放 2026-10-06 在
// llmgo.kxpms.cn 上实测抓到的**原始分片**，逐字照抄。
//
// 这道门存在的理由：此前 37 处变异全绿，而生产上 agent 每轮都坏。
// 根因是「一轮里并发两个工具调用」这个形态**在所有夹具里都不存在**
// ——夹具每轮只发一个调用。于是旧实现（从 id 猜下标）在夹具下恒成立，
// 在真网关上必然失败。
//
// 抓包形态（model=deepseek-v4-flash）：
//
//	idx0  id=call_5zy4…  name=search_notes  args=""
//
//	idx0  name=""  args='{"query": "'        ← 逐字增量
//
//	idx0  name=""  args=北极 …
//
//	idx0  name=""  args='}'
//
//	idx1  id=call_gxg…  name=search_notes    ← 并发的第二个调用
//
//	idx1  name=""  args='{"query": "' …
//
//	idx1  name=""  args='}'
func TestAgent_ReplaysRealGatewayParallelToolCalls(t *testing.T) {
	const (
		fragOpen  = "{\"query\": \""
		fragQuote = "\""
		fragBrace = "}"
	)
	frames := []llmbff.Delta{
		{ToolCalls: []llmbff.ToolCall{{Index: 0, ID: "call_5zy4c6vk9eds203sq9miyn97", Type: "function",
			Function: llmbff.ToolCallFunc{Name: "search_notes"}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 0, Function: llmbff.ToolCallFunc{Arguments: fragOpen}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 0, Function: llmbff.ToolCallFunc{Arguments: "北极"}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 0, Function: llmbff.ToolCallFunc{Arguments: "星项目续期合同"}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 0, Function: llmbff.ToolCallFunc{Arguments: fragQuote}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 0, Function: llmbff.ToolCallFunc{Arguments: fragBrace}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 1, ID: "call_gxgypmgx0s5ie8asaf32oqzm", Type: "function",
			Function: llmbff.ToolCallFunc{Name: "search_notes"}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 1, Function: llmbff.ToolCallFunc{Arguments: fragOpen}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 1, Function: llmbff.ToolCallFunc{Arguments: "评审会客户会议室"}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 1, Function: llmbff.ToolCallFunc{Arguments: fragQuote}}}},
		{ToolCalls: []llmbff.ToolCall{{Index: 1, Function: llmbff.ToolCallFunc{Arguments: fragBrace}}}},
		{Done: true},
	}
	chat := &fakeChat{turns: [][]llmbff.Delta{frames, {{Content: "结论", Done: true}}}}
	tool := &fakeTool{name: "search_notes", out: "命中"}

	res, err := newRunner(chat).Run(context.Background(), "sys", "转写", []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}

	// 核心断言：**执行到的工具名都干净**。
	//
	// 旧实现下第二个调用的名字是 "search_notessearch_notes" ⇒ execOne
	// 走「未知工具」分支，但 res.ToolCalls 记的是**调用时的名字**，
	// 所以坏名字在这里直接现形。
	if len(res.ToolCalls) != 2 {
		t.Fatalf("执行的工具调用数=%d，期望 2：%v", len(res.ToolCalls), res.ToolCalls)
	}
	for i, got := range res.ToolCalls {
		if got != "search_notes" {
			t.Fatalf("第 %d 个调用的工具名=%q，期望 search_notes（并发调用被当成增量分片了）", i, got)
		}
	}
	// 参数也必须各自是**一个合法 JSON**，而不是两个对象拼在一起。
	wantArgs := []string{"{\"query\": \"北极星项目续期合同\"}", "{\"query\": \"评审会客户会议室\"}"}
	if len(tool.calls) != 2 {
		t.Fatalf("工具执行次数=%d，期望 2：%v", len(tool.calls), tool.calls)
	}
	for i, w := range wantArgs {
		if tool.calls[i] != w {
			t.Errorf("第 %d 个调用的参数=%q，期望 %q", i, tool.calls[i], w)
		}
		var probe map[string]string
		if err := json.Unmarshal([]byte(tool.calls[i]), &probe); err != nil {
			t.Errorf("第 %d 个调用的参数不是合法 JSON：%v", i, err)
		}
	}
	if res.Truncated {
		t.Error("不该被截断：两轮足够给出结论")
	}
}
