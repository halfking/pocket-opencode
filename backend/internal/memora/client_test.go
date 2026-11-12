package memora

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// newFakeMemora 起一个最小 memora 假服务：记录收到的 Authorization 头与
// X-Project-Id，按路径回假响应。返回 (server, 录制器)。
type recorded struct {
	authHeader string
	projectID  string
	tenantID   string
	addBody    map[string]any
	searchBody map[string]any
}

func newFakeMemora(t *testing.T, status int, body string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		rec.projectID = r.Header.Get("X-Project-Id")
		rec.tenantID = r.Header.Get("X-Tenant-Id")
		_ = json.NewDecoder(r.Body).Decode(&rec.addBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mem_123","content":"hello"}`))
	})
	mux.HandleFunc("/api/search/hybrid", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&rec.searchBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"results":[{"id":"m1","memory":"note body","score":0.9,"bm25_score":0.5,"rrf_score":0.03,"final_score":0.7}],"strategy":"bm25_rrf_mmr","total":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

// notebookRecorder 录制 /api/v2/notebook/* 假端的收包（A5 notebook 面）。
type notebookRecorder struct {
	authHeader       string
	legacyAuthHeader string
	projectID        string
	tenantID         string
	ifMatch          string
	listQuery        url.Values
	saveBody         map[string]any
	createStatus     int // POST /notes 的状态码，默认 201
}

// newFakeNotebook 在 newFakeMemora 的 mux 之外单起一个 notebook 假端，
// 响应形状逐字段对齐 memora internal/notebook/handler.go（worktree 6dc951d7）：
//   - GET  /notes → {items:[{id,kb_id,title,lifecycle,current_version,tags,created_at,updated_at}],total}
//   - GET  /notes/:id → noteViewBody + etag（含 revision/html/plain_text/removed）
//   - POST /notes → 201 {…, etag}
//   - PUT  /notes/:id（If-Match 必带）→ 200 {…, etag}
func newFakeNotebook(t *testing.T) (*httptest.Server, *notebookRecorder) {
	t.Helper()
	rec := &notebookRecorder{createStatus: http.StatusCreated}
	mux := http.NewServeMux()
	// 旧通道端点也在：V2 claims 增补不得影响 /api/* 的 token 形状（同一
	// client 两个面的回归对照）。
	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		rec.legacyAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mem_123","content":"hi"}`))
	})
	mux.HandleFunc("/api/v2/notebook/notes", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		rec.projectID = r.Header.Get("X-Project-Id")
		rec.tenantID = r.Header.Get("X-Tenant-Id")
		switch r.Method {
		case http.MethodGet:
			rec.listQuery = r.URL.Query()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[{"id":"nb_1","kb_id":"kb_a","title":"购物清单","lifecycle":"active","current_version":3,"tags":["openpocket","sync:openpocket:note:p1","sync:openpocket:hash:abcdef0123456789"],"created_at":"2026-11-13T01:02:03Z","updated_at":"2026-11-13T04:05:06Z"}],"total":1}`))
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&rec.saveBody)
			w.WriteHeader(rec.createStatus)
			_, _ = w.Write([]byte(`{"id":"nb_new","kb_id":"kb_a","title":"t","lifecycle":"active","revision":1,"html":"<p>t</p>","plain_text":"t","tags":[],"change_reason":"sync:openpocket","created_at":"2026-11-13T01:02:03Z","updated_at":"2026-11-13T01:02:03Z","removed":false,"etag":"1"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v2/notebook/notes/", func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		rec.projectID = r.Header.Get("X-Project-Id")
		rec.ifMatch = r.Header.Get("If-Match")
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"nb_1","kb_id":"kb_a","title":"购物清单","lifecycle":"active","revision":3,"html":"<p>清单</p>","plain_text":"清单","tags":["openpocket"],"change_reason":"sync:openpocket","created_at":"2026-11-13T01:02:03Z","updated_at":"2026-11-13T04:05:06Z","removed":false,"etag":"3"}`))
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&rec.saveBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"nb_1","kb_id":"kb_a","title":"购物清单","lifecycle":"active","revision":4,"html":"<p>新</p>","plain_text":"新","tags":[],"change_reason":"sync:openpocket","created_at":"2026-11-13T01:02:03Z","updated_at":"2026-11-13T09:09:09Z","removed":false,"etag":"4"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestNewClientEmptyBaseURL(t *testing.T) {
	if c := NewClient("", "", "", "", "", "", time.Second); c != nil {
		t.Fatalf("空 baseURL 应返回 nil（未配置降级），got %v", c)
	}
}

func TestAddAndSearchWithJWT(t *testing.T) {
	srv, rec := newFakeMemora(t, http.StatusOK, `{"status":"ok"}`)
	c := NewClient(srv.URL, "", "secret-123", "tenant-a", "proj-b", "pocketd", 5*time.Second)
	if c == nil {
		t.Fatal("client 不应为 nil")
	}
	ctx := context.Background()
	if err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if !strings.HasPrefix(rec.authHeader, "Bearer ") {
		t.Fatalf("应带 Bearer JWT，got %q", rec.authHeader)
	}
	token := strings.TrimPrefix(rec.authHeader, "Bearer ")
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte("secret-123"), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("JWT 应可用 secret-123 验证: %v", err)
	}
	if claims["tenant_id"] != "tenant-a" || claims["user_id"] != "pocketd" {
		t.Fatalf("claims 缺 tenant/user: %v", claims)
	}

	add, err := c.Add(ctx, AddRequest{Content: "hello", Tags: []string{"pocket-note"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if add.ID != "mem_123" {
		t.Fatalf("add.id = %q", add.ID)
	}
	if rec.addBody["content"] != "hello" || rec.addBody["user_id"] != "pocketd" {
		t.Fatalf("add body 不符: %v", rec.addBody)
	}
	if rec.projectID != "proj-b" || rec.tenantID != "tenant-a" {
		t.Fatalf("project/tenant 头不符: %q/%q", rec.projectID, rec.tenantID)
	}

	res, err := c.SearchHybrid(ctx, "note body", 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.OK || res.Total != 1 || res.Results[0].ID != "m1" {
		t.Fatalf("search 响应不符: %+v", res)
	}
	if rec.searchBody["top_k"] != float64(5) {
		t.Fatalf("top_k 未透传: %v", rec.searchBody)
	}
}

func TestStaticAPIKeyPreferred(t *testing.T) {
	srv, rec := newFakeMemora(t, http.StatusOK, `{}`)
	c := NewClient(srv.URL, "sk-static", "secret-should-not-be-used", "", "", "", 5*time.Second)
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	if rec.authHeader != "Bearer sk-static" {
		t.Fatalf("静态 key 优先: %q", rec.authHeader)
	}
}

func TestUpstreamErrorSurfaced(t *testing.T) {
	srv, _ := newFakeMemora(t, http.StatusUnauthorized, `{"error":"bad token"}`)
	c := NewClient(srv.URL, "", "secret", "", "", "", 5*time.Second)
	err := c.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应带出 HTTP 401，got %v", err)
	}
}

// ─── notebook 面（/api/v2/notebook/*，A5）─────────────────────────────────

// TestNotebookV2JWTHasFailClosedClaims 验证 V2 面的 token 按 memora V2JWTAuth
// 的必需 claims 清单（iss/aud/sub/tenant_id/scope/exp）签发，aud 精确匹配
// MEMORA_V2_JWT_AUDIENCE 默认值 memora-api；同时验证旧通道 token（/api/add）
// 的 claims 形状**不变**——两个面互不影响。
func TestNotebookV2JWTHasFailClosedClaims(t *testing.T) {
	srv, rec := newFakeNotebook(t)
	c := NewClientV2(srv.URL, "", "secret-123", "tenant-a", "proj-b", "alice", "", "", "", 5*time.Second)
	ctx := context.Background()
	if _, err := c.SearchNotes(ctx, NotebookQuery{Q: "清单", Limit: 5}); err != nil {
		t.Fatalf("search notes: %v", err)
	}
	if !strings.HasPrefix(rec.authHeader, "Bearer ") {
		t.Fatalf("notebook 应带 Bearer JWT，got %q", rec.authHeader)
	}
	v2Claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(strings.TrimPrefix(rec.authHeader, "Bearer "), v2Claims,
		func(*jwt.Token) (any, error) { return []byte("secret-123"), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("V2 JWT 应可验证: %v", err)
	}
	for _, claim := range []string{"iss", "aud", "sub", "tenant_id", "scope", "exp"} {
		if _, ok := v2Claims[claim]; !ok {
			t.Fatalf("V2 JWT 缺必需 claim %q: %v", claim, v2Claims)
		}
	}
	if v2Claims["aud"] != "memora-api" {
		t.Fatalf("aud 默认应为 memora-api，got %v", v2Claims["aud"])
	}
	if v2Claims["iss"] != "pocketd" {
		t.Fatalf("iss 默认应为 pocketd，got %v", v2Claims["iss"])
	}
	if v2Claims["sub"] != "alice" || v2Claims["tenant_id"] != "tenant-a" {
		t.Fatalf("sub/tenant_id 不符: %v", v2Claims)
	}
	if scope, ok := v2Claims["scope"].(string); !ok || !strings.Contains(scope, "notebook.read") || !strings.Contains(scope, "notebook.write") {
		t.Fatalf("scope 应含 notebook.read/write，got %v", v2Claims["scope"])
	}
	if rec.projectID != "proj-b" || rec.tenantID != "tenant-a" {
		t.Fatalf("X-Project-Id/X-Tenant-Id 头不符: %q/%q", rec.projectID, rec.tenantID)
	}

	// 旧通道 claims 不受影响：/api/add 的 token 仍只有旧 claims（无 iss/aud/scope）。
	if _, err := c.Add(ctx, AddRequest{Content: "hi"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	legacyClaims := jwt.MapClaims{}
	parsedLegacy, err := jwt.ParseWithClaims(strings.TrimPrefix(rec.legacyAuthHeader, "Bearer "), legacyClaims,
		func(*jwt.Token) (any, error) { return []byte("secret-123"), nil })
	if err != nil || !parsedLegacy.Valid {
		t.Fatalf("旧通道 JWT 应可验证: %v", err)
	}
	for _, added := range []string{"iss", "aud", "scope"} {
		if _, ok := legacyClaims[added]; ok {
			t.Fatalf("旧通道 token 不应被 v2 claims 污染（%s 出现了）: %v", added, legacyClaims)
		}
	}
	if legacyClaims["tenant_id"] != "tenant-a" {
		t.Fatalf("旧通道 tenant_id 应保留: %v", legacyClaims)
	}
}

// TestNotebookV2JWTRespectsOverrides 验证 iss/aud/scope 可经 NewClientV2 覆盖
// （memora 部署改过 MEMORA_V2_JWT_AUDIENCE 时的对口配置）。
func TestNotebookV2JWTRespectsOverrides(t *testing.T) {
	srv, rec := newFakeNotebook(t)
	c := NewClientV2(srv.URL, "", "secret-123", "", "", "", "openpocket-svc", "memora-api-staging", "notebook.read", 5*time.Second)
	if _, err := c.GetNote(context.Background(), "nb_1"); err != nil {
		t.Fatalf("get note: %v", err)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(strings.TrimPrefix(rec.authHeader, "Bearer "), claims,
		func(*jwt.Token) (any, error) { return []byte("secret-123"), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("V2 JWT 应可验证: %v", err)
	}
	if claims["iss"] != "openpocket-svc" || claims["aud"] != "memora-api-staging" || claims["scope"] != "notebook.read" {
		t.Fatalf("覆盖值未生效: %v", claims)
	}
}

// TestNotebookListAndDetailContract 契约形状复核：GET /notes 的 {items,total}
// 蛇形字段（id/kb_id/lifecycle/current_version/tags）与 GET /notes/:id 的
// etag/revision/removed。
func TestNotebookListAndDetailContract(t *testing.T) {
	srv, rec := newFakeNotebook(t)
	c := NewClient(srv.URL, "", "secret-123", "tenant-a", "proj-b", "pocketd", 5*time.Second)
	ctx := context.Background()

	list, err := c.SearchNotes(ctx, NotebookQuery{Q: "清单", Tag: "openpocket", Lifecycle: "active", KBID: "kb_a", Page: 2, Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("list 形状不符: %+v", list)
	}
	item := list.Items[0]
	if item.ID != "nb_1" || item.KBID != "kb_a" || item.Title != "购物清单" ||
		item.Lifecycle != "active" || item.CurrentVersion != 3 || len(item.Tags) != 3 {
		t.Fatalf("list item 蛇形字段不符: %+v", item)
	}
	q := rec.listQuery
	if q.Get("q") != "清单" || q.Get("tag") != "openpocket" || q.Get("lifecycle") != "active" ||
		q.Get("kb_id") != "kb_a" || q.Get("page") != "2" || q.Get("limit") != "5" {
		t.Fatalf("query 透传不符: %v", q)
	}

	detail, err := c.GetNote(ctx, "nb_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if detail.ID != "nb_1" || detail.Revision != 3 || detail.Etag != "3" || detail.Removed ||
		detail.HTML != "<p>清单</p>" || detail.PlainText != "清单" {
		t.Fatalf("detail 形状不符: %+v", detail)
	}
}

// TestNotebookPushAndUpdate 契约复核：POST /notes 打到 201 且 body 逐字段
// 对齐 noteSaveRequest（kb_id/title/html/tags/change_reason/source_updated_at）；
// PUT /notes/:id 必带 If-Match: base_version。
func TestNotebookPushAndUpdate(t *testing.T) {
	srv, rec := newFakeNotebook(t)
	c := NewClient(srv.URL, "", "secret-123", "tenant-a", "proj-b", "pocketd", 5*time.Second)
	ctx := context.Background()

	created, err := c.PushNote(ctx, PushNoteRequest{
		KBID:            "kb_a",
		Title:           "会议纪要",
		HTML:            "<p>正文</p>",
		Tags:            []string{"openpocket", "sync:openpocket:note:p1"},
		ChangeReason:    "sync:openpocket",
		SourceUpdatedAt: "2026-11-13T08:00:00Z",
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if created.ID != "nb_new" || created.Revision != 1 || created.Etag != "1" || created.Removed {
		t.Fatalf("create 响应不符: %+v", created)
	}
	if rec.saveBody["title"] != "会议纪要" || rec.saveBody["html"] != "<p>正文</p>" ||
		rec.saveBody["change_reason"] != "sync:openpocket" || rec.saveBody["source_updated_at"] != "2026-11-13T08:00:00Z" {
		t.Fatalf("create body 不符: %v", rec.saveBody)
	}
	tags, _ := rec.saveBody["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("tags 未透传: %v", rec.saveBody["tags"])
	}

	updated, err := c.UpdateNote(ctx, "nb_1", 3, PushNoteRequest{Title: "t", HTML: "<p>新</p>"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Revision != 4 || updated.Etag != "4" {
		t.Fatalf("update 响应不符: %+v", updated)
	}
	if rec.ifMatch != "3" {
		t.Fatalf("If-Match 头应为 base_version 3，got %q", rec.ifMatch)
	}
	if rec.saveBody["html"] != "<p>新</p>" {
		t.Fatalf("update body 不符: %v", rec.saveBody)
	}

	// 412 version_conflict：非 2xx 原样成为带状态码的 error。
	rec2 := rec // 复用录制器语义说明：真 412 在 UpdateNote 里是上游错误路径，
	// 这里用独立假端验证错误透出而非解析成功。
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/notebook/notes/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":"version_conflict","current_version":7,"current_updated_at":"2026-11-13T09:00:00Z"}`))
	})
	conflictSrv := httptest.NewServer(mux)
	t.Cleanup(conflictSrv.Close)
	cc := NewClient(conflictSrv.URL, "", "s", "", "", "", 5*time.Second)
	_, err = cc.UpdateNote(ctx, "nb_1", 3, PushNoteRequest{Title: "t", HTML: "<p>x</p>"})
	if err == nil || !strings.Contains(err.Error(), "412") || !strings.Contains(err.Error(), "version_conflict") {
		t.Fatalf("412 应带出状态码与 error 字段，got %v", err)
	}
	_ = rec2
}

// TestNotebookPushValidation 客户端侧守门：空标题 / 空 id / 无 base 的更新
// 不出网（memora 侧也会拒，但客户端先挡掉可省一次注定失败的往返）。
func TestNotebookPushValidation(t *testing.T) {
	srv, rec := newFakeNotebook(t)
	c := NewClient(srv.URL, "", "s", "", "", "", 5*time.Second)
	ctx := context.Background()
	if _, err := c.PushNote(ctx, PushNoteRequest{HTML: "<p></p>"}); err == nil {
		t.Fatal("空标题应被拒绝")
	}
	if _, err := c.GetNote(ctx, " "); err == nil {
		t.Fatal("空 id 应被拒绝")
	}
	if _, err := c.UpdateNote(ctx, "nb_1", 0, PushNoteRequest{Title: "t"}); err == nil {
		t.Fatal("无 base_version 的更新应被拒绝")
	}
	if rec.saveBody != nil {
		t.Fatalf("非法请求不应出网: %v", rec.saveBody)
	}
}

// TestNotebookUpstreamErrorSurfaced 上游 4xx 透出（与旧通道同一纪律）。
func TestNotebookUpstreamErrorSurfaced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/notebook/notes", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "", "s", "", "", "", 5*time.Second)
	_, err := c.SearchNotes(context.Background(), NotebookQuery{Q: "x"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应带出 HTTP 401，got %v", err)
	}
}
