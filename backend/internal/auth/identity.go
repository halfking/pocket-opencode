// Package auth — identity-go bridge for cross-project JWT trust.
//
// 本文件实现"跨项目互信合约"：
//   - 优先使用 identity-go 的多 issuer 校验（IDENTITY_SHARED_SECRET +
//     IDENTITY_ISSUER_ALLOWLIST），把 memora / llm-gateway / redclaw / acc
//     签发的 token 也认作合法 bearer。
//   - 若共享密钥未配置（IDENTITY_SHARED_SECRET 为空），静默回退到原
//     auth.Signer 的单密钥校验，保持本地开发与存量部署的兼容性。
//
// 影子表（identity_shadow）由 RecordShadow 写入；DSN 未配置时降级到 noop。
package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"

	"github.com/kaixuan/identity-go/token"
)

// envIdentitySharedSecret is the canonical env var name shared with sibling
// projects. Keep in sync with memora/RedClaw/llm-gateway-go/agent-control-center.
const envIdentitySharedSecret = "IDENTITY_SHARED_SECRET"

// envIdentityIssuerAllowlist is the comma-separated issuer allowlist
// shared across the six projects.
const envIdentityIssuerAllowlist = "IDENTITY_ISSUER_ALLOWLIST"

// envIdentityShadowDSN is the DSN for the cross-project shadow database.
// When empty, shadow recording is disabled and RecordShadow becomes a noop.
const envIdentityShadowDSN = "IDENTITY_SHADOW_DSN"

// DefaultIssuerAllowlist matches the identity_shadow provider contract.
//
// `asm` is deliberately absent — ai-session-manager is a pure consumer of
// llm-gateway user tokens and must not appear as an independent identity
// provider.
var DefaultIssuerAllowlist = []string{
	"redclaw", "memora", "llm-gateway", "pocket", "acc",
}

// IdentityVerifierResult is the normalized output for either legacy or
// multi-issuer verification. Callers (the requireAuth middleware) treat
// the two paths identically.
type IdentityVerifierResult struct {
	UserID      string
	Role        string
	WorkspaceID string
	TenantID    string
	Issuer      string
	Audience    string
	ExpiresAt   time.Time
}

// ErrTokenInvalid is returned when neither verify path can validate the token.
var ErrTokenInvalid = sentinelError("auth: invalid or expired token")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// VerifyToken tries, in order:
//  1. Multi-issuer verification via identity-go (if shared secret configured)
//  2. Legacy single-key verification via signer.Parse (always available)
//
// Returns ErrTokenInvalid when both paths reject.
func VerifyToken(legacy *Signer, raw string) (*IdentityVerifierResult, error) {
	if raw == "" {
		return nil, ErrTokenInvalid
	}

	if issuers, _, ok := loadMultiIssuerConfig(); ok {
		if c, err := token.VerifyMultiIssuer(raw, issuers, AudienceName); err == nil {
			ws := strings.TrimSpace(stringFromExtra(c.Extra, "workspace_id"))
			if ws == "" {
				ws = strings.TrimSpace(c.TenantID)
			}
			return &IdentityVerifierResult{
				UserID:      firstNonEmpty(c.UserID, c.Subject),
				Role:        firstRole(c.Roles, c.Scope),
				WorkspaceID: ws,
				TenantID:    c.TenantID,
				Issuer:      c.Issuer,
				Audience:    AudienceName,
				ExpiresAt:   time.Unix(c.ExpiresAt, 0),
			}, nil
		}
	}

	if legacy != nil {
		c, err := legacy.Parse(raw)
		if err == nil && c != nil && c.UserID != "" {
			exp := time.Time{}
			if c.ExpiresAt != nil {
				exp = c.ExpiresAt.Time
			}
			return &IdentityVerifierResult{
				UserID:      c.UserID,
				Role:        c.Role,
				WorkspaceID: c.WorkspaceID,
				TenantID:    c.TenantID,
				Issuer:      c.Issuer,
				Audience:    audienceFirstOrEmpty(c.Audience),
				ExpiresAt:   exp,
			}, nil
		}
	}

	return nil, ErrTokenInvalid
}

var (
	multiIssuerMu       sync.Mutex
	multiIssuerIssuers  []token.Issuer
	multiIssuerLoaded   bool
	multiIssuerDisabled bool
)

func loadMultiIssuerConfig() ([]token.Issuer, []byte, bool) {
	multiIssuerMu.Lock()
	defer multiIssuerMu.Unlock()

	if multiIssuerDisabled {
		return nil, nil, false
	}

	if strings.TrimSpace(os.Getenv(envIdentitySharedSecret)) == "" {
		multiIssuerDisabled = true
		return nil, nil, false
	}

	if multiIssuerLoaded {
		return multiIssuerIssuers, nil, true
	}

	secret, err := token.LoadSharedSecret(envIdentitySharedSecret)
	if err != nil {
		// Transient / misconfigured secret — log but don't latch
		// multiIssuerDisabled, so a corrected secret (e.g. rotated
		// mount in Kubernetes) takes effect on the next call.
		slog.Default().Warn("identity-go: shared secret invalid", "err", err)
		return nil, nil, false
	}

	allowlist := strings.TrimSpace(os.Getenv(envIdentityIssuerAllowlist))
	if allowlist == "" {
		allowlist = strings.Join(DefaultIssuerAllowlist, ",")
	}

	issuers, err := token.Allowlist(allowlist, secret)
	if err != nil {
		// Same rationale as above: don't latch on transient allowlist
		// parse failures (which only happen if a future change makes
		// Allowlist re-parse the env string).
		slog.Default().Warn("identity-go: allowlist invalid", "err", err)
		return nil, nil, false
	}

	multiIssuerIssuers = issuers
	multiIssuerLoaded = true
	return issuers, secret, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstRole(roles []string, scope string) string {
	for _, r := range roles {
		if strings.TrimSpace(r) != "" {
			return r
		}
	}
	if s := strings.TrimSpace(scope); s != "" {
		parts := strings.Fields(s)
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return "user"
}

func audienceFirstOrEmpty(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

func stringFromExtra(extra map[string]any, key string) string {
	if v, ok := extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// =====================================================================
// Shadow recording
// =====================================================================

var (
	shadowDBOnce sync.Once
	shadowDB     *sql.DB
	shadowLog    = slog.Default().With("component", "identity-shadow")
)

// ShadowDatabase returns the lazily-opened *sql.DB for the shadow store.
// Returns nil when IDENTITY_SHADOW_DSN is unset — RecordShadow becomes a noop.
func ShadowDatabase() *sql.DB {
	shadowDBOnce.Do(func() {
		dsn := strings.TrimSpace(os.Getenv(envIdentityShadowDSN))
		if dsn == "" {
			shadowLog.Info("IDENTITY_SHADOW_DSN not set; shadow recording disabled")
			return
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			shadowLog.Error("shadow: open db failed", "err", err)
			return
		}
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
		shadowDB = db
		shadowLog.Info("shadow DB initialized")
	})
	return shadowDB
}

// RecordShadow upserts a (provider, subject, tenant_id) → shadow_user_id
// mapping. Failures are logged but never propagate — login must not break
// when the shadow store is unreachable.
func RecordShadow(provider, subject, tenantID, displayName, primaryEmail string) {
	db := ShadowDatabase()
	if db == nil {
		return
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(subject) == "" {
		return
	}
	if tenantID == "" {
		tenantID = "default"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const upsertSQL = `
INSERT INTO identity_shadow.shadow_users (provider, subject, tenant_id, display_name, primary_email, last_seen_at)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), now())
ON CONFLICT (provider, subject, tenant_id) DO UPDATE
    SET display_name  = COALESCE(EXCLUDED.display_name, identity_shadow.shadow_users.display_name),
        primary_email = COALESCE(EXCLUDED.primary_email, identity_shadow.shadow_users.primary_email),
        last_seen_at  = now()`
	if _, err := db.ExecContext(ctx, upsertSQL, provider, subject, tenantID, displayName, primaryEmail); err != nil {
		shadowLog.Warn("shadow upsert failed", "provider", provider, "subject", subject, "err", err)
	}
}