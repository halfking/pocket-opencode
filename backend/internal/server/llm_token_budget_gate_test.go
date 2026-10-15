// llm_token_budget_gate_test.go — §101：**会议链不得设置 `max_tokens`**。
//
// ── 为什么这条值得立成门 ────────────────────────────────────────────
//
// `llmChatOnce` 构造的 `llmbff.ChatRequest` 里**没有** `MaxTokens`，而
// `llmgateway.ChatRequest.MaxTokens` 的 tag 是 `omitempty`
// （`internal/llmgateway/client.go:188`）⇒ `max_tokens` 整个不进 payload，
// 上限由网关/模型自己决定。
//
// 这个「什么都没设」看起来像是遗漏，实际上是**实测逼出来的正确选择**：
//
//	glm-5.2（推理模型），§101 生产口径 vs 探测口径，同一条 refine 提示词：
//	  不发 max_tokens → finish="stop"，completion_tokens=4472，正文 1097 字，60.1s
//	  发   max_tokens=2048 → finish="length"，completion_tokens=2048，**正文 0**，31.9s
//
// 推理模型先把 token 花在 reasoning 上，给正文的上限等于**只给推理留预算**，
// 于是正文一个字都吐不出来 —— 这与 `handleNoteSummarize` 注释里记的
// 「max_tokens=300 → content 长度 0，finish_reason=length」是同一个坑。
//
// ★ 而这个坑**极易被"好心"踩回去**：任何一次「这条链太慢，给它加个上限吧」的
//
//	优化，都会精确地把正文清零。本门就是拦这个动作的。
//
// 三条走 `llmChatOnce` 的链（recommend / summary / refine）共用这一处构造，
// 所以这道门一次守住三条链。
package server

import (
	"strings"
	"testing"
)

// TestMeetingChainsMustNotCapMaxTokens 钉住 `llmChatOnce` 的请求构造里
// 不得出现 `MaxTokens`。
//
// ⚠ 这是源码门：它守的是**「这里什么都没设」**这件无法用行为断言表达的事
// （行为上「不设」与「设成 0」完全同形）。局限写在下面：
// 若有人改成 `llmChatOnce(ctx, r, model, msgs, maxTokens)` 再从调用方传值，
// 本门只看构造体就看不见了 —— 所以下面那条负控刻意用**同形**的形态。
func TestMeetingChainsMustNotCapMaxTokens(t *testing.T) {
	src := readFrontend(t, "server_assistant.go")
	body := goFuncBody(t, src, "func (s *Server) llmChatOnce(")

	i := strings.Index(body, "llmbff.ChatRequest{")
	if i < 0 {
		t.Fatalf("llmChatOnce 里找不到 llmbff.ChatRequest{ —— 实现改写法了，本门失效，请重读 §101")
	}
	lit := matchedBraces(t, body, i+len("llmbff.ChatRequest"))
	if strings.Contains(lit, "MaxTokens") {
		t.Fatalf("llmChatOnce 给请求设了 MaxTokens：\n%s\n"+
			"实测（§101，glm-5.2）：设 2048 ⇒ finish=length 且**正文 0 字**；"+
			"不发 ⇒ finish=stop / 4472 tok / 正文 1097 字。"+
			"推理模型先把预算花在 reasoning 上，给正文封顶等于不给正文留预算。",
			lit)
	}
	// 顺带钉住「确实构造了请求」——否则上面那条恒真（改成别的构造方式就空过）。
	if !strings.Contains(lit, "Model:") {
		t.Fatalf("构造体里连 Model 都没有，判据前提失效：%s", lit)
	}
}

// TestMeetingChainsMustNotCapMaxTokens_HasTeeth 负控：证明上面那条检测器
// 在**同形**的违规样本上会转红。
//
// ⚠ 同形是硬要求（§95.3 的教训）：第一版负控写成了「设置别的东西」，
// 检测器只找 `MaxTokens` 字面量，样本里若写成 `maxTokens` 就漏了。
// 这里刻意让样本与真实构造体**形状一致**（同一个 struct 字面量、同样的字段写法）。
func TestMeetingChainsMustNotCapMaxTokens_HasTeeth(t *testing.T) {
	const violating = `func (s *Server) llmChatOnce(ctx context.Context, model string, msgs []aigate.ChatMessage) (string, string, error) {
	if s.llmBFF != nil {
		resp, err := s.llmBFF.Chat(ctx, llmbff.ChatRequest{
			WorkspaceID: "ws",
			Model:       model,
			Messages:    bffMsgs,
			MaxTokens:   2048,
			User:        "u",
		}, "meeting")
		_ = resp
		_ = err
	}
	return "", "", nil
}`
	// pad 让锚点不落在第 0 个字符：解析器若把绝对偏移传进已切片字符串，
	// 在 pad=0 的样本上恰好也对（§92.3 踩过这个坑）。
	body := goFuncBody(t, "const pad = 1\n"+violating, "func (s *Server) llmChatOnce(")
	i := strings.Index(body, "llmbff.ChatRequest{")
	if i < 0 {
		t.Fatalf("负控样本里找不到 ChatRequest 构造，负控失效")
	}
	lit := matchedBraces(t, body, i+len("llmbff.ChatRequest"))
	if !strings.Contains(lit, "MaxTokens") {
		t.Fatalf("负控失败：检测器认不出同形的违规样本 ⇒ TestMeetingChainsMustNotCapMaxTokens 形同虚设。样本构造体：%s", lit)
	}
}

// TestNoteSummarizeTokenCapIsScoped 只做读数，登记一个**例外**。
//
// `handleNoteSummarize` **确实**设了 `MaxTokens: 2048`（server_assistant.go:688），
// 且有它自己的实测依据（2026-10-02：max_tokens=300 → 正文 0；2000 → 正文 63 字 /
// finish=stop）—— 笔记摘要只要 3~5 句话，不是 glm-5.2 那种长正文。
//
// ⇒ 门只覆盖走 `llmChatOnce` 的三条会议链，**不覆盖**随手记摘要。
//
//	把这条读数删掉的话，下一个人会顺手把 2048 也删掉，白白丢掉那个已验证的修复。
func TestNoteSummarizeTokenCapIsScoped(t *testing.T) {
	src := readFrontend(t, "server_assistant.go")
	body := goFuncBody(t, src, "func (s *Server) handleNoteSummarize(")
	if !strings.Contains(body, "MaxTokens: 2048") {
		t.Logf("★ handleNoteSummarize 里的 MaxTokens: 2048 不在了 —— " +
			"本登记已过期。若是有意删除请忽略；若不是，2026-10-02 那次实测" +
			"（300 → 正文 0；2000 → 正文 63 字）说明这个上限是被逼出来的")
		return
	}
	t.Log("现状：随手记摘要单独设了 MaxTokens: 2048（不经 llmChatOnce），" +
		"有 2026-10-02 的实测支撑；会议三条链不设。本门只守会议链，别把它一起删了")
}
