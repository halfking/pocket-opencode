package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnVerifier 提供 challenge-session 管理与 COSE 签名验证能力。
//
// 设计：
//   - Challenge 存储在内存 TTL map（300s 过期，自动清理）
//   - register/finish 验证 attestation response（COSE 公钥 + counter）
//   - login/finish 验证 assertion response（签名 + counter 单调性）
//
// 生产环境升级路径：替换 challengeStore 为 Redis TTL，支持多实例部署。
type WebAuthnVerifier struct {
	webAuthn       *webauthn.WebAuthn
	challengeStore *challengeStore
}

// challengeSession 存储一次性的 challenge 与关联的用户上下文。
type challengeSession struct {
	Challenge  []byte
	UserID     string // register 场景已知；login 场景为空（等客户端回传 credential_id）
	ExpiresAt  time.Time
}

// challengeStore 是内存 TTL map，定期清理过期 challenge（每分钟扫一次）。
type challengeStore struct {
	mu       sync.RWMutex
	sessions map[string]*challengeSession // key: base64url(challenge)
	stopCh   chan struct{}
}

func newChallengeStore() *challengeStore {
	s := &challengeStore{
		sessions: make(map[string]*challengeSession),
		stopCh:   make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

func (s *challengeStore) Put(challengeB64 string, userID string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challengeBytes, err := base64.RawURLEncoding.DecodeString(challengeB64)
	if err != nil {
		return fmt.Errorf("invalid challenge base64: %w", err)
	}
	s.sessions[challengeB64] = &challengeSession{
		Challenge: challengeBytes,
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (s *challengeStore) Get(challengeB64 string) (*challengeSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[challengeB64]
	if !ok {
		return nil, errors.New("challenge not found")
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, errors.New("challenge expired")
	}
	return sess, nil
}

func (s *challengeStore) Delete(challengeB64 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, challengeB64)
}

func (s *challengeStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for k, v := range s.sessions {
				if now.After(v.ExpiresAt) {
					delete(s.sessions, k)
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

func (s *challengeStore) Close() {
	close(s.stopCh)
}

// NewWebAuthnVerifier 构造 WebAuthn 验证器。
//
// rpDisplayName: 显示名称（如 "OpenCode Pocket"）
// rpID: RP 标识符（如 "pocket.kaixuan.com"，必须是 origin 的有效域名）
// rpOrigin: 客户端 origin（如 "https://pocket.kaixuan.com"）
func NewWebAuthnVerifier(rpDisplayName, rpID, rpOrigin string) (*WebAuthnVerifier, error) {
	if rpDisplayName == "" || rpID == "" || rpOrigin == "" {
		return nil, errors.New("rpDisplayName, rpID, rpOrigin are required")
	}
	wconfig := &webauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     []string{rpOrigin},
		// 默认支持 ES256/RS256（常见平台覆盖）
		// AuthenticatorSelection 留空 = 接受所有类型（platform/cross-platform）
	}
	wa, err := webauthn.New(wconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to init webauthn: %w", err)
	}
	return &WebAuthnVerifier{
		webAuthn:       wa,
		challengeStore: newChallengeStore(),
	}, nil
}

// BeginRegistration 生成注册 challenge。
//
// 返回：(challengeB64, creationOptions, error)
// challengeB64 用于存储到 session；creationOptions 是返回给客户端的完整参数。
func (v *WebAuthnVerifier) BeginRegistration(ctx context.Context, userID, displayName string) (string, *protocol.CredentialCreation, error) {
	// WebAuthn User 实现：只需提供 ID/Name/DisplayName
	user := &webAuthnUser{
		id:          []byte(userID),
		name:        userID, // 可改为 email
		displayName: displayName,
	}
	creation, session, err := v.webAuthn.BeginRegistration(user)
	if err != nil {
		return "", nil, fmt.Errorf("begin registration failed: %w", err)
	}
	// session.Challenge is already a base64url string in v0.11.2
	challengeB64 := session.Challenge
	if err := v.challengeStore.Put(challengeB64, userID, 5*time.Minute); err != nil {
		return "", nil, err
	}
	return challengeB64, creation, nil
}

// FinishRegistration 验证客户端提交的 attestation response。
//
// 返回：(credentialID, publicKey, counter, error)
func (v *WebAuthnVerifier) FinishRegistration(ctx context.Context, challengeB64 string, ccr *protocol.ParsedCredentialCreationData) (string, []byte, uint32, error) {
	sess, err := v.challengeStore.Get(challengeB64)
	if err != nil {
		return "", nil, 0, fmt.Errorf("invalid challenge: %w", err)
	}
	defer v.challengeStore.Delete(challengeB64)

	// 构造 webauthn.SessionData（必需字段）
	session := &webauthn.SessionData{
		Challenge: string(sess.Challenge), // convert []byte to string
		UserID:    []byte(sess.UserID),
	}
	user := &webAuthnUser{id: []byte(sess.UserID)}
	credential, err := v.webAuthn.CreateCredential(user, *session, ccr)
	if err != nil {
		return "", nil, 0, fmt.Errorf("attestation verification failed: %w", err)
	}
	credID := base64.RawURLEncoding.EncodeToString(credential.ID)
	publicKey := credential.PublicKey // COSE 编码
	counter := credential.Authenticator.SignCount
	return credID, publicKey, counter, nil
}

// BeginLogin 生成登录 challenge（无需预知 userID）。
func (v *WebAuthnVerifier) BeginLogin(ctx context.Context) (string, *protocol.CredentialAssertion, error) {
	// allowCredentials 留空 = 允许任意已注册凭据
	assertion, session, err := v.webAuthn.BeginLogin(nil)
	if err != nil {
		return "", nil, fmt.Errorf("begin login failed: %w", err)
	}
	// session.Challenge is already a base64url string in v0.11.2
	challengeB64 := session.Challenge
	if err := v.challengeStore.Put(challengeB64, "", 5*time.Minute); err != nil {
		return "", nil, err
	}
	return challengeB64, assertion, nil
}

// FinishLogin 验证客户端提交的 assertion response。
//
// 参数：
//   - challengeB64: 之前 BeginLogin 返回的 challenge
//   - credentialID: 客户端使用的凭据 ID（base64url）
//   - storedPublicKey: 从 BiometricStore 查出的公钥（COSE 编码）
//   - storedCounter: 从 BiometricStore 查出的 counter（防重放）
//   - par: 客户端提交的 assertion response
//
// 返回：(newCounter, error)
func (v *WebAuthnVerifier) FinishLogin(ctx context.Context, challengeB64, credentialID string, storedPublicKey []byte, storedCounter uint32, par *protocol.ParsedCredentialAssertionData) (uint32, error) {
	sess, err := v.challengeStore.Get(challengeB64)
	if err != nil {
		return 0, fmt.Errorf("invalid challenge: %w", err)
	}
	defer v.challengeStore.Delete(challengeB64)

	// 构造 webauthn credential（需要 ID + PublicKey + SignCount）
	credIDBytes, err := base64.RawURLEncoding.DecodeString(credentialID)
	if err != nil {
		return 0, fmt.Errorf("invalid credentialID base64: %w", err)
	}
	credential := &webauthn.Credential{
		ID:        credIDBytes,
		PublicKey: storedPublicKey,
		Authenticator: webauthn.Authenticator{
			SignCount: storedCounter,
		},
	}
	// 构造 SessionData (Challenge is already base64url string)
	session := &webauthn.SessionData{
		Challenge: string(sess.Challenge),
	}
	// ValidateLogin 需要 user 参数（提供 credential 列表）
	user := &webAuthnUserWithCredentials{
		id:          credIDBytes, // use credential ID as user ID for login
		credentials: []webauthn.Credential{*credential},
	}
	updatedCred, err := v.webAuthn.ValidateLogin(user, *session, par)
	if err != nil {
		return 0, fmt.Errorf("assertion verification failed: %w", err)
	}
	return updatedCred.Authenticator.SignCount, nil
}

// webAuthnUser 实现 webauthn.User 接口（注册时需要）。
type webAuthnUser struct {
	id          []byte
	name        string
	displayName string
}

func (u *webAuthnUser) WebAuthnID() []byte              { return u.id }
func (u *webAuthnUser) WebAuthnName() string            { return u.name }
func (u *webAuthnUser) WebAuthnDisplayName() string     { return u.displayName }
func (u *webAuthnUser) WebAuthnIcon() string            { return "" }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return nil }

// webAuthnUserWithCredentials 实现 webauthn.User 接口（登录验证时需要提供 credentials）。
type webAuthnUserWithCredentials struct {
	id          []byte
	credentials []webauthn.Credential
}

func (u *webAuthnUserWithCredentials) WebAuthnID() []byte       { return u.id }
func (u *webAuthnUserWithCredentials) WebAuthnName() string     { return string(u.id) }
func (u *webAuthnUserWithCredentials) WebAuthnDisplayName() string { return string(u.id) }
func (u *webAuthnUserWithCredentials) WebAuthnIcon() string     { return "" }
func (u *webAuthnUserWithCredentials) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

// GenerateChallenge 生成安全的 32 字节随机 challenge。
func GenerateChallenge() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}
