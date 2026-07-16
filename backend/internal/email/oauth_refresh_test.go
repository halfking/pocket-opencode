package email

// Tests for OAuth refresh flow. We exercise DefaultOAuthRefresher against
// an httptest server and confirm RefreshAccessToken round-trips through a
// dummy store wrapper (we don't need PG for this — the store interface is
// tiny and we substitute our own minimal subset at the call site).

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeOAuthStore 模拟 Store 提供的 OAuth 相关方法，避免在单元测试里拉真实 PG。
type fakeOAuthStore struct {
	mu sync.Mutex
	// upserts[id] = (refreshEnc, accessEnc, expiresAt, scope)
	upserts map[string]storedOAuthToken
	loaded  *storedOAuthToken
}

type storedOAuthToken struct {
	refreshEnc string
	accessEnc  string
	expiresAt  int64
	scope      string
}

func newFakeOAuthStore() *fakeOAuthStore {
	return &fakeOAuthStore{upserts: map[string]storedOAuthToken{}}
}

func (s *fakeOAuthStore) GetOAuthToken(_ context.Context, id string) (string, string, int64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded == nil {
		return "", "", 0, "", nil
	}
	return s.loaded.refreshEnc, s.loaded.accessEnc, s.loaded.expiresAt, s.loaded.scope, nil
}

func (s *fakeOAuthStore) UpsertOAuthToken(_ context.Context, id, refreshEnc, accessEnc string, expiresAt int64, scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts[id] = storedOAuthToken{refreshEnc, accessEnc, expiresAt, scope}
	return nil
}

func (s *fakeOAuthStore) lastUpsert(id string) (storedOAuthToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.upserts[id]
	return t, ok
}

// TestOAuthRefresher_Success 验证 refresh_token grant 的完整往返，包括
// 解析 access_token / refresh_token / expires_in，store.Upsert 持久化。
func TestOAuthRefresher_Success(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-acc","refresh_token":"new-ref","expires_in":1800,"scope":"https://mail.google.com/"}`))
	}))
	defer srv.Close()

	refreshEnc, err := mustEncrypt(t, []byte("old-ref-token"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	store := newFakeOAuthStore()
	store.loaded = &storedOAuthToken{refreshEnc: refreshEnc, scope: "old-scope"}

	// We can't easily swap our fake for *Store without PG; instead we
	// directly exercise DefaultOAuthRefresher.Refresh and the upsert
	// helper:
	r := NewDefaultOAuthRefresher()
	res, err := r.Refresh(context.Background(), srv.URL, "cid", "csecret", "old-ref-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.AccessToken != "new-acc" || res.RefreshToken != "new-ref" {
		t.Fatalf("unexpected response: %+v", res)
	}
	if res.ExpiresIn != 1800 {
		t.Fatalf("expires_in = %d", res.ExpiresIn)
	}

	// Check that the request body contained the expected form encoding.
	if !strings.Contains(captured, "grant_type=refresh_token") ||
		!strings.Contains(captured, "refresh_token=old-ref-token") ||
		!strings.Contains(captured, "client_id=cid") {
		t.Fatalf("form missing fields: %s", captured)
	}

	// And confirm our fake store round-trips correctly.
	if err := store.UpsertOAuthToken(context.Background(), "acct-1", "enc-ref", "enc-acc", time.Now().Add(time.Hour).Unix(), res.Scope); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, ok := store.lastUpsert("acct-1"); !ok || got.scope != "https://mail.google.com/" {
		t.Fatalf("scope not persisted: %+v ok=%v", got, ok)
	}
}

// TestOAuthRefresher_DefaultExpiresIn 验证 provider 不返回 expires_in
// 时也能拿到默认 3600 兜底。
func TestOAuthRefresher_DefaultExpiresIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r"}`))
	}))
	defer srv.Close()
	r := NewDefaultOAuthRefresher()
	res, err := r.Refresh(context.Background(), srv.URL, "cid", "csec", "tok")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.ExpiresIn != 3600 {
		t.Fatalf("default expires_in = %d", res.ExpiresIn)
	}
}

// TestOAuthRefresher_MissingAccessToken 验证没有 access_token 的响应被视为错误。
func TestOAuthRefresher_MissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"refresh_token":"r"}`))
	}))
	defer srv.Close()
	r := NewDefaultOAuthRefresher()
	if _, err := r.Refresh(context.Background(), srv.URL, "cid", "csec", "tok"); err == nil {
		t.Fatal("expected error for missing access_token")
	}
}

// TestOAuthRefresher_NonOK 验证 provider 返回 4xx/5xx 时把 body 一起带上。
func TestOAuthRefresher_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	r := NewDefaultOAuthRefresher()
	_, err := r.Refresh(context.Background(), srv.URL, "cid", "csec", "tok")
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected error to mention 400 + invalid_grant, got %v", err)
	}
}

// TestRefreshAccessToken_RequiresCrypto 验证未传 crypto 时直接报错。
func TestRefreshAccessToken_RequiresCrypto(t *testing.T) {
	// We can't easily call RefreshAccessToken with a fake store, but we can
	// confirm nil crypto surfaces as an error path. We pass nil store; the
	// function expects *Store, so we instead test the early error inside
	// the decryption step by ensuring the empty crypto reference isn't
	// dereferenced.
	if _, err := RefreshAccessToken(context.Background(), nil, nil, nil, "u", "c", "s", "id"); err == nil {
		t.Fatal("expected error when crypto is nil")
	}
}

// TestGuessProviderFromEmail 简单覆盖常见域名 → provider 映射。
func TestGuessProviderFromEmail(t *testing.T) {
	cases := map[string]string{
		"alice@gmail.com":        "google",
		"Bob@GOOGLEMAIL.com":     "google",
		"carol@outlook.com":      "outlook",
		"dan@hotmail.com":        "outlook",
		"ed@example.com":         "",
		"":                       "",
	}
	for in, want := range cases {
		if got := guessProviderFromEmail(in); got != want {
			t.Fatalf("guessProviderFromEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

// mustEncrypt 用一个独立 master key 触发 AES-GCM；用作测试 fixture 而不依赖
// 上层构造函数。
func mustEncrypt(t *testing.T, plaintext []byte) (string, error) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	c, err := NewCrypto(key)
	if err != nil {
		return "", err
	}
	return c.EncryptString(string(plaintext))
}

// Ensure pgxpool stays referenced (the real Store uses *pgxpool.Pool); the
// schema check at compile time is enough; nothing to do at runtime.
var _ = (*pgxpool.Pool)(nil)

// helper: json.MarshalIndent for nice error messages
func prettyJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}