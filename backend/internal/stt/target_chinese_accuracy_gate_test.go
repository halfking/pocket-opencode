package stt

import (
	"strings"
	"testing"
)

// TestExternalCandidatesCarryChineseAccuracy 钉住「外部 ASR 推荐清单必须携带
// **中文会议精度**这个维度」。
//
// ── 缺陷（§414）──
//
// 本仓的主场景是「中文会议录音」。而 `RecommendedExternalModels` 的每一条
// 标的维度只有：价格、语言数、是否流式、是否含 diarization ——
// **全文 0 处 CER**。
//
// ⚠ 后果实测（用户可见）：排在最前的 `openai/whisper-large-v3-turbo` 的
//
//	Note 是「最低价档，99+ 语言，可取 word 级时间戳」——
//	用户照这份清单选，选中的恰好是**中文最不准**的一档：
//	Whisper-large-v3 中文平均 CER 9.86%、**WenetSpeech 会议场景 18.87%**，
//	而同期中文专用模型 3.05%–4.56%。
//	⇒ **价格最低**与**精度最差**指向相反，清单只说前者就是在误导选型。
//
// ── 为什么这道门必须有 ──
//
// `TestGatewayModelNotesAreNotStale` 管的是**网关**候选的备注时效性与顺序，
// 不碰外部候选的精度维度。
//
// ⭐ 而这个维度的特点是**很容易被整段删掉而不触发任何别的门**：
// 它是 `Accuracy` 里的**一段散文**，不是类型、不是必填字段、
// 没有任何运行时行为依赖它。
// ⇒ 不专门钉住，下次有人「顺手精简一下备注」它就没了，而且门还是全绿。
//
// ⚠ 因此判据刻意**不**只判「清单里出现过 CER」——
//
//	那样把某一条的精度标注删掉、留另一条也能过。
//	必须**逐条**核对本场景下最该被标注的那一条。
func TestExternalCandidatesCarryChineseAccuracy(t *testing.T) {
	opts := RecommendedExternalModels()
	if len(opts) == 0 {
		t.Fatal("RecommendedExternalModels 为空")
	}

	// ① 维度必须存在：整个清单里至少有一条带中文精度标注。
	anyChinese := false
	for _, o := range opts {
		if strings.Contains(o.Accuracy, "CER") {
			anyChinese = true
			break
		}
	}
	if !anyChinese {
		t.Error("外部 ASR 清单里没有任何一条标注中文 CER —— " +
			"本仓主场景是中文会议录音，而清单此前只标价格/语言数/流式/diarization，" +
			"用户按它选会选中最便宜也最不准的一档（§414）")
	}

	// ② 承重：whisper-turbo 那一条**必须**同时带两样东西。
	//    只准加精度、**不准**因此丢掉原有的计费口径说明。
	const want = "openai/whisper-large-v3-turbo"
	var got string
	for _, o := range opts {
		if o.Model == want {
			got = o.Accuracy
			break
		}
	}
	if got == "" {
		t.Fatalf("推荐清单里找不到 %s —— 判据锚点没了，先确认清单是不是被改过", want)
	}
	if !strings.Contains(got, "CER") {
		t.Errorf("%s 的 Accuracy 没有中文 CER：\n%s", want, got)
	}
	if !strings.Contains(got, "会议") {
		t.Errorf("%s 的 Accuracy 只字未提**会议**场景 —— 而会议正是 Whisper 掉到 18.87%% 的那一档：\n%s", want, got)
	}
	if !strings.Contains(got, "pricing.prompt") {
		t.Errorf("%s 的 Accuracy 把原有的计费口径说明（pricing.prompt）弄丢了 —— "+
			"补精度不该以删掉别的维度为代价：\n%s", want, got)
	}

	// ③ 负控的形状：清单里**不允许**出现「本项目主场景下精度最差」这种
	//    结论被写回去却不带数字的空话（与 §413.6 同一个教训：
	//    门要钉「有人读」，不能只钉「那句话在」）。
	if strings.Contains(got, "最差") && !strings.Contains(got, "18.87") {
		t.Errorf("%s 断言了「最差」却没给数字（%q）—— 空话不能当证据", want, "18.87")
	}
}
