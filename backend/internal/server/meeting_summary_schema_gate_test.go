package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// 会议滚动摘要的 JSON 契约门禁。
//
// 2026-10-06 真网关实测（llmgo.kxpms.cn）发现的缺陷：
// llmMeetingSummary 在 prev != "" 时把提示词换成
//
//	「请更新摘要，严格返回**相同** JSON 格式。」
//
// 而「相同」指向的格式在这一条消息里**从没出现过**。实测连跑五轮：
//
//	第 1 轮（有 schema） key_points=1 action_items=0
//	第 2 轮            key_points=0 action_items=0
//	第 3 轮            summary **空**
//
// ⇒ 「录音时的即时总结」在第二轮之后基本失效，且没有 action_items
// ⇒ 需求「把时间点自动加入日程」整条断掉；空 summary 也不报错。
//
// 修复：把 schema 提成常量，**两条分支都原文带上**。
//
// ⚠ 2026-10-07（§66.2）判据本身也换了写法：
// 此前是**按源码切片**找 `if prev != ""` 那一段、检查里面有没有
// summaryJSONSchema。那是「正则抽源码」——生产代码一重构（把拼接提成
// buildSummaryPrompt）门就名存实亡，而且它判的是**文本出现**，
// 不是提示词**真的带上了契约**。
//
// 现在直接调纯函数断言**输出**：行为判据，重构不掉，
// 也顺带能钉住「滚动轮转写被拼两次」这种错（结构上多一次 fmt.Sprintf 就会红）。
func TestRollingSummaryPromptAlwaysCarriesSchema(t *testing.T) {
	const transcript = "[张伟] 下周三下午三点跟王总开评审会。\n[王总] 对比表先准备着。"
	const prev = `{"summary":"上一轮结论","action_items":[]}`

	first := buildSummaryPrompt(transcript, "")
	rolling := buildSummaryPrompt(transcript, prev)

	for name, p := range map[string]string{"首轮": first, "滚动轮": rolling} {
		// 判据锚在**滚动分支**上才是当年漏 schema 的那一处，
		// 但首轮同样必须带 —— 两个都要查，不能只查一个。
		if !strings.Contains(p, summaryJSONSchema) {
			t.Errorf("%s提示词没有带上 JSON schema —— 真网关实测：第 2 轮起 action_items 归零、第 3 轮 summary 为空", name)
		}
		// ⚠ 2026-10-07（§66.10）：这条断言**删过又加回来**。
		//   删的理由（§66.3）是「同窗 A/B 证明它让两个模型都变差」——
		//   而那个 A/B 用的是 **due 数量**，把「待定」/「未指定」占位符计成了成功。
		//   改用**解析率**（due 原文逐条喂产品侧 resolveTodoDue）后方向相反：
		//   haiku 32% → 100%。见 server_meeting.go 里 summaryFieldRules 上方的记录。
		if !strings.Contains(p, summaryFieldRules) {
			t.Errorf("%s提示词没有带上 summaryFieldRules —— 无规则时 haiku 只有 32%% 的 due 能进日程（其余是「待定」占位符）", name)
		}
		// 负控：旧那句「相同 JSON 格式」的指代不许复活。
		// 滚动轮的消息里根本没有格式可「相同」。
		if strings.Contains(p, "严格返回相同 JSON 格式") {
			t.Errorf("%s提示词又用回「严格返回相同 JSON 格式」—— 真网关实测会让 action_items 归零", name)
		}
		// 转写必须**恰好出现一次**。多一次就是输入翻倍，
		// 滚动轮首尾相接时每一轮都翻倍，45s 预算必崩。
		if n := strings.Count(p, transcript); n != 1 {
			t.Errorf("%s提示词里转写出现了 %d 次，应为 1 次（多拼一次会让输入翻倍、拖垮预算）", name, n)
		}
	}

	// 滚动轮必须真的带上「已有摘要」，否则形同首轮（滚动形态失效）。
	if !strings.Contains(rolling, prev) {
		t.Errorf("滚动轮没有带上已有摘要 —— 滚动摘要会退化成首轮")
	}
}

// TestParseSummaryJSONRejectsValidButEmpty 是 §20.2 那道兜底的行为断言。
//
// 模型完全可能返回**合法却空**的 JSON（真网关第 3 轮就是）。
// 旧实现只拦「JSON 非法」，于是 summary 是空串、解析一路绿灯、
// 用户的摘要面板什么都没有。「合法但空」与「非法」对用户是同一件事，
// 必须归到同一处回落。
func TestParseSummaryJSONRejectsValidButEmpty(t *testing.T) {
	const fallback = "张伟说续期合同要在十一月底之前签完"

	cases := []struct {
		name    string
		content string
	}{
		{"非法 JSON", "这不是 JSON"},
		{"合法但 summary 为空串", `{"summary":"","key_points":[],"action_items":[]}`},
		{"合法但缺 summary 字段", `{"key_points":["a"],"action_items":[]}`},
		{"合法但 summary 只有空白", `{"summary":"   ","key_points":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSummaryJSON(tc.content, fallback)
			if err != nil {
				t.Fatal(err)
			}
			summary, _ := got["summary"].(string)
			if strings.TrimSpace(summary) == "" {
				t.Fatalf("回落没生效，summary 仍是空的：%v", got)
			}
			if !strings.Contains(summary, "十一月底") {
				t.Errorf("summary=%q，应回落到原始转写", summary)
			}
		})
	}

	// 正常结果不得被误伤。
	ok, err := parseSummaryJSON(`{"summary":"会议确定了续期方案","action_items":[{"text":"签合同"}]}`, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := ok["summary"].(string); s != "会议确定了续期方案" {
		t.Errorf("正常结果被改动了：%q", s)
	}
	items, _ := ok["action_items"].([]any)
	if len(items) != 1 {
		t.Errorf("正常结果的 action_items 丢了：%v", ok["action_items"])
	}
	// 回落形状里各字段必须仍是数组/对象，不能是 nil（前端 JSON.stringify 后
	// 会变成 null，渲染侧会炸）。
	empty, _ := parseSummaryJSON(`{"summary":""}`, fallback)
	for _, k := range []string{"key_points", "action_items", "decisions", "open_questions"} {
		if _, isArr := empty[k].([]any); !isArr {
			b, _ := json.Marshal(empty[k])
			t.Errorf("回落结果里 %s 不是数组（前端会拿到 null）：%s", k, b)
		}
	}
}

// TestRefineJSONRejectsValidButEmpty 与 TestParseSummaryJSONRejectsValidButEmpty 同源，
// 目标是 parseRefineJSON。
//
// ★ 这条比 §17 的 refine_fallback 标记更靠底层：§17 那个标记只在
// **解析报错**时置位，所以「合法却空的精校结果」会绕过它 ——
// 界面照实弹「精翻完成」，屏幕上一片空白。这里堵住源头。
func TestRefineJSONRejectsValidButEmpty(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"空串", `{"refined_transcript":"","translations":{},"structured_minutes":{}}`},
		{"缺字段", `{"translations":{},"structured_minutes":{}}`},
		{"纯空白", `{"refined_transcript":"  \n "}`},
		{"类型不对", `{"refined_transcript":123}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRefineJSON(tc.content)
			if err == nil {
				t.Fatalf("合法但空的精校结果被放行了：%v", got)
			}
			if got != nil {
				t.Errorf("出错时不应同时返回结果：%v", got)
			}
		})
	}

	// 正常结果不得被误伤。
	ok, err := parseRefineJSON(`{"refined_transcript":"张伟：今天下午三点开会。","structured_minutes":{}}`)
	if err != nil {
		t.Fatalf("正常结果被拒：%v", err)
	}
	if s, _ := ok["refined_transcript"].(string); !strings.Contains(s, "下午三点") {
		t.Errorf("正常结果内容不对：%q", s)
	}
}

// TestNoteSummaryPayloadRejectsValidButEmpty 同源，目标是随手记的
// parseNoteSummaryPayload。
//
// 缺陷形态：解析成功但 summary 空 ⇒ 随手记的 AI 总结面板空白。
// 回落成模型原文（与「解析失败」同一条路）至少还有内容。
func TestNoteSummaryPayloadRejectsValidButEmpty(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"空串", `{"summary":"","action_items":[]}`},
		{"缺字段", `{"action_items":[{"text":"买咖啡"}]}`},
		{"纯空白", `{"summary":"   "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, items := parseNoteSummaryPayload(tc.content)
			if strings.TrimSpace(got) == "" {
				t.Fatalf("summary 为空：应回落到模型原文 %q", tc.content)
			}
			// 回落时行动项一并作废：模型没给出可信摘要，
			// 它同时给出的 action_items 也不可信（宁可少建日程，不建错日程）。
			if len(items) != 0 {
				t.Errorf("回落时不应保留 action_items，实际 %d 条：%v", len(items), items)
			}
		})
	}

	// 正常结果不得被误伤。
	sum, items := parseNoteSummaryPayload(`{"summary":"确认了续费方案","action_items":[{"text":"发报价单","due":"下周三"}]}`)
	if sum != "确认了续费方案" {
		t.Errorf("正常 summary 被改动：%q", sum)
	}
	if len(items) != 1 || items[0].Due != "下周三" {
		t.Errorf("正常 action_items 丢了：%+v", items)
	}
}
