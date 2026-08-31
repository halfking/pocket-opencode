package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// sharedTestDSN 单测 + 集成测试使用同一 DSN；CI 上若 DSN 未配置则跳过。
var sharedTestDSN atomic.Pointer[string]

func testDSN() string {
	if v := sharedTestDSN.Load(); v != nil {
		return *v
	}
	dsn := os.Getenv("POCKET_TEST_PG_DSN")
	if dsn == "" {
		dsn = "postgres://llm_gateway:4Q92cFTaYY8Z3AO07XTBBH-1g7kceaxg@localhost:5432/llm_gateway?sslmode=disable&search_path=redclaw_test_2026_09_01"
	}
	sharedTestDSN.Store(&dsn)
	return dsn
}

// mustTestPool 为 C8 集成测试创建独立 schema + pgx pool。
func mustTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testDSN()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("skip: parse pgx config: %v", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("skip: pgx connect: %v", err)
	}
	if err := auth.EnsureSchema(context.Background(), pool); err != nil {
		pool.Close()
		t.Fatalf("EnsureSchema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newExtendedAuthTestServer 构造带 userStore/codeStore/smtp/redclaw 的 Server。
// redclawAuth 在集成测试中始终 nil（fail-soft 路径覆盖由单元测试负责）。
func newExtendedAuthTestServer(t *testing.T, pool *pgxpool.Pool) *Server {
	t.Helper()
	cfg := config.Load()
	cfg.JWTSecret = "test-secret-for-extended-auth-0123456789"
	cfg.SMTPDebugEcho = true
	cfg.DevAuth = false

	signer, err := auth.NewSigner(cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	us, err := auth.NewUserStore(pool)
	if err != nil {
		t.Fatalf("NewUserStore: %v", err)
	}
	cs, err := auth.NewCodeStore(pool)
	if err != nil {
		t.Fatalf("NewCodeStore: %v", err)
	}

	// 清空测试数据（保证幂等）
	if _, err := pool.Exec(context.Background(), `TRUNCATE TABLE users CASCADE`); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM email_verification_codes`); err != nil {
		t.Fatalf("delete codes: %v", err)
	}

	// 用 nil SMTP（不实际发送邮件；debug 模式回显 code）
	srv := New(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, us, signer, nil, nil, nil, nil, "", pool)
	srv.SetAuthExt(cs, nil, nil)
	return srv
}

func postJSON(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, out any) {
	t.Helper()
	body, _ := io.ReadAll(rr.Body)
	if len(body) == 0 {
		return
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("unmarshal status=%d: %v (body: %s)", rr.Code, err, string(body))
	}
}

// ============================================================================
// C8 联调证据：9 条 curl 测试矩阵映射为 9 个集成测试
// ============================================================================

const testEmail = "alice+redclaw@example.com"
const testUsername = "alice_rc"
const testPassword = "Test1234Pass"

func TestC8_1_SendCode_HappyPath(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"register"}`, testEmail))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK        bool   `json:"ok"`
		TTLSec    int    `json:"ttl_sec"`
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &resp)
	if !resp.OK {
		t.Errorf("expected ok=true, got %+v", resp)
	}
	if resp.TTLSec <= 0 {
		t.Errorf("expected ttl_sec>0, got %d", resp.TTLSec)
	}
	if resp.DebugCode == "" {
		t.Error("expected debug_code when smtp not configured + debug=true")
	}
}

func TestC8_2_SendCode_RateLimited(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	// 第一次成功
	rr := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"register"}`, testEmail))
	if rr.Code != http.StatusOK {
		t.Fatalf("first send expected 200, got %d", rr.Code)
	}
	// 第二次 60s 内同邮箱应 429
	rr2 := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"register"}`, testEmail))
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestC8_3_Register_BadCode(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":%q,"code":"000000","username":%q,"password":%q}`,
		testEmail, testUsername, testPassword,
	))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (bad code), got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestC8_4_Register_Success(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"register"}`, testEmail))
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)
	if sent.DebugCode == "" {
		t.Fatal("missing debug_code from send-code")
	}
	rr2 := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":%q,"code":%q,"username":%q,"password":%q}`,
		testEmail, sent.DebugCode, testUsername, testPassword,
	))
	if rr2.Code != http.StatusOK {
		t.Fatalf("register expected 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	var reg struct {
		Token       string `json:"token"`
		User        string `json:"user"`
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	decodeJSON(t, rr2, &reg)
	if reg.Token == "" {
		t.Error("expected token in response")
	}
	if reg.UserID == "" || reg.WorkspaceID == "" {
		t.Errorf("expected user_id + workspace_id, got %+v", reg)
	}
}

func TestC8_5_CodeLogin_Success(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rrReg := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"register"}`, testEmail))
	if rrReg.Code != http.StatusOK {
		t.Fatalf("send-code register: %d %s", rrReg.Code, rrReg.Body.String())
	}
	var regSent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rrReg, &regSent)
	if rr := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":%q,"code":%q,"username":%q,"password":%q}`,
		testEmail, regSent.DebugCode, testUsername+"_login", testPassword,
	)); rr.Code != http.StatusOK {
		t.Fatalf("register setup: %d %s", rr.Code, rr.Body.String())
	}

	rr := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"login"}`, testEmail))
	if rr.Code != http.StatusOK {
		t.Fatalf("send login code: %d %s", rr.Code, rr.Body.String())
	}
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)

	rrLogin := postJSON(t, srv, "/api/auth/code-login", fmt.Sprintf(
		`{"email":%q,"code":%q}`, testEmail, sent.DebugCode,
	))
	if rrLogin.Code != http.StatusOK {
		t.Fatalf("code-login expected 200, got %d body=%s", rrLogin.Code, rrLogin.Body.String())
	}
	var login struct {
		Token       string `json:"token"`
		User        string `json:"user"`
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	decodeJSON(t, rrLogin, &login)
	if login.Token == "" {
		t.Error("expected token from code-login")
	}
}

func TestC8_6_ForgotPassword_Success(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		fmt.Sprintf(`{"email":%q,"purpose":"reset"}`, testEmail))
	if rr.Code != http.StatusOK {
		t.Fatalf("send-code reset: %d %s", rr.Code, rr.Body.String())
	}
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)
	rrFp := postJSON(t, srv, "/api/auth/forgot-password", fmt.Sprintf(
		`{"email":%q,"code":%q,"new_password":%q}`,
		testEmail, sent.DebugCode, "NewPass1234",
	))
	if rrFp.Code != http.StatusOK {
		t.Fatalf("forgot-password expected 200, got %d body=%s", rrFp.Code, rrFp.Body.String())
	}
	var fp struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, rrFp, &fp)
	if !fp.OK {
		t.Errorf("expected ok=true, got %+v", fp)
	}
}

func TestC8_7_OldPasswordLogin_Regression(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		`{"email":"regression@example.com","purpose":"register"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("send-code regression: %d %s", rr.Code, rr.Body.String())
	}
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)
	rrReg := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":"regression@example.com","code":%q,"username":"regr_user","password":%q}`,
		sent.DebugCode, testPassword,
	))
	if rrReg.Code != http.StatusOK {
		t.Fatalf("register regression: %d %s", rrReg.Code, rrReg.Body.String())
	}
	rrLogin := postJSON(t, srv, "/api/auth/login", fmt.Sprintf(
		`{"username":"regr_user","password":%q}`, testPassword,
	))
	if rrLogin.Code != http.StatusOK {
		t.Fatalf("old password login expected 200, got %d body=%s", rrLogin.Code, rrLogin.Body.String())
	}
	var login struct {
		Token string `json:"token"`
		User  string `json:"user"`
	}
	decodeJSON(t, rrLogin, &login)
	if login.Token == "" {
		t.Error("expected token from legacy login")
	}
	if login.User != "regr_user" {
		t.Errorf("unexpected user: %q", login.User)
	}
}

func TestC8_8_Register_RejectsBadUsername(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		`{"email":"baduser@example.com","purpose":"register"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("send-code: %d %s", rr.Code, rr.Body.String())
	}
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)
	rrReg := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":"baduser@example.com","code":%q,"username":"x","password":%q}`,
		sent.DebugCode, testPassword,
	))
	if rrReg.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for short username, got %d body=%s", rrReg.Code, rrReg.Body.String())
	}
}

func TestC8_9_Register_RejectsWeakPassword(t *testing.T) {
	pool := mustTestPool(t)
	srv := newExtendedAuthTestServer(t, pool)

	rr := postJSON(t, srv, "/api/auth/send-code",
		`{"email":"weakpw@example.com","purpose":"register"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("send-code: %d %s", rr.Code, rr.Body.String())
	}
	var sent struct {
		DebugCode string `json:"debug_code"`
	}
	decodeJSON(t, rr, &sent)
	rrReg := postJSON(t, srv, "/api/auth/register", fmt.Sprintf(
		`{"email":"weakpw@example.com","code":%q,"username":"weak_pw_user","password":"short"}`,
		sent.DebugCode,
	))
	if rrReg.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for weak password, got %d body=%s", rrReg.Code, rrReg.Body.String())
	}
}
