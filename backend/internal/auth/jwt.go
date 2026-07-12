package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是 JWT payload 结构。
//
// WorkspaceID is populated by S0-A Identity Core once the user has a default
// workspace; legacy/single-tenant callers that use Sign (without a workspace)
// leave it empty and handlers fall back to "default" for backwards
// compatibility with pre-S0 data.
type Claims struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	jwt.RegisteredClaims
}

// Signer 签发和校验 JWT。
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner 构造签名器。secret 是 HS256 密钥（建议 >= 32 字节）。
func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl}
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
func (s *Signer) SignWithWorkspace(userID, role, workspaceID string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:      userID,
		Role:        role,
		WorkspaceID: workspaceID,
		RegisteredClaims: jwt.RegisteredClaims{
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
	return nil, fmt.Errorf("invalid token")
}
