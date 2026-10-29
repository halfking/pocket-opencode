// Package identity — 工作区成员绑定断言（2026-10-29）。
//
// ⚠ 为什么需要这一层（服务间可信验证的落点）：
//
//	目前「这个 token 属于哪个工作区」是**客户端一句话**说了算的：
//	  - openpocket 登录时用影子表 EnsureDefaultWorkspace 造一个 ws_<userID>，
//	    把它写进响应体的 workspace_id；
//	  - 客户端把 workspace_id 存进 localStorage；
//	  - 恢复会话时前端调用 GET /api/auth/identity，后端**从 JWT claim** 取
//	    workspace_id 回给客户端。
//	而 RedClaw 侧用的是 **tenant_id**（另一个命名空间），且 RedClaw 的令牌
//	**根本没有 workspace 声明** ⇒ 两端的工作区标识既不同源、又没人签过。
//
//	⇒ 结果：任何能拿到一个合法令牌的一方，都能让服务端认领**任意**工作区
//	  （例如把 workspace_id 改成别人的 ws_xxx），而没有任何签名能戳破这件事。
//	  本地归属列（config/meeting 表的 owner_user_id + owner_workspace_id）
//	  于是建立在**未经签名的自报值**上。
//
// 📌 本文件提供的正是缺的那块签名：
//
//	BindingAssertion —— 由工作区权威（openpocket）签发的成员绑定断言，
//	  覆盖：用户、租户、工作区、成员角色、签发方、受众、有效期。
//	  验证方（RedClaw / 任何服务间调用方）据此判断：
//	  「这个调用方**确实**是某工作区的成员」，
//	  而不是「调用方**说**自己是某工作区的成员」。
//
// 📌 三条不可让步的语义：
//  1. **签发方唯一**：只有工作区权威能签（HS256 共享密钥 + issuer 白名单）。
//  2. **归属精确匹配**：user_id 与 workspace_id **都**要匹配才算数；
//     workspace_id 单独不足以定位账号，user_id 单独不足以定位工作区。
//  3. **过期即失效**：断言自带 exp；过期后不得续用旧的。
package identity

import (
	"errors"
	"strings"
	"time"
)

// BindingAudience 是断言的受众（resource-server 边界）。
//
// ⚠ 为什么固定成一个具名受众而不是「任意 aud 都收」：
//
//	断言证明的是「此主体在此工作区有成员资格」，它**不该**被转发到
//	另一个服务去当那里的通行证。受众即边界。
const BindingAudience = "workspace-binding"

// BindingIssuer 是工作区权威的签发方标识。
//
// ⚠ 必须落在 identity-go 的 issuer 白名单里（pocket/redclaw/…），
//
//	否则验证方会拒收 —— 这是有意的：白名单之外的主体签不出可信断言。
const BindingIssuer = "pocket"

// DefaultBindingTTL 是断言的默认有效期。
//
// ⚠ 为什么不是「永久」：成员资格**可被撤销**（RemoveMember）。永久断言
//
//	等于把撤销能力从权威手里拿走 —— 撤销后旧断言仍在有效期内可用。
//	短 TTL 让撤销在有界时间内生效。
const DefaultBindingTTL = 15 * time.Minute

// Errors returned by the binding package. Callers map these to
// fail-closed decisions: a binding that cannot be verified is
// **not** evidence of membership.
var (
	// ErrBindingInvalid means the assertion is malformed or failed
	// verification (bad signature, wrong issuer, wrong audience).
	ErrBindingInvalid = errors.New("identity: workspace binding assertion invalid")
	// ErrBindingExpired means the assertion was valid but is past exp.
	ErrBindingExpired = errors.New("identity: workspace binding assertion expired")
	// ErrBindingMismatch means the assertion does not describe the
	// user/workspace pair the caller asked about.
	ErrBindingMismatch = errors.New("identity: workspace binding assertion does not match the requested scope")
)

// BindingRole is the member role carried by an assertion.
type BindingRole string

const (
	BindingRoleOwner   BindingRole = "owner"
	BindingRoleInvitee BindingRole = "invitee"
)

// BindingAssertion is a signed statement of workspace membership.
//
// ⚠ 它是**独立于用户会话令牌**的东西：会话令牌证明「你是谁」，
//
//	断言证明「你**在这个工作区**有成员资格」。两者是不同的问题。
type BindingAssertion struct {
	// UserID is the member's user id.
	UserID string
	// TenantID is the tenant the workspace belongs to.
	TenantID string
	// WorkspaceID is the workspace the membership is scoped to.
	WorkspaceID string
	// Role is the member role (owner | invitee).
	Role BindingRole
	// IssuedAt is when the assertion was minted (Unix seconds).
	IssuedAt int64
	// NotBefore is when the assertion starts being valid (Unix seconds).
	NotBefore int64
	// ExpiresAt is when the assertion stops being valid (Unix seconds).
	ExpiresAt int64
	// Issuer signed it; always BindingIssuer for assertions this package mints.
	Issuer string
	// Audience is who may consume it; always BindingAudience.
	Audience string
}

// IsExpired reports whether the assertion is past its validity window at now.
func (b *BindingAssertion) IsExpired(now time.Time) bool {
	return b.ExpiresAt <= now.Unix()
}

// Covers reports whether the assertion describes exactly this user and
// workspace pair.
//
// ⚠ 两个标识**都**要匹配：只有 user_id 匹配不足以定位工作区
//
//	（同一个人可以加入多个工作区），只有 workspace_id 匹配不足以定位账号。
func (b *BindingAssertion) Covers(userID, workspaceID string) bool {
	return b.UserID == strings.TrimSpace(userID) &&
		b.WorkspaceID == strings.TrimSpace(workspaceID)
}
