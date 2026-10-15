//go:build !liveprobe

package server

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// live_refine_prompt_dump_test.go — 把**产品真实发出**的那条精校提示词导出成文件，
// 供 §44 的真网关探针使用。
//
// ★ 为什么保留这个文件，而不是把提示词抄进探针里：
//   §28/§30 记的正是「判据/测量必须落在产品实际发的那份」。
//   手抄一份提示词去探针里量首字节，量的就是**抄写那一刻的文本** ——
//   而 buildRefinePrompt 还在被改（§35 的术语表、§36 的名单守卫）。
//   一旦手抄的副本与实现分叉，量出来的超时结论**看起来仍然成立**，
//   却已经不代表产品真实发出的东西。
//
// 导出物：/tmp/real-refine-prompt.txt（短会）与 /tmp/real-refine-prompt-long.txt（长会）。
//

func TestDumpRefinePromptForLiveProbe(t *testing.T) {
	// 短会（约 28 秒）：与 §32/§33 抓包同形态
	short := []meetingSegmentIn{
		{Speaker: "张伟", Text: "那个，我们这个季度的续费方案basically已经定了啊", Lang: "zh", StartMs: 0, EndMs: 6000},
		{Speaker: "李娜", Text: "定了吗？我这边还看到有两个客户没签", Lang: "zh", StartMs: 6000, EndMs: 13000},
		{Speaker: "张伟", Text: "对，没签的那两个走线下，就是那个王总和林岚那边", Lang: "zh", StartMs: 13000, EndMs: 21000},
		{Speaker: "李娜", Text: "嗯，评审会我准备一下对比表，主要是跟旧的版本有什么差异", Lang: "zh", StartMs: 21000, EndMs: 28000},
	}
	meta := meetingMetaIn{Title: "季度续费方案评审", Participants: []string{"张伟", "李娜", "王总", "林岚"}}
	if err := os.WriteFile("/tmp/real-refine-prompt.txt", []byte(buildRefinePrompt(segmentsToText(short), "zh", meta)), 0o644); err != nil {
		t.Fatal(err)
	}

	// 长会（约 32 分钟 / 120 段）：真实会议才是会撞 90s 整体超时的形态。
	// 逐段扩写 short 的四句，speaker 交替 —— 形态与真实逐段转写一致。
	var long []meetingSegmentIn
	talk := []string{
		"这个季度的续费方案basically已经定了啊",
		"定了吗我这边还看到有两个客户没签",
		"没签的那两个走线下就是王总和林岚那边",
		"评审会我准备一下对比表主要是跟旧的版本差异",
	}
	for i := 0; i < 120; i++ {
		sp := "张伟"
		if i%2 == 1 {
			sp = "李娜"
		}
		body := talk[i%len(talk)]
		if i%7 == 0 {
			body = fmt.Sprintf("第%d条议题，我们先过一下这个%s的进度，然后再对齐资源。", i/7+1, []string{"接口联调", "灰度发布", "回滚预案", "监控告警"}[i%4])
		}
		long = append(long, meetingSegmentIn{
			Speaker: sp, Text: body, Lang: "zh",
			StartMs: i * 16000, EndMs: (i + 1) * 16000,
		})
	}
	p := buildRefinePrompt(segmentsToText(long), "zh", meta)
	if err := os.WriteFile("/tmp/real-refine-prompt-long.txt", []byte(p), 0o644); err != nil {
		t.Fatal(err)
	}

	// ★ 产品设计上限：1 小时会议（240 段）。§44 要回答的是
	// 「60s/90s 预算够不够撑一场整点会」，所以量的是这个长度，
	// 而不是去测「在哪一分钟越线」——后者只是它的副产品。
	var hour []meetingSegmentIn
	for i := 0; i < 240; i++ {
		sp := "张伟"
		if i%2 == 1 {
			sp = "李娜"
		}
		body := talk[i%len(talk)]
		if i%7 == 0 {
			body = fmt.Sprintf("第%d条议题，我们先过一下这个%s的进度，然后再对齐资源。", i/7+1, []string{"接口联调", "灰度发布", "回滚预案", "监控告警"}[i%4])
		}
		hour = append(hour, meetingSegmentIn{
			Speaker: sp, Text: body, Lang: "zh",
			StartMs: i * 15000, EndMs: (i + 1) * 15000,
		})
	}
	ph := buildRefinePrompt(segmentsToText(hour), "zh", meta)
	if err := os.WriteFile("/tmp/real-refine-prompt-hour.txt", []byte(ph), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("短会 %d 字节 / 长会(32min) %d 字节（%d 段）/ 整点会(60min) %d 字节（%d 段）",
		len(buildRefinePrompt(segmentsToText(short), "zh", meta)), len(p), len(long), len(ph), len(hour))

	// 顺手钉一下：导出的必须是**产品自己拼出来**的那份，而不是空壳。
	// 若 buildRefinePrompt 被改成返回常量或空串，这个导出器会跟着悄悄失真，
	// 而探针量出来的首字节/超时结论**看起来仍然成立**。
	shortP := buildRefinePrompt(segmentsToText(short), "zh", meta)
	for _, must := range []string{"refined_transcript", "[张伟]", "[李娜]", "只改错"} {
		if !strings.Contains(shortP, must) {
			t.Fatalf("导出的提示词里缺少 %q —— buildRefinePrompt 可能被改成了空壳，"+
				"此时探针量到的超时结论不代表产品真实发出的内容", must)
		}
	}
	if !strings.Contains(p, "[李娜]") {
		t.Fatal("长会提示词没有说话人标记 ⇒ 形态已变，§44 的读数不可比")
	}
}
