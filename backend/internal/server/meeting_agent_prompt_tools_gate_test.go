// meeting_agent_prompt_tools_gate_test.go 守 §469 这条不变量：
//
//	**提示词里提不提 search_notes，必须由本次实际注册的工具集推导。**
//
// ── 为什么需要这道门 ────────────────────────────────────────────────
//
// §328.7 用真网关坐实过：notesStore 为 nil 的部署（remote-only，
// main.go 只在 pool != nil 时赋值 notesStore）下，server_meeting.go 一个
// 工具都不注册，而旧提示词无条件宣传 search_notes ⇒ 模型去调用一个
// 不存在的工具，调用漏进正文。那批运行 9/12 在智能体层失败，
// 而 §109 的降级提示因 `agent != true` 为假**一句话都不说**。
//
// ⚠ 已有的接线门（server_meeting_agent_wiring_test.go）拦不住它：
//
//	那些判据全是「源码里出现过某个字面量」，
//	分不清「真的注册了」与「文件里出现过这句话」。
//
// ── 三条判据，缺一不可 ──────────────────────────────────────────────
//
//	T1 不变量（行为级）：任取工具集，提示词提及 search_notes ⇔ 工具集里有它。
//	T2 接线同源（源码级）：提示词拿到的必须是交给 runner 的**同一个** tools。
//	T3 前提坐实（行为级）：notesStore 为 nil ⇒ 工具集确为空。
//
// ⚠ T3 不是重复 T1：T1 只说「给空集就出工具为空提示词」，
//
//	而 §328 的另一半前提是「生产在 remote-only 下真的交出空集」。
//	那条前提过去只写在注释里，今天才第一次被机器检查。
package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/meetingagent"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// gateStubTool 是本门专用的**别的名字**的工具。
//
// ⚠ 为什么必须有它：只有 nil / NoteSearcher 两种工具集时，
//
//	「按名字认工具」与「看第一个元素是不是 NoteSearcher」读数完全一样 ——
//	后者就是 §469 明确否掉的实现，但它在真网关实验里是**绿的**。
//	加一个异名工具才让两种实现分道扬镳。
type gateStubTool struct{ name string }

func (t gateStubTool) Definition() llmbff.Tool {
	return llmbff.Tool{Type: "function", Function: llmbff.ToolFunction{Name: t.name}}
}
func (t gateStubTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }

// gateWrapTool 模拟探针里的 recordingTool：包装一层但如实转发 Definition。
//
// ⚠ 这一层不是装饰：生产用的是「按 Definition 的名字认」，
//
//	而探针与本门都通过它证明「包装过仍然认得出」。
//	若生产改成类型断言 `*meetingagent.NoteSearcher`，
//	探针的 A/C/E 臂与本门 T1 都会失真 —— 那是**同一种**失真。
type gateWrapTool struct{ inner meetingagent.Tool }

func (w gateWrapTool) Definition() llmbff.Tool { return w.inner.Definition() }
func (w gateWrapTool) Execute(ctx context.Context, a json.RawMessage) (string, error) {
	return w.inner.Execute(ctx, a)
}

// promptProblems 返回「提示词与工具集不符」之处。空切片 = 相符。
//
// ⚠ 判据只用**行为**（注册了什么 / 提示词说了什么），不复述生产分支：
//
//	复述分支的判据在分支被改坏时会跟着改坏，然后一起绿。
func promptProblems(tools []meetingagent.Tool, prompt string) []string {
	var out []string
	registered := promptHasTool(tools, meetingagent.SearchNotesToolName)
	switch {
	case registered && !strings.Contains(prompt, meetingagent.SearchNotesToolName):
		out = append(out, "注册了 search_notes，提示词却不提它 ⇒ 工具永远不会被调")
	case !registered && strings.Contains(prompt, meetingagent.SearchNotesToolName):
		out = append(out, "没有注册 search_notes，提示词却宣传它 ⇒ §328 的原缺陷")
	}
	// toollessPromptAdd 是**探针文件里那一份**字面量，不是这里手抄的：
	// 它与生产文案逐字相同，重复一份就多一个能漂移的点。
	if !registered && !strings.Contains(prompt, toollessPromptAdd) {
		out = append(out, "没有检索工具时缺少「不要调用任何工具」那条指令")
	}
	if registered && strings.Contains(prompt, toollessPromptAdd) {
		out = append(out, "注册了 search_notes 却塞了「当前没有可用工具」")
	}
	return out
}

// T1 —— 不变量本体：提示词 ⇔ 工具集。
func TestG469T1PromptFollowsTools(t *testing.T) {
	searcher := func() meetingagent.Tool { return &meetingagent.NoteSearcher{} }

	cases := []struct {
		name  string
		tools []meetingagent.Tool
		why   string
	}{
		{"nil 工具集", nil, "§328 的现场：notesStore == nil ⇒ 一个工具都不注册"},
		{"空切片工具集", []meetingagent.Tool{}, "与 nil 语义相同，不许分道扬镳"},
		{"含 nil 元素", []meetingagent.Tool{nil}, "不许 panic，也不许被当成有工具"},
		{"只有异名工具", []meetingagent.Tool{gateStubTool{name: "search_files"}}, "别处的工具不许触发笔记检索那一支"},
		{"注册了 search_notes", []meetingagent.Tool{searcher()}, "有笔记可查 ⇒ 必须宣传"},
		{"包装过的 search_notes", []meetingagent.Tool{gateWrapTool{inner: searcher()}}, "探针与生产都经这一层"},
		{"异名 + search_notes", []meetingagent.Tool{gateStubTool{name: "search_files"}, searcher()}, "不能只看第一个"},
	}
	for _, c := range cases {
		p := meetingAgentSystemPrompt("", c.tools)
		if probs := promptProblems(c.tools, p); len(probs) > 0 {
			t.Errorf("%s（%s）：%v", c.name, c.why, probs)
		}
	}
}

// T1b —— **量具自证**：直接钉 promptHasTool 本身。
//
// ⚠⚠ 这条是补 T1 的洞，补的过程本身就是证据：
//
//	头一版只有 T1，而 T1 问的是「提示词与工具集自洽吗」。
//	把 promptHasTool 换成类型断言 `t.(*meetingagent.NoteSearcher)` 后，
//	包装过的工具被当成「没有工具」⇒ 产出的**仍然是自洽的**提示词
//	⇒ T1 全绿。而那次外部变异（M2）只被 live_agent_refs_fabrication_probe_test.go
//	里的一条旁证抓住 —— 也就是说**当时那道门并不覆盖它**。
//
//	⇒ 「自洽」不等于「正确」：一个认错工具的量具会产出自洽的错值。
//	所以凡是「两样东西必须一致」的判据，都必须**再钉一次量具本身**。
func TestG469T1bPromptHasToolSeesName(t *testing.T) {
	const want = meetingagent.SearchNotesToolName

	cases := []struct {
		name  string
		tools []meetingagent.Tool
		want  bool
		why   string
	}{
		{"nil", nil, false, "空集不得被读成有工具"},
		{"异名工具", []meetingagent.Tool{gateStubTool{name: "search_files"}}, false,
			"⚠ 只有这一条能分辨「按名字认」与「看第一个元素/类型」"},
		{"search_notes", []meetingagent.Tool{&meetingagent.NoteSearcher{}}, true, ""},
		{"包装过的 search_notes", []meetingagent.Tool{gateWrapTool{inner: &meetingagent.NoteSearcher{}}}, true,
			"探针的 recordingTool 就是这一层；认不出 ⇒ 探针 A/C/E 臂全部失真"},
		{"异名 + search_notes", []meetingagent.Tool{gateStubTool{name: "search_files"}, &meetingagent.NoteSearcher{}}, true,
			"不能只看第一个元素"},
		{"异名 + 包装", []meetingagent.Tool{gateStubTool{name: "search_files"}, gateWrapTool{inner: &meetingagent.NoteSearcher{}}}, true,
			"包装 + 非首位"},
	}
	for _, c := range cases {
		if got := promptHasTool(c.tools, want); got != c.want {
			t.Errorf("%s：promptHasTool = %v，期望 %v（%s）", c.name, got, c.want, c.why)
		}
	}
	// ⚠ 钉住「按名字」这个实现选择本身，不只是读数：
	//   将来若有人改成类型断言，这条会红，而它**该**红。
	//   代价是探针/夹具必须继续如实转发 Definition —— 那正是真实接线的做法。
	if !promptHasTool([]meetingagent.Tool{gateWrapTool{inner: &meetingagent.NoteSearcher{}}}, want) {
		t.Error("包装层没有被认成 search_notes ⇒ promptHasTool 大概改成了类型断言；" +
			"而那会让 live_agent_refs_fabrication_probe_test.go 的 A/C/E 臂在用户真实检索不到东西时仍读成正常")
	}
}

// T2 —— 接线同源：提示词与 runner 必须拿到**同一个** tools。
//
// ⚠ 为什么不能只靠 T1：T1 保证「给什么工具集出什么提示词」，
//
//	而 §328 的真正成因是**接线把两件事算成了两遍**。
//	若有人在 meetingSummaryViaAgent 里写成
//	`meetingAgentSystemPrompt(prev, nil)`，T1 全绿而缺陷原样存在。
func TestG469T2WiringSharesOneTools(t *testing.T) {
	b, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// ⚠ 整行断言而不是逐个片段：逐片段会把「给两处传**不同**的表达式」
	//   判成通过，而那正是本条要拦的。
	const want = "runner.Run(ctx, meetingAgentSystemPrompt(prev, tools), transcript, tools)"
	if !strings.Contains(s, want) {
		t.Errorf("接线不是「同一个 tools 既喂提示词又喂 runner」：\n"+
			"  期望原文含 %q\n"+
			"  （提示词与接线不同源就是 §328 的根因，见本文件头）", want)
	}
	// ⚠ 工具集必须来自 meetingTools，不能是就地拼的：
	//   就地拼的话 T3 测不到生产实际交出的那一份。
	if !strings.Contains(s, "tools := s.meetingTools(") {
		t.Error("tools 不是由 s.meetingTools 产出 ⇒ T3 守的就不是生产实际用的那一份")
	}
	// ⚠ 反向：函数体里不许再就地注册工具（那就是第二个生产者）。
	if strings.Contains(s, "tools = append(tools, ") {
		t.Error("meetingSummaryViaAgent 里仍有就地注册工具的代码 ⇒ 工具集有了第二个生产者")
	}
}

// T3 —— 前提坐实：remote-only 部署真的交出空工具集。
//
// ⚠ 这条过去只写在 main.go 与 §328.7 的注释里。
//
//	没有它，「提示词按工具分支」修的是一个**没人走过的分支**。
func TestG469T3ToolsFollowNotesStore(t *testing.T) {
	// remote-only：main.go 只在 pool != nil 时赋值 notesStore。
	remoteOnly := &Server{}
	tools := remoteOnly.meetingTools("u1", "ws1")
	if len(tools) != 0 {
		t.Fatalf("notesStore 为 nil 却注册了 %d 个工具 ⇒ T1/T2 守的是一条生产走不到的分支", len(tools))
	}
	if probs := promptProblems(tools, meetingAgentSystemPrompt("", tools)); len(probs) > 0 {
		t.Errorf("remote-only 下的提示词与工具集不符：%v", probs)
	}

	// 有 PG：恰好一个工具，作用域齐全。
	withPG := &Server{notesStore: &notes.Store{}}
	tools = withPG.meetingTools("u1", "ws1")
	if len(tools) != 1 {
		t.Fatalf("notesStore 非 nil 却注册了 %d 个工具（want 1）", len(tools))
	}
	searcher, ok := tools[0].(*meetingagent.NoteSearcher)
	if !ok {
		t.Fatalf("注册的工具类型是 %T，不是 *meetingagent.NoteSearcher", tools[0])
	}
	// ⚠ §330.5：漏作用域不是「检索不到」，是**跨工作区泄漏** —— 比查不到更糟。
	if searcher.UserID != "u1" || searcher.WorkspaceID != "ws1" {
		t.Errorf("检索作用域没跟着传（UserID=%q WorkspaceID=%q）："+
			"要么恒检索不到，要么跨工作区泄漏", searcher.UserID, searcher.WorkspaceID)
	}
	if searcher.Src == nil {
		t.Error("NoteSearcher.Src 为 nil ⇒ 检索恒零行")
	}
	if probs := promptProblems(tools, meetingAgentSystemPrompt("", tools)); len(probs) > 0 {
		t.Errorf("有 PG 下的提示词与工具集不符：%v", probs)
	}
}

// T4 —— 判据自证：promptProblems 必须能拒绝坏的提示词。
//
// ⚠ 没有这一条，上面三条全绿也可能只是「判据恒真」。
//
//	样本由**生产提示词做手术**得到，不用手抄 —— 手抄会漂，
//	而且抄出来的「坏提示词」可能压根不是生产可能的形态。
func TestG469T4CheckerHasTeeth(t *testing.T) {
	good := meetingAgentSystemPrompt("", nil)
	if probs := promptProblems(nil, good); len(probs) > 0 {
		t.Fatalf("正样本都判不过，判据可能恒假：%v", probs)
	}

	// ⚠⚠ 每个负样本都靠**手抄锚点**做手术。锚点漂移时 `strings.Replace`
	//   **原样返回**（它不报错）—— 样本于是不再是「坏样本」而是「好样本」，
	//   而判据对好样本返回空 ⇒ 报出来的是「判据没抓到」，
	//   **读起来像量具坏了，实际是手术没装上**。
	//   ⇒ 先断言锚点存在，把这两种失败彻底拆开。
	for _, anchor := range []string{"你是会议纪要助手", toollessPromptAdd} {
		if !strings.Contains(good, anchor) {
			t.Fatalf("负样本锚点 %q 已不在生产提示词里（%d 字节）：手术会退化成恒等变换，"+
				"下面那些负样本将全部不再是坏样本，报出来的「判据没抓到」是假象",
				anchor, len(good))
		}
	}

	bad := []struct {
		name    string
		tools   []meetingagent.Tool
		prompt  string
		wantHit string // 期望判据抓住的那一条
	}{
		{
			// ⭐ §328 的原形态：工具为空，提示词照旧宣传。
			name:    "工具为空却宣传 search_notes",
			tools:   nil,
			prompt:  strings.Replace(good, "你是会议纪要助手", "你可以调用 "+meetingagent.SearchNotesToolName+" 检索笔记。\n"+"\n你是会议纪要助手", 1),
			wantHit: "没有注册 search_notes，提示词却宣传它",
		},
		{
			name:    "工具为空却少了「不要调用任何工具」",
			tools:   nil,
			prompt:  strings.Replace(good, toollessPromptAdd, "", 1),
			wantHit: "没有检索工具时缺少",
		},
		{
			name:    "注册了工具却塞了「当前没有可用工具」",
			tools:   []meetingagent.Tool{&meetingagent.NoteSearcher{}},
			prompt:  good + "\n\n" + toollessPromptAdd,
			wantHit: "注册了 search_notes 却塞了",
		},
		{
			name:    "注册了工具却只字不提它",
			tools:   []meetingagent.Tool{&meetingagent.NoteSearcher{}},
			prompt:  strings.Replace(good, "你可以调用 "+meetingagent.SearchNotesToolName, "你可以查资料", 1),
			wantHit: "注册了 search_notes，提示词却不提它",
		},
		{
			// ⚠ 只有异名工具这一条能分辨「按名字认」与「看第一个元素」。
			//   少了它，类型断言式实现在这四行样本上全绿。
			name:    "异名工具被当成了笔记检索",
			tools:   []meetingagent.Tool{gateStubTool{name: "search_files"}},
			prompt:  meetingAgentSystemPrompt("", []meetingagent.Tool{&meetingagent.NoteSearcher{}}),
			wantHit: "没有注册 search_notes，提示词却宣传它",
		},
	}
	for _, c := range bad {
		probs := promptProblems(c.tools, c.prompt)
		if len(probs) == 0 {
			t.Errorf("%s：判据没抓到（这一条样本存在时，上面三条门可能是恒真的）", c.name)
			continue
		}
		hit := false
		for _, p := range probs {
			if strings.Contains(p, c.wantHit) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%s：判据抓到了但不是预期那一条\n  实际：%v\n  期望含：%s", c.name, probs, c.wantHit)
		}
	}
}
