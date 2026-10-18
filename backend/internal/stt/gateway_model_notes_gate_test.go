package stt

import (
	"os"
	"regexp"
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

// catalogModalityClaim 匹配「网关目录**当前**没把它标成 audio」这一族说法。
//
// 为什么单独一道门（2026-10-18）：上面那道门把「过期结论」钉成了**一句过去的措辞**
// （`Note: "…（目录里被标成 text）"`），于是同一句假话换个说法就绕过去了 ——
// 实测 `target.go` 一度写着「网关 /models 未标 audio，勿按目录过滤掉它」，
// 而上面那道门 **PASS**。钉措辞的门，量的是「那句话还在不在」，不是「那句话成不成立」。
var catalogModalityClaim = regexp.MustCompile(`(?i)` +
	`(未标|没标|没有标|未标注|未标记|不标|不是|并非)[^，。；·|]{0,8}(audio|text|语音|音频)` +
	`|(被)?标(成|为|记|注)[^，。；·|]{0,4}(text|audio)`)

// TestNoCatalogModalityClaimInNotes 禁掉「Note 断言目录当前 modality 标注」这一族。
//
// ★ 这里禁的**不是某个值，是一整类断言**。理由是结构性的：
// 目录的 modality 标注会自己变（实测 2026-10-08 起 mimo-v2.5-asr 已标 audio，
// 而写这句话时目录还没标），而 Note 是编进二进制的
// ⇒ 任何写死「目录现在是什么」的说法都必然再次腐烂，且腐烂时没有任何门会响。
//
// 要表达的是**操作建议**（「别按 modality 过滤掉它」），不是**目录快照**
// （「目录没标 audio」）—— 前者今天成立、明天也成立。
func TestNoCatalogModalityClaimInNotes(t *testing.T) {
	all := []struct {
		fn   string
		opts []ModelOption
	}{
		{"RecommendedGatewayModels", RecommendedGatewayModels()},
		{"RecommendedExternalModels", RecommendedExternalModels()},
		{"RecommendedModels", RecommendedModels()},
	}
	for _, g := range all {
		if len(g.opts) == 0 {
			t.Fatalf("%s() 为空：这道门没量到任何 Note，等于没跑", g.fn)
		}
		for _, o := range g.opts {
			if m := catalogModalityClaim.FindString(o.Note); m != "" {
				t.Errorf("%s() 模型 %q 的 Note 断言了目录当前的 modality 标注（命中 %q）：\n  %s\n"+
					"目录标注会自己变 ⇒ 写死它必然再次腐烂，且届时没有门会响。请改成操作建议，别写目录快照。",
					g.fn, o.Model, m, o.Note)
			}
		}
	}

	// 负控：同一族的不同措辞都必须被抓到。
	// 少了这一段，这道门就退化成「钉死那一句」，也就是它要替换掉的那个形态。
	for _, s := range []string{
		"实测可用 · 网关 /models 未标 audio，勿按目录过滤掉它",
		"实测可用 · 网关目录没标 audio",
		"网关 ASR 模型（目录里被标成 text）",
		"该模型不是 audio 模型",
		"目录把它标注为 text",
		"网关侧未标注 audio 模态",
	} {
		if catalogModalityClaim.FindString(s) == "" {
			t.Errorf("负控失败：本该被这道门抓到却放过了 —— %q", s)
		}
	}

	// 正控：正当措辞不许被误伤，否则这道门会变成「一律不许提目录」。
	for _, s := range []string{
		"实测可用（2026-10-06 zh/en 全对）· 勿按目录的 modality 过滤掉它",
		"客户端勿改打 speech_to_text，网关内部适配",
		"最低价档，官方称支持 22 种中文方言",
		"会议首选：说话人分离 + 词级时间戳 + 热词，支持中文",
	} {
		if m := catalogModalityClaim.FindString(s); m != "" {
			t.Errorf("误伤：正当措辞被判违规（命中 %q）—— %q", m, s)
		}
	}
}
