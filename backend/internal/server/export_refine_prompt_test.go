// export_refine_prompt_test.go — 把**生产同款**精校提示词导出成文件，
// 供延迟测量脚本（/tmp/opstt/measure-refine-latency.mjs）使用。
//
// ⚠ 为什么导出而不是让测量脚本去解析 server_meeting.go 的字符串字面量：
//
//	我第一版就是这么做的（正则抽 `"..."` 再反转义），锚点写成
//	`"以下是会议录音的自动转写结果`，而源码里那个字面量开头是
//	`"%s%s以下是…` ⇒ **锚点没命中，脚本直接退出**，而且是静默失败。
//	抽取式的致命处就在这：格式一变就失明，而它是「量具」失明。
//
//	改成直接调用被测函数本身 ⇒ 拿到的必然是线上那份文字，
//	改一个字、换个转义方式，这里跟着变，不存在漂移。
//
// 默认 skip（它会写文件，不该在普通回归里跑）：
//
//	go test ./internal/server -run TestExportRefinePrompt -v
package server

import (
	"os"
	"testing"
)

func TestExportRefinePrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("导出用工具测试")
	}
	if os.Getenv("POCKET_EXPORT_REFINE_PROMPT") == "" {
		t.Skip("未设 POCKET_EXPORT_REFINE_PROMPT")
	}
	// ⚠ 短样本只有两句话（§61 的口径边界）。真实会议是 8~1768 字，
	//   而**长输入下的改写倾向与延迟都和短样本不一样** ——
	//   拿一段「已经干净」的文本当输入，任何改动都算改写，
	//   所以它同时是一次**抗改写**测试。
	//   设 POCKET_EXPORT_REFINE_TRANSCRIPT=<文件> 即可切到长输入。
	transcript := "[张伟] 下周三下午三点，跟王总还有林兰开个客户评审会，" +
		"要准备悬界芯片和机载传感器的对比表。张伟负责工头安装的预算确认，" +
		"所有续期合同必须在十一月底之前签完。"
	if path := os.Getenv("POCKET_EXPORT_REFINE_TRANSCRIPT"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		transcript = string(raw)
	}
	meta := meetingMetaIn{Title: "客户评审会筹备", Participants: []string{"张伟", "林岚", "  ", ""}}
	// ★ §64：非人名专名（工头安装）修不了，是因为提示词里**没有任何地方写着「工头」**。
	//   验证 §30 那条机制：给依据就能修。这里把术语放进**会议标题**（产品已有的元数据字段），
	//   看模型是否会据此纠正 —— 若成立，则「维护会议标题」就是零改动的修复路径。
	if t := os.Getenv("POCKET_EXPORT_REFINE_TITLE"); t != "" {
		meta.Title = t
	}
	if l := os.Getenv("POCKET_EXPORT_REFINE_LOCATION"); l != "" {
		meta.Location = l
	}

	out := os.Getenv("POCKET_EXPORT_REFINE_PROMPT")
	prompt := buildRefinePrompt(transcript, "", meta)
	if err := os.WriteFile(out, []byte(prompt), 0o600); err != nil {
		t.Fatalf("写 %s: %v", out, err)
	}
	t.Logf("已导出 %d 字符（转写 %d 字符）到 %s", len(prompt), len(transcript), out)
}
