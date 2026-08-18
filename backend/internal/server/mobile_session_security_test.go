package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

type mobileRouteAdapter struct {
	mu          sync.Mutex
	createCalls int
	sendCalls   int
	lastCreate  *adapter.CreateSessionRequest
}

func (a *mobileRouteAdapter) ListSessions(context.Context, string) ([]adapter.OpenCodeSession, error) {
	return nil, nil
}
func (a *mobileRouteAdapter) GetSessionSummary(context.Context, string, string) (string, error) {
	return "", nil
}
func (a *mobileRouteAdapter) ListRemoteTasks(context.Context, string, string, int) ([]adapter.RemoteTask, error) {
	return nil, nil
}
func (a *mobileRouteAdapter) CreateSession(_ context.Context, _ string, req *adapter.CreateSessionRequest) (*adapter.OpenCodeSessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.createCalls++
	a.lastCreate = req
	return &adapter.OpenCodeSessionInfo{ID: "session-owned"}, nil
}
func (a *mobileRouteAdapter) GetMessages(context.Context, string, string, int, string) ([]adapter.OpenCodeMessage, error) {
	return nil, nil
}
func (a *mobileRouteAdapter) SendPrompt(context.Context, string, string, *adapter.SendPromptRequest) (*adapter.SendPromptResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sendCalls++
	return &adapter.SendPromptResponse{MessageID: "message-owned"}, nil
}
func (a *mobileRouteAdapter) InterruptSession(context.Context, string, string) error { return nil }
func (a *mobileRouteAdapter) DeleteSession(context.Context, string, string) error    { return nil }
func (a *mobileRouteAdapter) SubscribeEvents(context.Context, string, string, string) (<-chan adapter.OpenCodeEvent, func(), error) {
	return nil, nil, errors.New("not used")
}
func (a *mobileRouteAdapter) HealthCheck(context.Context, string) error { return nil }

func newMobileRouteServer(t *testing.T) (*Server, *mobileRouteAdapter, *auth.Signer, map[string]string) {
	t.Helper()
	signer, err := auth.NewSigner("mobile-route-test-secret-0123456789", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry()
	for _, info := range []model.RegisteredInstanceInfo{
		{ID: "owned-a", WorkspaceID: "ws-a", APIBaseURL: "http://owned-a.test"},
		{ID: "owned-b", WorkspaceID: "ws-b", APIBaseURL: "http://owned-b.test"},
		{ID: "shared", APIBaseURL: "http://shared.test"},
	} {
		if err := reg.RegisterRegisteredInstance(info); err != nil {
			t.Fatal(err)
		}
	}
	ad := &mobileRouteAdapter{}
	cfg := config.Load()
	srv := New(cfg, adapter.NewStaticNPSAdapter(), ad, nil, reg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, signer, nil, nil, nil, nil, "", nil)
	tokens := map[string]string{}
	for _, ws := range []string{"ws-a", "ws-b", ""} {
		token, err := signer.SignWithWorkspace("user-"+strings.ReplaceAll(ws, "-", ""), "member", ws)
		if err != nil {
			t.Fatal(err)
		}
		tokens[ws] = token
	}
	return srv, ad, signer, tokens
}

func mobileRequest(method, target, token, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func structuredCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v body=%q", err, rr.Body.String())
	}
	code, _ := body["code"].(string)
	if code == "" {
		t.Fatalf("missing structured code: %s", rr.Body.String())
	}
	if requestID, _ := body["request_id"].(string); requestID == "" {
		t.Fatalf("missing request_id: %s", rr.Body.String())
	}
	return code
}

func TestMobileSessionRoutesFailClosedBeforeUpstream(t *testing.T) {
	srv, ad, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()
	cases := []struct {
		name   string
		target string
		token  string
		body   string
		status int
		code   string
	}{
		{"no token", "/api/mobile/sessions?instance_id=owned-a", "", "{}", http.StatusUnauthorized, CodeUnauthenticated},
		{"empty workspace claim", "/api/mobile/sessions?instance_id=owned-a", tokens[""], "{}", http.StatusBadRequest, CodeWorkspaceRequired},
		{"cross workspace private instance", "/api/mobile/sessions?instance_id=owned-b", tokens["ws-a"], "{}", http.StatusNotFound, CodeNotFound},
		{"shared instance write", "/api/mobile/sessions?instance_id=shared", tokens["ws-a"], "{}", http.StatusNotFound, CodeNotFound},
		{"unknown json field", "/api/mobile/sessions?instance_id=owned-a", tokens["ws-a"], `{"unknown":true}`, http.StatusBadRequest, CodeInvalidRequest},
		{"trailing json", "/api/mobile/sessions?instance_id=owned-a", tokens["ws-a"], `{} {}`, http.StatusBadRequest, CodeInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, mobileRequest(http.MethodPost, tc.target, tc.token, tc.body))
			if rr.Code != tc.status {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if got := structuredCode(t, rr); got != tc.code {
				t.Fatalf("code=%q want=%q", got, tc.code)
			}
			ad.mu.Lock()
			calls := ad.createCalls
			ad.mu.Unlock()
			if calls != 0 {
				t.Fatalf("rejected request reached upstream: createCalls=%d", calls)
			}
		})
	}
}

func TestMobileSessionCreateBindsClaimWorkspace(t *testing.T) {
	srv, ad, _, tokens := newMobileRouteServer(t)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodPost, "/api/mobile/sessions?instance_id=owned-a", tokens["ws-a"], `{}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Fatal("success response missing X-Request-ID")
	}
	ad.mu.Lock()
	defer ad.mu.Unlock()
	if ad.createCalls != 1 || ad.lastCreate == nil || ad.lastCreate.Location == nil || ad.lastCreate.Location.WorkspaceID == nil || *ad.lastCreate.Location.WorkspaceID != "ws-a" {
		t.Fatalf("unexpected create payload: %#v", ad.lastCreate)
	}
}

func TestMobileMessagesRejectInvalidPaginationBeforeUpstream(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	for _, query := range []string{"limit=0", "limit=101", "limit=nope", "order=sideways"} {
		t.Run(query, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/mobile/sessions/s-1/messages?instance_id=owned-a&"+query, tokens["ws-a"], ""))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if got := structuredCode(t, rr); got != CodeInvalidRequest {
				t.Fatalf("code=%q", got)
			}
		})
	}
}
