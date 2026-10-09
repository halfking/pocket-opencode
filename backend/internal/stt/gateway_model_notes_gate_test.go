package stt

import (
	"os"
	"strings"
	"testing"
)

// TestGatewayModelNotesAreNotStale 钉住「推荐模型列表里的事实性说明不许过期」。
//
// 2026-10-06 真网关复测发现的缺陷：`target.go` 里写着
// 「这三个当前都返回 503 no_candidate」（2026-10-01 的实测），
// 而 2026-10-06 实测 `mimo-v2.5-asr` 已经返回 **200 并正确转写**。
//
// 这条注释是**事实性**的，不是描述性代码：它会直接让人把唯一可用的
// ASR 模型判成死路。与 §13 里 classifyNoteAsync 那条「Note 只有 Snippet，
// 完整内容在客户端」是同一类缺陷 —— 注释一旦过期就会变成
// 「不用再修了」的伪证据。
//
// 顺带钉住顺序：唯一实测可用的模型必须排第一。
func TestGatewayModelNotesAreNotStale(t *testing.T) {
	src, err := os.ReadFile("target.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// 负控：过期结论不许复活。
	if strings.Contains(s, "这三个是网关侧唯一值得预置的候选，当前都返回 503") {
		t.Error("target.go 仍写着「三个都返回 503」—— 2026-10-06 实测 mimo-v2.5-asr 已返回 200")
	}
	if strings.Contains(s, `Note: "网关 ASR 模型（目录里被标成 text）"`) {
		t.Error("target.go 仍用「目录里被标成 text」描述 mimo-v2.5-asr —— 实测它作为 ASR 可用，" +
			"这条备注会让人误判该模型不是 ASR")
	}
	// 正控：今天的实测结论必须在场，防止有人把注释整段删掉。
	if !strings.Contains(s, "mimo-v2.5-asr") {
		t.Error("target.go 里找不到 mimo-v2.5-asr")
	}
	if !strings.Contains(s, "503 no_provider") {
		t.Error("target.go 应如实记录另两个模型当前 503 no_provider 的实测状态")
	}

	// 顺序：唯一实测可用的那个排第一。
	opts := RecommendedGatewayModels()
	if len(opts) == 0 {
		t.Fatal("RecommendedGatewayModels 为空")
	}
	if opts[0].Model != "mimo-v2.5-asr" {
		t.Errorf("推荐列表第一位=%q，期望 mimo-v2.5-asr（2026-10-06 实测唯一可用）", opts[0].Model)
	}
	for _, o := range opts {
		if o.Note == "" {
			t.Errorf("模型 %q 没有说明文字：设置页要显示真实探测状态", o.Model)
		}
	}
}
