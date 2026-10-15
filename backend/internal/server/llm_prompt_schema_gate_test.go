package server

import (
	"os"
	"strings"
	"testing"
)

// 「要求模型返回 JSON」与「解析器读哪些字段」必须对得上。
//
// 背景（2026-10-06 真网关实测）：llmMeetingSummary 在滚动分支把提示词换成
// 「请更新摘要，严格返回**相同** JSON 格式」—— 而「相同」指向的格式在这一条
// 消息里从没出现过。真实读数：第 2 轮起 action_items 归零、第 3 轮 summary 为空。
//
// 这类缺陷的形态是**很容易复发**的：改提示词的人只要写「相同格式」四个字
// 就跑掉了，而单测、全部门禁都不会红。所以把它变成门禁：
//
//	每一条要求返回 JSON 的提示词，都必须**字面**带着它的解析器要读的字段名。
//
// 这是源码扫描（跨函数、跨文件），能抓住「schema 被删 / 被换成指代」，
// 抓不住「schema 写错但字段名还在」。后者由各解析器的行为断言负责。
// 本门复用同包已有的 stripGoComments（pg_test_isolation_guard_test.go）。
// 扫描前必须先剥注释，否则这道门会自欺：
//   - 负控「相同格式」会匹配到**解释这个缺陷的注释**；
//   - 「提示词里有 action_items」可能被函数注释满足 ——
//     那度量的是「注释里提过」，不是「提示词里有」。
// 与 §13.5（注释里提到 found.Snippet）、§17（注释里引述「精翻完成」）同族。
//
// ⚠ 已知局限：stripGoComments 是按行启发式剥的，字符串字面量里若含
// `//`（且前一个字符不是 `:`）会被误砍。本仓的提示词里没有这种写法，
// 但这不是零风险 —— 新增提示词时要留意别在字面量里写裸 `//`。

func TestLLMPromptsCarryTheFieldsTheirParserReads(t *testing.T) {
	meeting, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := os.ReadFile("server_assistant.go")
	if err != nil {
		t.Fatal(err)
	}
	// 扫描前先剥注释（见 stripGoComments 的说明）。
	meetingS := stripGoComments(string(meeting))
	assistantS := stripGoComments(string(assistant))

	cases := []struct {
		name   string
		src    string
		anchor string // 函数名或常量名
		fields []string
		why    string
	}{
		{
			// ⚠ 2026-10-07（§66.2）：锚点从 llmMeetingSummary 改成 buildSummaryPrompt。
			//
			// 提示词拼装已抽成纯函数（为了让导出器与生产共用同一字符串）。
			// **本门因此红过一次** —— 正是下面「会议精校」那条注释里
			// 提前写下的「抽取函数时每一道按 anchor 取源码的门都要一起改」。
			// 那条注释到这一天才拿到实测证据，不是空话。
			name: "会议滚动摘要", src: meetingS, anchor: "func buildSummaryPrompt",
			fields: []string{"summaryJSONSchema"},
			why:    "两条分支都要引用 schema 常量（滚动分支曾退化成「相同格式」）",
		},
		{
			name: "会议智能体", src: meetingS, anchor: "func meetingAgentSystemPrompt",
			fields: []string{"key_points", "action_items", "decisions", "open_questions", "references", "note_id"},
			why:    "parseSummaryJSON 读的字段；缺一个就少一块内容",
		},
		{
			// ⚠ 2026-10-08（§223）：锚点从 llmMeetingRecommend 改成
			// llmMeetingRecommendWithFallback。
			//
			// 提示词没搬家，但**多了一个两值包装函数**，而 `strings.Index`
			// 取的是**第一个**匹配 ⇒ 包装函数（函数名是真实函数的前缀）先命中，
			// 切出来的那段只有三行委托，自然找不到 items/title/snippet/query。
			//
			// ⇒ 这与 §66.2 那次**完全同型**，而且更隐蔽：
			//   那是「函数被搬走」，这次是「多了一个同前缀的壳」。
			// ⇒ 本门第三次兑现下面那条注释里写下的程序
			//   （「抽取函数时每一道按 anchor 取源码的门都要一起改」）。
			//   ⚠ 顺带提醒：这类门的锚点是**前缀敏感的**，
			//   以后再加包装函数时要一并核对全部 anchor。
			name: "相关推荐", src: meetingS, anchor: "func (s *Server) llmMeetingRecommendWithFallback",
			fields: []string{"items", "title", "snippet", "query"},
			why:    "parseRecommendJSON 读的字段",
		},
		{
			// ⚠ 2026-10-07：锚点从 llmMeetingRefine 改成 buildRefinePrompt。
			//
			// 提示词已从 llmMeetingRefine 抽进纯函数 buildRefinePrompt
			//（为了让 §35 的接线能被行为断言覆盖）。本门仍按「从 anchor 切到
			// 下一个顶层声明」取源码，锚点不动的话它切到的区间里
			// **一个字提示词都没有** ⇒ 四个字段全报缺失。
			//
			// 这是**假红**，但它比假绿更危险：没人会去核对一个「刚好
			// 是自己刚改的那块」的红。抽取函数时**每一道按 anchor 取源码的门**
			// 都要一起改，这是清单里必须加的一条。
			name: "会议精校", src: meetingS, anchor: "func buildRefinePrompt",
			// ⚠ 字段名必须带**转义引号**，锚在 JSON 键上。
			//
			// 不带引号时这条是**弱判据**：把 schema 里的
			// `refined_transcript` 改成 `refined_text` 后，
			// 同一段提示词里的「refined_transcript 的硬性要求」
			// 照样满足它 ⇒ 实测注入违规，这条门**仍然全绿**。
			// （真正的 schema 契约由 refine_prompt_contract_test.go 的
			//   `"refined_transcript"` 形式把住，那条确实转红了。）
			fields: []string{`\"refined_transcript\"`, `\"translations\"`, `\"structured_minutes\"`, `\"todos\"`},
			why:    "parseRefineJSON 与前端 normalizeRefine 读的字段",
		},
		{
			name: "随手记总结", src: assistantS, anchor: "func buildNoteSummaryPrompt",
			fields: []string{"summary", "action_items", "assignee", "due"},
			why:    "parseNoteSummaryPayload 读的字段；缺 due 就没有时间点进日程",
		},
		{
			name: "摘要 schema 常量", src: meetingS, anchor: "const summaryJSONSchema",
			fields: []string{"summary", "key_points", "action_items", "decisions", "open_questions"},
			why:    "parseSummaryJSON 读的字段",
		},
		{
			// ⚠ 2026-10-07（§66.10）：删过一次又加回来（见该文件里 §66.3/§66.10 的记录）。
			// 这次加回来是因为**解析率**才是对指标：
			//   无规则时 haiku 19 条 due 只有 6 条能进日程（32%），其余是「待定」占位符。
			name: "摘要字段要求", src: meetingS, anchor: "const summaryFieldRules",
			fields: []string{"action_items", "只放转写里真实提到", "due 保留用户原话里的时间表述", "不要自己换算成日期", "指代词"},
			why:    "摘要把时间点交给下游进日程；缺它 ⇒ due 里全是「待定」占位符，一条也进不了日程",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := strings.Index(tc.src, tc.anchor)
			if i < 0 {
				t.Fatalf("找不到 %s", tc.anchor)
			}
			// 切到下一个顶层声明为止；找不到就取后面 2000 字。
			seg := tc.src[i:]
			if end := strings.Index(seg[1:], "\n}\n"); end > 0 {
				seg = seg[:end+2]
			} else {
				seg = seg[:min(len(seg), 2000)]
			}
			for _, f := range tc.fields {
				if !strings.Contains(seg, f) {
					t.Errorf("%s 的提示词里没有 %q —— %s", tc.name, f, tc.why)
				}
			}
		})
	}

	// 负控：「相同 / 同样 格式」这种指代不许出现在任何提示词里。
	// 它在跨轮次场景下无所指，是 2026-10-06 那次静默失效的直接原因。
	for _, name := range []string{"server_meeting.go", "server_assistant.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		clean := stripGoComments(string(src))
		for _, bad := range []string{"相同 JSON 格式", "同样的 JSON 格式", "相同格式", "同样的格式"} {
			if strings.Contains(clean, bad) {
				t.Errorf("%s 里出现「%s」：跨轮次时模型看不到上一条提示词，"+
					"「相同」无所指，实测会让 action_items 归零、summary 变空", name, bad)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
