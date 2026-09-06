// internal/server/server_finance_created_test.go
package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/finance"
)

func TestFinanceCreateScoped_CreatedField(t *testing.T) {
	signer, err := auth.NewSigner("test-secret-012345678901234567890123", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenA, err := signer.SignWithWorkspace("u1", "member", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := signer.SignWithWorkspace("u1", "member", "ws-b")
	if err != nil {
		t.Fatal(err)
	}

	srv := newServer(
		config.Config{OpenCodeTimeoutMS: "5000"},
		adapter.NewStaticNPSAdapter(),
		adapter.NewOpenCodeHTTPAdapter(5000),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		signer, nil, nil, nil, nil, "", false, nil,
	)
	h := srv.Handler()

	// 首次入账：created=true
	payload1 := `{"type":"expense","amount":120,"category":"办公","note":"测试","source":"invoice","note_ref":"invoice:test_inv"}`
	rr1 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", tokenA, payload1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", rr1.Code, rr1.Body.String())
	}
	var res1 struct {
		finance.Transaction
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rr1.Body.Bytes(), &res1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if !res1.Created {
		t.Errorf("first create: expected created=true, got false")
	}
	if res1.Amount != 120 || res1.NoteRef != "invoice:test_inv" {
		t.Errorf("first create: unexpected data amount=%v note_ref=%s", res1.Amount, res1.NoteRef)
	}

	// 同幂等键重复入账：created=false，返回同一记录，HTTP 200
	rr2 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", tokenA, payload1)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second create status=%d (expected 200), body=%s", rr2.Code, rr2.Body.String())
	}
	var res2 struct {
		finance.Transaction
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if res2.Created {
		t.Errorf("second create: expected created=false, got true")
	}
	if res2.ID != res1.ID {
		t.Errorf("second create: expected same ID %s, got %s", res1.ID, res2.ID)
	}

	// 不同 workspace 同幂等键：新建，created=true
	rr3 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", tokenB, payload1)
	if rr3.Code != http.StatusCreated {
		t.Fatalf("other workspace create status=%d body=%s", rr3.Code, rr3.Body.String())
	}
	var res3 struct {
		finance.Transaction
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rr3.Body.Bytes(), &res3); err != nil {
		t.Fatalf("decode other workspace response: %v", err)
	}
	if !res3.Created {
		t.Errorf("other workspace: expected created=true, got false")
	}
	if res3.ID == res1.ID {
		t.Errorf("other workspace: should create new tx, got same ID=%s", res3.ID)
	}
	if res3.WorkspaceID != "ws-b" {
		t.Errorf("other workspace: expected workspace_id=ws-b, got %s", res3.WorkspaceID)
	}

	// 空幂等键：每次都新建
	emptyRefPayload := `{"type":"income","amount":200,"category":"奖金"}`
	rr4 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", tokenA, emptyRefPayload)
	if rr4.Code != http.StatusCreated {
		t.Fatalf("empty ref first status=%d body=%s", rr4.Code, rr4.Body.String())
	}
	var res4 struct {
		finance.Transaction
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rr4.Body.Bytes(), &res4); err != nil {
		t.Fatalf("decode empty ref first: %v", err)
	}
	if !res4.Created {
		t.Errorf("empty ref first: expected created=true, got false")
	}

	rr5 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/finance", tokenA, emptyRefPayload)
	if rr5.Code != http.StatusCreated {
		t.Fatalf("empty ref second status=%d body=%s", rr5.Code, rr5.Body.String())
	}
	var res5 struct {
		finance.Transaction
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rr5.Body.Bytes(), &res5); err != nil {
		t.Fatalf("decode empty ref second: %v", err)
	}
	if !res5.Created {
		t.Errorf("empty ref second: expected created=true, got false")
	}
	if res5.ID == res4.ID {
		t.Errorf("empty ref: should create separate tx, got same ID=%s", res5.ID)
	}
}
