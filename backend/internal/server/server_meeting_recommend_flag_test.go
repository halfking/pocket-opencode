package server

import (
	"testing"
)

// §223：推荐链的「没拿到」信号。
//
// 缺陷形态：`/recommend` 的失败输出是**空数组**，而空数组同时也是
// 「模型确实没有值得建议的建议」（提示词明写「没有就返回空数组」）。
// ⇒ 两者在响应里同形 ⇒ 前端只能靠 `recommendations.length` 判断，
// 那等于没有判断：整块「相关 · 笔记 / 知识库 / 网络」静默消失。
// 最糟的一格在服务端：`llmChatOnce` 报错时也是 200 + `{items: []}`，
// **一次失败被报成了成功**。
//
// ★ 本文件最要紧的一条是「空但答了 ⇒ 回落标志必须是 false」：
// 把它写成 true 看起来更"安全"，但会让**每一次**会议都弹
// 「没有拿到参考资料与建议」—— 那是 §30 明确反对的噪音
// （宁可少说一句，不可说错一句），且会把真失败淹掉。

func TestRecommendFallbackFlagDistinguishesEmptyFromFailed(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantFellBck bool
		wantItems   int
	}{
		{"有建议", `{"items":[{"title":"看下季度预算","snippet":"预算按季度复核","query":"季度预算"}]}`, false, 1},
		// ★ 空但答了：模型答了「没有值得建议的」⇒ 不得报回落。
		{"空但答了", `{"items":[]}`, false, 0},
		// 解析成功但每条 title 都是空白 ⇒ 同样**不是**回落（模型答了）。
		{"每条 title 空白", `{"items":[{"title":"   "},{"title":""}]}`, false, 0},
		{"散文不是 JSON", "好的，我来推荐几条相关的搜索关键词。", true, 0},
		{"空响应", "", true, 0},
		{"截断的 JSON", `{"items":[{"title":"看下季度预算","snip`, true, 0},
		{"错误信息", `{"error":"no candidates"}`, true, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, fellBack := parseRecommendJSONWithFallback(tc.content)
			if fellBack != tc.wantFellBck {
				t.Fatalf("fellBack=%v，期望 %v（原文=%q）", fellBack, tc.wantFellBck, tc.content)
			}
			if len(items) != tc.wantItems {
				t.Fatalf("条目数 %d，期望 %d", len(items), tc.wantItems)
			}
			// 任何形态下都必须是**非 nil** 空切片：调用方按 len()==0 判断，
			// nil 与空切片在 JSON 里一样，但 Go 侧 range/append 行为不同。
			if items == nil {
				t.Fatal("返回了 nil 切片而非空切片")
			}
		})
	}
}

// TestRecommendParserKeepsPinnedItemShape 钉住「只加标志、不动条目形状」。
//
// 契约夹具 meeting-response-keys.json 把 recommendItem 的六个键写死了
// （id/score/snippet/title/type/url）。加标志很容易顺手改到条目构造，
// 那一改前端 onOpenRelated 就会掉进「哪个分支都不匹配 ⇒ 点击静默无反应」。
func TestRecommendParserKeepsPinnedItemShape(t *testing.T) {
	items, fellBack := parseRecommendJSONWithFallback(
		`{"items":[{"title":"季度预算复核","snippet":"按季度复核","query":"季度预算"}]}`)
	if fellBack {
		t.Fatal("正常响应被判成回落")
	}
	if len(items) != 1 {
		t.Fatalf("应解析出 1 条，实得 %d", len(items))
	}
	got := items[0]
	for _, k := range []string{"id", "score", "snippet", "title", "type", "url"} {
		if _, ok := got[k]; !ok {
			t.Errorf("条目缺字段 %q（契约夹具把它写死了，缺一个就崩一条链路）", k)
		}
	}
	if got["url"] == "" {
		t.Error("url 为空：前端 onOpenRelated 会点进一个不存在的站内页面")
	}
	// 单条缺 query ⇒ 用 title 顶上（既有行为，回归保护）。
	items2, _ := parseRecommendJSONWithFallback(`{"items":[{"title":"只有标题"}]}`)
	if len(items2) != 1 {
		t.Fatalf("应解析出 1 条，实得 %d", len(items2))
	}
	if url, _ := items2[0]["url"].(string); url == "" || url == "https://www.bing.com/search?q=" {
		t.Errorf("缺 query 时 url 没有用 title 顶上：%q", items2[0]["url"])
	}
}

// TestRecommendSingleValueWrapperKeepsItsContract 钉住老签名没被顺手改语义。
func TestRecommendSingleValueWrapperKeepsItsContract(t *testing.T) {
	for _, content := range []string{"不是 JSON", "", `{"error":"x"}`, `{"items":[]}`} {
		if got := parseRecommendJSON(content); got == nil {
			t.Errorf("content=%q：老签名必须返回非 nil 空切片", content)
		}
	}
	if got := parseRecommendJSON(`{"items":[{"title":"t"}]}`); len(got) != 1 {
		t.Errorf("正常响应解析出 %d 条，应为 1", len(got))
	}
}
