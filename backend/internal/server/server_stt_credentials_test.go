package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// 本文件是 STT 凭据哨兵（__clear__）的判据。
//
// 缺陷来源：2026-10-08 真机验证（真实 pocketd + 真实 MiniMax key）时发现
// 「清除 MiniMax Key」点了没反应 —— hasMiniMaxKey 一直为 true，转写继续
// 用那个字符串当凭据，恒 401。
//
// 根因不是「清理分支没写」，而是**顺序**：先保存（把字面量 "__clear__"
// 当真凭据写进存储），再清理（读到的已是空串）。于是第一次碰巧成功，
// 之后每一次请求都会重复「先写回哨兵」⇒ 永远清不掉。
//
// ★ 这类缺陷单测结构上抓不到：它只在**连续两次**请求里显形
//   （第一次写、第二次清、第三次又写）。所以判据必须按序列请求，
//   而不是只发一次 —— 这一点比断言内容本身更重要。

// newSTTSettingsServer 造一个只带 STT 设置存储的 Server（不需要 PG，
// 走 MemStore 兜底，见 sttFallbackStore 的注释）。
func newSTTSettingsServer(t *testing.T) *Server {
	t.Helper()
	return &Server{}
}

func putSTTConfig(t *testing.T, s *Server, userID, wsID string, payload map[string]any) sttSettingsView {
	t.Helper()

	// 走**真实**的归一化 + 保存路径。
	//
	// 为什么不能用「直接构造 payload 再塞进 store」的简化写法：那样绕过了
	// normalizeMinimaxKeyInput，于是判据就量不到「哨兵有没有被归一化」——
	// 而那正是缺陷本身。判据必须打在会被用户请求走的那条路径上。
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		sttSettingsPayload
		ExternalAPIKey string  `json:"externalApiKey"`
		MiniMaxAPIKey  *string `json:"minimaxApiKey"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	key := normalizeExternalKeyInput(decoded.ExternalAPIKey)
	mmKey := normalizeMinimaxKeyInput(decoded.MiniMaxAPIKey)
	if err := s.saveSTTSettingsWithKeys(userID, wsID, decoded.sttSettingsPayload, key, mmKey); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved, _ := s.loadSTTSettings(userID, wsID)
	return sttSettingsView{
		sttSettingsPayload: saved,
		HasExternalKey:     s.sttExternalKey(userID, wsID) != "",
		HasMiniMaxKey:      s.sttMiniMaxKey(userID, wsID) != "",
	}
}

func TestMinimaxClearSentinelClearsRepeatedly(t *testing.T) {
	s := newSTTSettingsServer(t)
	const user, ws = "u-clear", "w-clear"

	base := map[string]any{
		"channel": stt.ChannelMiniMax, "provider": stt.ProviderMiniMax,
		"minimaxBaseURL": "https://api.minimax.cn", "minimaxModel": "asr-1.0",
		"language": "zh",
	}

	// 第 1 次：存真 key。
	p1 := cloneMap(base)
	p1["minimaxApiKey"] = "sk-api-real-key"
	view1 := putSTTConfig(t, s, user, ws, p1)
	if !view1.HasMiniMaxKey {
		t.Fatalf("第 1 次：存了 key 却报 hasMiniMaxKey=false")
	}

	// 第 2 次：清空。
	p2 := cloneMap(base)
	p2["minimaxApiKey"] = sttClearSentinel
	view2 := putSTTConfig(t, s, user, ws, p2)
	if view2.HasMiniMaxKey {
		t.Errorf("第 2 次：发 %q 后 hasMiniMaxKey 仍为 true —— 清空没生效", sttClearSentinel)
	}

	// ★ 第 3 次是判据的核心：再清一次。
	//   缺陷形态正是「第 2 次看起来对（因为它后置清理时读到的是空串），
	//   但存储里其实被写进了哨兵字面量，第 3 次又被写回来」。
	p3 := cloneMap(base)
	p3["minimaxApiKey"] = sttClearSentinel
	view3 := putSTTConfig(t, s, user, ws, p3)
	if view3.HasMiniMaxKey {
		t.Errorf("第 3 次：再次清空后 hasMiniMaxKey 仍为 true ⇒ 哨兵字面量被当成凭据存了进去" +
			"（先存后清的顺序缺陷），用户怎么点都没用")
	}

	// ★ 存储里绝不能留下哨兵字面量：它一旦落进去，就会变成一把"能用"的
	//   凭据，排查时看到的是「key 已设置」—— 比直接报错难查得多。
	if got := s.sttMiniMaxKey(user, ws); strings.TrimSpace(got) == sttClearSentinel {
		t.Errorf("存储里的 Secret 是哨兵字面量 %q，这是一把假凭据", sttClearSentinel)
	}
	if got := s.sttMiniMaxKey(user, ws); got != "" {
		t.Errorf("清空后 Secret 应为空串，实际 %q", got)
	}
}

// TestMinimaxOmittedKeyMeansUnchanged 钉住「不传 = 不改动」。
//
// 这是与「清空」必须区分开的第二种语义。若把两者混同，
// 用户只改一下语种就会连带清掉 key（因为保存会带上未改动的空字段）。
func TestMinimaxOmittedKeyMeansUnchanged(t *testing.T) {
	s := newSTTSettingsServer(t)
	const user, ws = "u-omit", "w-omit"

	p := map[string]any{
		"channel": stt.ChannelMiniMax, "provider": stt.ProviderMiniMax,
		"minimaxModel": "asr-1.0", "language": "zh",
		"minimaxApiKey": "sk-api-keep-me",
	}
	putSTTConfig(t, s, user, ws, p)

	// 只改语种，**不带** minimaxApiKey 字段。
	p2 := cloneMap(p)
	delete(p2, "minimaxApiKey")
	p2["language"] = "ja"
	view := putSTTConfig(t, s, user, ws, p2)
	if !view.HasMiniMaxKey {
		t.Errorf("没提交 minimaxApiKey 时不该动它（用户只改了语种），但 hasMiniMaxKey 变成了 false")
	}
	if got := s.sttMiniMaxKey(user, ws); got != "sk-api-keep-me" {
		t.Errorf("没提交时凭据应原样保留，实际 %q", got)
	}
}

// TestExternalClearSentinelAlsoConverges 钉住外部 key 的同类收敛。
//
// 外部 key 的既有语义是「空串 = 清空」（老契约），本轮只做最小修复：
// 把哨兵在入口归一化，消除「先存后清」的中间态。不改它的缺省语义。
func TestExternalClearSentinelAlsoConverges(t *testing.T) {
	s := newSTTSettingsServer(t)
	const user, ws = "u-ext", "w-ext"

	p := map[string]any{
		"channel": stt.ChannelExternal, "externalBaseURL": "https://api.openai.com/v1",
		"externalModel": "gpt-4o-mini-transcribe", "language": "zh",
		"externalApiKey": "sk-ext-real",
	}
	putSTTConfig(t, s, user, ws, p)

	for i := 1; i <= 3; i++ {
		p2 := cloneMap(p)
		p2["externalApiKey"] = sttClearSentinel
		view := putSTTConfig(t, s, user, ws, p2)
		if view.HasExternalKey {
			t.Errorf("第 %d 次清空外部 key 后 hasExternalKey 仍为 true", i)
		}
		if got := s.sttExternalKey(user, ws); got != "" {
			t.Errorf("第 %d 次：存储里的 Secret 应为空，实际 %q", i, got)
		}
	}
}

// TestSentinelLiteralRejectedByNormalizer 钉住归一化函数本身的三态。
func TestSentinelLiteralRejectedByNormalizer(t *testing.T) {
	// nil → nil（不改动）
	if got := normalizeMinimaxKeyInput(nil); got != nil {
		t.Errorf("nil 应原样返回 nil（本次不改动），实际 %v", got)
	}
	// 哨兵 → 空串指针（清空）
	empty := ""
	got := normalizeMinimaxKeyInput(&empty)
	got = normalizeMinimaxKeyInput(ptr(sttClearSentinel))
	if got == nil || *got != "" {
		t.Errorf("哨兵应归一化成「显式清空」，实际 %v", got)
	}
	// 真实 key → 去空白后保留
	got2 := normalizeMinimaxKeyInput(ptr("  sk-api-xyz  "))
	if got2 == nil || *got2 != "sk-api-xyz" {
		t.Errorf("真实 key 应去空白后保留，实际 %v", got2)
	}
	// 外部 key 同构
	if v := normalizeExternalKeyInput(sttClearSentinel); v != "" {
		t.Errorf("外部 key 的哨兵应归一化成空串，实际 %q", v)
	}
	if v := normalizeExternalKeyInput("  sk-ext  "); v != "sk-ext" {
		t.Errorf("外部 key 应去空白，实际 %q", v)
	}
}

// TestUnknownTemplateRejectedOnSave 钉住「保存时拒」而不是「转写时才报」。
//
// 理由：转写报错发生在用户已经开始录音之后，那时才知道配置错了是最坏的时机。
func TestUnknownTemplateRejectedOnSave(t *testing.T) {
	s := newSTTSettingsServer(t)
	err := s.saveSTTSettingsWithKeys("u", "w", sttSettingsPayload{
		Channel: stt.ChannelExternal, Provider: "no-such-template", Language: "zh",
	}, "k", nil)
	if err == nil {
		t.Fatalf("未知模板 id 必须在保存时被拒")
	}
	if !strings.Contains(err.Error(), "no-such-template") {
		t.Errorf("错误里要点名那个模板，实际：%v", err)
	}
	if !strings.Contains(err.Error(), stt.ProviderMiniMax) {
		t.Errorf("错误里应列出可选模板（否则用户无从下手），实际：%v", err)
	}
}

// TestResolveTargetWithoutMiniMaxKeyIsHonest 钉住「没配 key」的诚实报错。
//
// 不许回退到别的通道：那会让用户以为自己配的 MiniMax 在用，
// 实际转写走的是别处 —— 一个不报错的错配。
func TestResolveTargetWithoutMiniMaxKeyIsHonest(t *testing.T) {
	s := newSTTSettingsServer(t)
	_, err := s.resolveSTTTarget(context.Background(), stt.Scope{UserID: "u-none", WorkspaceID: "w-none"})
	if err == nil {
		t.Fatalf("没有任何配置时必须报错")
	}
	if !strings.Contains(err.Error(), "stt_unavailable") {
		t.Errorf("错误应带 stt_unavailable 前缀（前端按它渲染），实际：%v", err)
	}
}

func ptr(s string) *string { return &s }

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
