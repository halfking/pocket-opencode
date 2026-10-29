package identity

// binding_store_test.go — 成员资格判定与断言签发的集成测试（需 PG）。
//
// ⚠ 这些用例回答的是同一个问题的不同侧面：
//   「一个**不是**成员的人，能不能拿到一份能进这个工作区的断言？」
//
// 与 binding_test.go（纯逻辑）分开：前者测**签名**，
// 这里测**签发前的资格判定** —— 两者缺一不可：
//   签名再强，签发时不查库也会给撤销者开门；
//   查库再严，签名被伪造也照样能进。

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIssueBinding_OwnerGetsAssertion(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_bind_a", OwnerID: "u_owner", Name: "bind"}); err != nil {
		t.Fatal(err)
	}
	signer := testSigner(t)
	signed, assertion, err := s.IssueBinding(ctx, signer, "ws_bind_a", "u_owner", "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("IssueBinding: %v", err)
	}
	if signed == "" || assertion == nil {
		t.Fatal("no assertion issued")
	}
	if assertion.WorkspaceID != "ws_bind_a" || assertion.UserID != "u_owner" {
		t.Fatalf("assertion describes the wrong scope: %+v", assertion)
	}
	if assertion.Role != BindingRoleOwner {
		t.Fatalf("role = %q want owner", assertion.Role)
	}
}

func TestIssueBinding_NonMemberGetsNothing(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_bind_b", OwnerID: "u_owner", Name: "bind"}); err != nil {
		t.Fatal(err)
	}
	signer := testSigner(t)
	// u_stranger 不在成员表里 ⇒ 签不出来。
	if _, _, err := s.IssueBinding(ctx, signer, "ws_bind_b", "u_stranger", "tenant_a", time.Minute); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("non-member got an assertion: err = %v", err)
	}
}

func TestIssueBinding_ExpiredMemberGetsNothing(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_bind_c", OwnerID: "u_owner", Name: "bind"}); err != nil {
		t.Fatal(err)
	}
	// 一个**已过期**的成员（expires_at 在过去）。
	if err := s.AddMember(ctx, &Member{
		WorkspaceID: "ws_bind_c",
		UserID:      "u_temp",
		Role:        RoleInvitee,
		ExpiresAt:   time.Now().Add(-time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	signer := testSigner(t)
	// ⚠ 已过期的成员**不得**拿到断言 —— 否则「临时协作者到期自动失效」
	//   这个语义就是假的。
	if _, _, err := s.IssueBinding(ctx, signer, "ws_bind_c", "u_temp", "tenant_a", time.Minute); !errors.Is(err, ErrBindingExpired) {
		t.Fatalf("expired member got an assertion: err = %v", err)
	}
}

func TestIssueBinding_RevokedMemberGetsNothing(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_bind_d", OwnerID: "u_owner", Name: "bind"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, &Member{WorkspaceID: "ws_bind_d", UserID: "u_temp", Role: RoleInvitee}); err != nil {
		t.Fatal(err)
	}
	signer := testSigner(t)
	// 撤销前能签。
	if _, _, err := s.IssueBinding(ctx, signer, "ws_bind_d", "u_temp", "tenant_a", time.Minute); err != nil {
		t.Fatalf("IssueBinding before revoke: %v", err)
	}
	// 撤销后签不出来。
	if err := s.RemoveMember(ctx, "ws_bind_d", "u_temp"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.IssueBinding(ctx, signer, "ws_bind_d", "u_temp", "tenant_a", time.Minute); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("revoked member got an assertion: err = %v", err)
	}
}

func TestCheckMembership_ReportsStatus(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_bind_e", OwnerID: "u_owner", Name: "bind"}); err != nil {
		t.Fatal(err)
	}
	status, _, err := s.CheckMembership(ctx, "ws_bind_e", "u_owner")
	if err != nil {
		t.Fatal(err)
	}
	if status != MembershipActive {
		t.Fatalf("owner status = %q want active", status)
	}
	status, _, err = s.CheckMembership(ctx, "ws_bind_e", "u_nobody")
	if err != nil {
		t.Fatal(err)
	}
	if status != MembershipRevoked {
		t.Fatalf("stranger status = %q want revoked", status)
	}
}
