package email

// scheduler_refresh_test.go — `Scheduler.refreshOnce` 的 OAuth 刷新语义。
//
// ## 为什么测它
//
// §7bw 的覆盖率盘点里 `scheduler.go:319 refreshOnce` 是 0%，当时我把它标成
// 「需求 1 的开机重排判定」——**那是错的**（见 §7ca）。开机重排在 Android
// Java 层（`cc6753d` 的 `shouldReschedule`，JUnit 5 个用例 + 负控已做）。
// `refreshOnce` 实际管的是 **OAuth token 定时刷新**。
//
// 它的安全语义值得钉住，因为 `oauth_refresh.go` 里
// `classifyRefreshStatus` 的注释直接写明了赌注：
//
//	5xx and 429 are transient. Anything else falls into the transient bucket so
//	the scheduler will retry on the next tick instead of nuking the account.
//
// 「nuking the account」= `RevokeOAuthTokenScoped`：`DELETE FROM
// email_oauth_tokens` + `UPDATE email_accounts SET auth_type='password',
// enabled=FALSE`（store.go:2139-2143）。也就是说撤销会让**这个账户从此不再收信**，
// 用户必须重新走一次 OAuth 授权。
//
// 所以这组用例的核心命题只有一条：**临时失败绝不能撤销账户**。
// 分类器把「未知情况」归到 transient 是刻意的保守设计，
// 这条断言就是它的守门人。

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRefresher 实现 OAuthRefresher，计数并返回预设结果。
type fakeRefresher struct {
	calls    atomic.Int32
	gotToken atomic.Value // string：最后一次收到的 refresh_token
	err      error
	access   string
	scope    string
}

func (f *fakeRefresher) Refresh(_ context.Context, _, _, _, refreshToken string) (*OAuthRefreshResult, error) {
	f.calls.Add(1)
	f.gotToken.Store(refreshToken)
	if f.err != nil {
		return nil, f.err
	}
	return &OAuthRefreshResult{AccessToken: f.access, ExpiresIn: 3600, Scope: f.scope}, nil
}

// newRefreshFixture 造一个「已过期 + 已配好 provider」的 OAuth 账户。
// 返回 store / crypto / 账户 ID。
func newRefreshFixture(t *testing.T, authType string) (*Store, *Crypto, string) {
	t.Helper()
	store, cleanup := newWorkspaceTestStore(t)
	t.Cleanup(cleanup)

	crypto, err := NewCrypto([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	const (
		acctID = "acct-oauth-1"
		userID = "u-refresh"
		wsID   = "ws-refresh"
	)
	// 复用本包 seedAccount（已钉 search_path + cleanup DROP 自己的 schema），
	// 它把 auth_type 固定成 password；OAuth 分支需要 oauth2，所以显式改一次。
	seedAccount(t, store, acctID, userID, wsID)
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE email_accounts SET auth_type=$1 WHERE id=$2`, authType, acctID); err != nil {
		t.Fatalf("set auth_type: %v", err)
	}

	refreshEnc, err := crypto.EncryptString("refresh-token-value")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	// expires_at 已是过去：ListExpiredOAuthTokens 的条件是
	// `expires_at > 0 AND expires_at <= now()+leeway`，过去的时间必然命中。
	if err := store.UpsertOAuthToken(context.Background(), acctID, refreshEnc, "",
		time.Now().Add(-time.Minute).Unix(), "https://mail.google.com/"); err != nil {
		t.Fatalf("seed oauth token: %v", err)
	}
	return store, crypto, acctID
}

func tokenRowCount(t *testing.T, store *Store, acctID string) int {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_oauth_tokens WHERE account_id=$1`, acctID).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	return n
}

func accountState(t *testing.T, store *Store, acctID string) (authType string, enabled bool) {
	t.Helper()
	if err := store.pool.QueryRow(context.Background(),
		`SELECT auth_type, enabled FROM email_accounts WHERE id=$1`, acctID).Scan(&authType, &enabled); err != nil {
		t.Fatalf("read account: %v", err)
	}
	return
}

// 早退守卫：refresher / crypto 任一为 nil 就直接返回。
//
// 这两条**故意不建 store**（store 字段是 nil 指针）。如果代码越过了早退，
// 下一行 `s.store.ListExpiredOAuthTokens` 就会 nil panic —— 于是
// 「不 panic」本身就证明了早退真的在 store 之前发生。
// 这比断言某个 mock 没被调用更强：panic 是不可假绿的。
func TestRefreshOnce_NilRefresherReturnsBeforeTouchingStore(t *testing.T) {
	s := &Scheduler{store: nil, refresher: nil, crypto: nil}
	s.refreshOnce(context.Background(), 300, time.Second) // 不 panic 即通过
}

func TestRefreshOnce_NilCryptoReturnsBeforeTouchingStore(t *testing.T) {
	s := &Scheduler{store: nil, refresher: &fakeRefresher{}, crypto: nil}
	s.refreshOnce(context.Background(), 300, time.Second)
}

// provider 没配好时必须跳过，且**不能撤销**：账户还没试过刷新，
// 就把它禁掉是最糟的一种误伤。
func TestRefreshOnce_UnconfiguredProviderSkipsWithoutRevoking(t *testing.T) {
	store, crypto, acctID := newRefreshFixture(t, "oauth2")
	// providers 故意留空 -> 走 scheduler.go:350 的「provider not configured」分支
	s := &Scheduler{store: store, crypto: crypto, refresher: &fakeRefresher{}, providers: map[string]OAuthProviderConfig{}}
	s.refreshOnce(context.Background(), 300, 5*time.Second)

	if n := tokenRowCount(t, store, acctID); n != 1 {
		t.Fatalf("token rows = %d, want 1 (an unconfigured provider must not revoke)", n)
	}
	authType, enabled := accountState(t, store, acctID)
	if authType != "oauth2" || !enabled {
		t.Fatalf("account state changed: auth_type=%q enabled=%v", authType, enabled)
	}
}

// 核心安全断言：临时失败**只记审计，不撤销**。
func TestRefreshOnce_TransientFailureKeepsTokenAndAccount(t *testing.T) {
	store, crypto, acctID := newRefreshFixture(t, "oauth2")
	ref := &fakeRefresher{err: &RefreshError{Permanent: false, Code: "internal_failure"}}
	s := &Scheduler{
		store: store, crypto: crypto, refresher: ref,
		providers: map[string]OAuthProviderConfig{
			"oauth2": {ProviderID: "google", TokenURL: "https://oauth2.example/token", ClientID: "cid", ClientSecret: "sec"},
		},
	}
	s.refreshOnce(context.Background(), 300, 5*time.Second)

	if ref.calls.Load() != 1 {
		t.Fatalf("refresher calls = %d, want 1", ref.calls.Load())
	}
	if got, _ := ref.gotToken.Load().(string); got != "refresh-token-value" {
		t.Fatalf("refresher got refresh_token = %q; it must receive the DECRYPTED plaintext", got)
	}
	if n := tokenRowCount(t, store, acctID); n != 1 {
		t.Fatalf("token rows = %d, want 1: a transient failure must NOT revoke", n)
	}
	authType, enabled := accountState(t, store, acctID)
	if authType != "oauth2" || !enabled {
		t.Fatalf("transient failure disabled the account: auth_type=%q enabled=%v", authType, enabled)
	}
}

// 永久失败才撤销：token 行删除 + 账户被禁用。
func TestRefreshOnce_PermanentFailureRevokes(t *testing.T) {
	store, crypto, acctID := newRefreshFixture(t, "oauth2")
	ref := &fakeRefresher{err: &RefreshError{Permanent: true, Code: "invalid_grant"}}
	s := &Scheduler{
		store: store, crypto: crypto, refresher: ref,
		providers: map[string]OAuthProviderConfig{
			"oauth2": {ProviderID: "google", TokenURL: "https://oauth2.example/token", ClientID: "cid", ClientSecret: "sec"},
		},
	}
	s.refreshOnce(context.Background(), 300, 5*time.Second)

	if n := tokenRowCount(t, store, acctID); n != 0 {
		t.Fatalf("token rows = %d, want 0: a permanent failure must revoke", n)
	}
	authType, enabled := accountState(t, store, acctID)
	if authType != "password" || enabled {
		t.Fatalf("after revoke: auth_type=%q enabled=%v, want password/false", authType, enabled)
	}
}

// 成功路径：新 access token 落库且到期时间被推到未来，
// 于是下一轮 ListExpiredOAuthTokens 不会再选中它（否则每 5 分钟刷一次）。
func TestRefreshOnce_SuccessPersistsNewTokenAndPushesExpiry(t *testing.T) {
	store, crypto, acctID := newRefreshFixture(t, "oauth2")
	ref := &fakeRefresher{access: "new-access-token", scope: "https://mail.google.com/"}
	s := &Scheduler{
		store: store, crypto: crypto, refresher: ref,
		providers: map[string]OAuthProviderConfig{
			"oauth2": {ProviderID: "google", TokenURL: "https://oauth2.example/token", ClientID: "cid", ClientSecret: "sec"},
		},
	}
	s.refreshOnce(context.Background(), 300, 5*time.Second)

	if n := tokenRowCount(t, store, acctID); n != 1 {
		t.Fatalf("token rows = %d, want 1", n)
	}
	var expiresAt int64
	var accessEnc string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT expires_at, access_token_encrypted FROM email_oauth_tokens WHERE account_id=$1`, acctID).
		Scan(&expiresAt, &accessEnc); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if expiresAt <= time.Now().Unix() {
		t.Fatalf("expires_at = %d, still in the past; the token would be refreshed every tick", expiresAt)
	}
	if plain, err := crypto.DecryptString(accessEnc); err != nil || plain != "new-access-token" {
		t.Fatalf("stored access token = %q (err=%v), want the new one", plain, err)
	}
	// 成功绝不能顺手禁用账户。
	if _, enabled := accountState(t, store, acctID); !enabled {
		t.Fatal("a successful refresh disabled the account")
	}
}

// 刻意**不重复**已有覆盖：`oauth_refresh_test.go:183` 的
// `TestClassifyRefreshStatus` 已经测了 5xx / 429 / invalid_grant /
// invalid_client / unauthorized_client / 400-unknown / 410 / network / 200，
// `TestIsPermanentRefreshError_TypeAssertions` 也已经测了非 `RefreshError`。
// 把那些再抄一遍只是增加维护负担，不增加保障。
//
// 这里只补那两条**已有表格里确实没有**的：
//  1. `invalid_request` / `invalid_scope` 是 classifyRefreshStatus 里的两个
//     永久码，但表格只列了前三个 —— 它们是同一个 switch 分支，补齐才算覆盖完整。
//  2. **非标准 status**（418/599）必须落进 transient 桶。已有表格的
//     「400 with unknown code」测的是「已知 status + 未知 code」，
//     方向相反；「保守归 transient」这个决定真正要防的是**没见过的 status**。
func TestClassifyRefreshStatus_UncoveredCodesAndUnknownStatuses(t *testing.T) {
	for _, code := range []string{"invalid_request", "invalid_scope"} {
		if !classifyRefreshStatus(http.StatusBadRequest, code).Permanent {
			t.Fatalf("code %q classified transient; it is unrecoverable per RFC 6749 5.2", code)
		}
	}
	for _, status := range []int{http.StatusTeapot, 599, 499} {
		if classifyRefreshStatus(status, "weird_code").Permanent {
			t.Fatalf("unknown status %d classified permanent; a provider returning something new must not nuke accounts", status)
		}
	}
}
