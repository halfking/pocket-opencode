package email

// store_account_lww_test.go — 邮箱账户配置的 LWW 守卫（需求 8）。
//
// 需求原文：「这个信息有最后修改时间，在服务端与客户端中，以最后时间为准来
// 更新旧的一方。」
//
// 旧实现 UpdateAccountScoped 无条件 `updated_at = now()` 覆盖：离线客户端
// 回传旧配置会把服务端的新配置冲掉（last-write-by-arrival）。这里用真 PG
// 钉住三条语义：
//  1. 基准版本不旧 → 写入成功，updated_at 单调递增；
//  2. 服务端已被别人写得更晚 → 拒绝（ErrStaleWrite）且**不改动**那一行；
//  3. 跨 user/workspace 的写仍然 404（守卫不能把越权写变成 409）。

import (
	"context"
	"errors"
	"testing"
	"time"
)

func accountDisplayName(t *testing.T, store *Store, id, userID, wsID string) (string, int64) {
	t.Helper()
	acc, _, err := store.GetAccountByIDScoped(context.Background(), id, userID, wsID)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	return acc.DisplayName, acc.UpdatedAt
}

// 客户端拿着刚读到的版本上行 → 成功，且 updated_at 严格变大。
func TestUpdateAccountLWW_FreshBaseSucceeds(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-ok", "u1", "ws1")
	_, base := accountDisplayName(t, store, "lww-ok", "u1", "ws1")

	acc := &Account{ID: "lww-ok", DisplayName: "改过的名字", IMAPHost: "imap.example.com",
		IMAPPort: 993, AuthType: "password", SyncIntervalMin: 30, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, acc, "u1", "ws1", "", false, base); err != nil {
		t.Fatalf("fresh-base write rejected: %v", err)
	}
	name, now := accountDisplayName(t, store, "lww-ok", "u1", "ws1")
	if name != "改过的名字" {
		t.Fatalf("display name not applied: %q", name)
	}
	if now <= base {
		t.Fatalf("updated_at must advance monotonically: base=%d now=%d", base, now)
	}
}

// 服务端已被另一端改过 → 拒绝，并保持服务端内容不变。
func TestUpdateAccountLWW_StaleBaseRejected(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-stale", "u1", "ws1")
	_, base := accountDisplayName(t, store, "lww-stale", "u1", "ws1")

	// 另一台设备先写成功（模拟服务端已有更新版本）
	newer := &Account{ID: "lww-stale", DisplayName: "另一台设备改的", IMAPHost: "imap.example.com",
		IMAPPort: 993, AuthType: "password", SyncIntervalMin: 45, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, newer, "u1", "ws1", "", false, base); err != nil {
		t.Fatalf("first write: %v", err)
	}
	_, serverStamp := accountDisplayName(t, store, "lww-stale", "u1", "ws1")

	// 离线设备拿旧基准回来写 → 必须被拒，且不能覆盖
	stale := &Account{ID: "lww-stale", DisplayName: "离线设备的旧改动", IMAPHost: "imap.example.com",
		IMAPPort: 993, AuthType: "password", SyncIntervalMin: 10, Enabled: false}
	err := store.UpdateAccountLWTScoped(ctx, stale, "u1", "ws1", "", false, base)
	if !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got %v", err)
	}
	name, afterStamp := accountDisplayName(t, store, "lww-stale", "u1", "ws1")
	if name != "另一台设备改的" {
		t.Fatalf("stale write overwrote server copy: %q", name)
	}
	if afterStamp != serverStamp {
		t.Fatalf("stale write must not bump updated_at: %d -> %d", serverStamp, afterStamp)
	}
}

// 守卫不能把越权写从 404 变成 409（否则会泄露「这一行存在」）。
func TestUpdateAccountLWW_ForeignScopeStillNotFound(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-scope", "owner", "ws-owner")
	_, base := accountDisplayName(t, store, "lww-scope", "owner", "ws-owner")

	// base 故意给一个很旧的值：守卫若优先于 scope 判定就会误报 409。
	acc := &Account{ID: "lww-scope", DisplayName: "越权", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true}
	err := store.UpdateAccountLWTScoped(ctx, acc, "attacker", "ws-owner", "", false, base-1000)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for foreign scope, got %v", err)
	}
}

// 旧客户端不带版本号 → 保持旧行为（服务端无条件覆盖），否则升级会打断写入。
func TestUpdateAccountLWW_ZeroBaseKeepsLegacyBehavior(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-legacy", "u1", "ws1")
	_, base := accountDisplayName(t, store, "lww-legacy", "u1", "ws1")
	time.Sleep(1100 * time.Millisecond) // 秒级时间戳：确保 now 真的会变大

	acc := &Account{ID: "lww-legacy", DisplayName: "无版本写入", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, acc, "u1", "ws1", "", false, 0); err != nil {
		t.Fatalf("legacy write rejected: %v", err)
	}
	name, now := accountDisplayName(t, store, "lww-legacy", "u1", "ws1")
	if name != "无版本写入" {
		t.Fatalf("legacy write not applied: %q", name)
	}
	if now <= base {
		t.Fatalf("legacy path must still refresh updated_at: base=%d now=%d", base, now)
	}
}
