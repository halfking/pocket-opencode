// llmbff_anthropic_unavailable_shape_test.go — §234：**特征化判据（不是目标判据）**。
//
// ⚠⚠ 本文件断言的是**当前的（有缺陷的）行为**，目的是把「这是一个真缺口」
// 钉成可复现的读数，而不是把缺口当成正确。
// **修复之后本文件必然转红** —— 那正是修复生效的信号，届时把
// `testAnthropicShapeIsNotRecognized` 改成断言 true 即可。
//
// ── 它守的缺口 ──
//
// 2026-10-08 真网关实测（§233 的 next_meeting 探针，12 次调用里 4 次失败）抓到的
// **同一个语义、两种错误体**：
//
//	chat 路径（§233 样本 1）：
//	  llm-gateway chat 503: {"error":{"code":"model_not_found",
//	    "gateway_debug":{...,"kind":"transient","retryable":true,...},
//	    "kind":"transient",
//	    "message":"No available provider for model 'glm-5.3'. All 1 candidates failed.",
//	    "request_id":"...","type":"server_error"}}
//
//	anthropic 路径（§233 样本 5/6/7 —— minimax-m3 走的就是这条）：
//	  llm-gateway anthropic messages 503:
//	    {"error":{"message":"No available provider for model 'minimax-m3'",
//	              "type":"overloaded_error"},"type":"error"}
//
// `isModelUnavailableError` 判的是 `Code`/`Kind` 三个字面量，
// 而 `llmGatewayNoCandidateBody` **结构上只反序列化这两个字段** ——
// anthropic 那条**没有 code、没有 kind** ⇒ 判 false ⇒
// `streamAttemptFallbackEligible` 为 false ⇒ **回退链不启动**，
// 用户拿到的是硬失败而不是「换一个模型再试一次」。
//
// ★ 这不是猜测：同一段注释里记着同一类问题在 chat 形状上已经被修过一次
//
//	（`llmbff_provider_adapters.go:71-74`：「2026-09-05 下午网关侧改版后再添
//	两个同义形状…code==\"model_not_found\" 与 kind==\"no_candidates\"」），
//	但**anthropic 形状漏了**。
//	既有门 `llmbff_provider_adapters_test.go:638` 也只测了 chat 形状。
//
// ⚠ 为什么本文件是**特征化**而不是目标判据：
//
//	修复点在 `llmbff_provider_adapters.go`，而那个文件是**并行会话的在制品**
//	（`git status` 里是 `M`，非本会话所有）。按纪律不擅自改别人的在途文件，
//	所以这里只负责**证明缺口存在 + 把实测原文钉住**，修复由属主/该会话决定。
package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 这三条是 §233 探针**实测**到的原文，逐字照抄，不构造、不改写。
const (
	// 形状一：chat 路径 —— 既有门已覆盖，这里作为**阳性对照**。
	anthropicGapChatShapeObserved = `llm-gateway chat 503: {"error":{"code":"model_not_found","gateway_debug":{"attempts":null,"kind":"transient","retryable":true,"stage":"execution","tried":1},"kind":"transient","message":"No available provider for model 'glm-5.3'. All 1 candidates failed.","request_id":"bbdf8dc2ae4b2e4255cf1ebc7ec356a8","type":"server_error"}}`

	// 形状二：anthropic 路径 —— **本轮发现的缺口**。
	anthropicGapObserved = `llm-gateway anthropic messages 503: {"error":{"message":"No available provider for model 'minimax-m3'","type":"overloaded_error"},"type":"error"}`
)

// anObservedErr 造一个 err，Error() 原样返回实测字符串。
func anObservedErr(raw string) error { return fmt.Errorf("%s", raw) }

func TestAnthropicNoProviderShapeIsNotRecognized(t *testing.T) {
	err := anObservedErr(anthropicGapObserved)

	// ⚠ 当前读数：false。修复后应为 true，届时本断言要改。
	if isModelUnavailableError(err) {
		t.Log("✅ 已修复：anthropic 形状现在能被识别为「模型不可用」⇒ 记得把本断言反过来并删掉本文件头部的说明")
		return
	}
	t.Log("⚠️ 缺口复现：anthropic 路径的 503「No available provider」被判为「非模型不可用」")
	t.Logf("   ⇒ isNoCandidateError=%v（没有 code=no_candidate）", isNoCandidateError(err))
	t.Logf("   ⇒ streamAttemptFallbackEligible=%v ⇒ 回退链不启动，用户拿硬失败",
		streamAttemptFallbackEligible(err, false))
	// 本文件不 fail：这是**记录**缺口，不是要求当前行为正确的门。
}

func TestAnthropicNoProviderShapeBlocksFallbackEligible(t *testing.T) {
	err := anObservedErr(anthropicGapObserved)

	// 两个 answered 取值都试：这条错误发生在一个字节都没吐出来的时候，
	// answered 只可能为 false，但两条都断是为了防止将来有人以为它与 answered 有关。
	for _, answered := range []bool{false, true} {
		got := streamAttemptFallbackEligible(err, answered)
		if got {
			t.Errorf("⚠️ 读数已变（answered=%v）：现在被判为可回退了 —— 若这是修复，请改本文件", answered)
			continue
		}
		t.Logf("缺口复现（answered=%v）：streamAttemptFallbackEligible=false ⇒ 不会换模型重试", answered)
	}
}

// ── 阳性对照：证明本文件用的判据本身是好的 ──
//
// 若上面两条转红的原因是「这两个函数恒返回 false」，那本文件就没有守住任何东西。
// 下面三条必须为 true，其中第一条用的就是**同一个模型**（minimax-m3 走 chat 路径时）。
func TestModelUnavailableRecognizedForChatShape(t *testing.T) {
	if !isModelUnavailableError(anObservedErr(anthropicGapChatShapeObserved)) {
		t.Fatal("量具坏了：chat 形状（code=model_not_found）竟然不被识别 —— " +
			"那么本文件「anthropic 形状不被识别」的读数就分不清是缺口还是量具问题")
	}
	if !streamAttemptFallbackEligible(anObservedErr(anthropicGapChatShapeObserved), false) {
		t.Fatal("量具坏了：chat 形状竟然不可回退")
	}
	t.Log("✅ 量具自证：chat 形状两条路径都判 true ⇒ 下面那些 false 是真的 false")
}

// ── 负控：不能「碰巧」什么都返回 false 就被判通过 ──
func TestUnrelatedErrorsStillNotRecognized(t *testing.T) {
	for name, raw := range map[string]string{
		"nil-形状无 JSON": `llm-gateway chat 503: connection refused`,
		"无关的 400":      `llm-gateway chat 400: {"error":{"code":"invalid_api_key","message":"bad key"}}`,
		"上游 500":       `llm-gateway chat 500: {"error":{"code":"internal","message":"boom"}}`,
	} {
		if isModelUnavailableError(anObservedErr(raw)) {
			t.Errorf("%s 被误判成「模型不可用」 ⇒ 判据过宽，无关错误也会触发换模型重试", name)
		}
	}
	if isModelUnavailableError(nil) {
		t.Error("nil 判成「模型不可用」")
	}
	if streamAttemptFallbackEligible(nil, false) {
		t.Error("nil 判成「可回退」")
	}
}

// ── 结构层证据：缺口不是「忘了写判断」，是**结构上取不到那个字段** ──
//
// 这是本文件最硬的一格：即使有人去改 isModelUnavailableError 去匹配 message，
// 只要 llmGatewayNoCandidateBody 还在，message 就会被丢掉。
// 本格把「错误体里确实有 message，但反序列化目标里没有该字段」钉成读数。
func TestNoCandidateBodyCannotSeeAnthropicMessage(t *testing.T) {
	var body llmGatewayNoCandidateBody
	if err := json.Unmarshal([]byte(`{"error":{"message":"No available provider for model 'minimax-m3'","type":"overloaded_error"},"type":"error"}`), &body); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if body.Error.Code != "" || body.Error.Kind != "" {
		t.Fatalf("读数已变：anthropic 形状现在落进了 Code=%q Kind=%q ⇒ 结构缺口可能已修",
			body.Error.Code, body.Error.Kind)
	}
	t.Log("⚠️ 缺口在**结构层**：llmGatewayNoCandidateBody 只有 Code/Kind 两个字段，")
	t.Log("   而 anthropic 形状把信息放在 message/type ⇒ 光改 isModelUnavailableError 够不到它")
	t.Log("   ⇒ 修复必须同时给结构体加 Message 字段（或改走别的识别方式）")
}
