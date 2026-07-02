package email

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
)

// PKCEPair 包含 code_verifier 和 code_challenge。
type PKCEPair struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE 生成 PKCE 对（RFC 7636）。
func GeneratePKCE() (*PKCEPair, error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return nil, err
	}
	verifierStr := base64.RawURLEncoding.EncodeToString(verifier)
	hash := sha256.Sum256([]byte(verifierStr))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	return &PKCEPair{Verifier: verifierStr, Challenge: challenge}, nil
}

// RandomState 生成 32 字节随机 state（CSRF 防护）。
func RandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// BuildAuthURL 构造 OAuth2 授权 URL（PKCE + state）。
func BuildAuthURL(provider Provider, redirectURI, state string, pkce *PKCEPair) (string, error) {
	if !provider.SupportsOAuth2 {
		return "", fmt.Errorf("provider %s does not support OAuth2", provider.ID)
	}
	u, err := url.Parse(provider.OAuth2AuthURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", "") // 调用方填充
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", joinScopes(provider.OAuth2Scopes))
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	if provider.ID == "outlook" {
		q.Set("prompt", "select_account")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func joinScopes(scopes []string) string {
	out := ""
	for i, s := range scopes {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
