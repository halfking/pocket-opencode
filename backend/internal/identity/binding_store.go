package identity

// 工作区成员资格与绑定断言的衔接（2026-10-29）。
//
// ⚠ 为什么「成员资格判定」必须在 store 里而不在 handler 里：
//   workspace_members 是成员资格的**唯一来源**（邀请、撤销、过期都在这里）。
//   handler 若自己判一遍，就是第二份真相 —— 撤销后 handler 那份还写着
//   「是成员」，断言照发不误 ⇒ 撤销能力失效。
//   ⇒ store 是唯一能回答「这个人现在还是不是成员」的地方。

import (
	"context"
	"strings"
	"time"
)

// MembershipStatus describes whether a user currently holds membership in a
// workspace.
type MembershipStatus string

const (
	// MembershipActive means the user is a current member.
	MembershipActive MembershipStatus = "active"
	// MembershipRevoked means the membership row is gone.
	MembershipRevoked MembershipStatus = "revoked"
	// MembershipExpired means the membership row exists but past its expiry.
	MembershipExpired MembershipStatus = "expired"
)

// CheckMembership reports the current membership status of userID in workspaceID.
//
// ⚠ **过期即无效**：member.ExpiresAt 非 0 且已过 ⇒ 过期。
//
//	这是「临时协作者」的语义（invitee 可带有效期），不是硬删除。
func (s *Store) CheckMembership(ctx context.Context, workspaceID, userID string) (MembershipStatus, *Member, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	userID = strings.TrimSpace(userID)
	if workspaceID == "" || userID == "" {
		return MembershipRevoked, nil, nil
	}
	member, err := s.GetMember(ctx, workspaceID, userID)
	if err != nil {
		return MembershipRevoked, nil, err
	}
	if member == nil {
		return MembershipRevoked, nil, nil
	}
	if member.ExpiresAt != 0 && member.ExpiresAt <= time.Now().Unix() {
		return MembershipExpired, member, nil
	}
	return MembershipActive, member, nil
}

// IssueBinding mints a signed membership assertion for userID in workspaceID.
//
// ⚠ **先查库、再签发**：签发前必须确认成员资格**当下**成立。
//
//	否则一个已被撤销的成员仍能拿到有效断言 ⇒ 撤销形同虚设。
//
// ⚠ 成员资格不成立 ⇒ 返回错误，**不返回**一个「空断言」。
//
//	调用方绝不能把「签不出来」降级成「放行」。
func (s *Store) IssueBinding(ctx context.Context, signer *BindingSigner, workspaceID, userID, tenantID string, ttl time.Duration) (string, *BindingAssertion, error) {
	status, member, err := s.CheckMembership(ctx, workspaceID, userID)
	if err != nil {
		return "", nil, err
	}
	switch status {
	case MembershipActive:
		// fall through
	case MembershipExpired:
		return "", nil, ErrBindingExpired
	case MembershipRevoked:
		return "", nil, ErrBindingMismatch
	default:
		return "", nil, ErrBindingMismatch
	}
	return signer.Mint(*member, tenantID, ttl)

}
