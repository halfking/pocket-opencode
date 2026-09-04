package server

// server_gateway_migration_test.go — 2026-09-05 动态网关迁移第二批（runbook §16）：
//   1. /api/embed 迁移 BFF：llmBFF 已装配且静态 s.embedder 为 nil 时必须 200；
//   2. server_meeting.go 摘要经 llmChatOnce 走 BFF（s.llm 为 nil 不再恒 503）；
//   3. POST /api/auth/refresh 滑动续期：有效 token 换新、无效 token 401。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

type stubEmbedProvider struct {
	gotInput string
	vec      []float32
}

func (p *stubEmbedProvider) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func (p *stubEmbedProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	return nil, llmbff.ErrNotConfigured
}

func (p *stubEmbedProvider) Embed(_ context.Context, req llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	p.gotInput = req.Input
	return &llmbff.EmbedResponse{Embedding: p.vec, Model: "text-embedding-3-small"}, nil
}

func TestEmbedViaBFFWorksWithoutStaticEmbedder(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	stub := &stubEmbedProvider{vec: []float32{0.1, 0.2, 0.3}}
	srv.SetLLMBFF(llmbff.NewService(stub, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("embed-user", "member", "ws-embed")
	if err != nil {
		t.Fatal(err)
	}

	// 前置条件：静态 embedder 必须为 nil——旧实现在这里必然 503。
	if srv.embedder != nil {
		t.Fatal("precondition: static embedder must be nil in test server")
	}

	req := mobileRequest(http.MethodPost, "/api/embed", tok, `{"text":"hello gateway"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 via dynamic BFF despite nil s.embedder, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if body["model"] != "text-embedding-3-small" {
		t.Fatalf("unexpected model: %s", rr.Body.String())
	}
	dim, _ := body["dim"].(float64)
	if dim != 3 {
		t.Fatalf("expected dim=3, got %s", rr.Body.String())
	}
	if stub.gotInput != "hello gateway" {
		t.Fatalf("provider received input %q", stub.gotInput)
	}
}

func TestMeetingSummaryViaBFFWorksWithoutStaticLLM(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	stub := &stubChatProvider{content: `{"summary":"BFF-SUMMARY-OK","key_points":[]}`}
	srv.SetLLMBFF(llmbff.NewService(stub, llmbff.NoopRecorder{}), nil)
	tok, err := signer.SignWithWorkspace("meeting-user", "member", "ws-meeting")
	if err != nil {
		t.Fatal(err)
	}

	if srv.llm != nil {
		t.Fatal("precondition: static llm must be nil in test server")
	}

	req := mobileRequest(http.MethodPost, "/api/meetings/m-bff-1/summary", tok,
		`{"segments":[{"speaker":"A","text":"hello","lang":"en","start_ms":0,"end_ms":1000}]}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 via dynamic BFF despite nil s.llm, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if body["summary"] != "BFF-SUMMARY-OK" {
		t.Fatalf("summary not produced via BFF path: %s", rr.Body.String())
	}
}

type stubRefreshProvider struct{ content string }

func (p *stubRefreshProvider) Chat(_ context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	return &llmbff.ChatResponse{Content: p.content, Model: req.Model}, nil
}
func (p *stubRefreshProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	return nil, llmbff.ErrNotConfigured
}
func (p *stubRefreshProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, llmbff.ErrNotConfigured
}

func TestAuthRefreshIssuesFreshToken(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	tok := tokens["ws-a"]
	if tok == "" {
		t.Fatal("precondition: token missing")
	}
	req := mobileRequest(http.MethodPost, "/api/auth/refresh", tok, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from refresh, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if body["token"] == "" || body["token"] == tok {
		t.Fatalf("refresh must return a NEW token, got user=%s ws=%s", body["user"], body["workspace_id"])
	}
	if body["user"] != "user-wsa" {
		t.Fatalf("unexpected user in refresh response: %s", rr.Body.String())
	}
	// 新 token 必须能通过 requireAuth（用 /api/auth/me 验证）。
	me := mobileRequest(http.MethodGet, "/api/auth/me", body["token"], "")
	rrMe := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrMe, me)
	if rrMe.Code != http.StatusOK {
		t.Fatalf("refreshed token rejected by requireAuth: %d: %s", rrMe.Code, rrMe.Body.String())
	}
}

func TestAuthRefreshRejectsInvalidToken(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	req := mobileRequest(http.MethodPost, "/api/auth/refresh", "not-a-jwt", "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for garbage token, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid") && !strings.Contains(rr.Body.String(), "unauthenticated") {
		t.Logf("note: 401 body: %s", rr.Body.String())
	}
}
