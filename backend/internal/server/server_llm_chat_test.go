package server

// server_llm_chat_test.go — /api/llm/chat 迁移动态网关（BFF）后的行为回归。
//
// 关键契约（2026-09-05 迁移，见 runbook §14/§15）：
//  1. llmBFF 已装配时，即使静态 s.llm 为 nil（启动时未配 POCKET_LLM_API_KEY），
//     /api/llm/chat 也必须 200 —— 旧的恒 503 分支已消除；
//  2. 网关未配置（provider 缺失 → ErrNotConfigured）→ 503，错误信息指向
//     动态网关配置入口（/api/llm-gateway/config）；
//  3. 响应形状保持 {"content","model"}，前端（meetings.ts 兜底路径）无需改动。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// stubChatProvider 只实现 Chat，记录收到的 model 供断言；Stream/Embed 不会被
// 本文件触达。
type stubChatProvider struct {
	gotModel string
	content  string
}

func (p *stubChatProvider) Chat(_ context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	p.gotModel = req.Model
	return &llmbff.ChatResponse{Content: p.content, Model: req.Model}, nil
}

func (p *stubChatProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	return nil, llmbff.ErrNotConfigured
}

func (p *stubChatProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func TestLLMChatViaBFFWorksWithoutStaticLLM(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	stub := &stubChatProvider{content: "pong"}
	srv.SetLLMBFF(llmbff.NewService(stub, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("chat-user", "member", "ws-chat")
	if err != nil {
		t.Fatal(err)
	}

	// 前置条件：测试服的静态 s.llm 必须为 nil——旧实现在这里必然 503。
	if srv.llm != nil {
		t.Fatal("precondition: static llm must be nil in test server")
	}

	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok,
		`{"model":"glm-5.2","messages":[{"role":"user","content":"ping"}]}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 via dynamic BFF despite nil s.llm, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if body["content"] != "pong" || body["model"] != "glm-5.2" {
		t.Fatalf("unexpected response body: %s", rr.Body.String())
	}
	if stub.gotModel != "glm-5.2" {
		t.Fatalf("provider received model %q, want glm-5.2", stub.gotModel)
	}
}

func TestLLMChatUnconfiguredGatewayReturns503(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	// provider 为 nil 模拟「BFF 装配了但网关 URL/key 未配置」。
	srv.SetLLMBFF(llmbff.NewService(nil, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("chat-user", "member", "ws-chat")
	if err != nil {
		t.Fatal(err)
	}
	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok,
		`{"model":"auto","messages":[{"role":"user","content":"ping"}]}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when gateway unconfigured, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "llm-gateway") {
		t.Fatalf("error should point at dynamic gateway config: %s", rr.Body.String())
	}
}
