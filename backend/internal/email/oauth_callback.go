package email

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// pendingEntry 保存 OAuth 流程中间状态。
type pendingEntry struct {
	UserID       string
	ProviderID   string
	EmailAddress string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AccountID    string
	CreatedAt    time.Time
}

// PendingOAuthEntry 是导出的构造函数。
func PendingOAuthEntry(accountID, userID, providerID, emailAddress, verifier, clientID, clientSecret, redirectURI string) pendingEntry {
	return pendingEntry{
		AccountID:    accountID,
		UserID:       userID,
		ProviderID:   providerID,
		EmailAddress: emailAddress,
		CodeVerifier: verifier,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
	}
}

// NewPendingEntry 是别名。
func NewPendingEntry(accountID, userID, providerID, emailAddress, verifier, clientID, clientSecret, redirectURI string) pendingEntry {
	return PendingOAuthEntry(accountID, userID, providerID, emailAddress, verifier, clientID, clientSecret, redirectURI)
}

// PendingOAuth 是内存 map，存储 state → pendingEntry。
type PendingOAuth struct {
	mu      sync.RWMutex
	entries map[string]pendingEntry
}

// NewPendingOAuth 构造 PendingOAuth。
func NewPendingOAuth() *PendingOAuth {
	return &PendingOAuth{entries: make(map[string]pendingEntry)}
}

// Put 存储 state → entry。
func (p *PendingOAuth) Put(state string, entry pendingEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry.CreatedAt = time.Now()
	p.entries[state] = entry
}

// Pop 取出并删除 state 对应的 entry。
func (p *PendingOAuth) Pop(state string) (pendingEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[state]
	if ok {
		delete(p.entries, state)
	}
	return e, ok
}

// GCLoop 每 5 分钟清理超过 10 分钟的 entry。
func (p *PendingOAuth) GCLoop(ctx context.Context) {
	p.gcLoop(ctx)
}

func (p *PendingOAuth) gcLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			now := time.Now()
			for state, e := range p.entries {
				if now.Sub(e.CreatedAt) > 10*time.Minute {
					delete(p.entries, state)
				}
			}
			p.mu.Unlock()
		}
	}
}

// OAuthCallbackConfig 配置 OAuth callback handler 的依赖。
type OAuthCallbackConfig struct {
	Store               *Store
	Crypto              *Crypto
	Pending             *PendingOAuth
	Broadcaster         interface{ Broadcast(string, interface{}) }
	TargetedBroadcaster interface {
		BroadcastToUser(userID, msgType string, payload interface{})
	}
}

// HandleOAuthCallback 返回 GET /callback/email/oauth 的 handler。
func HandleOAuthCallback(cfg OAuthCallbackConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		errParam := r.URL.Query().Get("error")
		if errParam != "" {
			log.Printf("[oauth] callback error: %s, desc: %s", errParam, r.URL.Query().Get("error_description"))
			http.Error(w, "OAuth error: "+errParam, http.StatusBadRequest)
			return
		}
		if code == "" || state == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}
		entry, ok := cfg.Pending.Pop(state)
		if !ok {
			http.Error(w, "invalid or expired state", http.StatusBadRequest)
			return
		}
		provider, found := LookupProviderByID(entry.ProviderID)
		if !found {
			http.Error(w, "unknown provider", http.StatusInternalServerError)
			return
		}
		// 交换 code → token
		tokens, err := exchangeCodeForToken(provider, code, entry.CodeVerifier, entry.ClientID, entry.ClientSecret, entry.RedirectURI)
		if err != nil {
			log.Printf("[oauth] exchange token: %v", err)
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}
		// 加密并持久化 refresh_token 和 access_token
		refreshEnc, _ := cfg.Crypto.EncryptString(tokens.RefreshToken)
		accessEnc, _ := cfg.Crypto.EncryptString(tokens.AccessToken)
		expiresAt := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix()
		if err := cfg.Store.UpsertOAuthToken(r.Context(), entry.AccountID, refreshEnc, accessEnc, expiresAt, tokens.Scope); err != nil {
			log.Printf("[oauth] upsert token: %v", err)
			http.Error(w, "store token failed", http.StatusInternalServerError)
			return
		}
		// 更新 account.auth_type = "oauth2"
		if err := cfg.Store.SetAccountAuthType(r.Context(), entry.AccountID, "oauth2"); err != nil {
			log.Printf("[oauth] set auth type: %v", err)
		}
		// 广播 WS 事件通知前端
		payload := map[string]string{
			"accountId": entry.AccountID,
			"userId":    entry.UserID,
		}
		if cfg.TargetedBroadcaster != nil && entry.UserID != "" {
			cfg.TargetedBroadcaster.BroadcastToUser(entry.UserID, "email.oauth.completed", payload)
		} else if cfg.Broadcaster != nil {
			cfg.Broadcaster.Broadcast("email.oauth.completed", payload)
		}
		// 返回成功页面（或重定向到移动端 deep link）
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><body><h2>✅ OAuth 授权成功</h2><p>账户 %s 已连接，请返回应用。</p></body></html>`, entry.EmailAddress)
	}
}

// tokenResponse 是 OAuth token endpoint 的响应。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// exchangeCodeForToken 用 authorization code 交换 access + refresh token。
func exchangeCodeForToken(provider Provider, code, codeVerifier, clientID, clientSecret, redirectURI string) (*tokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code_verifier", codeVerifier)

	req, err := http.NewRequest(http.MethodPost, provider.OAuth2TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange failed: %d %s", resp.StatusCode, string(body))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return &tokens, nil
}
