package server

// Contract for POST /api/snippets required-field validation.
//
// snippet.CreateScoped treats Language as required (store.go: "snippet
// language cannot be empty", covered by store_test.go's "empty language"
// case), but handleCreateSnippet only checked Title and Code. A client that
// omitted language therefore fell through to the store, got a validation
// error back, and had it reported as
//
//	500 {"error":"failed to create snippet"}
//
// which is a server-fault status for what is a client input error. These
// tests drive the handler directly with injected claims, so no PostgreSQL and
// no token are needed — snippet.Store is in-memory.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/snippet"
)

func newSnippetCreateServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServerWithAuth(t)
	srv.snippetStore = snippet.NewStore()
	return srv
}

func postSnippet(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/snippets", strings.NewReader(body))
	req = withTestClaims(req, "test-user", "admin", "test-workspace")
	rr := httptest.NewRecorder()
	srv.handleCreateSnippet(rr, req)
	return rr
}

// TestHandleCreateSnippet_RequiresLanguage is the regression lock: omitting a
// field the store already considers required must be a 400, not a 500.
func TestHandleCreateSnippet_RequiresLanguage(t *testing.T) {
	srv := newSnippetCreateServer(t)

	rr := postSnippet(t, srv, `{"title":"t","code":"c"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("缺 language 应回 400，实际 %d（body=%q）——存储层的必填校验被当成了服务端故障", rr.Code, rr.Body.String())
	}
}

// TestHandleCreateSnippet_AcceptsValid guards the positive case, so the fix
// cannot be "reject everything".
func TestHandleCreateSnippet_AcceptsValid(t *testing.T) {
	srv := newSnippetCreateServer(t)

	rr := postSnippet(t, srv, `{"title":"t","code":"c","language":"go"}`)

	if rr.Code != http.StatusCreated {
		t.Fatalf("完整请求应回 201，实际 %d（body=%q）", rr.Code, rr.Body.String())
	}
	var snip snippet.Snippet
	if err := json.Unmarshal(rr.Body.Bytes(), &snip); err != nil {
		t.Fatalf("解码 201 响应: %v", err)
	}
	if snip.ID == "" {
		t.Fatal("201 响应里没有 id")
	}
	if snip.Language != "go" {
		t.Fatalf("落库 language=%q，期望 go", snip.Language)
	}
}

// TestHandleCreateSnippet_RequiresTitleAndCode keeps the existing two checks
// pinned while the third one is added.
func TestHandleCreateSnippet_RequiresTitleAndCode(t *testing.T) {
	for name, body := range map[string]string{
		"缺 title":  `{"code":"c","language":"go"}`,
		"缺 code":   `{"title":"t","language":"go"}`,
		"title 空串": `{"title":"","code":"c","language":"go"}`,
		"code 空串":  `{"title":"t","code":"","language":"go"}`,
		// 纯空格：存储层用 TrimSpace 判空，handler 必须同样，否则这些会漏到
		// CreateScoped 的 err 分支变成 500。
		"title 全空格":    `{"title":"   ","code":"c","language":"go"}`,
		"code 全空格":     `{"title":"t","code":"   ","language":"go"}`,
		"language 全空格": `{"title":"t","code":"c","language":"   "}`,
		"language 缺失":  `{"title":"t","code":"c"}`,
		"三字段全缺":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newSnippetCreateServer(t)
			rr := postSnippet(t, srv, body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s 应回 400，实际 %d（body=%q）", name, rr.Code, rr.Body.String())
			}
		})
	}
}
