package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// 本文件钉住一条**用户可见**的不变量：
//
//	智能体摘要这条路**永远不得**把原始转写当作摘要交出去。
//	拿到的是摘要就交摘要；拿不到就交 nil，让调用方走它已经写好的回落。
//
// ⚠ 为什么这条要单独钉（它曾经完全不成立）：
//
//	parseSummaryJSON 的两条返回路径**都**返回 nil error（「JSON 非法」与
//	「合法但空」都回落成 emptySummary(fallback) + nil）。于是
//	meetingSummaryViaAgent 里的 `perr != nil` 分支**可证不可达**，
//	回落形状照样被打上 agent_turns 并以 (parsed, true) 上抛 ⇒ handler 广播
//	meeting.summary_updated、标 result["agent"]=true、返回 HTTP 200。
//
//	⇒ 用户看到「智能体摘要已更新」，内容是自己刚说过的话的**前 500 字节**，
//	key_points / action_items / decisions / open_questions 全空，零错误提示。
//	而这四个空数组正是下游建待办、排日程要读的东西（§16 的会议三链）。
//
//	对照精校侧：parseRefineJSON 在同样情况下返回 **error**，调用方据此置
//	refine_fallback，界面不会说「精翻完成」（refine_truncation_test.go）。
//	⇒ 摘要侧原本缺的就是这个区分，不是缺一条更严的解析规则。
//
// ⚠ 这条判据的缝在 llmbff.Provider（oneTurn 之下），所以它**只**覆盖
// 「摘要契约」这一层：网关 SSE 怎么把 `: thinking:` 帧丢掉、20s 尝试窗怎么
// 切候选，都不在本文件的责任范围内（那些是 llmbff_provider_adapters.go 的事）。
// 换句话说：这里判红不代表链路没问题，这里判绿只代表「转写没被当成摘要」。

// summaryFallbackProvider 按脚本回放网关的正文，模拟同一路径上的各种形态。
type summaryFallbackProvider struct {
	// deltas 是逐帧推给 oneTurn 的内容分片。
	deltas []string
	// calls 记录 Stream 被调了几次（回落路径会让调用方自己再走一条路）。
	calls int
}

func (p *summaryFallbackProvider) Stream(_ context.Context, _ llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	p.calls++
	for _, d := range p.deltas {
		if !fn(llmbff.Delta{Content: d}) {
			return nil, nil
		}
	}
	return nil, nil
}

func (p *summaryFallbackProvider) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	return &llmbff.ChatResponse{Content: strings.Join(p.deltas, "")}, nil
}

func (p *summaryFallbackProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return &llmbff.EmbedResponse{}, nil
}

// fallbackSegs 是一条**逐字具体**的转写，含说话人标记 —— 判据要能认出
// 「摘要其实就是转写」，前提是转写本身长得像转写而不是像一句提示词。
func fallbackSegs() []meetingSegmentIn {
	return []meetingSegmentIn{
		{Speaker: "张伟", Text: "下周三跟客户过一遍，要准备悬界芯片和机载传感器的对比表"},
		{Speaker: "李娜", Text: "预算按季度复核，续期方案周五前给结论"},
	}
}

func newFallbackTestServer(t *testing.T, deltas ...string) (*Server, *summaryFallbackProvider) {
	t.Helper()
	p := &summaryFallbackProvider{deltas: deltas}
	return &Server{llmBFF: llmbff.NewService(p, llmbff.NoopRecorder{})}, p
}

func runAgentSummary(t *testing.T, s *Server) (map[string]any, bool) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/meetings/m1/summary", nil)
	res, used := s.meetingSummaryViaAgent(r.Context(), r, fallbackSegs(), "")
	return res, used
}

// TestMeetingAgentSummaryNeverReturnsTranscriptAsSummary 是本文件的主判据。
//
// 判据形态：两条一起断，缺一不可 ——
//
//	① 逐例断「拿不到摘要的形态必须交 nil」（正向指定期望）
//	② 全例兜断「只要交出去，summary 就不能是转写片段」（形态无关的终局断言）
//
// ② 才是真正的不变量：将来有人再加一种新的解析失败分支（模型换格式、
// schema 变严、多加一道校验），只要它落到 emptySummary 回落形状上就会被抓住，
// 不需要改这个文件。
func TestMeetingAgentSummaryNeverReturnsTranscriptAsSummary(t *testing.T) {
	const goodSummary = "会议确定悬界芯片续期方案，预算改为按季度复核。"

	cases := []struct {
		name string
		// deltas 是假 provider 逐帧吐出的内容。
		deltas []string
		// wantNil=true 表示「调用方必须能看出没拿到摘要」。
		wantNil bool
		// wantSummary 非空时表示「必须原样交出这句摘要」。
		wantSummary string
	}{
		{
			// 阳性对照：形态完全正常时必须交出摘要。
			// 没有这一条，「永远返回 nil」这种把功能改瘫的变异也会判绿。
			name: "正常摘要",
			deltas: []string{`{"summary":"` + goodSummary + `","key_points":["预算按季度复核"],` +
				`"action_items":[{"text":"周五前给续期结论"}],"decisions":[],"open_questions":[]}`},
			wantSummary: goodSummary,
		},
		{
			// 真机已观测形态（parseSummaryJSON 注释里记着「第 3 轮就返回了它」）：
			// 合法 JSON、summary 空串。以前解析一路绿灯。
			name:    "合法但 summary 为空",
			deltas:  []string{`{"summary":"","key_points":[],"action_items":[],"decisions":[],"open_questions":[]}`},
			wantNil: true,
		},
		{
			name:    "自由文本不是 JSON",
			deltas:  []string{"好的，我来帮你整理这段会议的内容。", "首先我们看一下行动项……"},
			wantNil: true,
		},
		{
			// 截断形态：finish_reason=length 时 JSON 必然缺外层 }，
			// extractJSON 取不到闭合括号 ⇒ 解析失败。
			name: "截断在数组中间",
			deltas: []string{`{"summary":"会议确定续期","key_points":["预算按季度复核","接口联调"],` +
				`"action_items":[{"text":"签`},
			wantNil: true,
		},
		{
			// 无害形态，**不许**被误杀：模型带 markdown 围栏是常规输出，
			// extractJSON 本来就能剥掉。判据若把它判红就是在惩罚正确行为。
			name: "带代码围栏的完整摘要",
			deltas: []string{"```json\n{\"summary\":\"" + goodSummary +
				"\",\"key_points\":[],\"action_items\":[],\"decisions\":[],\"open_questions\":[]}\n```"},
			wantSummary: goodSummary,
		},
		{
			// 短摘要：只有 summary、四个数组全空。这**是**合法摘要（模型
			// 就是话少），不是回落形状。判据必须放过它，否则就是在惩罚正确行为。
			name:        "只有 summary 的短摘要",
			deltas:      []string{`{"summary":"` + goodSummary + `"}`},
			wantSummary: goodSummary,
		},
		{
			// 分片形态：正文被拆成多帧才吐出来（流式的常态）。
			// 一条判据只按单帧设计的话，会在这里假红。
			name: "分两帧吐出",
			deltas: []string{`{"summary":"会议确定续` +
				`期方案","key_points":[],"action_items":[],"decisions":[],"open_questions":[]}`},
			wantSummary: "会议确定续期方案",
		},
		{
			// 零帧：网关只发 `: thinking:` 进度帧就被 20s 尝试窗切掉时，
			// oneTurn 拿到的就是空串 + nil error。
			name:    "零帧无正文",
			deltas:  nil,
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newFallbackTestServer(t, tc.deltas...)
			got, used := runAgentSummary(t, s)

			if !used {
				t.Fatalf("used=false：本部署应当具备 agent 条件（llmBFF 已装配），" +
					"判据跑不到被测分支")
			}
			if p.calls == 0 {
				t.Fatal("假 provider 一次都没被调用：判据没量到被测对象")
			}

			if tc.wantNil && got != nil {
				t.Fatalf("期望交 nil 让调用方回落，却拿到一份结果：summary=%q",
					got["summary"])
			}
			if !tc.wantNil && got == nil {
				t.Fatalf("期望拿到摘要，却交了 nil（回落判据把正确形态也判成错）")
			}

			if got == nil {
				return
			}

			// ② 形态无关的终局断言：交出去的 summary 绝不能是转写片段。
			summary, _ := got["summary"].(string)
			if strings.TrimSpace(summary) == "" {
				t.Fatal("交出去的摘要为空串")
			}
			transcript := segmentsToText(fallbackSegs())
			if strings.HasPrefix(summary, truncateStr(transcript, 500)) {
				t.Fatalf("把原始转写当摘要交出去了：summary 前 %d 字节 == 转写前 %d 字节",
					len(summary), len(truncateStr(transcript, 500)))
			}
			if tc.wantSummary != "" && summary != tc.wantSummary {
				t.Fatalf("summary 不是模型给的那句：期望 %q，实得 %q", tc.wantSummary, summary)
			}
		})
	}
}

// TestParseSummaryJSONWrapperKeepsItsPinnedContract 钉住重构没有顺手改掉
// 别处依赖的语义：parseSummaryJSON 回落时**不**返回 error。
//
// 这条不是重复 meeting_response_contract_test.go —— 那个文件钉的是「回落分支
// 不该返回 error」这条结论；这条钉的是「重构 parseSummaryJSON 的函数体没有
// 把它悄悄改成返回 error」。函数体现在是一行委托，任何人日后动它都会在这里红。
func TestParseSummaryJSONWrapperKeepsItsPinnedContract(t *testing.T) {
	for _, content := range []string{
		"这不是 JSON",
		`{"summary":""}`,
		`{"summary":"   "}`,
	} {
		got, err := parseSummaryJSON(content, "原始转写兜底")
		if err != nil {
			t.Errorf("content=%q：回落分支不应返回 error，实得 %v", content, err)
		}
		if got == nil {
			t.Errorf("content=%q：回落分支仍须返回 emptySummary 形状，不能是 nil", content)
		}
	}
}

// TestSummaryFallbackFlagAgreesWithWrapper 把两个出口钉成同一件事：
// parseSummaryJSONWithFallback 的 fellBack 标志，必须与 parseSummaryJSON
// 实际返回的形状一致 —— 即「fellBack=true ⇒ 拿到的确实是 emptySummary 形状」。
//
// 为什么单独钉：meetingSummaryViaAgent 现在只读 fellBack、不读返回值形状。
// 万一将来有人改了 emptySummary 的字段（比如把 summary 从转写改成空串），
// 这个标志就会开始撒谎，而主判据的 ② 恰好抓不到（转写片段不再出现）。
func TestSummaryFallbackFlagAgreesWithWrapper(t *testing.T) {
	const transcript = "张伟：下周三跟客户过一遍。"

	for _, tc := range []struct {
		name        string
		content     string
		wantFellBck bool
	}{
		{"正常摘要", `{"summary":"定了续期方案","key_points":["预算按季度复核"]}`, false},
		{"非法 JSON", "这不是 JSON", true},
		{"summary 空串", `{"summary":"","key_points":[]}`, true},
		{"summary 全空白", `{"summary":"  \n ","key_points":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, fellBack := parseSummaryJSONWithFallback(tc.content, transcript)
			if fellBack != tc.wantFellBck {
				t.Fatalf("fellBack=%v，期望 %v", fellBack, tc.wantFellBck)
			}
			wrapper, _ := parseSummaryJSON(tc.content, transcript)
			if _, ok := wrapper["summary"]; !ok {
				t.Fatal("回落形状里必须有 summary 字段：前端要读它")
			}
			// 回落时 summary 必须仍是「认得出是自己说过的话」的那段转写 ——
			// 这是 parseSummaryJSON 原注释写下的设计意图（宁可回落也别假装成功），
			// 本次改动只让调用方**看得见**这次回落，没有改回落内容本身。
			//
			// 连带把回落形状的另一半钉住：四个结构化字段全空。带着它广播等于
			// 告诉前端「这次没有待办 / 没有决议」，而实际上我们连摘要都没拿到 ——
			// 而这四个数组正是下游建待办、排日程要读的东西。
			if !fellBack {
				return
			}
			if got["summary"] != truncateStr(transcript, 500) {
				t.Fatalf("回落形状的 summary 变了：%q", got["summary"])
			}
			for _, k := range []string{"key_points", "action_items", "decisions", "open_questions"} {
				arr, ok := got[k].([]any)
				if !ok {
					t.Errorf("回落形状缺字段 %q（前端按它渲染空列表，读不到会炸）", k)
					continue
				}
				if len(arr) != 0 {
					t.Errorf("回落形状的 %s 有 %d 项，应为 0", k, len(arr))
				}
			}
		})
	}
}
