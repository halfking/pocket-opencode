package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRefineTruncationNeverYieldsPartialTranscript 钉住一条**用户可见**的不变量：
//
//	精校响应被截断（finish_reason=length / 传输中断 / 上游提前收尾）时，
//	parseRefineJSON 要么报错（走 refine_fallback），要么返回**与完整响应逐字节相同**
//	的 refined_transcript —— 绝不能返回一段**半截**正文却让界面显示「精翻完成」。
//
// ⚠ 为什么这条要用真实抓包，而不是自己编一段 JSON：
//
//	判据的强度取决于截断点落在哪。「截在字符串值中间 → JSON 非法」和
//	「截在某个嵌套对象闭合之后、外层 } 之前 → 括号不平衡 → JSON 非法」
//	这两条推理只有在**真实响应形状**（哪个键在最前、正文多长、有没有嵌套对象、
//	外层闭括号在第几个字符）上才成立。编一份短 JSON 去证明它，是在证明自己造的东西。
//	testdata/refine_capture_auto_long.txt 是 llm.kxpms.cn 上 auto 模型的真实响应体，
//	请求提示词由 live_refine_prompt_dump_test.go 从 buildRefinePrompt 真实导出。
//
// ⚠ 判据刻意**不**写成「截断必须解析失败」：
//
//	真实扫描 3867 个截断点里有 4 个能解析成功 —— 它们落在 99.9%~100%，
//	即 JSON 外层 } 闭合**之后**的换行与 ``` 围栏字符。extractJSON 用的是
//	LastIndex("}")，所以那 4 个点取回的仍是**完整**对象。
//	把「能解析成功」本身判红，等于要求产品对无害输入也报错；
//	真正的危害只有一种 —— **拿到半截正文**。所以判据落在「取回的正文是否完整」。
func TestRefineTruncationNeverYieldsPartialTranscript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "refine_capture_auto_long.txt"))
	if err != nil {
		t.Fatalf("读不到真实抓包: %v", err)
	}
	full := string(raw)

	// 基线①：完整响应必须解析成功，否则下面全是空跑。
	parsedFull, err := parseRefineJSON(full)
	if err != nil {
		t.Fatalf("完整响应必须解析成功: %v", err)
	}
	want, _ := parsedFull["refined_transcript"].(string)
	if strings.TrimSpace(want) == "" {
		t.Fatal("完整响应的 refined_transcript 为空，抓包不可用")
	}

	runes := []rune(full)
	if len(runes) < 3000 {
		t.Fatalf("抓包只有 %d 字符，太短，扫描会近乎空跑", len(runes))
	}

	var partial, accepted, failed int
	for i := 1; i < len(runes); i++ {
		got, err := parseRefineJSON(string(runes[:i]))
		if err != nil {
			failed++
			continue
		}
		accepted++
		refined, _ := got["refined_transcript"].(string)
		if refined != want {
			partial++
			t.Errorf("截断点 %d/%d（%.1f%%）返回了**半截** refined_transcript：完整版 %d 字符，截断版 %d 字符",
				i, len(runes), float64(i)*100/float64(len(runes)), len([]rune(want)), len([]rune(refined)))
		}
	}
	t.Logf("扫描 %d 个截断点：解析失败 %d，解析成功 %d，其中半截正文 %d",
		len(runes)-1, failed, accepted, partial)
	if partial > 0 {
		t.Fatalf("有 %d 个截断点返回了半截正文", partial)
	}
}

// TestRefineTruncationOfEmptyAndFenceOnlyResponses 钉住另外两个**实测到过**的
// finish_reason=length 形态：预算全花在 reasoning ⇒ content 为空；
// 或 content 只有围栏没有 JSON。
//
// 这两条与上面的「半截」是不同的形态，必须分别钉：空 content 走
// json.Unmarshal("") 失败 ⇒ fallback，与「半截」互不相干。
func TestRefineTruncationOfEmptyAndFenceOnlyResponses(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"空响应", ""},
		{"只有代码围栏", "```json\n```"},
		{"只有开括号", "{"},
		{"闭合了外层括号但正文截断", `{"refined_transcript":"[张伟] 讨论了接口联调的进度，然`},
		{"合法但空 refined_transcript", `{"refined_transcript":"","translations":{},"structured_minutes":{"agenda":[],"decisions":[],"action_items":[],"next_meeting":null},"todos":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRefineJSON(tc.content)
			if err == nil {
				// 解析成功就必须给出**可用的**正文，否则等于把空结果当成功返回。
				refined, _ := got["refined_transcript"].(string)
				if strings.TrimSpace(refined) != "" {
					t.Fatalf("期望判失败，却返回了 %d 字符正文", len([]rune(refined)))
				}
				t.Fatal("期望判失败（不得把空/半截结果当成功返回），却返回 nil error")
			}
		})
	}
}

// 把 fixture 里的响应反序列化成结构，确认真实抓包确实含说话人标记与结构化字段 ——
// 避免「拿到一份其实不是精校响应的文件」时上面那些断言假绿。
func TestRefineCaptureFixtureIsRealRefineResponse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "refine_capture_auto_long.txt"))
	if err != nil {
		t.Fatalf("读不到真实抓包: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(extractJSON(string(raw))), &probe); err != nil {
		t.Fatalf("抓包不是合法精校 JSON: %v", err)
	}
	for _, k := range []string{"refined_transcript", "translations", "structured_minutes", "todos"} {
		if _, ok := probe[k]; !ok {
			t.Errorf("抓包缺字段 %q", k)
		}
	}
	if s, _ := probe["refined_transcript"].(string); !strings.Contains(s, "[张伟]") || !strings.Contains(s, "[李娜]") {
		t.Errorf("抓包的 refined_transcript 里没有说话人标记")
	}
}
