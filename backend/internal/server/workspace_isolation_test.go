package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/finance"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

func serveWorkspaceJSON(t *testing.T, h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeWorkspaceList[T any](t *testing.T, rr *httptest.ResponseRecorder, key string) ([]T, int) {
	t.Helper()
	var payload struct {
		Items []T `json:"-"`
		Total int `json:"total"`
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode list response: %v body=%q", err, rr.Body.String())
	}
	if err := json.Unmarshal(raw["total"], &payload.Total); err != nil {
		t.Fatalf("decode list total: %v body=%q", err, rr.Body.String())
	}
	if err := json.Unmarshal(raw[key], &payload.Items); err != nil {
		t.Fatalf("decode list %s: %v body=%q", key, err, rr.Body.String())
	}
	return payload.Items, payload.Total
}

func newWorkspaceIsolationServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()

	signer, err := auth.NewSigner("workspace-isolation-test-secret-012345", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokens := make(map[string]string)
	for _, workspaceID := range []string{"ws-a", "ws-b"} {
		token, err := signer.SignWithWorkspace("shared-user", "member", workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		tokens[workspaceID] = token
	}

	return newServer(
		config.Config{OpenCodeTimeoutMS: "5000"},
		adapter.NewStaticNPSAdapter(),
		adapter.NewOpenCodeHTTPAdapter(5000),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		signer, nil, nil, nil, nil, "", false, nil,
	), tokens
}
func TestMeetingWorkspaceIsolation(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()

	create := func(token, title string) *meeting.Meeting {
		rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", token, `{"title":"`+title+`"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create meeting status=%d body=%s", rr.Code, rr.Body.String())
		}
		var m meeting.Meeting
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode meeting: %v", err)
		}
		return &m
	}

	meetingA := create(tokens["ws-a"], "workspace A meeting")
	meetingB := create(tokens["ws-b"], "workspace B meeting")
	if meetingA.OwnerID != "shared-user" || meetingA.WorkspaceID != "ws-a" {
		t.Fatalf("meeting A identity=%s/%s, want shared-user/ws-a", meetingA.OwnerID, meetingA.WorkspaceID)
	}
	if meetingB.OwnerID != "shared-user" || meetingB.WorkspaceID != "ws-b" {
		t.Fatalf("meeting B identity=%s/%s, want shared-user/ws-b", meetingB.OwnerID, meetingB.WorkspaceID)
	}

	for _, tc := range []struct {
		name   string
		token  string
		target string
		method string
		want   int
	}{
		{name: "list A", token: tokens["ws-a"], target: "/api/meetings", method: http.MethodGet, want: 1},
		{name: "list B", token: tokens["ws-b"], target: "/api/meetings", method: http.MethodGet, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, tc.method, tc.target, tc.token, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
			}
			items, total := decodeWorkspaceList[meeting.Meeting](t, rr, "meetings")
			if total != tc.want || len(items) != tc.want {
				t.Fatalf("list total/items=%d/%d, want %d", total, len(items), tc.want)
			}
			if tc.name == "list A" && (items[0].ID != meetingA.ID || items[0].WorkspaceID != "ws-a") {
				t.Fatalf("workspace A list leaked or omitted meeting: %+v", items[0])
			}
			if tc.name == "list B" && (items[0].ID != meetingB.ID || items[0].WorkspaceID != "ws-b") {
				t.Fatalf("workspace B list leaked or omitted meeting: %+v", items[0])
			}
		})
	}

	crossGet := serveWorkspaceJSON(t, h, http.MethodGet, "/api/meetings/"+meetingA.ID, tokens["ws-b"], "")
	if crossGet.Code != http.StatusNotFound || strings.Contains(crossGet.Body.String(), meetingA.Title) {
		t.Fatalf("cross-workspace meeting GET status=%d body=%s", crossGet.Code, crossGet.Body.String())
	}
	crossDelete := serveWorkspaceJSON(t, h, http.MethodDelete, "/api/meetings/"+meetingA.ID, tokens["ws-b"], "")
	if crossDelete.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace meeting DELETE status=%d body=%s", crossDelete.Code, crossDelete.Body.String())
	}
	stillThere := serveWorkspaceJSON(t, h, http.MethodGet, "/api/meetings/"+meetingA.ID, tokens["ws-a"], "")
	if stillThere.Code != http.StatusOK {
		t.Fatalf("cross-workspace delete removed owner meeting: status=%d body=%s", stillThere.Code, stillThere.Body.String())
	}

	crossTranscribe := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingA.ID+"/transcribe", tokens["ws-b"], "audio")
	if crossTranscribe.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace meeting transcribe status=%d body=%s", crossTranscribe.Code, crossTranscribe.Body.String())
	}
	ownerBeforeTranscribe := serveWorkspaceJSON(t, h, http.MethodGet, "/api/meetings/"+meetingA.ID, tokens["ws-a"], "")
	var before meeting.Meeting
	if err := json.Unmarshal(ownerBeforeTranscribe.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode owner meeting before transcribe: %v", err)
	}
	if before.Status != "recording" || before.Transcript != "" {
		t.Fatalf("cross-workspace transcribe changed meeting: status=%q transcript=%q", before.Status, before.Transcript)
	}
	ownerTranscribe := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingA.ID+"/transcribe", tokens["ws-a"], "audio")
	if ownerTranscribe.Code != http.StatusOK {
		t.Fatalf("owner meeting transcribe status=%d body=%s", ownerTranscribe.Code, ownerTranscribe.Body.String())
	}
	var afterTranscribe meeting.Meeting
	ownerAfterTranscribe := serveWorkspaceJSON(t, h, http.MethodGet, "/api/meetings/"+meetingA.ID, tokens["ws-a"], "")
	if err := json.Unmarshal(ownerAfterTranscribe.Body.Bytes(), &afterTranscribe); err != nil {
		t.Fatalf("decode owner meeting after transcribe: %v", err)
	}
	if afterTranscribe.Status != "transcribed" || afterTranscribe.Transcript == "" {
		t.Fatalf("owner transcribe did not persist: status=%q transcript=%q", afterTranscribe.Status, afterTranscribe.Transcript)
	}

	// Seed a separate transcript so summarize isolation is tested before any
	// owner-side action can make the meeting look accessible to workspace B.
	meetingForSummary, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: "summary meeting"}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("seed summary meeting: %v", err)
	}
	meetingForSummary.Transcript = "张三: 同意发布\n李四: 我来负责上线"
	if err := srv.meetingStore.UpdateScoped(meetingForSummary, "shared-user", "ws-a"); err != nil {
		t.Fatalf("seed summary transcript: %v", err)
	}
	crossSummarize := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingForSummary.ID+"/summarize", tokens["ws-b"], "")
	if crossSummarize.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace meeting summarize status=%d body=%s", crossSummarize.Code, crossSummarize.Body.String())
	}
	ownerSummarize := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingForSummary.ID+"/summarize", tokens["ws-a"], "")
	if ownerSummarize.Code != http.StatusOK {
		t.Fatalf("owner meeting summarize status=%d body=%s", ownerSummarize.Code, ownerSummarize.Body.String())
	}
	var summarized meeting.Meeting
	if err := json.Unmarshal(ownerSummarize.Body.Bytes(), &summarized); err != nil {
		t.Fatalf("decode summarized meeting: %v", err)
	}
	if summarized.Status != "done" || summarized.Summary == "" || len(summarized.KeyDecisions) == 0 || len(summarized.ActionItems) == 0 {
		t.Fatalf("summary not persisted: %+v", summarized)
	}
}

func TestFinanceWorkspaceIsolation(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()

	create := func(token, body string) *finance.Transaction {
		rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", token, body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create transaction status=%d body=%s", rr.Code, rr.Body.String())
		}
		var tx finance.Transaction
		if err := json.Unmarshal(rr.Body.Bytes(), &tx); err != nil {
			t.Fatalf("decode transaction: %v", err)
		}
		return &tx
	}

	txA := create(tokens["ws-a"], `{"type":"income","amount":100,"category":"salary","note":"A only","workspace_id":"ws-b"}`)
	txB := create(tokens["ws-b"], `{"type":"expense","amount":40,"category":"travel","note":"B only","workspace_id":"ws-a"}`)
	if txA.OwnerID != "shared-user" || txA.WorkspaceID != "ws-a" || txA.Amount != 100 {
		t.Fatalf("transaction A identity/data=%+v", txA)
	}
	if txB.OwnerID != "shared-user" || txB.WorkspaceID != "ws-b" || txB.Amount != 40 {
		t.Fatalf("transaction B identity/data=%+v", txB)
	}

	for _, tc := range []struct {
		name  string
		token string
		id    string
		want  string
	}{
		{name: "workspace A", token: tokens["ws-a"], id: txA.ID, want: "A only"},
		{name: "workspace B", token: tokens["ws-b"], id: txB.ID, want: "B only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, http.MethodGet, "/api/finance", tc.token, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
			}
			items, total := decodeWorkspaceList[finance.Transaction](t, rr, "transactions")
			if total != 1 || len(items) != 1 || items[0].ID != tc.id || items[0].Note != tc.want {
				t.Fatalf("unexpected scoped finance list total=%d items=%+v", total, items)
			}
		})
	}

	crossGet := serveWorkspaceJSON(t, h, http.MethodGet, "/api/finance/"+txA.ID, tokens["ws-b"], "")
	if crossGet.Code != http.StatusNotFound || strings.Contains(crossGet.Body.String(), txA.Note) {
		t.Fatalf("cross-workspace finance GET status=%d body=%s", crossGet.Code, crossGet.Body.String())
	}
	crossDelete := serveWorkspaceJSON(t, h, http.MethodDelete, "/api/finance/"+txA.ID, tokens["ws-b"], "")
	if crossDelete.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace finance DELETE status=%d body=%s", crossDelete.Code, crossDelete.Body.String())
	}
	ownerGet := serveWorkspaceJSON(t, h, http.MethodGet, "/api/finance/"+txA.ID, tokens["ws-a"], "")
	if ownerGet.Code != http.StatusOK {
		t.Fatalf("cross-workspace finance delete removed owner transaction: status=%d body=%s", ownerGet.Code, ownerGet.Body.String())
	}

	for _, tc := range []struct {
		name        string
		token       string
		wantCount   int
		wantIncome  float64
		wantExpense float64
		wantBalance float64
	}{
		{name: "workspace A stats", token: tokens["ws-a"], wantCount: 1, wantIncome: 100, wantExpense: 0, wantBalance: 100},
		{name: "workspace B stats", token: tokens["ws-b"], wantCount: 1, wantIncome: 0, wantExpense: 40, wantBalance: -40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, http.MethodGet, "/api/finance/stats", tc.token, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("stats status=%d body=%s", rr.Code, rr.Body.String())
			}
			var stats finance.MonthlyStats
			if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
				t.Fatalf("decode stats: %v", err)
			}
			if stats.Count != tc.wantCount || stats.TotalIncome != tc.wantIncome || stats.TotalExpense != tc.wantExpense || stats.Balance != tc.wantBalance {
				t.Fatalf("stats=%+v, want count/income/expense/balance=%d/%.2f/%.2f/%.2f", stats, tc.wantCount, tc.wantIncome, tc.wantExpense, tc.wantBalance)
			}
		})
	}

	ownerDelete := serveWorkspaceJSON(t, h, http.MethodDelete, "/api/finance/"+txA.ID, tokens["ws-a"], "")
	if ownerDelete.Code != http.StatusNoContent {
		t.Fatalf("owner finance DELETE status=%d body=%s", ownerDelete.Code, ownerDelete.Body.String())
	}
}

func TestSttTranscribeAuthAndUnknownFields(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	srv.transcriber = stt.NewTranscriber("", "", "")
	h := srv.Handler()

	unauthenticated := serveWorkspaceJSON(t, h, http.MethodPost, "/api/stt/transcribe", "", `{"audioBase64":"YQ==","workspace_id":"ws-b","tenant_id":"ws-b"}`)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated STT status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	for _, tc := range []struct {
		name  string
		token string
		body  string
	}{
		{name: "workspace A ignores spoofed workspace", token: tokens["ws-a"], body: `{"audioBase64":"YQ==","workspace_id":"ws-b","tenant_id":"ws-b"}`},
		{name: "workspace B ignores spoofed workspace", token: tokens["ws-b"], body: `{"audioBase64":"YQ==","workspace_id":"ws-a","tenant_id":"ws-a"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/stt/transcribe", tc.token, tc.body)
			if rr.Code != http.StatusBadGateway {
				t.Fatalf("STT status=%d body=%s", rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "ws-a") || strings.Contains(rr.Body.String(), "ws-b") {
				t.Fatalf("STT response reflected client tenant fields: %s", rr.Body.String())
			}
		})
	}
}
