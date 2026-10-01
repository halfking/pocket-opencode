package email

import (
	"context"
	"errors"
	"testing"
)

// 操作日志（email_ops_log）的跨租户防护。
//
// 这一条比目录登记那条严重得多。目录登记只是"多一行脏数据"；ops 行会被
// POST /api/emails/ops/sync **真的执行**：
//
//  1. 攻击者 POST /api/emails/ops，accountId 填受害者的账户、uid 随便填
//  2. 该行以攻击者的 user/workspace 落库
//  3. 攻击者 POST /api/emails/ops/sync（不带 ids = 全量）
//     → ClaimPendingOpsScoped(攻击者) 把自己种的那行取了回来
//  4. → executeOpsEntries → Fetcher.MoveUIDsToMailbox(ctx, 受害者账户, uids, ...)
//  5. → dialAndLogin → GetAccountByID(accountID) **不带用户维度**
//     → 解密出受害者的 IMAP 凭据并登录
//
// 结果是：可以用别人的邮箱账户把任意 UID 移出 INBOX（移到 Trash 即从收件箱
// 消失）。归属校验必须在写库那一刻就拦下来。
func TestInsertOpsLogScoped_RejectsForeignAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		victimUser = "ops-x-victim"
		victimWS   = "ops-x-ws-victim"
		victimAcct = "ops-x-acct-victim"

		attackerUser = "ops-x-attacker"
		attackerWS   = "ops-x-ws-attacker"
	)
	seedAccount(t, store, victimAcct, victimUser, victimWS)

	_, err := store.InsertOpsLogScoped(ctx, []OpsLogEntry{{
		AccountID:    victimAcct,
		EmailID:      "whatever",
		UID:          12345,
		Action:       "move",
		TargetFolder: "Trash",
	}}, attackerUser, attackerWS)

	if err == nil {
		t.Fatal("InsertOpsLogScoped accepted an account_id belonging to another user/workspace; " +
			"该行会被 /ops/sync 取回并用受害者的 IMAP 凭据执行移动")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}

	// 关键回归：攻击者自己的 pending 里**不能**出现这条操作，
	// 否则 /ops/sync 就会真的去动受害者的信箱。
	pending, perr := store.ClaimPendingOpsScoped(ctx, attackerUser, attackerWS, 200)
	if perr != nil {
		t.Fatalf("claim pending: %v", perr)
	}
	if len(pending) != 0 {
		t.Fatalf("attacker got %d pending op(s) pointing at a foreign account: %+v", len(pending), pending)
	}

	// 受害者那边同样不该凭空多出日志。
	rows, rerr := store.ListOpsLogScoped(ctx, victimUser, victimWS, "", 200)
	if rerr != nil {
		t.Fatalf("list victim ops: %v", rerr)
	}
	if len(rows) != 0 {
		t.Fatalf("victim ops = %d, want 0", len(rows))
	}
}

// 反向护栏：自己作用域内的操作必须照常记录，别把守卫收得过紧。
func TestInsertOpsLogScoped_AllowsOwnAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "ops-own-user"
		ws   = "ops-own-ws"
		acct = "ops-own-acct"
	)
	seedAccount(t, store, acct, user, ws)

	n, err := store.InsertOpsLogScoped(ctx, []OpsLogEntry{{
		AccountID: acct, EmailID: "own-1", UID: 7, Action: "move", TargetFolder: "账单",
	}}, user, ws)
	if err != nil {
		t.Fatalf("own account must still be allowed, got: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted = %d, want 1", n)
	}
	pending, perr := store.ClaimPendingOpsScoped(ctx, user, ws, 200)
	if perr != nil {
		t.Fatalf("claim: %v", perr)
	}
	if len(pending) != 1 || pending[0].AccountID != acct {
		t.Fatalf("pending = %+v, want one op on %s", pending, acct)
	}
}

// AccountOwnedBy 本身：存在、归属正确、不存在/越权三种情形。
func TestAccountOwnedBy(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "own-by-acct", "own-by-user", "own-by-ws")

	cases := []struct {
		name      string
		accountID string
		user, ws  string
		want      bool
	}{
		{"本人账户", "own-by-acct", "own-by-user", "own-by-ws", true},
		{"别人 user", "own-by-acct", "other-user", "own-by-ws", false},
		{"别人 workspace", "own-by-acct", "own-by-user", "other-ws", false},
		{"不存在的账户", "nope", "own-by-user", "own-by-ws", false},
	}
	for _, c := range cases {
		got, err := store.AccountOwnedBy(ctx, c.accountID, c.user, c.ws)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: owned = %v, want %v", c.name, got, c.want)
		}
	}
}
