package identity

// 工作区成员绑定断言的**签发**与**验证**（2026-10-29）。
//
// ⚠ 为什么用 identity-go 的 HS256 共享密钥，而不是自己手写 HMAC：
//   - identity-go 已经是 6 个项目的跨项目互信通道（pocket/redclaw/memora/
//     llm-gateway/acc），断言复用同一条信任链 ⇒ 验证方**已经在**用它验令牌；
//   - 自己另造一套签名格式，等于给验证方多加一个必须同步维护的秘密。
//
// ⚠ 为什么签发用「共享密钥」而不是非对称：
//   断言的两端（openpocket 签、RedClaw 验）**都在同一个信任域**里，
//   与会话令牌走的是同一条 identity-go 通道。用同一把钥匙 ⇒ 同一个撤销点、
//   同一套轮换流程、同一份审计。非对称签名是给「签发方不该能读内容」的场景，
//   这里不适用。

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kaixuan/identity-go/token"
)

// envBindingSharedSecret is the env var holding the shared secret used to
// sign and verify binding assertions.
//
// ⚠ 与会话令牌的 IDENTITY_SHARED_SECRET 是**同一把**钥匙：
//
//	断言不是第二种凭据，它是「在既有身份之上补一个工作区维度」。
//	分开两把钥匙 ⇒ 运维要轮换两个秘密、验证方要配两个 env、
//	而两者的信任域其实完全相同 ⇒ 没有额外安全收益，只有额外复杂度。
const envBindingSharedSecret = "IDENTITY_SHARED_SECRET"

// BindingSigner mints and verifies workspace binding assertions.
//
// ⚠ nil-safe：零值 Signer 的 Mint/Verify 都返回错误而不是 panic。
type BindingSigner struct {
	issuer token.Issuer
}

// NewBindingSigner builds a signer from the shared secret in the env.
//
// ⚠ 共享密钥未配置 ⇒ 返回 nil（调用方必须把 nil 当成「无法验证」，
//
//	也就是 fail-closed，绝不能降级成「信任客户端自报的值」）。
func NewBindingSigner() (*BindingSigner, error) {
	secret, err := token.LoadSharedSecret(envBindingSharedSecret)
	if err != nil {
		return nil, fmt.Errorf("identity: binding signer: %w", err)
	}
	return &BindingSigner{issuer: token.Issuer{Name: BindingIssuer, Secret: secret}}, nil
}

// NewBindingSignerWithSecret builds a signer over an explicit secret (tests).
func NewBindingSignerWithSecret(secret []byte) (*BindingSigner, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("identity: binding secret too short (need >=32 bytes, got %d)", len(secret))
	}
	return &BindingSigner{issuer: token.Issuer{Name: BindingIssuer, Secret: secret}}, nil
}

// Mint signs a binding assertion for the given member.
//
// ⚠ 它**不查库**：调用方必须先用 store 确认成员资格仍然成立。
//
//	「签发」是权威的动作，判定「是不是成员」是 store 的职责 ——
//	两者分开，才能让「谁有权签」这件事只有一个来源。
func (s *BindingSigner) Mint(member Member, tenantID string, ttl time.Duration) (string, *BindingAssertion, error) {
	if s == nil {
		return "", nil, fmt.Errorf("identity: binding signer not configured")
	}
	userID := strings.TrimSpace(member.UserID)
	workspaceID := strings.TrimSpace(member.WorkspaceID)
	if userID == "" || workspaceID == "" {
		return "", nil, fmt.Errorf("identity: binding needs user and workspace")
	}
	if ttl <= 0 {
		ttl = DefaultBindingTTL
	}
	role := BindingRole(member.Role)
	if role == "" {
		role = BindingRoleInvitee
	}
	now := time.Now()
	expiresAt := now.Add(ttl).Unix()
	if member.ExpiresAt > 0 && member.ExpiresAt < expiresAt {
		expiresAt = member.ExpiresAt
	}
	if expiresAt <= now.Unix() {
		return "", nil, ErrBindingExpired
	}
	assertion := &BindingAssertion{
		UserID:      userID,
		TenantID:    strings.TrimSpace(tenantID),
		WorkspaceID: workspaceID,
		Role:        role,
		IssuedAt:    now.Unix(),
		NotBefore:   now.Unix(),
		ExpiresAt:   expiresAt,
		Issuer:      BindingIssuer,
		Audience:    BindingAudience,
	}
	// ⚠ 不用 token.SignHS256：它用 `now.Add(ttl)` **覆盖** claims.ExpiresAt，
	//   签发方因此无法控制过期时刻（也就无法签出「已经过期」的断言）。
	//   而验证方必须能区分「过期」（时间问题）与「无效」（伪造问题）——
	//   两者要给出不同的错误，运维才知道该等还是该查入侵。
	//   ⇒ 这里自己签，显式写 iat/nbf/exp。
	signed, err := s.signWithExpiry(assertion)
	if err != nil {
		return "", nil, fmt.Errorf("identity: sign binding: %w", err)
	}
	return signed, assertion, nil
}

// Verify checks a binding assertion's signature, issuer, audience and
// validity window, and returns the decoded statement.
//
// ⚠ 失败一律返回错误，**没有**「无法验证就当通过」的路径：
//
//	绑定断言的作用就是让「能不能进这个工作区」有一个可验证的答案，
//	没有答案时正确的动作是**拒绝**，不是放行。
func (s *BindingSigner) Verify(raw string) (*BindingAssertion, error) {
	if s == nil {
		return nil, fmt.Errorf("identity: binding signer not configured")
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrBindingInvalid
	}
	// ⚠ 不用 token.VerifyMultiIssuer：它在 jwt.Parse 里就把 exp 过期的
	//   令牌判成 parse 失败 ⇒ 「过期」与「签名不对」变成同一个错误。
	//   这里自己解析，先验签、再判时刻，把两件事分开报。
	claims, err := s.parseAndVerify(raw)
	if err != nil {
		return nil, err
	}
	workspaceID := strings.TrimSpace(stringClaim(claims.Extra, "workspace_id"))
	if workspaceID == "" {
		return nil, ErrBindingInvalid
	}
	userID := strings.TrimSpace(claims.UserID)
	if userID == "" {
		userID = strings.TrimSpace(claims.Subject)
	}
	if userID == "" {
		return nil, ErrBindingInvalid
	}
	now := time.Now()
	assertion := &BindingAssertion{
		UserID:      userID,
		TenantID:    strings.TrimSpace(claims.TenantID),
		WorkspaceID: workspaceID,
		Role:        BindingRole(strings.TrimSpace(stringClaim(claims.Extra, "role"))),
		IssuedAt:    claims.IssuedAt,
		NotBefore:   claims.NotBefore,
		ExpiresAt:   claims.ExpiresAt,
		Issuer:      claims.Issuer,
		Audience:    BindingAudience,
	}
	if assertion.IsExpired(now) {
		return nil, ErrBindingExpired
	}
	// ⚠ nbf（生效时刻）也要自己判：jwt 的默认校验被关掉了（为了把
	//   「过期」和「无效」分开报），副作用是 nbf 也不再自动生效。
	//   一份「还没生效」的断言同样不该被接受。
	if assertion.NotBefore > 0 && now.Unix() < assertion.NotBefore {
		return nil, ErrBindingInvalid
	}
	return assertion, nil
}

// VerifyFor checks a binding assertion against a specific user/workspace pair.
//
// ⚠ 这是**授权**入口，Verify 只是**认证**：验签通过只说明「断言是真的」，
//
//	还要断言描述的正是调用方要进的那个工作区，才谈得上准入。
func (s *BindingSigner) VerifyFor(raw, userID, workspaceID string) (*BindingAssertion, error) {
	assertion, err := s.Verify(raw)
	if err != nil {
		return nil, err
	}
	if !assertion.Covers(userID, workspaceID) {
		return nil, ErrBindingMismatch
	}
	return assertion, nil
}

func stringClaim(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	if v, ok := extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// BindingSignerFromEnv is the process-wide accessor.
//
// ⚠ 返回 nil 表示共享密钥未配置 —— 调用方**必须**把 nil 当成
//
//	「无法验证」（fail-closed），不能退回客户端自报值。
func BindingSignerFromEnv() *BindingSigner {
	signer, err := NewBindingSigner()
	if err != nil {
		return nil
	}
	return signer
}

// bindingSharedSecretConfigured reports whether the shared secret env is set.
// Used by startup diagnostics to explain a degraded (fail-closed) mode.
func bindingSharedSecretConfigured() bool {
	return strings.TrimSpace(os.Getenv(envBindingSharedSecret)) != ""
}

// signWithExpiry signs the assertion with an explicit iat/nbf/exp.
//
// ⚠ 为什么不直接用 token.SignHS256：它按 ttl 重算过期时刻，签发方
//
//	就失去了对有效期的控制。绑定断言的有效期是**安全参数**
//	（决定撤销多久生效），必须由签发方显式决定。
func (s *BindingSigner) signWithExpiry(assertion *BindingAssertion) (string, error) {
	mc := jwt.MapClaims{
		"iss":          BindingIssuer,
		"sub":          assertion.UserID,
		"aud":          BindingAudience,
		"user_id":      assertion.UserID,
		"workspace_id": assertion.WorkspaceID,
		"role":         string(assertion.Role),
		"iat":          assertion.IssuedAt,
		"nbf":          assertion.IssuedAt,
		"exp":          assertion.ExpiresAt,
	}
	if assertion.TenantID != "" {
		mc["tenant_id"] = assertion.TenantID
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, mc)
	return t.SignedString(s.issuer.Secret)
}

// parseAndVerify verifies the signature, the issuer and the audience, and
// returns the decoded claims.
//
// ⚠ 时刻判定**不在**这里做：过期的令牌在这里仍然能验签成功（签名是对的），
//
//	由调用方区分「过期」与「无效」。jwt 的默认校验会把过期令牌直接判成
//	parse 失败 ⇒ 这里显式关掉那个校验，自己判。
func (s *BindingSigner) parseAndVerify(raw string) (*token.Claims, error) {
	parsed, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("identity: unexpected signing method %v", t.Header["alg"])
		}
		return s.issuer.Secret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithoutClaimsValidation(),
	)
	if err != nil {
		return nil, ErrBindingInvalid
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrBindingInvalid
	}
	iss, _ := mc["iss"].(string)
	if iss != BindingIssuer {
		return nil, ErrBindingInvalid
	}
	if !audienceMatches(mc["aud"], BindingAudience) {
		return nil, ErrBindingInvalid
	}
	out := &token.Claims{Issuer: iss, Audience: BindingAudience}
	if v, ok := mc["sub"].(string); ok {
		out.Subject = v
	}
	if v, ok := mc["user_id"].(string); ok {
		out.UserID = v
	}
	if v, ok := mc["tenant_id"].(string); ok {
		out.TenantID = v
	}
	if v, ok := mc["iat"].(float64); ok {
		out.IssuedAt = int64(v)
	}
	if v, ok := mc["exp"].(float64); ok {
		out.ExpiresAt = int64(v)
	}
	if v, ok := mc["nbf"].(float64); ok {
		out.NotBefore = int64(v)
	}
	extras := map[string]any{}
	for k, v := range mc {
		switch k {
		case "iss", "sub", "aud", "user_id", "tenant_id", "iat", "exp", "nbf", "roles", "scope":
			continue
		default:
			extras[k] = v
		}
	}
	if len(extras) > 0 {
		out.Extra = extras
	}
	return out, nil
}

// audienceMatches reports whether the token's aud contains the expected
// value. A token may carry several audiences; a single match is enough.
func audienceMatches(raw any, expected string) bool {
	switch v := raw.(type) {
	case string:
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == expected {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) == expected {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if strings.TrimSpace(s) == expected {
				return true
			}
		}
	}
	return false
}

// MintWithExpiry mints an assertion with an explicit validity window.
//
// ⚠ 生产路径用 Mint（TTL 由调用方给）；这个入口给**测试**造出
//
//	「已经过期」「尚未生效」的断言 —— 验证方必须能把这两种情况
//	与「签名不对」区分开。
func (s *BindingSigner) MintWithExpiry(member Member, tenantID string, notBefore, expiresAt time.Time) (string, *BindingAssertion, error) {
	if s == nil {
		return "", nil, fmt.Errorf("identity: binding signer not configured")
	}
	userID := strings.TrimSpace(member.UserID)
	workspaceID := strings.TrimSpace(member.WorkspaceID)
	if userID == "" || workspaceID == "" {
		return "", nil, fmt.Errorf("identity: binding needs user and workspace")
	}
	role := BindingRole(member.Role)
	if role == "" {
		role = BindingRoleInvitee
	}
	assertion := &BindingAssertion{
		UserID:      userID,
		TenantID:    strings.TrimSpace(tenantID),
		WorkspaceID: workspaceID,
		Role:        role,
		IssuedAt:    notBefore.Unix(),
		NotBefore:   notBefore.Unix(),
		ExpiresAt:   expiresAt.Unix(),
		Issuer:      BindingIssuer,
		Audience:    BindingAudience,
	}
	signed, err := s.signWithExpiry(assertion)
	if err != nil {
		return "", nil, fmt.Errorf("identity: sign binding: %w", err)
	}
	return signed, assertion, nil
}
