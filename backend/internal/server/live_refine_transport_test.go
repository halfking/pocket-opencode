// 真网关探针（默认 skip）。验 §47 那次**已回退**的改动之后，
// 产品真实走的那条路（llmChatOnce → llmBFF.Chat，非流式）在真网关上还通不通、
// 以及真实响应的 finish_reason 到底是什么。
//
// 为什么需要它：§47 曾把 llmChatOnce 改成消费流式，单测全绿，
// 真网关一跑就露馅（流式 90s 零帧、必超时失败；非流式 22.1s 正常）。
// 这道探针就是把那次教训变成可重复的取证：谁再想改传输层，
// 先跑它，别拿单测当证据。
//
// 默认 skip：会消耗真实额度、依赖外网。显式开关：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveRefineTransport -v
//
// ⚠ 只有 2 次调用（短会 + 长会），不做分布采样 ——
//
//	定超时预算需要的样本量另说（§44.8）。本探针只回答「这条路通不通」。
package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

func liveProbeService(t *testing.T) *llmbff.Service {
	t.Helper()
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("设 POCKET_LIVE_GATEWAY=1 才跑真网关探针（会消耗真实额度）")
	}
	baseURL := os.Getenv("POCKET_LLM_GATEWAY_URL")
	apiKey := os.Getenv("POCKET_LLM_GATEWAY_API_KEY")
	if baseURL == "" || apiKey == "" {
		t.Fatal("POCKET_LIVE_GATEWAY=1 但缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	resolve := func(_, _ string) GatewayConfig {
		return GatewayConfig{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Format:  strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_FORMAT")),
		}
	}
	p := NewDynamicLLMGatewayBFFProvider(resolve)
	if p == nil {
		t.Fatal("NewDynamicLLMGatewayBFFProvider 返回 nil")
	}
	return llmbff.NewService(p, llmbff.NoopRecorder{})
}

func liveShortRefineSegments() []meetingSegmentIn {
	return []meetingSegmentIn{
		{Speaker: "张伟", Text: "那个，我们这个季度的续费方案basically已经定了啊", Lang: "zh", StartMs: 0, EndMs: 6000},
		{Speaker: "李娜", Text: "定了吗？我这边还看到有两个客户没签", Lang: "zh", StartMs: 6000, EndMs: 13000},
		{Speaker: "张伟", Text: "对，没签的那两个走线下，就是那个王总和林岚那边", Lang: "zh", StartMs: 13000, EndMs: 21000},
		{Speaker: "李娜", Text: "嗯，评审会我准备一下对比表，主要是跟旧的版本有什么差异", Lang: "zh", StartMs: 21000, EndMs: 28000},
	}
}

func liveLongRefineSegments() []meetingSegmentIn {
	talk := []string{
		"这个季度的续费方案basically已经定了啊",
		"定了吗我这边还看到有两个客户没签",
		"没签的那两个走线下就是王总和林岚那边",
		"评审会我准备一下对比表主要是跟旧的版本差异",
	}
	var segs []meetingSegmentIn
	for i := 0; i < 120; i++ {
		sp := "张伟"
		if i%2 == 1 {
			sp = "李娜"
		}
		body := talk[i%len(talk)]
		if i%7 == 0 {
			body = fmt.Sprintf("第%d条议题，我们先过一下这个%s的进度，然后再对齐资源。", i/7+1,
				[]string{"接口联调", "灰度发布", "回滚预案", "监控告警"}[i%4])
		}
		segs = append(segs, meetingSegmentIn{
			Speaker: sp, Text: body, Lang: "zh",
			StartMs: i * 16000, EndMs: (i + 1) * 16000,
		})
	}
	return segs
}

// TestLiveRefineTransport 在真网关上跑产品真实那条精校链路，
// 两个长度各一次，并把真实 finish_reason 打出来。
func TestLiveRefineTransport(t *testing.T) {
	svc := liveProbeService(t)
	srv := &Server{}
	srv.SetLLMBFF(svc, nil)
	req := httptest.NewRequest("POST", "/api/meetings/probe/refine", nil)
	meta := meetingMetaIn{Title: "季度续费方案评审", Participants: []string{"张伟", "李娜", "王总", "林岚"}}

	arms := []struct {
		name string
		segs []meetingSegmentIn
	}{
		{"短会(4段/28秒)", liveShortRefineSegments()},
		{"长会(120段/32分钟)", liveLongRefineSegments()},
	}
	for _, a := range arms {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		st := time.Now()
		got, err := srv.llmMeetingRefine(ctx, req, a.segs, []string{"zh"}, meta)
		el := time.Since(st)
		cancel()

		refined, _ := got["refined_transcript"].(string)
		fmt.Printf("REFINE %-20s total=%-9s err=%v fallback=%v refined_chars=%d\n",
			a.name, el.Round(time.Millisecond), err, got["refine_fallback"] == true, len([]rune(refined)))
		if err != nil {
			t.Errorf("★ %s 在真网关上跑不通：%v", a.name, err)
		}
	}

	// 把真实响应的 finish_reason 打出来：它决定 §12.4 第 8 条那道闸在实践中会不会开。
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := svc.Chat(ctx, llmbff.ChatRequest{
		WorkspaceID: "ws_live_probe", Model: "auto",
		Messages: []llmbff.Message{{Role: llmbff.RoleUser, Content: "回复一个字：好"}},
	}, "meeting")
	if err != nil {
		fmt.Printf("FINISH_REASON 取样失败：%v\n", err)
		return
	}
	fmt.Printf("FINISH_REASON model=%s finish_reason=%q chars=%d\n",
		resp.Model, resp.FinishReason, len([]rune(resp.Content)))
	if resp.FinishReason == "" {
		fmt.Printf("FINISH_REASON ⚠ 网关没回 finish_reason ⇒ 截断闸在这条链路上不会触发（读数，不是结论）\n")
	}
}
