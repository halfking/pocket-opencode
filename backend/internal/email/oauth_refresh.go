package email

// OAuth refresh helpers — call the provider token endpoint with a refresh
// token, parse the response, and persist the new access/refresh pair back to
// email_oauth_tokens via Store.UpsertOAuthToken.
//
// We keep the refresh itself in a tiny helper so unit tests can drive it
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
		return nil, fmt.Errorf("refresh: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh: provider returned %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("refresh: parse: %w", err)
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("refresh: missing access_token in response")
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