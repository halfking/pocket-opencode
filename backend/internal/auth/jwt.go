package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// IssuerName is the JWT `iss` value minted by openpocket.
//
// This must match the entry in IDENTITY_ISSUER_ALLOWLIST used by sibling
// projects (memora / llm-gateway-go / RedClaw / acc-go / ai-session-manager).
const IssuerName = "pocket"

// AudienceName is the JWT `aud` value this server validates.
//
// Each project enforces its own resource-server audience boundary.
const AudienceName = "pocket-api"

// Claims 是 JWT payload 结构。
//
// WorkspaceID is populated by S0-A Identity Core once the user has a default
// workspace; legacy/single-tenant callers that use Sign (without a workspace)
// leave it empty and handlers fall back to "default" for backwards
// compatibility with pre-S0 data.
//
// TenantID is populated when the token is issued by an upstream user-identity
// provider (e.g. llm-gateway-go) and propagated through identity-go's
// VerifyMultiIssuer; legacy openpocket-minted tokens leave it empty and
// handlers fall back to the workspace-derived value.
type Claims struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	jwt.RegisteredClaims
}

// Signer 签发和校验 JWT。
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner 构造签名器。secret 是 HS256 密钥（建议 >= 32 字节）。
// Returns error if secret is too short or ttl is invalid.
func NewSigner(secret string, ttl time.Duration) (*Signer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt secret must be at least 32 bytes, got %d", len(secret))
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("jwt ttl must be positive, got %v", ttl)
	}
	return &Signer{secret: []byte(secret), ttl: ttl}, nil
}

// Sign 签发 JWT，包含 user_id 和 role claim。
//
// 保留为向后兼容入口：不带 workspace 的单租户场景。S0-A Identity Core
// 登录流程应改用 SignWithWorkspace 把 workspace_id 写入 claim，这样后续
// handler 可以直接从 JWT 拿到 workspace 隔离边界。
func (s *Signer) Sign(userID, role string) (string, error) {
	return s.SignWithWorkspace(userID, role, "")
}

// SignWithWorkspace 签发带 workspace_id 的 JWT。workspaceID 为空时与 Sign 等价。
//
// 本地签发的 token 现在显式携带 iss="pocket" + aud="pocket-api"，以满足
// 跨项目互信合约（identity-go）。旧 token 缺少这两个字段时仍然可被现有
// 的 Parse 入口校验（向后兼容窗口期）。
func (s *Signer) SignWithWorkspace(userID, role, workspaceID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("userID cannot be empty")
	}
	if role == "" {
		return "", fmt.Errorf("role cannot be empty")
	}
	now := time.Now()
	claims := Claims{
		UserID:      userID,
		Role:        role,
		WorkspaceID: workspaceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    IssuerName,
			Audience:  jwt.ClaimStrings{AudienceName},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// Parse 校验 JWT 并返回 claims。
func (s *Signer) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token: claims validation failed")
}
