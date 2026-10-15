// llm_chat_once_stream_test.go
//
// 锁住 llmChatOnce 的**三条降级语义**（§47）。文件名保留 `stream` 是历史，
// 但注意：**§47 曾把这里改成消费流式，已回退** —— 真网关实测流式路径
// 在本项目网关请求形态下 90s 零帧、必超时失败，而非流式 22.1s 正常返回。
// 回退的是**传输层**，三条语义不依赖流式，所以本文件全部保留。
//
// ⚠ 这道门量的**不是**「传输方式」，而是三条语义各自的后果：
//   1. 调用失败 ⇒ 返回的 content 必须是**空串**（半截正文绝不返回）；
//   2. finish_reason=length ⇒ 必须走**可辨识的**错误，且三个调用方各自
//      落到「模型不听话」那条路，而不是「请求失败」；
//   3. 回退 provider 在主通道**已经吐过正文**后不许再回退（否则正文重复）。

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// scriptedChatProvider：按脚本决定「返回什么」，并把 finish_reason 透出来。
type scriptedChatProvider struct {
	content      string
	model        string
	finishReason string
	err          error
	chatCalls    int
}

func (p *scriptedChatProvider) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	p.chatCalls++
	if p.err != nil {
		return nil, p.err
	}
	return &llmbff.ChatResponse{
		Content:      p.content,
		Model:        p.model,
		FinishReason: p.finishReason,
	}, nil
}

func (p *scriptedChatProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	return nil, llmbff.ErrNotConfigured
}

func (p *scriptedChatProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func newChatTestServer(t *testing.T, stub llmbff.Provider) *Server {
	t.Helper()
	srv, _, _, _ := newMobileRouteServer(t)
	srv.SetLLMBFF(llmbff.NewService(stub, llmbff.NoopRecorder{}), nil)
	return srv
}

func plainRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/meetings/x/summary", nil)
}

func TestLLMChatOnceReturnsProviderModelAndContent(t *testing.T) {
	stub := &scriptedChatProvider{content: `{"summary":"ok"}`, model: "glm-5.1"}
	srv := newChatTestServer(t, stub)

	content, model, err := srv.llmChatOnce(context.Background(), plainRequest(), "auto",
		[]aigate.ChatMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if content != `{"summary":"ok"}` {
		t.Fatalf("正文原样返回，实得 %q", content)
	}
	// ★ 必须是 **provider 实际使用**的 model（auto 解析后的命中名），
	// 而不是请求里传进去的别名「auto」。
	if model != "glm-5.1" {
		t.Fatalf("model 必须是 provider 实际使用的那个，实得 %q", model)
	}
	if stub.chatCalls != 1 {
		t.Fatalf("应走非流式路径一次，实得 %d 次", stub.chatCalls)
	}
}

func TestLLMChatOnceReturnsNoContentOnFailure(t *testing.T) {
	stub := &scriptedChatProvider{err: errors.New("upstream reset")}
	srv := newChatTestServer(t, stub)

	content, _, err := srv.llmChatOnce(context.Background(), plainRequest(), "auto", nil)
	if err == nil {
		t.Fatal("调用失败必须报错")
	}
	if content != "" {
		t.Fatalf("★ 调用失败时 content 必须是空串，实得 %q —— "+
			"半截 JSON 会让调用方的解析器判失败，但更糟的是它看起来像一次成功", content)
	}
}

func TestLLMChatOnceFlagsTruncatedOutput(t *testing.T) {
	// 正文是**合法 JSON**，但上游声明输出被预算截断（§46 那 4 个尾部点）。
	stub := &scriptedChatProvider{content: `{"refined_transcript":"完整正文"}`, finishReason: "length"}
	srv := newChatTestServer(t, stub)

	content, _, err := srv.llmChatOnce(context.Background(), plainRequest(), "auto", nil)
	if !isTruncatedOutput(err) {
		t.Fatalf("finish_reason=length 必须可被单独辨识，实得 err=%v", err)
	}
	if content != "" {
		t.Fatalf("截断时不得把正文交出去（它「看起来完整」但上游说了被截断），实得 %q", content)
	}
}

// TestTruncationRoutesToBadOutputPathNotFailurePath —— 三个调用方逐个验。
// 这是本次改动最要紧的一条：截断**不是**请求失败。若没分开，界面会弹
// 「精翻失败」，而真实发生的是「模型没把话说完、转写仍安全落库」。
func TestTruncationRoutesToBadOutputPathNotFailurePath(t *testing.T) {
	segs := []meetingSegmentIn{{Speaker: "A", Text: "我们讨论了预算和排期", Lang: "zh", StartMs: 0, EndMs: 1000}}

	t.Run("摘要 → 回落原始转写，而不是 502", func(t *testing.T) {
		stub := &scriptedChatProvider{content: `{"summary":"说到一半`, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingSummary(context.Background(), plainRequest(), segs, "")
		if err != nil {
			t.Fatalf("截断不该以 error 形式冒到 handler（那会变成 502）：%v", err)
		}
		s, _ := got["summary"].(string)
		if strings.TrimSpace(s) == "" {
			t.Fatalf("摘要必须是回落转写，不能是空（空面板且不报错是最糟的一种）：%v", got)
		}
	})

	t.Run("推荐 → 空列表，而不是失败", func(t *testing.T) {
		stub := &scriptedChatProvider{content: `{"items":[{"title":"半`, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingRecommend(context.Background(), plainRequest(), segs, "旧摘要")
		if err != nil {
			t.Fatalf("截断不该以 error 形式冒出：%v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("期望空列表（与 parseRecommendJSON 失败同一条路），实得 %v", got)
		}
	})

	t.Run("精校 → refineFallbackPayload，而不是 502", func(t *testing.T) {
		stub := &scriptedChatProvider{content: `{"refined_transcript":"说到一半`, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingRefine(context.Background(), plainRequest(), segs, nil, meetingMetaIn{})
		if err != nil {
			t.Fatalf("截断不该以 error 形式冒出（那会让前端弹「精翻失败」）：%v", err)
		}
		if got["refine_fallback"] != true {
			t.Fatalf("必须带 refine_fallback 标记，前端据此才说「未生效」：%v", got)
		}
		if txt, _ := got["refined_transcript"].(string); !strings.Contains(txt, "我们讨论了预算和排期") {
			t.Fatalf("必须是原始转写，实得 %q", txt)
		}
	})
}

// TestCompleteJSONWithLengthFinishStillDegrades —— 这条是本组测试里**唯一**
// 能把「截断防护」与「恰好解析失败」区分开的形状。
//
// 背景（§46 的读数）：穷举 3867 个截断点时，正文内部的截断点**一个都解析不了**，
// 但有 4 个能解析的点全在 99.9%~100% —— 也就是「JSON 对象已闭合，后面只剩
// 换行/围栏」。换句话说，「**完整 JSON + finish_reason=length**」是真实可达的：
// 模型把 JSON 写完了，但网关按输出预算判定它触顶。
//
// 这时若没有截断防护：
//
//	· 解析**成功** ⇒ 返回真正的精校结果，界面照实弹「精翻完成」，
//	  而上游明明说了「这段输出被截断了」。
//
// 所以这三个子用例必须用**合法 JSON** 做正文，而不是半截 JSON ——
// 半截 JSON 会让没有防护的版本也走解析失败回落，这道门就恒真了。
func TestCompleteJSONWithLengthFinishStillDegrades(t *testing.T) {
	segs := []meetingSegmentIn{{Speaker: "A", Text: "我们讨论了预算和排期", Lang: "zh", StartMs: 0, EndMs: 1000}}
	const validRefine = `{"refined_transcript":"[A] 我们讨论了预算和排期","translations":{},"structured_minutes":{"agenda":[],"decisions":[],"action_items":[],"next_meeting":null},"todos":[]}`

	t.Run("精校：合法 JSON + length 也必须标成降级", func(t *testing.T) {
		stub := &scriptedChatProvider{content: validRefine, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingRefine(context.Background(), plainRequest(), segs, nil, meetingMetaIn{})
		if err != nil {
			t.Fatalf("不该冒 error：%v", err)
		}
		if got["refine_fallback"] != true {
			t.Fatalf("★ 正文是**合法 JSON**，没有防护时这里会返回成功结果并弹「精翻完成」；"+
				"而上游已声明 finish_reason=length，必须按降级处理。实得 %v", got)
		}
	})

	t.Run("摘要：合法 JSON + length 也必须回落原文", func(t *testing.T) {
		stub := &scriptedChatProvider{content: `{"summary":"看起来完整的摘要","key_points":["k"]}`, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingSummary(context.Background(), plainRequest(), segs, "")
		if err != nil {
			t.Fatalf("不该冒 error：%v", err)
		}
		if s, _ := got["summary"].(string); s == "看起来完整的摘要" {
			t.Fatalf("★ 没有防护时这里会返回模型那份「看起来完整」的摘要；上游已声明截断，必须回落原文")
		}
	})

	t.Run("推荐：合法 JSON + length 也必须给空列表", func(t *testing.T) {
		stub := &scriptedChatProvider{content: `{"items":[{"title":"看起来完整","snippet":"s","query":"q"}]}`, finishReason: "length"}
		srv := newChatTestServer(t, stub)
		got, err := srv.llmMeetingRecommend(context.Background(), plainRequest(), segs, "旧摘要")
		if err != nil {
			t.Fatalf("不该冒 error：%v", err)
		}
		if len(got) != 0 {
			t.Fatalf("★ 没有防护时这里会返回那条「看起来完整」的推荐；实得 %v", got)
		}
	})
}

// ── 回退 provider：已开始吐正文后不许再回退 ────────────────────────────
// 这条仍属 SSE 路径（/api/llm/stream 与会议 agent 走它），不受本次回退影响。
// fallbackPrimaryProvider：主通道按脚本回吐帧后按 endErr 决定成败。
type fallbackPrimaryProvider struct {
	frames []llmbff.Delta
	endErr error
}

func (p *fallbackPrimaryProvider) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	return nil, errors.New("not used")
}

func (p *fallbackPrimaryProvider) Stream(_ context.Context, _ llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	for _, f := range p.frames {
		if !fn(f) {
			break
		}
	}
	if p.endErr != nil {
		return nil, p.endErr
	}
	return &llmbff.Usage{TotalTokens: 1}, nil
}

func (p *fallbackPrimaryProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

type countingFallback struct {
	content string
	calls   int
}

func (p *countingFallback) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	p.calls++
	return &llmbff.ChatResponse{Content: p.content}, nil
}

func (p *countingFallback) Stream(_ context.Context, _ llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	p.calls++
	fn(llmbff.Delta{Content: p.content})
	fn(llmbff.Delta{Done: true, FinishReason: "stop"})
	return &llmbff.Usage{TotalTokens: 1}, nil
}

func (p *countingFallback) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func TestFallbackProviderDoesNotDuplicateContentAlreadyStreamed(t *testing.T) {
	// 主通道吐了一半才断（非 ctx 错误）
	primary := &fallbackPrimaryProvider{
		frames: []llmbff.Delta{{Content: "前半段"}},
		endErr: errors.New("upstream reset"),
	}
	fb := &countingFallback{content: "备用通道的完整答案"}
	prov := NewFallbackBFFProvider(primary, fb)

	var got strings.Builder
	_, err := prov.Stream(context.Background(), llmbff.ChatRequest{Model: "auto"}, func(d llmbff.Delta) bool {
		got.WriteString(d.Content)
		return true
	})
	if err == nil {
		t.Fatal("主通道已吐正文后失败，必须把错误原样返回")
	}
	if fb.calls != 0 {
		t.Fatalf("★ 主通道已经吐过正文就不许回退，备用通道仍被调了 %d 次 —— "+
			"消费者会拿到「前半段 + 完整答案」拼接成的重复正文", fb.calls)
	}
	if strings.Contains(got.String(), "备用通道") {
		t.Fatalf("输出里混进了备用通道的内容：%q", got.String())
	}
}

func TestFallbackProviderStillFallsBackBeforeAnyContent(t *testing.T) {
	primary := &fallbackPrimaryProvider{endErr: errors.New("no_candidate")}
	fb := &countingFallback{content: "备用答案"}
	prov := NewFallbackBFFProvider(primary, fb)

	var got strings.Builder
	_, err := prov.Stream(context.Background(), llmbff.ChatRequest{Model: "auto"}, func(d llmbff.Delta) bool {
		got.WriteString(d.Content)
		return true
	})
	if err != nil {
		t.Fatalf("首帧前失败应当回退并成功：%v", err)
	}
	if fb.calls != 1 {
		t.Fatalf("首帧前失败必须回退一次，实调 %d 次", fb.calls)
	}
	if got.String() != "备用答案" {
		t.Fatalf("回退后应拿到备用通道的完整答案，实得 %q", got.String())
	}
}
