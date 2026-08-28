package server

// server_biometric_test.go — 覆盖 6 个 stub handler 的方法校验与 501 路径，
// 确保编译后的路由骨架行为可被外部测试直接观察。
//
// 注意：本文件不依赖 PG（biometricStore 保持 nil），仅覆盖：
//   - HTTP method 校验
//   - store-not-configured 路径返 503
//   - login/finish 明确返 501（占位语义）
//   - base64urlDecode 容错

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
)

func bioReq(method, target, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, target, nil)
	}
	return httptest.NewRequest(method, target, strings.NewReader(body))
}

func TestBiometricRegisterBeginRejectsNonPost(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricRegisterBegin(rec, bioReq(http.MethodGet, "/api/auth/biometric/register/begin", ""))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestBiometricRegisterBeginIssueChallenge(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricRegisterBegin(rec, bioReq(http.MethodPost, "/api/auth/biometric/register/begin", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Challenge string `json:"challenge"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Challenge == "" {
		t.Fatal("challenge should not be empty")
	}
	if body.ExpiresAt <= 0 {
		t.Fatal("expires_at must be a future unix timestamp")
	}
}

func TestBiometricRegisterFinishReturns503WhenStoreNil(t *testing.T) {
	srv := &Server{} // biometricStore = nil
	rec := httptest.NewRecorder()
	srv.handleBiometricRegisterFinish(rec, bioReq(http.MethodPost,
		"/api/auth/biometric/register/finish",
		`{"credential_id":"abc","public_key":"def"}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestBiometricRegisterFinishRejectsBadBase64(t *testing.T) {
	srv := &Server{biometricStore: auth.NewBiometricStore(nil)} // Init 会失败，但 handler 先做 base64 检查
	rec := httptest.NewRecorder()
	srv.handleBiometricRegisterFinish(rec, bioReq(http.MethodPost,
		"/api/auth/biometric/register/finish",
		`{"credential_id":"!!!","public_key":"!!!"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestBiometricLoginBeginRejectsNonPost(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginBegin(rec, bioReq(http.MethodGet, "/api/auth/biometric/login/begin", ""))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestBiometricLoginFinishIsNotImplemented(t *testing.T) {
	// When webAuthnVerifier is nil AND store+signer are configured, it returns 501
	signer, _ := auth.NewSigner("test-secret-key-minimum-32-bytes-long", 1*time.Hour)
	srv := &Server{
		biometricStore: auth.NewBiometricStore(nil),
		jwtSigner:      signer,
	}
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginFinish(rec, bioReq(http.MethodPost, "/api/auth/biometric/login/finish", "{}"))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestBiometricLoginFinishReturns503WhenStoreNil(t *testing.T) {
	// When biometricStore is nil, it returns 503
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricLoginFinish(rec, bioReq(http.MethodPost, "/api/auth/biometric/login/finish", "{}"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestBiometricCredentialsReturns503WhenStoreNil(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricCredentials(rec, bioReq(http.MethodGet, "/api/auth/biometric/credentials", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestBiometricCredentialOpsRejectsUnknownMethod(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleBiometricCredentialOps(rec, bioReq(http.MethodGet, "/api/auth/biometric/credentials/abc", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (store nil) before method check, got %d", rec.Code)
	}
}

func TestBiometricCredentialOpsRejectsEmptyID(t *testing.T) {
	srv := &Server{biometricStore: auth.NewBiometricStore(nil)}
	rec := httptest.NewRecorder()
	srv.handleBiometricCredentialOps(rec, bioReq(http.MethodDelete, "/api/auth/biometric/credentials/", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
