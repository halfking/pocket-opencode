// 真网关探针（默认 skip）：**录后精校**这条需求核心链路，第一次对真网关跑。
//
// 为什么需要它：§1–§53 为「录音转写不准 / 录后要一次精校 / 时间点自动进日程」
// 做了大量前端修复，但**服务端精校端点从未对真实模型跑过一次**。
// 单测里 `buildRefinePrompt` / `parseRefineJSON` / `guardRefineResult` 全绿，
// 却没有任何证据说明：
//
//	① 真实模型会不会照契约吐出 `structured_minutes.action_items` 与 `todos`；
//	② **带期限的行动项**（用户需求：时间点自动加入计划日程）在真实输出里
//	   到底有没有 —— 这是整条需求在服务端的成败点；
//	③ §48-B 修的 `meta.participants` 传入后，`guardRefineResult` 在真实模型
//	   输出上会不会误拦（名单里的人名本就该出现在精校结果里）。
//
// ★ 用真实 ASR 输出当输入（2026-10-07 真网关 mimo-v2.5-asr 的返回原文），
//
//	不是自拟文本 —— 这段原文里**埋着两个时间点**（下周三下午三点 / 十一月底），
//	正是用来验 ② 的探针。
//
// 默认 skip：会消耗真实额度、依赖外网。显式开关：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveRefineChain -v
package server

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
)

/** 真网关 mimo-v2.5-asr 对 tts-meeting.wav 的返回原文（2026-10-07 实测）。 */
const realASRTranscript = "下周三下午三点，跟王总还有林兰开个客户评审会，要准备悬界芯片和机载传感器的对比表。" +
	"张伟负责工头安装的预算确认，所有续期合同必须在十一月底之前签完。"

func liveRefineResult(t *testing.T, segs []meetingSegmentIn, meta meetingMetaIn) (map[string]any, error) {
	t.Helper()
	bff := liveProbeService(t)
	// 与 live_refine_transport_test.go 同一构造方式：空 Server + SetLLMBFF。
	s := &Server{}
	s.SetLLMBFF(bff, nil)
	// ★ 必须钉一个具体模型：空模型让网关按 `auto` 路由，实测会落到
	//   deepseek-v4-flash（§44：首字节 24~142s），而 llmgateway 的
	//   ResponseHeaderTimeout 是 85s（2026-10-07 由 60s 上调）⇒ 慢的那几条
	//   仍可能 `timeout awaiting response headers`，但 60~85s 那段已不再是死预算。
	//   这不是探针的问题，是**生产也会遇到的预算问题**（§44.8 待拍板项）。
	//   glm-5.2 实测首字节 33s，在预算内；可用 POCKET_LIVE_REFINE_MODEL 覆盖。
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_REFINE_MODEL"))
	if model == "" {
		model = "glm-5.2"
	}
	s.cfg.LLMModel = model
	t.Logf("使用模型：%s", model)
	// ⚠ 2026-10-07：预算 120s → 240s。理由是**实测**，不是怕跑不过：
	//	glm-5.2 同一提示词首字节样本 14.38s / 20.76s / 40.81s / >120s（超时）
	//	网关 `auto` 路由下实测区间 24~142s（§44.8）
	// ⇒ 120s 探针会**间歇性**撞上 ResponseHeaderTimeout，
	//   那时候读到的是网关延迟，不是本仓代码的结论。
	// 探针预算调大不影响产品（产品预算另议，见 §54.7 三层预算待拍板项）。
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	req := httptest.NewRequest("POST", "/api/meetings/m1/refine", nil)
	return s.llmMeetingRefine(ctx, req, segs, []string{}, meta)
}

func TestLiveRefineChain(t *testing.T) {
	if testing.Short() {
		t.Skip("真网关探针")
	}
	segs := []meetingSegmentIn{
		{Speaker: "张伟", Text: realASRTranscript, Lang: "zh", StartMs: 0, EndMs: 18000},
	}

	t.Run("真实输出里有没有带期限的行动项（用户需求：时间点自动进日程）", func(t *testing.T) {
		res, err := liveRefineResult(t, segs, meetingMetaIn{Title: "客户评审会筹备", Participants: []string{"张伟", "林岚"}})
		if err != nil {
			t.Fatalf("llmMeetingRefine: %v", err)
		}
		reportRefinePayload(t, res)

		refined, _ := res["refined_transcript"].(string)
		if strings.TrimSpace(refined) == "" {
			t.Fatal("精校结果为空")
		}
		if res["refine_fallback"] == true {
			t.Fatal("这次走的是降级路径 —— 拿不到真实契约输出，后面的断言无意义")
		}
		if leak := participantLeak(refined, meetingMetaIn{Participants: []string{"张伟", "林岚"}}, realASRTranscript); leak != "" {
			t.Logf("※ participantLeak 命中 %q（名单里的人名不在原文里时属预期）", leak)
		}

		// ③ meta 传入后，名单里「确实在原文里」的人名不得被误判成外人。
		if leak := participantLeak(refined, meetingMetaIn{Participants: []string{"张伟"}}, realASRTranscript); leak != "" {
			t.Errorf("§48-B 修好后仍误拦：%q 在原文里出现过，不该被判成模型引入的外人", leak)
		}

		// ② 行动项与期限。
		sm, _ := res["structured_minutes"].(map[string]any)
		items, _ := sm["action_items"].([]any)
		if len(items) == 0 {
			t.Error("structured_minutes.action_items 为空 —— 服务端没有产出行动项")
		}
		todos, _ := res["todos"].([]any)
		if len(todos) == 0 {
			t.Error("todos 为空 —— §48-A 修的「行动项被丢弃」即使不丢弃，这里也没有东西可消费")
		}
		// 带期限的那条必须有 due 文本：时间点就是靠它进日程的。
		dueFound := false
		for _, bucket := range [][]any{items, todos} {
			for _, it := range bucket {
				m, _ := it.(map[string]any)
				due, _ := m["due"].(string)
				if strings.TrimSpace(due) != "" {
					dueFound = true
					t.Logf("带期限的行动项：text=%v due=%q", m["text"], due)
				}
			}
		}
		if !dueFound {
			t.Error("没有任何行动项带 due —— 「时间点自动加入计划日程」在服务端就没有输入")
		}
	})
}

/** 把真实响应原样打出来，便于人工核对契约形状。 */
func reportRefinePayload(t *testing.T, res map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(res))
	for k := range res {
		keys = append(keys, k)
	}
	t.Logf("响应键：%v", keys)
	for _, k := range []string{"refined_transcript", "structured_minutes", "todos", "next_meeting"} {
		if v, ok := res[k]; ok {
			t.Logf("── %s ──\n%#v", k, v)
		}
	}
}

/**
 * ★ 决定性一臂：**绕过 guardRefineResult**，把模型原始输出打出来。
 *
 * 为什么必须单独一条：主臂看到的是**降级后的原文**（因为守卫已经把它换掉了），
 * 从那里看不出「模型到底产出了什么」。守卫的判据是
 *   「名单里的词出现在精校结果里、却不在原始转写里 ⇒ 模型自己加的」
 * —— 而「模型把同音错**改对**」与「模型引入外人」在这个判据下**长得一模一样**。
 * 这一臂就是用来把两者区分开的。
 */
func TestLiveRefineRawOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("真网关探针")
	}
	bff := liveProbeService(t)
	s := &Server{}
	s.SetLLMBFF(bff, nil)
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_REFINE_MODEL"))
	if model == "" {
		model = "glm-5.2"
	}
	s.cfg.LLMModel = model

	segs := []meetingSegmentIn{
		{Speaker: "张伟", Text: realASRTranscript, Lang: "zh", StartMs: 0, EndMs: 18000},
	}
	meta := meetingMetaIn{Title: "客户评审会筹备", Participants: []string{"张伟", "林岚"}}
	transcript := segmentsToText(segs)

	// ⚠ 2026-10-07：预算 120s → 240s。理由是**实测**，不是怕跑不过：
	//	glm-5.2 同一提示词首字节样本 14.38s / 20.76s / 40.81s / >120s（超时）
	//	网关 `auto` 路由下实测区间 24~142s（§44.8）
	// ⇒ 120s 探针会**间歇性**撞上 ResponseHeaderTimeout，
	//   那时候读到的是网关延迟，不是本仓代码的结论。
	// 探针预算调大不影响产品（产品预算另议，见 §54.7 三层预算待拍板项）。
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	req := httptest.NewRequest("POST", "/api/meetings/m1/refine", nil)
	content, _, err := s.llmChatOnce(ctx, req, model, []aigate.ChatMessage{
		{Role: "user", Content: buildRefinePrompt(transcript, "", meta)},
	})
	if err != nil {
		t.Fatalf("llmChatOnce: %v", err)
	}
	parsed, err := parseRefineJSON(content)
	if err != nil {
		t.Fatalf("parseRefineJSON: %v\n原始内容：%s", err, content)
	}
	reportRefinePayload(t, parsed)

	refined, _ := parsed["refined_transcript"].(string)
	t.Logf("── 原始转写 ──\n%s", transcript)
	t.Logf("── 模型精校结果（未经守卫）──\n%s", refined)
	t.Logf("原文里有没有「林岚」：%v；精校里有没有：%v",
		strings.Contains(transcript, "林岚"), strings.Contains(refined, "林岚"))
	t.Logf("原文里有没有「林兰」：%v；精校里有没有：%v",
		strings.Contains(transcript, "林兰"), strings.Contains(refined, "林兰"))

	// 守卫的判定：把真实输出喂进去，看它判成什么。
	leak := participantLeak(refined, meta, transcript)
	if leak != "" {
		t.Logf("★ 守卫命中：%q —— 若它是「模型把同音错改对」而非「模型引入外人」，", leak)
		t.Logf("   那么整个精校连同全部行动项会被丢弃：这就是用户需求断在这里。")
	}
}
