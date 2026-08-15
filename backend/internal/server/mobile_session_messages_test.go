package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// messagesTestAdapter 以 V1 信封线格式喂给 handler，
// 验证 /messages 与 /summary 的响应已经过移动端视图归一化。
type messagesTestAdapter struct {
	mobileRouteAdapter
	messages []adapter.OpenCodeMessage
	title    string
}

func (a *messagesTestAdapter) GetMessages(context.Context, string, string, int, string) ([]adapter.OpenCodeMessage, error) {
	return a.messages, nil
}

func (a *messagesTestAdapter) GetSessionSummary(context.Context, string, string) (string, error) {
	return a.title, nil
}

func newMessagesTestServer(t *testing.T) (*Server, *messagesTestAdapter, string) {
	t.Helper()
	srv, _, _, tokens := newMobileRouteServer(t)
	ad := &messagesTestAdapter{title: "修复任务"}
	// 与上游 GET /session/:id/message 一致的 V1 裸数组线格式
	for _, raw := range []string{
		`{"info":{"id":"msg_u1","role":"user","time":{"created":1755300000000}},
		  "parts":[{"id":"prt_1","type":"text","text":"修复登录 bug"}]}`,
		`{"info":{"id":"msg_a1","role":"assistant","time":{"created":1755300001000}},
		  "parts":[{"id":"prt_t1","type":"text","text":"已修复"},
		           {"id":"prt_tl1","type":"tool","callID":"call_1","tool":"edit",
		            "state":{"status":"completed","input":{"file":"src/a.ts"},"output":"--- a\n+++ b\n",
		                     "time":{"start":1755300002000,"end":1755300006000}}}]}`,
	} {
		var m adapter.OpenCodeMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal fixture: %v", err)
		}
		ad.messages = append(ad.messages, m)
	}
	srv.opencode = ad
	return srv, ad, tokens["ws-a"]
}

func TestMobileSessionMessagesNormalizedShape(t *testing.T) {
	srv, _, token := newMessagesTestServer(t)
	h := srv.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet,
		"/api/mobile/sessions/ses_1/messages?instance_id=owned-a", token, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		SessionID string `json:"sessionId"`
		Total     int    `json:"total"`
		Messages  []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Text    string `json:"text"`
			Content []struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if body.SessionID != "ses_1" || body.Total != 2 || len(body.Messages) != 2 {
		t.Fatalf("envelope: %+v", body)
	}

	user := body.Messages[0]
	if user.ID != "msg_u1" || user.Role != "user" || user.Text != "修复登录 bug" {
		t.Fatalf("user row: %+v", user)
	}

	assistant := body.Messages[1]
	if assistant.ID != "msg_a1" || assistant.Role != "assistant" || assistant.Text != "已修复" {
		t.Fatalf("assistant row: %+v", assistant)
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant content: %+v", assistant.Content)
	}
	tool := assistant.Content[1]
	if tool.Type != "tool" || tool.ID != "call_1" || tool.Name != "edit" || tool.State != "completed" {
		t.Fatalf("tool content: %+v", tool)
	}
}

func TestMobileSessionSummaryCountsRoles(t *testing.T) {
	srv, _, token := newMessagesTestServer(t)
	h := srv.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet,
		"/api/mobile/sessions/ses_1/summary?instance_id=owned-a", token, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Title        string `json:"title"`
		Summary      string `json:"summary"`
		MessageCount int    `json:"messageCount"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MessageCount != 2 {
		t.Fatalf("messageCount: %d（V1 信封无顶层 type，旧逻辑恒为 0 计数）", body.MessageCount)
	}
	if body.Summary == body.Title {
		t.Fatalf("summary should include role counts: %q", body.Summary)
	}
}
