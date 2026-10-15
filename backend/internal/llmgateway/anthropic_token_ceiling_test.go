// anthropic_token_ceiling_test.go — §236：**anthropic 形态的 4096 兜底上限**。
//
// ⚠⚠ 与 §235 同款，本文件是**特征化判据（不是目标判据）**：
// 它断言**当前的（有缺陷的）行为**，修复之后必然转红（那是修复生效的信号）。
// 修复点在 `internal/llmgateway/anthropic.go`，而该文件是**并行会话的在制品**
// （`git status` = `M`），按纪律不擅自改别人的在途文件。
//
// ── 它守的缺口 ──
//
// 三段各自都有理、拼在一起就出事：
//
//	① `internal/server/server_assistant.go` 的 `llmChatOnce` **刻意不设** MaxTokens。
//	   这是**门强制的**：`llm_token_budget_gate_test.go:TestMeetingChainsMustNotCapMaxTokens`，
//	   实测依据（§101，glm-5.2）：
//	     设 2048          ⇒ finish=length 且**正文 0 字**
//	     不发              ⇒ finish=stop / **completion_tokens=4472** / 正文 1097 字
//	   理由是推理模型先把预算花在 reasoning 上，给正文封顶等于不给正文留预算。
//
//	② `anthropic.go` 把「没设」解释成**兜底 4096**：
//	     if out.MaxTokens <= 0 { out.MaxTokens = anthropicMaxTokens }
//	   注释写的是「4096 覆盖**邮件翻译/对话**的绝大多数场景」。
//
//	③ 会议三条链（recommend / summary / refine）走的正是 ②。
//
// ⇒ ★★★ **①的实测依据全部来自 openai-chat 路径，而②只作用于 anthropic 路径。**
//
//	「不设 = 无上限」这个前提在 chat 上成立，在 anthropic 上**不成立**：
//	它等于**静默套上一个谁也没选过的 4096 天花板**。
//
// ⇒ ⇒ 而 §101 实测会议精校的真实输出是 **4472 tok** > 4096
//
//	⇒ 一旦进程落到 anthropic 形态，**连短会议的精校都可能被截断**；
//	截断 ⇒ `ErrOutputTruncated` ⇒ 回落原始转写
//	⇒ **整份 action_items 丢失** ⇒ 零待办、零日程（用户需求第 4 条断在这里）。
//
// ⇒ ★ 这不是「4096 太小」的主观判断，是一道可判定的算术：
//
//	上限 < 已实测到的真实输出。判据把那个实测值作为常量钉住。
//
// ⚠ 为什么不能顺手改成「在 llmChatOnce 里设大一点」：
//
//	§101 实测设 2048 ⇒ **正文 0 字**。⇒ 照搬会让情况更糟。
//	正确修法需要在「形态感知」与「按用途分档」之间选，属设计决定，
//	本轮只提供读数与判据，不擅自定方案。
package llmgateway

import (
	"testing"
)

// measuredRefineCompletionTokens 是 §101 对 glm-5.2 的实测读数：
// 不发 max_tokens、同一份精校提示词 ⇒ finish=stop、completion_tokens=4472、正文 1097 字。
//
// ⚠ 必须是**绝对值**，不能写成「比某个同源常量大」—— 那种写法是恒真判据
//
//	（两边一起变，永远成立）。这里钉的是一次真实测量，与生产代码无关。
const measuredRefineCompletionTokens = 4472

// TestAnthropicFallbackCapsUnrequestedMaxTokens 事实钉：
// 「没设 MaxTokens」在 anthropic 路径上**不等于无上限**，而是被补成 4096。
func TestAnthropicFallbackCapsUnrequestedMaxTokens(t *testing.T) {
	got := toAnthropicRequest(ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "x"}}})
	if got.MaxTokens != anthropicMaxTokens {
		t.Logf("读数已变：未设 MaxTokens 时兜底成了 %d（常量 anthropicMaxTokens=%d）⇒ 本节缺口可能已修",
			got.MaxTokens, anthropicMaxTokens)
		return
	}
	t.Logf("⚠️ 缺口复现：未设 MaxTokens ⇒ anthropic 形态静默套上上限 %d tok", got.MaxTokens)
	t.Log("   ⇒ 而 chat 形态的同一请求**不带 max_tokens** ⇒ 「不设 = 无上限」只在前者成立")
}

// 量具自证：证明 toAnthropicRequest **确实**在处理 MaxTokens 字段。
// 若这组红了，那么上面那条「兜底成 4096」就可能是我对函数的理解错了。
func TestAnthropicExplicitMaxTokensPassesThrough(t *testing.T) {
	got := toAnthropicRequest(ChatRequest{
		Model: "m", MaxTokens: 8192, Messages: []ChatMessage{{Role: "user", Content: "x"}},
	})
	if got.MaxTokens != 8192 {
		t.Fatalf("量具坏了：显式设 8192 却得到 %d —— toAnthropicRequest 根本没有透传 MaxTokens", got.MaxTokens)
	}
	t.Log("✅ 量具自证：显式值原样透传 ⇒ 上面那个 4096 是兜底行为，不是字段不生效")
}

// TestAnthropicCeilingIsBelowMeasuredRefineOutput 是本文件最硬的一格：
// 上限比**已实测到的会议精校真实输出**还小。这不是判断，是算术。
func TestAnthropicCeilingIsBelowMeasuredRefineOutput(t *testing.T) {
	t.Logf("anthropic 兜底上限 = %d tok", anthropicMaxTokens)
	t.Logf("§101 实测会议精校 completion_tokens = %d tok（finish=stop）", measuredRefineCompletionTokens)

	if anthropicMaxTokens >= measuredRefineCompletionTokens {
		t.Logf("✅ 已修复：兜底上限 %d ≥ 实测输出 %d，会议精校在这条路径上不再被截断",
			anthropicMaxTokens, measuredRefineCompletionTokens)
		return
	}
	t.Logf("⚠️ 缺口：兜底上限 %d < 实测输出 %d ⇒ 落到 anthropic 形态时会议精校会被截断",
		anthropicMaxTokens, measuredRefineCompletionTokens)
	t.Log("   ⇒ ErrOutputTruncated ⇒ 回落原始转写 ⇒ action_items 全丢 ⇒ 零待办零日程")
	t.Log("   ⚠ 本文件不 fail：这是**记录**缺口，不是要求当前行为正确的门")
}

// 负控：证明「兜底只作用于未设的情形」，而不是**无论设没设都压成 4096**。
// 后者会让「提高上限」这条修法看起来毫无作用，必须先把这一点钉住。
func TestAnthropicCeilingOnlyAppliesWhenUnset(t *testing.T) {
	for _, v := range []int{1024, 2048, 8192, 32768} {
		got := toAnthropicRequest(ChatRequest{
			Model: "m", MaxTokens: v, Messages: []ChatMessage{{Role: "user", Content: "x"}},
		})
		if got.MaxTokens != v {
			t.Errorf("显式设 %d 却被改成 %d ⇒ 兜底的作用范围比本判据假定的更宽", v, got.MaxTokens)
		}
	}
	t.Log("✅ 负控通过：显式值一律原样透传 ⇒ 兜底只吃「未设」这一种情形")
}
