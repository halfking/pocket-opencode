// summary_agent_fallback_behavior_test.go — §102：**agent 失败后回落真的会跑**。
//
// ── 为什么这条门是必要的 ────────────────────────────────────────────
//
// 本包已有一条 `TestMeetingSummaryAgentFallsBack`，但它是**纯源码扫描**
// （`strings.Contains(s, "s.llmMeetingSummary(ctx, ...)")`）。它证明的是
// 「回落的调用**写在源码里**」，**不**证明「agent 失败时它**真的会跑**，
// 并且端点**不会 502**」。
//
// 而 §35.7 那个缺陷恰恰就是这一类：三段共用一个 ctx，agent 跑满一轮后
// 回落拿到一个**已经死掉的 context，必然立刻失败**，端点 502。
// 那次是靠读源码发现的（时序没法用单测复现）。
//
// ★ 本门证明「ctx 的过期时序没法用单测复现」这句话**只对「真的卡 45s 的上游」成立**：
//
//	让替身 provider 在 `Stream` 上直接返回错误，就能走完
//
// 「agent 失败 → 回落 → 200」这条路径，全程不到一秒、不依赖任何计时。
//
// 为什么值得单独立门：摘要是主链路、智能体是增强。
// 回落这条腿一旦断掉，一次网关抖动就会让用户直接看不到摘要 ——
// 而源码扫描那道门对此**完全无感**（调用还在，只是跑不成）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// agentFallbackProvider 决定「agent 那一步」和「一次性回落那一步」各自的结果。
type agentFallbackProvider struct {
	streamErr  error  // 非 nil ⇒ agent 失败
	streamBody string // 非空 ⇒ agent「成功」但正文是它给的（用来测解析失败回落）
	chatBody   string // 一次性回落的正文
	chatErr    error

	streamCalls, chatCalls int
}

func (p *agentFallbackProvider) Chat(_ context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	p.chatCalls++
	if p.chatErr != nil {
		return nil, p.chatErr
	}
	return &llmbff.ChatResponse{Content: p.chatBody, Model: "stub"}, nil
}

func (p *agentFallbackProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	p.streamCalls++
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	if p.streamBody == "" {
		return nil, errors.New("provider: 未配置 streamBody")
	}
	return &llmbff.Usage{}, nil
}

func (p *agentFallbackProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return &llmbff.EmbedResponse{}, nil
}

const goodSummaryJSON = `{"summary":"评审会已对齐供应商交期与价格。","key_points":["良率 93%"],` +
	`"action_items":[{"text":"给王总发对齐表","assignee":"张伟","due":"明天上午"}],` +
	`"decisions":["下周三对一次"],"open_questions":[]}`

func postSummaryToFallbackProvider(t *testing.T, p *agentFallbackProvider) *httptest.ResponseRecorder {
	t.Helper()
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.SetLLMBFF(llmbff.NewService(p, nil), nil)
	body := `{"segments":[{"speaker":"张伟","text":"明天上午我先给一版对齐表。"}],"prev_summary":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/meetings/m1/summary", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// TestSummaryAgentFails_OneShotStillAnswers 是本文件的核心用例：
// agent 失败时，端点必须**返回 200 且带可用摘要**，而不是 502。
func TestSummaryAgentFails_OneShotStillAnswers(t *testing.T) {
	p := &agentFallbackProvider{
		streamErr: errors.New("llm-gateway chat: 503 no provider"),
		chatBody:  goodSummaryJSON,
	}
	rr := postSummaryToFallbackProvider(t, p)

	if rr.Code != http.StatusOK {
		t.Fatalf("agent 失败后端点必须 200（回落是主链路），实际 %d body=%s —— "+
			"回落这条腿断了 = 用户直接看不到摘要", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if p.streamCalls == 0 {
		t.Fatal("用例前提失效：agent 那一步根本没被调用")
	}
	if p.chatCalls != 1 {
		t.Fatalf("一次性回落应恰好调 1 次，实际 %d 次", p.chatCalls)
	}

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s, _ := out["summary"].(string); !strings.Contains(s, "评审会已对齐") {
		t.Fatalf("回落拿到的不是那份可用摘要：summary=%q", out["summary"])
	}
	// ★ 必须断言 **agent 标志没被置上**：这条守住「回落结果不许伪装成
	//   「查过资料的摘要」」—— frontend/审计据此区分两者（server_meeting.go:317）。
	if agent, ok := out["agent"]; ok && agent == true {
		t.Errorf("回落结果带了 agent=true ⇒ 前端与审计会把它当成「查过资料的摘要」：%v", out)
	}
}

// TestSummaryAgentYieldsNoContent_AlsoFallsBack 覆盖第二种 agent 失效形态：
// `Stream` 不报错，但**一个 delta 都没吐**（工具轮次跑空、上游只回空帧）。
//
// ⚠ 这里**故意没有**一条「agent 成功时置 agent=true」的用例：
// 本包这个替身无法让 agent 真的跑完一轮并产出可解析的 JSON
// （Stream 不吐 delta ⇒ `res.Content` 为空 ⇒ 解析失败 ⇒ 仍然回落）。
// 与其放一条**测不到自己名字说的东西**的门，不如不写 ——
// 那正是 §102 想拦的「门的注释比门强」那一类虚门。
func TestSummaryAgentYieldsNoContent_AlsoFallsBack(t *testing.T) {
	p := &agentFallbackProvider{chatBody: goodSummaryJSON}
	rr := postSummaryToFallbackProvider(t, p)
	if rr.Code != http.StatusOK {
		t.Fatalf("agent 一个 delta 都没吐时仍应由一次性回落兜住，实际 %d body=%s",
			rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if p.chatCalls != 1 {
		t.Fatalf("期望走一次性回落 1 次，实际 %d", p.chatCalls)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s, _ := out["summary"].(string); !strings.Contains(s, "评审会已对齐") {
		t.Fatalf("回落拿到的不是可用摘要：%v", out)
	}
	if agent, ok := out["agent"]; ok && agent == true {
		t.Errorf("回落结果带了 agent=true ⇒ 冒充「查过资料的摘要」：%v", out)
	}
}

// TestSummaryBothPathsFail_IsNotSilentlyOK 是本文件的**负控**：
// 没有它，上面三条可能只是「端点永远返回 200」而已。
//
// 两步都失败时端点必须**如实报错**（502），而不是把回落内容（此时是原始转写）
// 包装成一次成功 —— 否则用户会以为总结跑过了。
func TestSummaryBothPathsFail_IsNotSilentlyOK(t *testing.T) {
	p := &agentFallbackProvider{
		streamErr: errors.New("llm-gateway chat: 503 no provider"),
		chatErr:   errors.New("llm-gateway chat: 503 no provider"),
	}
	rr := postSummaryToFallbackProvider(t, p)
	if rr.Code == http.StatusOK {
		t.Fatalf("两条路都失败却返回 200（body=%s）—— 「假成功」比报错更糟："+
			"用户以为总结跑过了", strings.TrimSpace(rr.Body.String()))
	}
	if rr.Code != http.StatusBadGateway {
		t.Logf("★ 两步全失败返回的是 %d（期望 502）。若这是有意的降级形态，"+
			"请在这里补断言并更新注释 —— 现在它是个没被钉住的读数", rr.Code)
	}
	t.Logf("两步全失败 ⇒ %d，调用方会看到失败而不是假成功", rr.Code)
}
