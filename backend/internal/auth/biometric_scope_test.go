package auth

// biometric_scope_test.go — §944 D1 判据：credential_id 的**归属守卫**。
//
// ⚠⚠ 本节查出来的缺陷（**修复前实测**）：
//
//	`BiometricStore.Register` 的 `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	⇒ ⇒ ⇒ 撞上一个**别的用户**的 credential_id 时，它会把**那一行的
//	public_key 整个换掉**（user_id / workspace_id **不在** SET 列表里
//	⇒ 那行仍然属于原主人）
//	⇒ ⇒ ⇒ 而登录链按 **credential_id 取行**（`Get` 不按用户过滤——登录那一刻
//	还不知道是谁）⇒ ⇒ ⇒ `FinishLogin(..., storedCred.PublicKey, ...)`
//	拿**被换掉的公钥**验签 ⇒ ⇒ ⇒ 而签发的 token 用的是那一行的 user_id
//	⇒ ⇒⇒⇒ ⇒ **攻击者用自己的私钥就能以原主人的身份登录**。
//
// ⚠ 判据量的是**行为**（撞号后：报错 + 受害者的行一字未改），不是源码文本。
//
// ⚠⚠ **两条实现都要跑**（PG 版 + 内存版）：
//
//	★ §943 的元教训：「门禁跑在内存版上，生产跑的是 PG 版」⇒ ⇒⇒ 只测一条
//	实现，另一条分叉了门禁是绿的。而内存版原来直接 `s.memory[c.ID] = &clone`
//	⇒ ⇒⇒ **连 user_id 都一起换掉** ⇒ ⇒⇒ 比 PG 版还严重
//	⇒ ⇒⇒ 两条实现必须**同形**，判据必须**同时**钉住它们。
//
// ⚠ 负控（同一文件内自带）：**同一个用户**用同一个 id 重注册 ⇒ 仍然是
//   「更新那一行」（升级场景）⇒ ⇒⇒ 这一腿证明「守卫没有把正常路径也挡掉」。
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	s944CredID   = "cred-s944-victim"
	s944Victim   = "user-s944-victim"
	s944Attacker = "user-s944-attacker"
	s944WS       = "ws-s944"
)

// s944DSN 只认测试专用 DSN（与仓内其它 PG 集成测试同一约定）。
func s944DSN() string {
	return os.Getenv("POCKET_TEST_POSTGRES_DSN")
}

// s944AuthPool 起一个**隔离 schema** 的 pool（每个用例一套，互不干扰）。
func s944AuthPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := s944DSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping auth integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect root pool: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "auth_scope_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	})
	return pool
}

// s944NewPGStore 造一个走 PG 的 BiometricStore（生产路径）。
func s944NewPGStore(t *testing.T) *BiometricStore {
	t.Helper()
	pool := s944AuthPool(t)
	store := NewBiometricStore(pool)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return store
}

// s944Cred 造一条凭证。
func s944Cred(id, user, pub string) *BiometricCredential {
	return &BiometricCredential{
		ID:          id,
		UserID:      user,
		WorkspaceID: s944WS,
		DeviceName:  "测试设备",
		PublicKey:   []byte(pub),
		Counter:     1,
		Transports:  "[]",
	}
}

// TestS944BiometricRegisterRejectsAnotherUsersCredentialID PG 版：撞号 ⇒ 报错 + 受害者的行一字未改。
func TestS944BiometricRegisterRejectsAnotherUsersCredentialID(t *testing.T) {
	ctx := context.Background()
	store := s944NewPGStore(t)

	victimKey := "victim-public-key"
	if err := store.Register(ctx, s944Cred(s944CredID, s944Victim, victimKey)); err != nil {
		t.Fatalf("受害者注册应成功: %v", err)
	}

	// 攻击者拿**同一个 credential_id** 来写。
	err := store.Register(ctx, s944Cred(s944CredID, s944Attacker, "attacker-public-key"))
	got, gerr := store.Get(ctx, s944CredID)
	if gerr != nil {
		t.Fatalf("读受害者的凭证失败: %v", gerr)
	}

	// ⚠⚠ **哑弹专用断言**（§942/§943 的元教训：「守卫必须配读得到拒绝的读法」）：
	//
	//	守卫可能**在**（受害者的行没被改）但拒绝**读不出来**
	//	（调用方拿到 nil ⇒ 它以为注册成功了 ⇒ 而受害者的凭证已经**报废**
	//	——他的设备再也登不进来，而服务端没有留下任何「被拒绝」的痕迹）。
	//
	//	⇒ 这一条只有「守卫在 + 拒绝读不出来」时才触发
	//	⇒ ⇒⇒ 它与「守卫根本不在」（下面那条）的**报错文本不同**
	//	⇒ ⇒⇒ 于是两条变异可以**各自**被期望消息钉住
	//	⇒ ⇒⇒⇒ （★ 「变异转红了」≠「变异验到了你想验的那条」）
	if err == nil && string(got.PublicKey) == victimKey {
		t.Errorf("守卫拒绝了写入（受害者的行未改）但调用方拿到 nil ⇒ 拒绝**读不出来**（哑弹）")
	}
	if err == nil {
		t.Errorf("攻击者撞 id 竟然成功了 ⇒ 归属守卫没生效（公钥已被换掉 ⇒ 可顶替身份）")
	} else if !errors.Is(err, ErrBiometricCredentialScopeConflict) {
		t.Errorf("撞 id 应返回 ErrBiometricCredentialScopeConflict，实际 %v", err)
	}

	// 受害者的行必须**一字未改**（公钥、归属、counter 全都没动）。
	if string(got.PublicKey) != victimKey {
		t.Errorf("受害者的公钥变成了 %q（want %q）⇒ **越权覆盖**", got.PublicKey, victimKey)
	}
	if got.UserID != s944Victim {
		t.Errorf("那一行的 user_id 变成了 %q（want %q）⇒ 归属被搬走了", got.UserID, s944Victim)
	}
	if got.Counter != 1 {
		t.Errorf("受害者的 counter 变成了 %d（want 1）", got.Counter)
	}
}

// TestS944BiometricRegisterSameUserStillUpdates 负控：同一个用户重注册 ⇒ 仍然更新那一行。
//
// ⚠ 这一腿证明「守卫没有把正常路径（升级场景）也挡掉」。
func TestS944BiometricRegisterSameUserStillUpdates(t *testing.T) {
	ctx := context.Background()
	store := s944NewPGStore(t)

	if err := store.Register(ctx, s944Cred(s944CredID, s944Victim, "old-key")); err != nil {
		t.Fatalf("首次注册应成功: %v", err)
	}
	if err := store.Register(ctx, s944Cred(s944CredID, s944Victim, "new-key")); err != nil {
		t.Fatalf("同一用户重注册（升级场景）应成功，实际: %v", err)
	}
	got, err := store.Get(ctx, s944CredID)
	if err != nil {
		t.Fatalf("读凭证失败: %v", err)
	}
	if string(got.PublicKey) != "new-key" {
		t.Errorf("同一用户重注册后公钥是 %q（want new-key）⇒ 守卫把正常路径也挡掉了", got.PublicKey)
	}
	if got.UserID != s944Victim {
		t.Errorf("同一用户重注册后 user_id 变成了 %q", got.UserID)
	}
}

// TestS944BiometricMemoryPathRejectsAnotherUsersCredentialID 内存版：撞号 ⇒ 报错 + **归属不许搬走**。
//
// ⚠⚠ 内存版（pool == nil 的降级路径）原来直接 `s.memory[c.ID] = &clone`
//
//	⇒ ⇒⇒ 连 user_id / workspace_id 都**一起**换成提交者的
//	⇒ ⇒⇒ 比 PG 版还严重（PG 版至少保留归属）⇒ ⇒⇒ 所以这一腿必须单独钉。
func TestS944BiometricMemoryPathRejectsAnotherUsersCredentialID(t *testing.T) {
	ctx := context.Background()
	store := NewBiometricStore(nil) // pool == nil ⇒ 内存路径

	if err := store.Register(ctx, s944Cred(s944CredID, s944Victim, "victim-public-key")); err != nil {
		t.Fatalf("受害者注册应成功: %v", err)
	}
	err := store.Register(ctx, s944Cred(s944CredID, s944Attacker, "attacker-public-key"))
	if err == nil {
		t.Fatalf("内存版撞 id 竟然成功了 ⇒ 内存路径没有守卫")
	}
	if !errors.Is(err, ErrBiometricCredentialScopeConflict) {
		t.Fatalf("撞 id 应返回 ErrBiometricCredentialScopeConflict，实际 %v", err)
	}
	got, err := store.Get(ctx, s944CredID)
	if err != nil {
		t.Fatalf("读受害者的凭证失败: %v", err)
	}
	if string(got.PublicKey) != "victim-public-key" {
		t.Errorf("受害者的公钥变成了 %q ⇒ **越权覆盖**", got.PublicKey)
	}
	if got.UserID != s944Victim {
		t.Errorf("那一行的 user_id 变成了 %q（want %q）⇒ 归属被搬走了", got.UserID, s944Victim)
	}
}
