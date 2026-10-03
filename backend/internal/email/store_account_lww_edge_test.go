package email

// store_account_lww_edge_test.go — LWW 守卫里 store_account_lww_test.go 没钉的
// 四条边界。
//
// 先说清楚分工，免得看起来像在重复覆盖：同目录的 store_account_lww_test.go
// 已经钉了「新鲜基准放行 / 过期基准拒绝且不改行 / 跨 scope 仍 404 / 零基准
// 退化为旧行为」四条主干。下面只补它确实没碰的：
//
//  1. ErrStaleWrite 时回填 a.UpdatedAt —— server_assistant.go 的 409 分支把
//     这个值发给客户端，客户端据此改走下行覆盖。拿不到它，客户端只能盲猜
//     「服务端现在几点」，于是要么覆盖要么放弃，两边都错。这是一条跨进程
//     契约，此前只在设备侧间接验过（8b3f8188 验的是客户端收到 409 后不上行），
//     「服务端到底回不回这个值」没人钉。
//  2. 同一秒内连续两次写入的严格递增 —— 已有用例靠 time.Sleep(1100ms)
//     跨过秒边界才断言，恰好绕开了「max(now, base+1)」这个承诺要解决的情形。
//  3. 凭据只在明确要求时更新 —— 两条 SQL 分支的占位符编号不同（$9 / $13 对
//     $8 / $12），是「占位符与列数对不上」那类事故的高发区。
//  4. 账户不存在时是 404 而不是 409 —— 已有用例只验了「存在但跨 scope」。
import (
	"context"
	"errors"
	"testing"
)

func lwwAcc(t *testing.T, store *Store, id, userID, wsID string) *Account {
	t.Helper()
	acc, _, err := store.GetAccountByIDScoped(context.Background(), id, userID, wsID)
	if err != nil {
		t.Fatalf("read account %s: %v", id, err)
	}
	return acc
}

// 409 必须带着服务端当前版本回去，否则客户端无从判断该覆盖成什么。
func TestUpdateAccountLWWBackfillsServerStampOnStale(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-stamp", "u1", "ws1")
	base := lwwAcc(t, store, "lww-stamp", "u1", "ws1").UpdatedAt

	newer := &Account{ID: "lww-stamp", DisplayName: "服务端的新配置", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 45, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, newer, "u1", "ws1", "", false, base); err != nil {
		t.Fatalf("first write: %v", err)
	}
	serverStamp := lwwAcc(t, store, "lww-stamp", "u1", "ws1").UpdatedAt

	stale := &Account{ID: "lww-stamp", DisplayName: "过期写入", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 5, Enabled: false}
	if err := store.UpdateAccountLWTScoped(ctx, stale, "u1", "ws1", "", false, base); !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("want ErrStaleWrite, got %v", err)
	}
	if stale.UpdatedAt != serverStamp {
		t.Fatalf("ErrStaleWrite 时回填的 UpdatedAt=%d，want %d（server 409 分支把它发给客户端）",
			stale.UpdatedAt, serverStamp)
	}
}

// 两次写入落在同一秒时 updated_at 仍必须严格递增，否则客户端下一轮会把
// 「没变过」当成「没被改过」。
func TestUpdateAccountLWWIsMonotonicWithinOneSecond(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-same-sec", "u1", "ws1")
	base := lwwAcc(t, store, "lww-same-sec", "u1", "ws1").UpdatedAt

	a1 := &Account{ID: "lww-same-sec", DisplayName: "第一次", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, a1, "u1", "ws1", "", false, base); err != nil {
		t.Fatalf("第一次写入: %v", err)
	}
	a2 := &Account{ID: "lww-same-sec", DisplayName: "第二次", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 15, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, a2, "u1", "ws1", "", false, a1.UpdatedAt); err != nil {
		t.Fatalf("第二次写入（同一秒内）: %v", err)
	}
	if a2.UpdatedAt <= a1.UpdatedAt {
		t.Fatalf("同一秒内两次写入 updated_at %d → %d，必须严格递增（承诺是 max(now, base+1)）",
			a1.UpdatedAt, a2.UpdatedAt)
	}
	if got := lwwAcc(t, store, "lww-same-sec", "u1", "ws1").DisplayName; got != "第二次" {
		t.Fatalf("display_name=%q，want 第二次", got)
	}
}

// updateCredential=false 时绝不能碰凭据：客户端只改同步间隔时不该把口令
// 写空（空凭据会让下一轮 IMAP 登录直接失败）。
func TestUpdateAccountLWWLeavesCredentialAloneUnlessAsked(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "lww-cred", "u1", "ws1") // 初始 credential_encrypted = "enc-cred"
	base := lwwAcc(t, store, "lww-cred", "u1", "ws1").UpdatedAt

	noCred := &Account{ID: "lww-cred", DisplayName: "只改同步间隔", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 30, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, noCred, "u1", "ws1", "enc-should-be-ignored", false, base); err != nil {
		t.Fatalf("不改凭据的写入: %v", err)
	}
	_, cred, err := store.GetAccountByIDScoped(ctx, "lww-cred", "u1", "ws1")
	if err != nil {
		t.Fatalf("read cred: %v", err)
	}
	if cred != "enc-cred" {
		t.Fatalf("updateCredential=false 却改了凭据: %q", cred)
	}
	if got := lwwAcc(t, store, "lww-cred", "u1", "ws1").DisplayName; got != "只改同步间隔" {
		t.Fatalf("display_name=%q，want 只改同步间隔", got)
	}

	base2 := lwwAcc(t, store, "lww-cred", "u1", "ws1").UpdatedAt
	withCred := &Account{ID: "lww-cred", DisplayName: "同时改口令", IMAPHost: "h", IMAPPort: 993,
		AuthType: "password", SyncIntervalMin: 30, Enabled: true}
	if err := store.UpdateAccountLWTScoped(ctx, withCred, "u1", "ws1", "enc-new", true, base2); err != nil {
		t.Fatalf("改凭据的写入: %v", err)
	}
	_, cred, err = store.GetAccountByIDScoped(ctx, "lww-cred", "u1", "ws1")
	if err != nil {
		t.Fatalf("read cred: %v", err)
	}
	if cred != "enc-new" {
		t.Fatalf("updateCredential=true 但凭据=%q，want enc-new", cred)
	}
}

// 账户根本不存在时也必须是 404：报成 409 等于把「这一行不存在」和「这一行
// 存在但版本旧」区分开，server 层会照着回不同的响应体。
func TestUpdateAccountLWWUnknownAccountIsNotFoundNotStale(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ghost := &Account{ID: "lww-does-not-exist", DisplayName: "x", IMAPPort: 993, AuthType: "password"}
	err := store.UpdateAccountLWTScoped(context.Background(), ghost, "u1", "ws1", "", false, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的账户 err=%v，want ErrNotFound（server 据此回 404 而不是 409）", err)
	}
}
