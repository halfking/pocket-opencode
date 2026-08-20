package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/vault"
)

// fakeVaultStore 是 vaultSyncStorer 的测试实现，全部走内存。
type fakeVaultStore struct {
	mu       sync.Mutex
	rows     map[string]map[int]string // workspaceID+"|"+userID -> version -> ciphertext
	current  map[string]int            // workspaceID+"|"+userID -> current version
	versions map[string][]vault.Version
}

func newFakeVaultStore() *fakeVaultStore {
	return &fakeVaultStore{
		rows:     map[string]map[int]string{},
		current:  map[string]int{},
		versions: map[string][]vault.Version{},
	}
}

func (f *fakeVaultStore) key(workspaceID, userID string) string {
	return workspaceID + "|" + userID
}

func (f *fakeVaultStore) PutLatest(_ context.Context, workspaceID, userID, ciphertext string, version int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(workspaceID, userID)
	if f.rows[k] == nil {
		f.rows[k] = map[int]string{}
	}
	for v := range f.rows[k] {
		_ = v
	}
	f.rows[k][version] = ciphertext
	f.current[k] = version
	f.versions[k] = append(f.versions[k], vault.Version{Version: version, IsCurrent: true})
	return nil
}

func (f *fakeVaultStore) GetLatest(_ context.Context, workspaceID, userID string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(workspaceID, userID)
	v, ok := f.current[k]
	if !ok {
		return "", 0, errVaultEmpty
	}
	return f.rows[k][v], v, nil
}

func (f *fakeVaultStore) GetByVersion(_ context.Context, workspaceID, userID string, version int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(workspaceID, userID)
	blob, ok := f.rows[k][version]
	if !ok {
		return "", errVaultEmpty
	}
	return blob, nil
}

func (f *fakeVaultStore) MarkCurrent(_ context.Context, workspaceID, userID string, version int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(workspaceID, userID)
	if _, ok := f.rows[k][version]; !ok {
		return errVaultEmpty
	}
	f.current[k] = version
	return nil
}

func (f *fakeVaultStore) ListVersions(_ context.Context, workspaceID, userID string) ([]vault.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versions[f.key(workspaceID, userID)], nil
}

var errVaultEmpty = &vaultEmptyError{}

type vaultEmptyError struct{}

func (*vaultEmptyError) Error() string { return "vault empty (fake)" }

// newVaultAuditServer 构造一个带 fake vault store + audit store 的最小
// Server。复用 mobileRouteServer 的 signer / registry 风格但只装配
// vault + audit 所需字段。
func newVaultAuditServer(t *testing.T) (*Server, *fakeVaultStore, string) {
	t.Helper()
	signer, err := auth.NewSigner("vault-audit-test-secret-0123456789ab", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.SignWithWorkspace("vault-user", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	vs := newFakeVaultStore()
	cfg := config.Load()
	srv := newServer(cfg, adapter.NewStaticNPSAdapter(), nil, nil,
		registry.NewRegistry(), nil, nil, nil, vs, nil, nil, nil, nil,
		nil, nil, nil, signer, nil, nil, nil, nil, "", false, nil)
	srv.auditStore = redclaw.NewAuditStore()
	return srv, vs, token
}

func TestVaultSyncUpload_WritesAudit_WithoutLeakingBlob(t *testing.T) {
	srv, _, token := newVaultAuditServer(t)
	h := srv.Handler()

	// 故意让 blob 内容里包含敏感字符串，验证 detail 不会泄露任何字节。
	const secretBlob = "BEGIN_VAULT_BLOB access_token=xyzzy-blob-payload END_VAULT_BLOB"
	body := `{"blob":"` + secretBlob + `","version":1}`
	req := mobileRequest(http.MethodPost, "/api/vault/sync/", token, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", rr.Code, rr.Body.String())
	}

	// 找到 vault.sync.upload 事件。
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	var found *redclaw.AuditEntry
	for _, e := range entries {
		if e.Action == "vault.sync.upload" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatalf("expected vault.sync.upload audit entry, got: %+v", entries)
	}
	if found.Resource != "vault:vault-user" {
		t.Fatalf("unexpected resource: %q", found.Resource)
	}
	if !strings.Contains(found.Detail, "version=1") {
		t.Fatalf("expected version in detail, got %q", found.Detail)
	}
	if !strings.Contains(found.Detail, "bytes=") {
		t.Fatalf("expected bytes in detail, got %q", found.Detail)
	}
	// 关键：blob 整段内容必须不出现在 detail 里。
	if strings.Contains(found.Detail, "BEGIN_VAULT_BLOB") ||
		strings.Contains(found.Detail, "END_VAULT_BLOB") ||
		strings.Contains(found.Detail, "xyzzy-blob-payload") {
		t.Fatalf("detail leaked blob content: %q", found.Detail)
	}
}

func TestVaultSyncRestore_WritesAudit_WithVersion(t *testing.T) {
	srv, vs, token := newVaultAuditServer(t)
	h := srv.Handler()
	// 准备两个版本。
	if err := vs.PutLatest(context.Background(), "ws-a", "vault-user", "blob-v1", 1); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutLatest(context.Background(), "ws-a", "vault-user", "blob-v2", 2); err != nil {
		t.Fatal(err)
	}
	// 清空由 PutLatest 自动写的 audit，只关心 restore 自身产生的事件。
	srv.auditStore = redclaw.NewAuditStore()

	req := mobileRequest(http.MethodPost, "/api/vault/sync/1/restore", token, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", rr.Code, rr.Body.String())
	}

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	var found *redclaw.AuditEntry
	for _, e := range entries {
		if e.Action == "vault.sync.restore" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatalf("expected vault.sync.restore audit entry, got: %+v", entries)
	}
	if !strings.Contains(found.Detail, "version=1") {
		t.Fatalf("expected version=1 in detail, got %q", found.Detail)
	}
}

// 跨 workspace：ws-b 用户绝对看不到 ws-a 的 vault，并且 restore 操作
// 落在 ws-a 的 audit 集合里。
func TestVaultSyncRestore_TenantScoped(t *testing.T) {
	srv, vs, tokenA := newVaultAuditServer(t)
	signer, _ := auth.NewSigner("vault-audit-test-secret-0123456789ab", time.Hour)
	tokenB, _ := signer.SignWithWorkspace("other-user", "admin", "ws-b")
	_ = vs.PutLatest(context.Background(), "ws-a", "vault-user", "blob-v1", 1)

	h := srv.Handler()
	// ws-b 用户对 ws-a 的 vault 不可见。
	req := mobileRequest(http.MethodPost, "/api/vault/sync/1/restore", tokenB, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-workspace restore, got %d %s", rr.Code, rr.Body.String())
	}

	// 之后由 ws-a admin 触发一次成功 restore，确认事件 TenantID=ws-a。
	req2 := mobileRequest(http.MethodPost, "/api/vault/sync/1/restore", tokenA, "")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("ws-a restore failed: %d", rr2.Code)
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	if len(entries) == 0 {
		t.Fatalf("expected at least one ws-a audit entry")
	}
	// ws-b 查询应为空（restore 事件只在 ws-a）。
	if got, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-b"}); len(got) != 0 {
		t.Fatalf("ws-b must not see ws-a restore events: %+v", got)
	}
}

// 防止 JSON 体里藏 access_token 字段被原样写进 detail：上传时的 body
// 由 vault handler 解析成 {Blob,Version}，detail 只应包含 version + bytes。
func TestVaultSyncUpload_DoesNotEchoRequestBodyFields(t *testing.T) {
	srv, _, token := newVaultAuditServer(t)
	h := srv.Handler()

	body := `{"blob":"abc","version":7,"access_token":"leak-me","api_key":"also-leak"}`
	req := mobileRequest(http.MethodPost, "/api/vault/sync/", token, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", rr.Code, rr.Body.String())
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	for _, e := range entries {
		if e.Action != "vault.sync.upload" {
			continue
		}
		if strings.Contains(e.Detail, "leak-me") || strings.Contains(e.Detail, "also-leak") {
			t.Fatalf("detail leaked unknown body field: %q", e.Detail)
		}
	}
}
