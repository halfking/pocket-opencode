// refine_prompt_contract_test.go — 门禁：精校的提示词必须写死「只纠错、不改写」。
//
// 2026-10-07 真网关实测发现的缺陷（TestLiveGatewayRefineFixesASRErrors，
// 喂 §27 测出的真实 ASR 错误样本）。原提示词写的是
// 「请**润色**以下会议转写」—— 「润色」这个词本身就是根：
// 它**要求**模型改写文本，而这条链路的职责是纠错。实测：
//
//	原句完全正确： 张伟和李娜都会参加，王强负责做会议纪要。
//	精校后：       张伟和李娜两位均已确认参加会议；会议纪要由王强负责记录。
//	                ↑ 被改写
//	原句： 请把接口升级到v2.5版本，并同步更新Challog。
//	精校后：…并同步更新 Changelog（变更日志）。      ← 加了原文没有的内容
//	原句：…跑一遍页百零一…（CI 被听成中文谐音）
//	精校后：…跑一遍 101…                              ← 改错
//
// ⇒ 对会议纪要产品，「精校结果与原始转写语义偏离」比「不纠错」严重得多：
//
//	**误改用户发现不了**（他不知道原文是什么），漏修一眼能看出来。
//	这个不对称决定了提示词该偏向哪一侧。
//
// ⚠ 这门是**源码断言**，钉的是「禁令字面存在」这件事。
//
//	它能抓住：禁令被删 / 被改回「润色」/ 数字禁令被去掉（第一版就犯过）。
//	它抓不住：禁令存在但模型不遵守 —— 那个只能靠真网关探针
//	（TestLiveGatewayRefineFixesASRErrors，env 门控）。
//	两层缺一不可：源码门保证契约不被静默删掉，探针保证契约真的有效。
package server

import (
	"os"
	"strings"
	"testing"
)

// goFuncBody 取出以 sig 开头的那个函数体，并剥掉注释。
//
// ★ 剥注释是硬要求，不是洁癖：本会话已经因「源码扫描被注释满足」踩了四次
//
//	（§13.5 found.Snippet、§17「精翻完成」、§22「相同格式」、§25 的常量使用点）。
//	而**本文件自己**的注释里就写满了「只改错不改写」「不得增加原文没有的内容」
//	这些字样 —— 不剥注释的话，这道门第一版会 100% 恒真。
func goFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("找不到 %s", sig)
	}
	j := strings.Index(src[i:], "\nfunc ")
	if j < 0 {
		j = len(src) - i
	}
	return stripGoComments(src[i : i+j])
}

// refinePromptSource 取出精校提示词的源码（剥掉注释）。
//
// ⚠ 2026-10-07：提示词已从 llmMeetingRefine 抽进 buildRefinePrompt
//
//	（为了让它能被行为断言覆盖）。本门仍读源码，所以取源范围必须跟着扩 ——
//	只扫 llmMeetingRefine 的话，这三条门会**假红**（字面量搬走了），
//	而更糟的形态是下一个人把范围悄悄改回去，门又假绿。
func refinePromptSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("server_meeting.go")
	if err != nil {
		t.Fatalf("读 server_meeting.go: %v", err)
	}
	src := string(raw)
	return goFuncBody(t, src, "func (s *Server) llmMeetingRefine(") + "\n" +
		goFuncBody(t, src, "func buildRefinePrompt(")
}

func TestRefinePromptDemandsCorrectionNotRewriting(t *testing.T) {
	code := refinePromptSource(t)

	// 1) 根因词：不得再出现「润色」。它要求的是改写，与职责相反。
	if strings.Contains(code, "润色") {
		t.Error("提示词里又出现「润色」—— 那要求模型改写文本，而这条链路的职责是纠错。" +
			"2026-10-07 实测：写「润色」时模型会把完全正确的句子改写掉")
	}

	// 2) 四条核心禁令必须字面在提示词里（不在注释里）。
	for _, must := range []struct{ what, lit string }{
		{"只纠错不改写", "只改错，不改写"},
		{"不得增加内容", "不得增加原文没有的内容"},
		{"不得删除内容", "不得删除原文有的内容"},
		{"保留说话人标记", "[说话人]"},
	} {
		if !strings.Contains(code, must.lit) {
			t.Errorf("提示词缺少「%s」禁令（字面量 %q）", must.what, must.lit)
		}
	}
}

func TestRefinePromptMustNotTouchNumbers(t *testing.T) {
	code := refinePromptSource(t)

	// ⚠ 这条是**实测逼出来的**，不是预防性设计。
	//
	// 第一版重写提示词时，我在「重点纠正」里写了「明显的数字错误」，
	// 同一批样本复测：原文「下周一就是十号」（本来正确）被改成「十二号」。
	// 模型无从知道正确答案是几，猜的那一下必然错，而用户看不出来。
	//
	// ⇒ 提示词必须**禁止**改数字，且不得再把数字错误列进「要纠正」的范围。
	if !strings.Contains(code, "不得改动数字") {
		t.Error("提示词缺少「不得改动数字」—— 模型会猜数字，且用户看不出猜错了")
	}
	// 反向：也不能把数字错误列进应当纠正的范围（与上一条互相印证）。
	if strings.Contains(code, "明显的数字错误") {
		t.Error("提示词又把「明显的数字错误」列进纠正范围了 —— 这会诱导模型猜数字。" +
			"2026-10-07 实测：原文正确的「十号」被改成「十二号」")
	}
}

func TestRefinePromptKeepsJSONSchema(t *testing.T) {
	code := refinePromptSource(t)
	// §22 立的规矩：要求模型返回 JSON 时，schema 必须**字面带上**，
	// 不能用「相同格式」这种跨轮次无所指的指代。
	for _, f := range []string{
		`\"refined_transcript\"`, `\"translations\"`, `\"structured_minutes\"`,
		`\"agenda\"`, `\"decisions\"`, `\"action_items\"`, `\"next_meeting\"`, `\"todos\"`,
	} {
		if !strings.Contains(code, f) {
			t.Errorf("精校提示词缺字段 %s —— 模型可能返回缺字段的 JSON", f)
		}
	}
}
