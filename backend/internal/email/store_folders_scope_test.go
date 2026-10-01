package email

import (
	"context"
	"errors"
	"testing"
)

// 目录登记的跨租户写入防护。
//
// 背景：`UpsertVacationReplyScoped` 早就加了「目标 account 必须在 scope 内」
// 的守卫，注释写明是"阻止创建 vacation 后修改 accountID 指向他人账户的越权"。
// 新的目录代码 `UpsertFolderScoped` 没有跟这一步：它把请求里的 account_id
// 原样写进 email_folders，只把 user_id/workspace_id 设成调用者的。
//
// 于是 POST /api/email/folders {"accountId": "<他人的 account>"} 可以在
// 别人的账户上凭空登记目录；而 email_folders 上有 UNIQUE(account_id, name)，
// 攻击者还能**抢注目录名**——受害者随后自建同名目录时，ON CONFLICT 会去更新
// 攻击者那一行（display_name 由攻击者指定），受害者在自己的目录列表里看到
// 一个来源不明、显示名被他人写死的目录。
func TestUpsertFolderScoped_RejectsForeignAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		victimUser = "fld-x-victim"
		victimWS   = "fld-x-ws-victim"
		victimAcct = "fld-x-acct-victim"

		attackerUser = "fld-x-attacker"
		attackerWS   = "fld-x-ws-attacker"
	)
	seedAccount(t, store, victimAcct, victimUser, victimWS)

	// 攻击者用自己的身份，给受害者的账户登记目录。
	err := store.UpsertFolderScoped(ctx,
		&MailFolder{AccountID: victimAcct, Name: "重要", DisplayName: "pwned", Source: "user"},
		attackerUser, attackerWS)

	if err == nil {
		t.Fatal("UpsertFolderScoped accepted an account_id belonging to another user/workspace; " +
			"this lets anyone plant folders in someone else's mail account")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (与 UpsertVacationReplyScoped 的越权路径保持一致)", err)
	}

	// 受害者的目录列表里不应该凭空多出任何东西。
	list, lerr := store.ListFoldersScoped(ctx, victimUser, victimWS, "")
	if lerr != nil {
		t.Fatalf("list victim folders: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("victim folders = %d, want 0 (attacker must not plant anything)", len(list))
	}
}

// 回归护栏：自己作用域内的账户仍然必须能正常登记目录，别把守卫收得过紧。
func TestUpsertFolderScoped_AllowsOwnAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "fld-own-user"
		ws   = "fld-own-ws"
		acct = "fld-own-acct"
	)
	seedAccount(t, store, acct, user, ws)

	if err := store.UpsertFolderScoped(ctx,
		&MailFolder{AccountID: acct, Name: "账单", Source: "user"}, user, ws); err != nil {
		t.Fatalf("own account must still be allowed, got: %v", err)
	}
	list, err := store.ListFoldersScoped(ctx, user, ws, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "账单" {
		t.Fatalf("folders = %+v, want one 账单", list)
	}
}
