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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestMarketplace_ReleaseBlobRoute 验证 blob 下载路由（Agent B, 签名链路 ADR §5）：
//   - release_id 含 / 时按 "前缀 releases/ + 后缀 /blob" 截取，能正确到达 handler；
//   - 形状不符（缺 release_id / 末段不是 blob）→ 404；
//   - memstore 等非 PG Store 没有内容寻址 blob 能力 → 501；
//   - 非 GET 方法 → 405。
func TestMarketplace_ReleaseBlobRoute(t *testing.T) {
	s := newMarketplaceTestServer()

	cases := []struct {
		name   string
		method string
		path   string
		expect int
	}{
		// release_id = "ws-a/skill-x@1.0.0-stable-1700000000000"（含 /）。
		{"release id with slash", http.MethodGet, "/api/marketplace/releases/ws-a/skill-x@1.0.0-stable-1700000000000/blob", http.StatusNotImplemented},
		{"missing release id", http.MethodGet, "/api/marketplace/releases/blob", http.StatusNotFound},
		{"extra suffix after blob", http.MethodGet, "/api/marketplace/releases/ws-a/pkg@1.0.0/blob/extra", http.StatusNotFound},
		{"not blob suffix", http.MethodGet, "/api/marketplace/releases/ws-a/pkg@1.0.0/versions", http.StatusNotFound},
		{"post not allowed", http.MethodPost, "/api/marketplace/releases/ws-a/pkg@1.0.0/blob", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := withClaims(newRequest(t, tc.method, tc.path, nil), "u", "ws-a")
			w := httptest.NewRecorder()
			s.handleMarketplaceRouter(w, r)
			if w.Code != tc.expect {
				t.Errorf("%s %s: want %d, got %d body=%s", tc.method, tc.path, tc.expect, w.Code, w.Body.String())
			}
		})
	}
}

// TestMarketplace_ReleaseBlobRequiresAuth 验证 blob 端点挂在 requireAuth
// 之后：未携带 Authorization → 401（与其它 marketplace 路由一致）。
func TestMarketplace_ReleaseBlobRequiresAuth(t *testing.T) {
	s := newMarketplaceTestServer()
	h := s.requireAuth(s.handleMarketplaceRouter)
	r := httptest.NewRequest(http.MethodGet, "/api/marketplace/releases/ws-a/pkg@1.0.0-stable-1/blob", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated blob download: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// newBlobRequest 构造一个 raw-body 请求（blob 上传正文不是 JSON）。
func newBlobRequest(t *testing.T, method, target string, content []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, bytes.NewReader(content))
	r.Header.Set("Content-Type", "application/octet-stream")
	return r
}

// blobDigestOf 计算测试内容的 sha256 hex。
func blobDigestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// newPGMarketplaceTestServer 构造 PG-backed marketplace 的 Server（复用
// mustTestPool 的随机 schema 隔离）。DSN 不可用时 skip。
func newPGMarketplaceTestServer(t *testing.T, blobQuota int64) (*Server, *marketplace.Store) {
	t.Helper()
	pool := mustTestPool(t)
	store := marketplace.NewStore(pool)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("marketplace Init: %v", err)
	}
	s := &Server{}
	s.SetMarketplaceStore(store)
	s.SetMarketplaceBlobQuota(blobQuota)
	return s, store
}

// TestMarketplace_BlobAndSigningRoutesNotPG 验证新端点在非 PG store 下的降级
// （ADR §10：memstore 无密钥/blob 设施）：一律 501，方法不符 405，路由形状
// 不符 404。
func TestMarketplace_BlobAndSigningRoutesNotPG(t *testing.T) {
	s := newMarketplaceTestServer()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		expect int
	}{
		{"blob upload via memstore", http.MethodPut, "/api/marketplace/blobs/" + blobDigestOf([]byte("x")), "x", http.StatusNotImplemented},
		{"blob download via memstore", http.MethodGet, "/api/marketplace/blobs/" + blobDigestOf([]byte("x")), "", http.StatusNotImplemented},
		{"blob usage via memstore", http.MethodGet, "/api/marketplace/blobs", "", http.StatusNotImplemented},
		{"key register via memstore", http.MethodPost, "/api/marketplace/signing/keys", `{"key_id":"k1","public_key":"AAAA"}`, http.StatusNotImplemented},
		{"key list via memstore", http.MethodGet, "/api/marketplace/signing/keys", "", http.StatusNotImplemented},
		{"key revoke via memstore", http.MethodDelete, "/api/marketplace/signing/keys/k1", "", http.StatusNotImplemented},
		{"blob digest too short", http.MethodPut, "/api/marketplace/blobs/abc", "x", http.StatusNotImplemented},
		{"signing subroute unknown", http.MethodPost, "/api/marketplace/signing/other", `{}`, http.StatusNotFound},
		{"blob path too deep", http.MethodGet, "/api/marketplace/blobs/aa/bb", "", http.StatusNotFound},
		{"blob upload wrong method", http.MethodPost, "/api/marketplace/blobs/" + blobDigestOf([]byte("x")), `{}`, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *http.Request
			switch {
			case tc.method == http.MethodPut:
				r = newBlobRequest(t, tc.method, tc.path, []byte(tc.body))
			case tc.body == "":
				r = httptest.NewRequest(tc.method, tc.path, nil)
			default:
				r = newRequest(t, tc.method, tc.path, json.RawMessage(tc.body))
			}
			r = withClaims(r, "user-1", "ws-a")
			w := httptest.NewRecorder()
			s.handleMarketplaceRouter(w, r)
			if w.Code != tc.expect {
				t.Errorf("%s %s: want %d, got %d body=%s", tc.method, tc.path, tc.expect, w.Code, w.Body.String())
			}
		})
	}
}

// TestMarketplace_BlobUploadDownloadPG 端到端验证 blob 上传/下载/用量
// （PG-backed）：内容寻址校验、幂等语义、X-Digest 回显、配额超限 507。
func TestMarketplace_BlobUploadDownloadPG(t *testing.T) {
	s, _ := newPGMarketplaceTestServer(t, 1<<20)
	h := s.handleMarketplaceRouter

	content := []byte("hello content-addressed world")
	digest := blobDigestOf(content)

	// 1. 上传成功 → 201 + X-Digest + 用量。
	r := withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/"+digest, content), "user-1", "ws-a")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Digest"); got != digest {
		t.Errorf("X-Digest: want %q, got %q", digest, got)
	}
	var upResp struct {
		Blob  marketplace.BlobMeta `json:"blob"`
		Usage struct {
			UsedBytes  int64 `json:"used_bytes"`
			QuotaBytes int64 `json:"quota_bytes"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &upResp); err != nil {
		t.Fatal(err)
	}
	if upResp.Blob.Size != int64(len(content)) || upResp.Usage.UsedBytes != int64(len(content)) {
		t.Errorf("upload meta/usage malformed: %+v", upResp)
	}

	// 2. 同 workspace 幂等重放 → 200。
	r = withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/"+digest, content), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("idempotent replay: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	// 3. digest 与内容不符 → 400。
	r = withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/"+digest, []byte("tampered")), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("digest mismatch: want 400, got %d body=%s", w.Code, w.Body.String())
	}

	// 4. digest 形状非法 → 400。
	r = withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/not-a-digest", content), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad digest: want 400, got %d body=%s", w.Code, w.Body.String())
	}

	// 5. 按 digest 下载 → 内容与头一致。
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/blobs/"+digest, nil), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("download: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.Bytes(); !bytes.Equal(got, content) {
		t.Errorf("download content mismatch: %q", got)
	}
	if got := w.Header().Get("X-Digest"); got != digest {
		t.Errorf("download X-Digest: want %q, got %q", digest, got)
	}

	// 6. 不存在的 digest（合法 hex）→ 404。
	missing := blobDigestOf([]byte("never uploaded anywhere"))
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/blobs/"+missing, nil), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing blob: want 404, got %d body=%s", w.Code, w.Body.String())
	}

	// 7. 用量端点。
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/blobs", nil), "user-1", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("usage: want 200, got %d", w.Code)
	}
	var usage struct {
		UsedBytes  int64 `json:"used_bytes"`
		QuotaBytes int64 `json:"quota_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.UsedBytes != int64(len(content)) || usage.QuotaBytes != 1<<20 {
		t.Errorf("usage malformed: %+v", usage)
	}

	// 8. 配额超限 → 507（基线上传恰好用尽配额后，第二个 distinct blob 被拒）。
	uniq := time.Now().Format(time.RFC3339Nano)
	wsQuota := "ws-quota-" + uniq
	fresh := []byte("quota baseline " + uniq)
	s2, _ := newPGMarketplaceTestServer(t, int64(len(fresh)))
	h2 := s2.handleMarketplaceRouter
	// 配额基线必须用"内容表里从未出现"的 blob + 全新 workspace：blob 行
	// 跨 workspace 全局去重（created 以内容行新建为准），归属行按
	// (workspace, digest) 计量，而测试 DSN 固定 schema、数据跨测试运行
	// 共享——复用 content/ws-a 都会撞上历史数据（幂等 200 或配额 507）。
	r = withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/"+blobDigestOf(fresh), fresh), "user-1", wsQuota)
	w = httptest.NewRecorder()
	h2(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("quota baseline upload: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	other := []byte("another distinct blob " + uniq)
	r = withClaims(newBlobRequest(t, http.MethodPut, "/api/marketplace/blobs/"+blobDigestOf(other), other), "user-1", wsQuota)
	w = httptest.NewRecorder()
	h2(w, r)
	if w.Code != http.StatusInsufficientStorage {
		t.Errorf("quota exceeded: want 507, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestMarketplace_SigningKeysPG 端到端验证 publisher key HTTP 生命周期
// （ADR §10：注册/列表/吊销从 store 级提升到 HTTP）：publisher 绑定 JWT
// userID；key_id 形状与保留字校验；吊销后列表可见 revoked。
func TestMarketplace_SigningKeysPG(t *testing.T) {
	s, _ := newPGMarketplaceTestServer(t, 0)
	h := s.handleMarketplaceRouter

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))

	// 1. 注册 → 201。
	r := withClaims(newRequest(t, http.MethodPost, "/api/marketplace/signing/keys", map[string]string{
		"key_id": "k1", "public_key": pubB64,
	}), "alice", "ws-a")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: want 201, got %d body=%s", w.Code, w.Body.String())
	}

	// 2. 重复注册同 key_id → 409。
	r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/signing/keys", map[string]string{
		"key_id": "k1", "public_key": pubB64,
	}), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate register: want 409, got %d body=%s", w.Code, w.Body.String())
	}

	// 3. key_id="root" 保留字 → 400；非法形状 → 400。
	for _, bad := range []string{"root", "bad/id", ""} {
		body := map[string]string{"key_id": bad, "public_key": pubB64}
		r = withClaims(newRequest(t, http.MethodPost, "/api/marketplace/signing/keys", body), "alice", "ws-a")
		w = httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("register key_id %q: want 400, got %d body=%s", bad, w.Code, w.Body.String())
		}
	}

	// 4. 列表 → 本人 1 把 active，公钥回传一致。
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/signing/keys", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}
	var listResp struct {
		Keys []marketplace.PublisherKey `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Keys) != 1 || listResp.Keys[0].KeyID != "k1" ||
		listResp.Keys[0].Status != "active" || listResp.Keys[0].PublicKey != pubB64 {
		t.Errorf("list malformed: %+v", listResp.Keys)
	}
	if listResp.Keys[0].PublisherID != "alice" {
		t.Errorf("publisher must bind to JWT userID: %+v", listResp.Keys[0])
	}

	// 5. 其他用户的列表隔离。
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/signing/keys", nil), "bob", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	var bobResp struct {
		Keys []marketplace.PublisherKey `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bobResp); err != nil {
		t.Fatal(err)
	}
	if len(bobResp.Keys) != 0 {
		t.Errorf("bob must see no keys: %+v", bobResp.Keys)
	}

	// 6. 吊销 → 200；重复吊销 → 404；吊销后列表 status=revoked。
	r = withClaims(httptest.NewRequest(http.MethodDelete, "/api/marketplace/signing/keys/k1", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	r = withClaims(httptest.NewRequest(http.MethodDelete, "/api/marketplace/signing/keys/k1", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("double revoke: want 404, got %d body=%s", w.Code, w.Body.String())
	}
	r = withClaims(httptest.NewRequest(http.MethodGet, "/api/marketplace/signing/keys", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Keys) != 1 || listResp.Keys[0].Status != "revoked" || listResp.Keys[0].RevokedAt == nil {
		t.Errorf("key not marked revoked after DELETE: %+v", listResp.Keys)
	}

	// 7. 非法 key_id 的 DELETE → 400。
	r = withClaims(httptest.NewRequest(http.MethodDelete, "/api/marketplace/signing/keys/bad/id", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	// "bad/id" 占两段，路由层就不会到 DELETE 分支 → 404；换成非法单段验证 400。
	if w.Code != http.StatusNotFound {
		t.Errorf("deep key path: want 404, got %d", w.Code)
	}
	r = withClaims(httptest.NewRequest(http.MethodDelete, "/api/marketplace/signing/keys/%E4%B8%AD", nil), "alice", "ws-a")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-ascii key_id: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}
