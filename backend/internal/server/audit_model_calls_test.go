package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// fakeLLMClient / fakeEmbedder：满足 aigate 接口的最小实现，让
// handleLLMChat / handleEmbed 在无外部依赖下走完审计路径。
type fakeLLMClient struct{ err error }

func (f *fakeLLMClient) Chat(_ context.Context, _ string, _ []aigate.ChatMessage) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "ok", nil
}

type fakeEmbedder struct{ err error }

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return []float32{0.1, 0.2}, "text-embedding-3-small", nil
}

// /api/llm/chat 成功调用必须写 llm.chat 审计事件，detail 只含
// model 与消息条数，绝不包含消息内容（P3 §2「模型调用…可检索 + 敏感值
// 不进入日志」）。
func TestLLMChat_WritesAuditWithoutContent(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.llm = &fakeLLMClient{}

	tok, _ := signer.SignWithWorkspace("llm-user", "member", "ws-a")
	body := `{"messages":[{"role":"user","content":"SECRET-PROMPT-DO-NOT-LOG"}],"model":"gpt-4o-mini"}`
	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat failed: %d %s", rr.Code, rr.Body.String())
	}

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.chat"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.chat entry, got %d", len(entries))
	}
	e := entries[0]
	if e.TenantID != "ws-a" || !e.Success {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if !strings.Contains(e.Detail, "model=gpt-4o-mini") || !strings.Contains(e.Detail, "messages=1") {
		t.Fatalf("detail missing model/messages count: %q", e.Detail)
	}
	if strings.Contains(e.Detail, "SECRET-PROMPT-DO-NOT-LOG") {
		t.Fatalf("detail leaked message content: %q", e.Detail)
	}
}

// 上游失败时 llm.chat 事件必须 success=false（可检索的失败模型调用）。
func TestLLMChat_FailureAuditedAsNotSuccess(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.llm = &fakeLLMClient{err: context.DeadlineExceeded}

	tok, _ := signer.SignWithWorkspace("llm-user", "member", "ws-a")
	req := mobileRequest(http.MethodPost, "/api/llm/chat", tok,
		`{"messages":[{"role":"user","content":"hi"}],"model":"gpt-4o-mini"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.chat"})
	if len(entries) != 1 || entries[0].Success {
		t.Fatalf("failure must be audited with success=false, got %+v", entries)
	}
}

// /api/embed 成功调用必须写 llm.embed；detail 只含 model 与字符数，
// 绝不记 body.Text。
func TestEmbed_WritesAuditWithoutText(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.embedder = &fakeEmbedder{}

	tok, _ := signer.SignWithWorkspace("llm-user", "member", "ws-a")
	req := mobileRequest(http.MethodPost, "/api/embed", tok,
		`{"text":"SECRET-EMBED-INPUT"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("embed failed: %d %s", rr.Code, rr.Body.String())
	}

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.embed"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.embed entry, got %d", len(entries))
	}
	e := entries[0]
	if !strings.Contains(e.Detail, "model=text-embedding-3-small") || !strings.Contains(e.Detail, "chars=") {
		t.Fatalf("detail missing model/chars: %q", e.Detail)
	}
	if strings.Contains(e.Detail, "SECRET-EMBED-INPUT") {
		t.Fatalf("detail leaked embed input: %q", e.Detail)
	}
}
