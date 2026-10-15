// strip_think_gate_test.go — §338：`<think>` 剥离的判据。
//
// ⚠ 判据的可信度**不由本文件自证**，由变异台 `/tmp/opstt/338` 给。
//
// 为什么值得钉一条门
// ------------------
// §329.5 记的是「extractJSON 在 7/90 次里解析失败」，
// §337.6 又在一条**有工具命中**的运行上抓到同款（整场总结回落成空）。
// 而它每 90 次就要吃掉一次总结，代价远高于它看起来的严重程度。
//
// ★ 金丝雀夹具复刻的是**真实响应的结构**（2026-10-09 真网关落盘
//
//	/tmp/opstt/3368/H_long_body_neutral_title-04.json）：
//	think 里**预告整份 JSON** → think 后再给一份真 JSON。
//	这个「两份 JSON」的形状是缺陷的关键 —— 只有一份 JSON 时，
//	第一个 `{` 到最后一个 `}` 恰好是对的，缺陷不显形。
//
// ⚠ 期望值是**手写的字面量**，不是从被测对象算出来的。
package server

import (
	"encoding/json"
	"strings"
	"testing"
)

const thinkCanaryPreview = `<think>
检索结果：
- 北极星项目：找到1条笔记 [nt_9f3a2c1e]
现在生成JSON格式的会议纪要：

` + "```json" + `
{"summary":"预告版","key_points":["a"]}
` + "```" + `
</think>`

const thinkCanaryReal = `
` + "```json" + `
{"summary":"正式版","key_points":["b"]}
` + "```"

// TestStripThinkBlocksRemovesPreviewedJSON 是本门的主判据：
// think 里的**预告版**JSON 必须被剥掉，取回的只能是正式版。
func TestStripThinkBlocksRemovesPreviewedJSON(t *testing.T) {
	out := extractJSON(thinkCanaryPreview + thinkCanaryReal)
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("剥 think 后仍解析不了（两份 JSON 被拼在一起了）: %v\n取回：%q", err, out)
	}
	// ⚠ 期望值是手写字面量：预览版与正式版的 summary 刻意不同，
	//   所以「拿到哪一份」这件事**无法被恒真判据蒙过去**。
	if got["summary"] != "正式版" {
		t.Fatalf("summary = %q，应为 %q（取到了 think 里的预告版？）",
			got["summary"], "正式版")
	}
}

// TestStripThinkBlocksLeavesPlainInputUntouched 是**负样本**：
// 没有 think 的输入必须逐字不变 —— 否则这条修法会伤到绝大多数正常响应。
func TestStripThinkBlocksLeavesPlainInputUntouched(t *testing.T) {
	plain := "会议纪要如下：\n```json\n{\"summary\":\"无推理段\"}\n```\n以上。"
	if got := stripThinkBlocks(plain); got != plain {
		t.Errorf("无 think 的输入被改动了：\n 原：%q\n 后：%q", plain, got)
	}
	// 且取值结果必须与修复前一致。
	var obj map[string]any
	if err := json.Unmarshal([]byte(extractJSON(plain)), &obj); err != nil {
		t.Fatalf("普通输入解析失败：%v", err)
	}
	if obj["summary"] != "无推理段" {
		t.Errorf("summary = %q，应为 %q", obj["summary"], "无推理段")
	}
}

// TestStripThinkBlocksUnclosedIsLeftAlone 钉住那条取舍：
// **未闭合的 think 按原样返回**，不做「切到末尾」那件事。
//
// 为什么要有这条：未闭合可能是模型忘了闭合，也可能是我们把一段
// 跨行 JSON 误判成 think。后者若按「切到末尾」处理会**丢掉真 JSON** ——
// 拿一个未证实的假设换一个已知失败，不划算。
func TestStripThinkBlocksUnclosedIsLeftAlone(t *testing.T) {
	unclosed := "<think>分析到一半被截断了" // 没有 </think>
	if got := stripThinkBlocks(unclosed); got != unclosed {
		t.Errorf("未闭合的 think 被改动（应原样返回）：\n 原：%q\n 后：%q", unclosed, got)
	}

	// ★ 负样本的关键：正文里只有一段 think、**真 JSON 在它之后**且未闭合标签。
	//   若将来有人把「未闭合 ⇒ 切到末尾」改成真值，这一条必须打红，
	//   因为那会正好把真 JSON 切掉。
	withReal := "<think>{预告}{\"summary\":\"真\"}"
	if got := stripThinkBlocks(withReal); strings.Contains(got, "真") {
		// 原样返回 ⇒ 「真」还在，这是**期望**行为。
		t.Logf("未闭合时原样返回（真 JSON 未被丢弃）：%q", got)
	} else {
		t.Errorf("未闭合时真 JSON 被切掉了 —— 这正是本函数刻意避免的失败")
	}
}

// TestStripThinkBlocksHandlesMultipleBlocks 覆盖循环路径：
// reasoning 模型可能吐多段 think。
func TestStripThinkBlocksHandlesMultipleBlocks(t *testing.T) {
	// ⚠ 夹具纪律：断言用的标记字「甲乙」**不能**出现在 JSON 正文里 ——
	//   第一版这里写的是 `{"summary":"唯一"}` 而断言查「一」不在输出，
	//   结果断言命中的正是 summary 里的那个「一」。
	//   这与 §334.6 同族：判据用了被测数据里也出现的字。
	in := "<think>甲</think>{\"summary\":\"正式内容\"}<think>乙</think>尾巴"
	out := stripThinkBlocks(in)
	if strings.Contains(out, "甲") || strings.Contains(out, "乙") {
		t.Errorf("多段 think 没有全部剥掉：%q", out)
	}
	if !strings.Contains(out, "正式内容") {
		t.Errorf("真 JSON 被误删：%q", out)
	}
}

// TestParseGatewaySummaryStripsThink 是**邮件侧**那条同款修复的判据。
//
// ⚠ 为什么必须单独一条：`parseGatewaySummary` 与 `extractJSON` 是两处
// 「第一个 { 到最后一个 }」的取法（§338.2 扫出来的），共用同一个
// `stripThinkBlocks`；但**只给会议侧写判据的话，邮件侧的调用被删掉时
// 全仓库依然全绿** —— 那就是「改了但没测」。
func TestParseGatewaySummaryStripsThink(t *testing.T) {
	// 与会议侧同构：think 里预告一份，think 后给正式的，外面套 ```json 围栏。
	raw := "<think>\n分析：{\"summary\":\"预告版\"}\n</think>\n" +
		"```json\n{\"summary\":\"邮件摘要正式版\",\"key_points\":[]}\n```"
	got := parseGatewaySummary(raw)
	if got != "邮件摘要正式版" {
		t.Fatalf("parseGatewaySummary = %q，应为 %q", got, "邮件摘要正式版")
	}
	if strings.Contains(got, "预告") {
		t.Errorf("取到了 think 里的预告版：%q", got)
	}
}

// TestParseGatewaySummaryPlainStillWorks 负样本：
// 无 think 的邮件摘要必须照常解析（这条修法不能伤到多数输入）。
func TestParseGatewaySummaryPlainStillWorks(t *testing.T) {
	raw := "```json\n{\"summary\":\"干净的邮件摘要\"}\n```"
	if got := parseGatewaySummary(raw); got != "干净的邮件摘要" {
		t.Fatalf("parseGatewaySummary = %q，应为 %q", got, "干净的邮件摘要")
	}
}
