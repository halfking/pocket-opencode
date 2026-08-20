package email

// OAuth refresh helpers — call the provider token endpoint with a refresh
// token, parse the response, and persist the new access/refresh pair back to
// email_oauth_tokens via Store.UpsertOAuthToken.
//
// We keep the refresh itself in a tiny helper so tests can drive it
// against an httptest server, and so the scheduler / fetcher stay free of
// provider-specific transport code.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RefreshError is the structured error returned by DefaultOAuthRefresher
// when the upstream provider explicitly rejects the refresh token. Permanent
// errors (4xx invalid_grant / unauthorized_client / invalid_client) signal
// that the user must reconnect via /api/email/oauth/start; transient errors
// (5xx, network timeouts) can be retried.
//
// Callers (e.g. Scheduler.refreshLoop) can use errors.As(err, &RefreshError{})
// to distinguish the two categories and surface the right UX hint.
type RefreshError struct {
	Permanent bool   // true for 4xx OAuth errors that won't recover without user re-consent
	Code      string // OAuth standard error code (invalid_grant / invalid_client / ...) or HTTP status text
	Cause     error  // underlying transport / decode error (may be nil)
}

func (e *RefreshError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("oauth refresh: %s: %v", e.Code, e.Cause)
	}
	return fmt.Sprintf("oauth refresh: %s", e.Code)
}

func (e *RefreshError) Unwrap() error { return e.Cause }

// truncateBody 限制 provider 错误 body 的长度，避免巨大 body 把日志撑爆。
const maxErrorBodyLen = 256

func truncateBody(body []byte) string {
	if len(body) <= maxErrorBodyLen {
		return string(body)
	}
	return string(body[:maxErrorBodyLen]) + "…"
}

// IsPermanentRefreshError returns true if the error represents an unrecoverable
// provider rejection (invalid_grant etc.). Use it where the caller only cares
// about the binary classifier, not the full RefreshError.
func IsPermanentRefreshError(err error) bool {
	var re *RefreshError
	if errors.As(err, &re) {
		return re.Permanent
	}
	return false
}

// classifyRefreshStatus maps an HTTP status + provider error code to a
// RefreshError classification. Permanent errors per RFC 6749 §5.2 + Google
// OAuth docs: invalid_grant, invalid_client, unauthorized_client, plus 400/401.
// 5xx and 429 are transient. Anything else falls into the transient bucket so
// the scheduler will retry on the next tick instead of nuking the account.
func classifyRefreshStatus(status int, providerCode string) *RefreshError {
	switch strings.ToLower(providerCode) {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_request", "invalid_scope":
		return &RefreshError{Permanent: true, Code: providerCode}
	}
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusGone:
		return &RefreshError{Permanent: true, Code: providerCode}
	case 0:
		return &RefreshError{Permanent: false, Code: "network_error"}
	}
	return &RefreshError{Permanent: false, Code: providerCode}
}

// parseOAuthErrorBody 提取 provider 返回的 oauth standard error code。
func parseOAuthErrorBody(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
		Code  string `json:"error_code"` // Google specific
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if parsed.Error != "" {
		return parsed.Error
	}
	return parsed.Code
}

// OAuthRefreshResult is what we get back from a provider token endpoint.
type OAuthRefreshResult struct {
	AccessToken  string
	RefreshToken string // optional: provider may rotate refresh tokens
	ExpiresIn    int    // seconds; default 3600 if 0
	Scope        string
}

// OAuthRefresher abstracts the HTTP call so tests can swap in a fake. The
// production implementation lives in DefaultOAuthRefresher (this file).
type OAuthRefresher interface {
	Refresh(ctx context.Context, tokenURL, clientID, clientSecret, refreshToken string) (*OAuthRefreshResult, error)
}

// DefaultOAuthRefresher uses net/http with a configurable client.
type DefaultOAuthRefresher struct {
	Client *http.Client
}

// NewDefaultOAuthRefresher returns a refresher with a 30s timeout client.
func NewDefaultOAuthRefresher() *DefaultOAuthRefresher {
	return &DefaultOAuthRefresher{Client: &http.Client{Timeout: 30 * time.Second}}
}

// Refresh issues a refresh_token grant against the provider token endpoint.
func (r *DefaultOAuthRefresher) Refresh(ctx context.Context, tokenURL, clientID, clientSecret, refreshToken string) (*OAuthRefreshResult, error) {
	if tokenURL == "" {
		return nil, errors.New("email: refresh tokenURL is empty")
	}
	if refreshToken == "" {
		return nil, errors.New("email: refresh token is empty")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, &RefreshError{Permanent: false, Code: "network_error", Cause: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		providerCode := parseOAuthErrorBody(body)
		classifier := classifyRefreshStatus(resp.StatusCode, providerCode)
		classifier.Cause = fmt.Errorf("status=%d body=%s", resp.StatusCode, truncateBody(body))
		return nil, classifier
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &RefreshError{Permanent: false, Code: "decode_error", Cause: err}
	}
	if parsed.AccessToken == "" {
		return nil, &RefreshError{Permanent: true, Code: "missing_access_token"}
	}
	if parsed.ExpiresIn == 0 {
		parsed.ExpiresIn = 3600
	}
	return &OAuthRefreshResult{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    parsed.ExpiresIn,
		Scope:        parsed.Scope,
	}, nil
}

// RefreshAccessToken decrypts the existing refresh token, asks the refresher
// for a new access token, re-encrypts + persists via store.UpsertOAuthToken,
// and returns the plaintext access token ready for IMAP login.
//
// If `crypto` is nil or fails to decrypt, we surface the error so the caller
// can skip this account without crashing the scheduler.
func RefreshAccessToken(
	ctx context.Context,
	crypto *Crypto,
	store *Store,
	refresher OAuthRefresher,
	tokenURL, clientID, clientSecret string,
	accountID string,
) (string, error) {
	if crypto == nil {
		return "", errors.New("email: refresh requires email crypto")
	}
	if store == nil {
		return "", errors.New("email: refresh requires store")
	}
	if refresher == nil {
		return "", errors.New("email: refresh requires refresher")
	}
	if accountID == "" {
		return "", errors.New("email: refresh requires accountID")
	}
	refreshEnc, _, _, scope, err := store.GetOAuthToken(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("load refresh token: %w", err)
	}
	refreshToken, err := crypto.DecryptString(refreshEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	res, err := refresher.Refresh(ctx, tokenURL, clientID, clientSecret, refreshToken)
	if err != nil {
		return "", fmt.Errorf("provider refresh: %w", err)
	}
	// RefreshAccessToken 透传结构化错误（保留 IsPermanentRefreshError 能力）；
	// 真正的 revoked 处理由 scheduler.refreshLoop 在收到错误后根据
	// IsPermanentRefreshError 决定是否广播 email.oauth.revoked。
	// Some providers rotate refresh tokens; keep the new one if returned,
	// otherwise fall back to the existing one.
	newRefresh := refreshToken
	if res.RefreshToken != "" {
		newRefresh = res.RefreshToken
	}
	accessEnc, err := crypto.EncryptString(res.AccessToken)
	if err != nil {
		return "", fmt.Errorf("encrypt new access token: %w", err)
	}
	refreshEncOut, err := crypto.EncryptString(newRefresh)
	if err != nil {
		return "", fmt.Errorf("encrypt new refresh token: %w", err)
	}
	if res.Scope == "" {
		res.Scope = scope
	}
	expiresAt := time.Now().Add(time.Duration(res.ExpiresIn) * time.Second).Unix()
	if err := store.UpsertOAuthToken(ctx, accountID, refreshEncOut, accessEnc, expiresAt, res.Scope); err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}
	return res.AccessToken, nil
}
