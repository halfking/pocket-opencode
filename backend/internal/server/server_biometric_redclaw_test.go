package server

// server_biometric_redclaw_test.go — 覆盖生物识别登录与 RedClaw 用户验证的集成分支。
//
// 重点验证 handleBiometricLoginFinish 在 webAuthnVerifier 已配置时，
// 会先走 WebAuthn 签名验证，再走 RedClaw 用户验证，且：
//   - RedClaw 用户无效 → 拒绝登录（401，fail-closed）
//   - RedClaw 未配置 → 跳过验证，直接签发 JWT（开发降级）
//
// 真实 WebAuthn assertion 需要浏览器 + 私钥签名，这里用 fake verifier
// （实现 WebAuthnVerifierIface）绕过签名验证，把测试聚焦在 RedClaw 集成分支。
// RedClaw 侧用 mock HTTP server 模拟真实 API 响应。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// fakeVerifier 绕过真实 WebAuthn 签名验证，直接返回存储的 counter=0。
type fakeVerifier struct{}

func (f *fakeVerifier) BeginRegistration(ctx context.Context, userID, displayName string) (string, *protocol.CredentialCreation, error) {
	return "challenge", nil, nil
}
func (f *fakeVerifier) FinishRegistration(ctx context.Context, challengeB64 string, ccr *protocol.ParsedCredentialCreationData) (string, []byte, uint32, error) {
	return "cred-id", []byte("pubkey"), 0, nil
}
func (f *fakeVerifier) BeginLogin(ctx context.Context) (string, *protocol.CredentialAssertion, error) {
	return "challenge", nil, nil
}
func (f *fakeVerifier) FinishLogin(ctx context.Context, challengeB64, credentialID string, pk []byte, counter uint32, par *protocol.ParsedCredentialAssertionData) (uint32, error) {
	return 0, nil
}

// newRedClawMockServer 启动一个 mock RedClaw HTTP server，/api/v1/users/verify 返回指定响应。
func newRedClawMockServer(t *testing.T, status int, resp redclaw.VerifyUserResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/verify" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(resp)
	}))
}

func newTestServerWithBiometric() *Server {
	signer, _ := auth.NewSigner("test-secret-key-minimum-32-bytes-long", 1*time.Hour)
	store := auth.NewBiometricStore(nil)
	return &Server{
		biometricStore:   store,
		jwtSigner:        signer,
		webAuthnVerifier: &fakeVerifier{},
		parseAssertionFn: func(raw json.RawMessage) (*protocol.ParsedCredentialAssertionData, error) {
			// 测试 seam：跳过协议层解析（避免构造合法 CBOR authenticatorData）
			return &protocol.ParsedCredentialAssertionData{}, nil
		},
	}
}

// 辅助：注入一个指向 mock server 的 redclawBridge。
func attachMockRedClaw(t *testing.T, srv *Server, status int, resp redclaw.VerifyUserResponse) func() {
	t.Helper()
	mockSrv := newRedClawMockServer(t, status, resp)
	client, err := redclaw.NewClient(redclaw.ClientConfig{
		BaseURL:    mockSrv.URL,
		Secret:     "test-secret",
		TenantID:   "tenant-1",
		TimeoutSec: 5,
	})
	if err != nil {
		mockSrv.Close()
		t.Fatalf("NewClient: %v", err)
	}
	bridge := redclaw.NewBridge(client, nil)
	bridge.Start()
	srv.redclawBridge = bridge
	return func() {
		bridge.Stop()
		mockSrv.Close()
	}
}

func TestBiometricLoginFinishRedClawInvalidUser(t *testing.T) {
	srv := newTestServerWithBiometric()
	cleanup := attachMockRedClaw(t, srv, http.StatusOK, redclaw.VerifyUserResponse{Valid: false})
	defer cleanup()

	srv.biometricStore.Register(context.Background(), &auth.BiometricCredential{
		ID:          "cred-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		PublicKey:   []byte("pubkey"),
		Counter:     0,
	})

	body := `{"challenge":"challenge","credential_id":"cred-1","assertion_raw":{"id":"AA","rawId":"AA","type":"public-key","response":{"authenticatorData":"AA","signature":"AA","clientDataJSON":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0IiwiY2hhbGxlbmdlIjoiQUEiLCJvcmlnaW4iOiJodHRwOi8vbG9jYWxob3N0IiwiY3Jvc3NPcmlnaW4iOmZhbHNlfQ"}}}`
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginFinish(rec, bioReq(http.MethodPost, "/api/auth/biometric/login/finish", body))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when RedClaw says user invalid, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestBiometricLoginFinishRedClawValidUser(t *testing.T) {
	srv := newTestServerWithBiometric()
	resp := redclaw.VerifyUserResponse{
		Valid:    true,
		UserInfo: &redclaw.UserInfo{UserID: "user-3", Roles: []string{"admin"}},
	}
	cleanup := attachMockRedClaw(t, srv, http.StatusOK, resp)
	defer cleanup()

	srv.biometricStore.Register(context.Background(), &auth.BiometricCredential{
		ID:          "cred-3",
		UserID:      "user-3",
		WorkspaceID: "ws-3",
		PublicKey:   []byte("pubkey"),
		Counter:     0,
	})

	body := `{"challenge":"challenge","credential_id":"cred-3","assertion_raw":{"id":"AA","rawId":"AA","type":"public-key","response":{"authenticatorData":"AA","signature":"AA","clientDataJSON":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0IiwiY2hhbGxlbmdlIjoiQUEiLCJvcmlnaW4iOiJodHRwOi8vbG9jYWxob3N0IiwiY3Jvc3NPcmlnaW4iOmZhbHNlfQ"}}}`
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginFinish(rec, bioReq(http.MethodPost, "/api/auth/biometric/login/finish", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var resp2 struct {
		Token     string `json:"token"`
		UserID    string `json:"user_id"`
		Workspace string `json:"workspace_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.Token == "" {
		t.Fatal("expected JWT token in response")
	}
	if resp2.UserID != "user-3" {
		t.Fatalf("expected user-3, got %s", resp2.UserID)
	}
}

func TestBiometricLoginFinishNoRedClawDegradesToLocal(t *testing.T) {
	// redclawBridge == nil → 跳过 RedClaw 验证，直接签发 JWT（开发降级）
	srv := newTestServerWithBiometric()
	srv.redclawBridge = nil

	srv.biometricStore.Register(context.Background(), &auth.BiometricCredential{
		ID:          "cred-4",
		UserID:      "user-4",
		WorkspaceID: "ws-4",
		PublicKey:   []byte("pubkey"),
		Counter:     0,
	})

	body := `{"challenge":"challenge","credential_id":"cred-4","assertion_raw":{"id":"AA","rawId":"AA","type":"public-key","response":{"authenticatorData":"AA","signature":"AA","clientDataJSON":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0IiwiY2hhbGxlbmdlIjoiQUEiLCJvcmlnaW4iOiJodHRwOi8vbG9jYWxob3N0IiwiY3Jvc3NPcmlnaW4iOmZhbHNlfQ"}}}`
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginFinish(rec, bioReq(http.MethodPost, "/api/auth/biometric/login/finish", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (local degrade), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if len(rec.Body.String()) == 0 {
		t.Fatal("expected non-empty body")
	}
}
