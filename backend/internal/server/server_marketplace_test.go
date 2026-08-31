package server

// server_marketplace_test.go — Phase 4 marketplace HTTP handler 端到端测试。
//
// 用 MemoryStore 模拟 marketplace.Service,直接调用 handler 方法以验证:
//   - workspace_id 严格来自注入的 claims,从不信任 body；
//   - 完整 submit→review→publish→install→rate 生命周期；
//   - invalid 输入返回 4xx,sentinel error 翻译为正确状态码；
//   - store 未配置时统一返回 503。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/marketplace"
)

// withClaims 把认证 claims 注入到 request context,绕过 requireAuth 中间件。
// handler 直接被调用,所以这一层必须由测试负责。
func withClaims(r *http.Request, userID, workspaceID string) *http.Request {
	ctx := context.WithValue(r.Context(), authClaimsContextKey{}, &authClaims{
		UserID:      userID,
		WorkspaceID: workspaceID,
	})
	return r.WithContext(ctx)
}

// newRequest 构造一个 POST 请求,body 为 JSON。
func newRequest(t *testing.T, method, target string, body interface{}) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, target, &buf)
	r.Header.Set("Content-Type", "application/json")
	return r
}

// newMarketplaceTestServer 返回一个最小可用的 Server + MemoryStore。
func newMarketplaceTestServer() *Server {
	s := &Server{}
	s.SetMarketplaceStore(marketplace.NewMemoryStore())
	return s
}

func TestMarketplace_FullLifecycle(t *testing.T) {
	s := newMarketplaceTestServer()

	// 1. Submit 合法请求。
	submitBody := marketplace.SubmitRequest{
		Name:    "skill-x",
		Kind:    "skill",
		Version: "1.0.0",
		Digest:  "sha256:abc",
		Manifest: marketplace.Manifest{
			Version:     "1.0.0",
			Digest:      "sha256:abc",
			Permissions: []string{"fs.read", "net.fetch"},
		},
		// 故意把 workspace_id 写在 body,期望被 server 覆盖。
		WorkspaceID: "evil-ws",
		// Publisher 留空,期望被 handler 默认从 user_id 派生。
	}
	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/submit", submitBody), "user-1", "ws-real")
	w := httptest.NewRecorder()
	s.handleMarketplaceSubmit(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}
	var v marketplace.PackageVersion
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.WorkspaceID != "ws-real" {
		t.Errorf("workspace_id not enforced from claims: %q", v.WorkspaceID)
	}
	// Publisher 在 MemoryStore 中落到 Package,而非 Version。验证通过 list
	// 拿到 Package.Publisher(已默认从 user_id 派生)。
	pkgs, _ := s.marketplaceStore.ListPackages(context.Background(), "ws-real")
	if len(pkgs) != 1 || pkgs[0].Publisher != "user-1" {
		t.Errorf("publisher not defaulted from user_id: %#v", pkgs)
	}

	// 2. Publish 在 approved 之前必须 409。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/publish", marketplace.PublishCommand{
		WorkspaceID: "ws-real", VersionID: v.VersionID,
	}), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplacePublish(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("publish on draft: want 409, got %d body=%s", w.Code, w.Body.String())
	}

	// 3. Review approved。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/review", struct {
		VersionID string `json:"version_id"`
		Approved  bool   `json:"approved"`
	}{VersionID: v.VersionID, Approved: true}), "reviewer-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplaceReview(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("review status=%d body=%s", w.Code, w.Body.String())
	}

	// 4. Publish。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/publish", marketplace.PublishCommand{
		WorkspaceID: "ws-real", VersionID: v.VersionID, Channel: "stable",
	}), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplacePublish(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", w.Code, w.Body.String())
	}
	var rel marketplace.ReleaseRef
	if err := json.Unmarshal(w.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}
	if rel.Channel != "stable" || rel.ReleaseID == "" {
		t.Errorf("release malformed: %+v", rel)
	}

	// 5. Install。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/install", marketplace.InstallCommand{
		WorkspaceID: "ws-real", ReleaseID: rel.ReleaseID, InstalledBy: "user-1",
	}), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplaceInstall(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("install status=%d body=%s", w.Code, w.Body.String())
	}

	// 6. Rate。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/rate", marketplace.RatingCommand{
		WorkspaceID: "ws-real", ReleaseID: rel.ReleaseID, RatedBy: "user-1", Score: 5,
	}), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplaceRate(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rate status=%d body=%s", w.Code, w.Body.String())
	}

	// 7. 列表：packages / releases / versions。
	r = withClaims(newRequest(t, http.MethodGet, "/api/marketplace/packages", nil), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplacePackages(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("packages list status=%d", w.Code)
	}

	r = withClaims(newRequest(t, http.MethodGet, "/api/marketplace/packages/"+v.PackageID+"/versions", nil), "user-1", "ws-real")
	w = httptest.NewRecorder()
	s.handleMarketplacePackageVersions(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("versions status=%d", w.Code)
	}
}

func TestMarketplace_SubmitValidation(t *testing.T) {
	s := newMarketplaceTestServer()

	cases := []struct {
		name   string
		body   marketplace.SubmitRequest
		expect int
	}{
		{"missing name", marketplace.SubmitRequest{Kind: "skill", Version: "1", Digest: "d"}, http.StatusBadRequest},
		{"missing kind", marketplace.SubmitRequest{Name: "n", Version: "1", Digest: "d"}, http.StatusBadRequest},
		{"missing version", marketplace.SubmitRequest{Name: "n", Kind: "skill", Digest: "d"}, http.StatusBadRequest},
		{"missing digest", marketplace.SubmitRequest{Name: "n", Kind: "skill", Version: "1"}, http.StatusBadRequest},
		{"invalid kind", marketplace.SubmitRequest{Name: "n", Kind: "bogus", Version: "1", Digest: "d"}, http.StatusBadRequest},
		{"invalid visibility", marketplace.SubmitRequest{Name: "n", Kind: "skill", Version: "1", Digest: "d", Visibility: "galaxy"}, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/submit", tc.body), "u", "ws")
			w := httptest.NewRecorder()
			s.handleMarketplaceSubmit(w, r)
			if w.Code != tc.expect {
				t.Errorf("want %d, got %d body=%s", tc.expect, w.Code, w.Body.String())
			}
		})
	}
}

func TestMarketplace_StoreUnavailableReturns503(t *testing.T) {
	s := &Server{} // 不注入 marketplaceStore
	r := withClaims(newRequest(t, http.MethodGet, "/api/marketplace/packages", nil), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplacePackages(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
}

func TestMarketplace_NotFoundTranslation(t *testing.T) {
	s := newMarketplaceTestServer()
	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/review", struct {
		VersionID string `json:"version_id"`
		Approved  bool   `json:"approved"`
	}{VersionID: "ws/none@1.0.0", Approved: true}), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplaceReview(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMarketplace_RateOutOfRange(t *testing.T) {
	s := newMarketplaceTestServer()

	// 先建一个 published release。
	v, err := s.marketplaceStore.Submit(context.Background(), marketplace.SubmitRequest{
		WorkspaceID: "ws", Name: "a", Kind: "skill", Version: "1", Digest: "d",
		Manifest: marketplace.Manifest{Version: "1", Digest: "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.marketplaceStore.Review(context.Background(), marketplace.ReviewCommand{
		WorkspaceID: "ws", VersionID: v.VersionID, Reviewer: "u", Approved: true,
	}); err != nil {
		t.Fatal(err)
	}
	rel, err := s.marketplaceStore.Publish(context.Background(), marketplace.PublishCommand{
		WorkspaceID: "ws", VersionID: v.VersionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/rate", marketplace.RatingCommand{
		WorkspaceID: "ws", ReleaseID: rel.ReleaseID, RatedBy: "u", Score: 10,
	}), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplaceRate(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("score=10: want 400, got %d", w.Code)
	}
}

func TestMarketplace_RevokeRequiresReason(t *testing.T) {
	s := newMarketplaceTestServer()
	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/revoke", struct {
		ReleaseID string `json:"release_id"`
		Reason    string `json:"reason"`
	}{ReleaseID: "x", Reason: ""}), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplaceRevoke(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty reason: want 400, got %d", w.Code)
	}
}

// TestMarketplace_RouterUnknownPath 验证 router 对未知子路径返回 404。
func TestMarketplace_RouterUnknownPath(t *testing.T) {
	s := newMarketplaceTestServer()
	r := withClaims(newRequest(t, http.MethodGet, "/api/marketplace/bogus", nil), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplaceRouter(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// TestMarketplace_PackageIDExtraction 验证路径解析稳健性:
//   - 没有后缀 → 拒绝；
//   - 后缀缺前缀 → 拒绝；
//   - 中间含 / → 拒绝（防止 path 注入）。
func TestExtractMarketplacePackageID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/marketplace/packages/ws/a/versions", "ws/a"},
		{"/api/marketplace/packages/foo/versions", "foo"},
		{"/api/marketplace/packages//versions", ""},        // 空 id
		{"/api/marketplace/packages/a/b/c/versions", ""},   // 含额外 /
		{"/api/marketplace/packages/a/versions/extra", ""}, // 含额外后缀
		{"/api/marketplace/packages/a/other", ""},
	}
	for _, tc := range cases {
		got := extractMarketplacePackageID(tc.path, "/api/marketplace/packages/", "/versions")
		if got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.path, tc.want, got)
		}
	}
}

// TestWriteMarketplaceError 验证 sentinel error 的 HTTP 翻译。
func TestWriteMarketplaceError(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{marketplace.ErrMarketplaceNotFound, http.StatusNotFound},
		{marketplace.ErrMarketplaceConflict, http.StatusConflict},
		{marketplace.ErrMarketplaceNotPublished, http.StatusConflict},
		{marketplace.ErrMarketplaceRateOutOfRange, http.StatusBadRequest},
		{errors.New("random"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		writeMarketplaceError(w, tc.err)
		if w.Code != tc.status {
			t.Errorf("%v: want %d, got %d", tc.err, tc.status, w.Code)
		}
		if !strings.Contains(w.Body.String(), "error") {
			t.Errorf("missing error field: %s", w.Body.String())
		}
	}
}

// TestMarketplace_CrossWorkspaceIsolation 验证 workspace A 创建的
// package/version/release 不能被 workspace B 通过 list 接口看到。
func TestMarketplace_CrossWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	store := marketplace.NewMemoryStore()

	v, err := store.Submit(ctx, marketplace.SubmitRequest{
		WorkspaceID: "ws-a", Name: "private-skill", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: marketplace.Manifest{Version: "1.0.0", Digest: "d"},
	})
	if err != nil {
		t.Fatal(err)
	}

	pkgs, err := store.ListPackages(ctx, "ws-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 0 {
		t.Errorf("ws-b should not see ws-a packages; got %d", len(pkgs))
	}

	versions, err := store.ListVersions(ctx, "ws-b", v.PackageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("ws-b should not see ws-a versions; got %d", len(versions))
	}
}

// TestMarketplace_DuplicateVersionConflicts 验证同名 workspace + name +
// version 的二次 Submit 返回 ErrMarketplaceConflict。
func TestMarketplace_DuplicateVersionConflicts(t *testing.T) {
	ctx := context.Background()
	store := marketplace.NewMemoryStore()
	req := marketplace.SubmitRequest{
		WorkspaceID: "ws-x", Name: "dup", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: marketplace.Manifest{Version: "1.0.0", Digest: "d"},
	}
	if _, err := store.Submit(ctx, req); err != nil {
		t.Fatal(err)
	}
	_, err := store.Submit(ctx, req)
	if !errors.Is(err, marketplace.ErrMarketplaceConflict) {
		t.Fatalf("duplicate submit: want ErrMarketplaceConflict, got %v", err)
	}
}

// TestMarketplace_PackageIDForcedFromClaims 验证 handler 在 body 提供
// package_id 时仍覆盖为空,下游 store 派生为 "<workspace>/<name>",
// 不允许 caller 跨 workspace 污染命名空间。
func TestMarketplace_PackageIDForcedFromClaims(t *testing.T) {
	s := newMarketplaceTestServer()

	body := marketplace.SubmitRequest{
		Name:        "fresh",
		Kind:        "skill",
		Version:     "1.0.0",
		Digest:      "d",
		Manifest:    marketplace.Manifest{Version: "1.0.0", Digest: "d"},
		PackageID:   "evil-ws/hostile-pkg",
		WorkspaceID: "evil-ws",
	}
	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/submit", body), "u", "ws-real")
	w := httptest.NewRecorder()
	s.handleMarketplaceSubmit(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}
	var v marketplace.PackageVersion
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.PackageID != "ws-real/fresh" {
		t.Errorf("package_id should be derived from claims workspace: got %q", v.PackageID)
	}
}

// TestMarketplace_PackageVersionsRequiresSuffix 验证 handleMarketplacePackageVersions
// 对非 /versions 后缀的路径返回 404。
func TestMarketplace_PackageVersionsRequiresSuffix(t *testing.T) {
	s := newMarketplaceTestServer()
	r := withClaims(newRequest(t, http.MethodGet, "/api/marketplace/packages/ws-a/foo", nil), "u", "ws")
	w := httptest.NewRecorder()
	s.handleMarketplacePackageVersions(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 for non-/versions path, got %d", w.Code)
	}
}

// TestSanitizeAuditDetail 验证 audit detail 字符串的转义与截断。
func TestSanitizeAuditDetail(t *testing.T) {
	got := sanitizeAuditDetail("hello world")
	if !strings.Contains(got, `"hello world"`) {
		t.Errorf("basic escape failed: %q", got)
	}

	got = sanitizeAuditDetail("line1\nline2\ttab")
	if strings.Contains(got, "\n") || strings.Contains(got, "\t") {
		t.Errorf("control characters not escaped: %q", got)
	}

	big := strings.Repeat("x", 2000)
	got = sanitizeAuditDetail(big)
	if len(got) > 1024+3 {
		t.Errorf("detail not truncated: len=%d", len(got))
	}
}
