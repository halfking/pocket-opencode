package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

type diskLookupAdapter struct {
	mobileRouteAdapter
}

func (a *diskLookupAdapter) GetSessionSummary(_ context.Context, base, id string) (string, error) {
	if base == "disk://cursor" && id == "sess-disk-1" {
		return "from disk cursor", nil
	}
	return "", errors.New("not found")
}

func TestLookupDiskSessionTask_MatchesListSemantics(t *testing.T) {
	signer, err := auth.NewSigner("disk-fallback-test-secret-0123456789", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry()
	inst := &model.PocketInstance{
		ID: "disk-cursor", DisplayName: "Cursor (disk)",
		APIBaseURL: "disk://cursor", Origin: "disk", Health: "unknown",
	}
	if err := reg.RegisterInstance(inst); err != nil {
		t.Fatal(err)
	}
	reg.SetInstanceAPIBase("disk-cursor", "disk://cursor")

	cfg := config.Load()
	ad := &diskLookupAdapter{}
	srv := New(cfg, adapter.NewStaticNPSAdapter(), ad, nil, reg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, signer, nil, nil, nil, nil, "", nil)
	token, err := signer.SignWithWorkspace("u1", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/sess-disk-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET disk session as task: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "sess-disk-1" || got["title"] != "from disk cursor" || got["source"] != "opencode" {
		t.Fatalf("unexpected task: %+v", got)
	}

	miss := httptest.NewRequest(http.MethodGet, "/api/tasks/no-such", nil)
	miss.Header.Set("Authorization", "Bearer "+token)
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, miss)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("missing session want 404 got %d", rr2.Code)
	}
}
