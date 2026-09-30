package stt

import "testing"

// TestIsASRCandidateExcludesTTS 锁死 2026-10-01 真机审计发现的缺陷：
// asrNameRe 里的 `voice` 会把**语音合成**模型拉进 ASR 候选。
//
// 现象（Redmi 真机，录音停止后的失败提示原文）：
//   网关暂无可用的语音转写模型（mimo-v2.5-asr=网关无上游 provider；
//   mimo-v2.5-tts-voiceclone=网关无上游 provider；mimo-v2.5-tts-voicedesign=…）
//
// 用户看到「语音转写失败」却收到两个 TTS 模型名，比只报一句通用文案更困惑。
// 另外探测预算是硬约束（maxProbeCandidates=6，网关限流实测 12 次/分钟），
// 两个注定失败的槽位被 TTS 吃掉，真正可能可用的 ASR 模型反而探不到。
func TestIsASRCandidateExcludesTTS(t *testing.T) {
	// 必须排除：语音合成模型。名字与 modality 取自 llm.kxpms.cn 真实目录。
	for _, id := range []string{
		"mimo-v2.5-tts-voiceclone",
		"mimo-v2.5-tts-voicedesign",
	} {
		for _, modality := range []string{"text", "audio"} {
			m := GatewayModel{ID: id, Modality: modality}
			if IsASRCandidate(m) {
				t.Errorf("IsASRCandidate(%s/%s)=true，语音合成模型不该进 ASR 候选", id, modality)
			}
		}
	}

	// 不能误杀：真正的 ASR / 多模态候选必须仍然入选。
	for _, m := range []GatewayModel{
		{ID: "gpt-audio", Modality: "audio"},
		{ID: "gpt-audio-mini", Modality: "audio"},
		{ID: "mimo-v2.5-asr", Modality: "text"}, // 网关把它错标成 text，只看 modality 会漏
		{ID: "whisper-large-v3"},
		{ID: "gpt-4o-transcribe"},
		{ID: "nemotron-3-nano-omni-30b-a3b-reasoning"},
		// TTS 词与强 ASR 标记同时出现的混合命名，不能被 ttsNameRe 误杀。
		{ID: "whisper-tts-hybrid", Modality: "text"},
	} {
		if !IsASRCandidate(m) {
			t.Errorf("IsASRCandidate(%s/%s)=false，它是可用的 ASR 候选，不该被 TTS 规则排除",
				m.ID, m.Modality)
		}
	}

	// 纯文本 / 嵌入模型依然不该被探测（防止规则放宽过头）。
	for _, m := range []GatewayModel{
		{ID: "deepseek-v4-pro", Modality: "text"},
		{ID: "text-embedding-3-small", Modality: "embedding"},
		{ID: "speak-nova-2", Modality: "text"}, // 只有 speak 词，纯 TTS
	} {
		if IsASRCandidate(m) {
			t.Errorf("IsASRCandidate(%s/%s)=true，该模型不是 ASR 候选", m.ID, m.Modality)
		}
	}
}
