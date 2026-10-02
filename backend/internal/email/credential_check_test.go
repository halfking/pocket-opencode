// credential_check_test.go — 启动自检 CheckCredentials 的判定矩阵。
//
// 覆盖的关键一格是 **Skipped**（空凭据 / oauth-pending）：它既不是成功也不是
// 失败。第一版实现只有 Decryptable/Failed 两个计数时，全新部署（所有账户都
// 是占位凭据）会被判成「一把都解不开」而误报 ERROR —— 那是会让运维去追一个
// 不存在的问题的假警报。必须有专门用例钉住它。
package email

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newCryptoForTest(t *testing.T, seed string) *Crypto {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed[i%len(seed)]
	}
	c, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	return c
}

func addAccountWithCipher(t *testing.T, store *Store, id, cipher string) {
	t.Helper()
	acc := &Account{
		ID: id, UserID: "u1", WorkspaceID: "ws1",
		DisplayName: id, EmailAddress: id + "@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password",
		Enabled: true, CreatedAt: time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, cipher); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestCheckCredentials_AllDecrypt(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	good := newCryptoForTest(t, "good-key-material-000000")
	c1, _ := good.EncryptString("pw-a")
	c2, _ := good.EncryptString("pw-b")
	addAccountWithCipher(t, store, "acct-1", c1)
	addAccountWithCipher(t, store, "acct-2", c2)

	chk, err := CheckCredentials(context.Background(), store, good)
	if err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
	if chk.Accounts != 2 || chk.Decryptable != 2 || chk.Failed != 0 {
		t.Fatalf("计数不对: %+v", chk)
	}
	if !chk.AllDecryptable() || chk.AllFailed() {
		t.Fatalf("正确 key 不该被判成有问题: %+v", chk)
	}
	if !strings.Contains(chk.Summary(), "all 2 enabled") {
		t.Fatalf("Summary 不对: %q", chk.Summary())
	}
}

// 核心：错 key 必须被**明确**判成「key 拿错了」，而不是含混地失败。
func TestCheckCredentials_WrongKeyIsReported(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	real := newCryptoForTest(t, "the-real-key-00000000")
	wrong := newCryptoForTest(t, "a-different-key-00000")
	c1, _ := real.EncryptString("pw-a")
	c2, _ := real.EncryptString("pw-b")
	addAccountWithCipher(t, store, "acct-1", c1)
	addAccountWithCipher(t, store, "acct-2", c2)

	chk, err := CheckCredentials(context.Background(), store, wrong)
	if err != nil {
		t.Fatalf("CheckCredentials 返回了 error —— key 错了是**事实**，不是调用失败: %v", err)
	}
	if chk.Decryptable != 0 || chk.Failed != 2 {
		t.Fatalf("计数不对: %+v", chk)
	}
	if !chk.AllFailed() {
		t.Fatalf("一把都解不开却没判成 AllFailed: %+v", chk)
	}
	if chk.AllDecryptable() {
		t.Fatal("一把都解不开却判成 AllDecryptable")
	}
	if !strings.Contains(chk.Summary(), "MASTER KEY LOOKS WRONG") {
		t.Fatalf("Summary 没点明是 key 的问题: %q", chk.Summary())
	}
	if chk.FirstAccountID == "" || chk.FirstError == nil {
		t.Fatalf("没记下第一个失败账户: %+v", chk)
	}
}

// 关键一格：全是占位凭据的**全新部署不得误报**。
//
// 负控：把 CheckCredentials 里的 `res.Skipped++` 去掉（让空凭据落进 Failed）
// -> 本条转红。这是第一版两计数实现会犯的错。
func TestCheckCredentials_PlaceholderCredentialsAreNotFailures(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	good := newCryptoForTest(t, "good-key-material-000000")
	pending, _ := good.EncryptString("oauth-pending-no-credential")
	addAccountWithCipher(t, store, "acct-pending", pending)
	addAccountWithCipher(t, store, "acct-empty", "")

	chk, err := CheckCredentials(context.Background(), store, good)
	if err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
	if chk.Accounts != 2 {
		t.Fatalf("账户数不对: %+v", chk)
	}
	if chk.Skipped != 2 {
		t.Fatalf("两个占位凭据都该记为 Skipped，实际 %+v", chk)
	}
	if chk.Failed != 0 {
		t.Fatalf("占位凭据被当成了解密失败: %+v", chk)
	}
	if chk.AllFailed() {
		t.Fatalf("全新部署被误判成「key 拿错了」: %+v / %q", chk, chk.Summary())
	}
	if !chk.AllDecryptable() {
		t.Fatalf("没有真实失败就不该报问题: %+v", chk)
	}
	if !strings.Contains(chk.Summary(), "nothing to verify") {
		t.Fatalf("Summary 应说明无需校验: %q", chk.Summary())
	}
}

// 一半对一半错：既不是「key 错」也不是「没事」。
func TestCheckCredentials_PartialIsNeitherAllFailedNorAllDecryptable(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	real := newCryptoForTest(t, "the-real-key-00000000")
	wrong := newCryptoForTest(t, "a-different-key-00000")
	good1, _ := real.EncryptString("pw-a")
	bad1, _ := wrong.EncryptString("pw-b")
	addAccountWithCipher(t, store, "acct-good", good1)
	addAccountWithCipher(t, store, "acct-bad", bad1)

	chk, err := CheckCredentials(context.Background(), store, real)
	if err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
	if chk.Decryptable != 1 || chk.Failed != 1 {
		t.Fatalf("计数不对: %+v", chk)
	}
	if chk.AllFailed() {
		t.Fatalf("有一把能解开就不该叫 AllFailed: %+v", chk)
	}
	if chk.AllDecryptable() {
		t.Fatalf("有失败就不该叫 AllDecryptable: %+v", chk)
	}
	if !strings.Contains(chk.Summary(), "partial") {
		t.Fatalf("Summary 应体现部分可解: %q", chk.Summary())
	}
}

// 一个账户都没有：不是故障。
func TestCheckCredentials_NoAccounts(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	good := newCryptoForTest(t, "good-key-material-000000")

	chk, err := CheckCredentials(context.Background(), store, good)
	if err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
	if chk.Accounts != 0 {
		t.Fatalf("不该有账户: %+v", chk)
	}
	if !chk.AllDecryptable() || chk.AllFailed() {
		t.Fatalf("0 个账户不是故障: %+v", chk)
	}
	if !strings.Contains(chk.Summary(), "no enabled email accounts") {
		t.Fatalf("Summary 不对: %q", chk.Summary())
	}
}

// 禁用（enabled=false）的账户不参与自检 —— 否则一个停用账户解不开就会报警。
func TestCheckCredentials_DisabledAccountsAreIgnored(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	real := newCryptoForTest(t, "the-real-key-00000000")
	enc, _ := real.EncryptString("pw-a")
	addAccountWithCipher(t, store, "acct-on", enc)
	acc := &Account{
		ID: "acct-off", UserID: "u1", WorkspaceID: "ws1",
		DisplayName: "off", EmailAddress: "off@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password",
		Enabled: false, CreatedAt: time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, "garbage-not-even-base64"); err != nil {
		t.Fatalf("insert off: %v", err)
	}

	// 只用 real 检查：若禁用账户被算进来，必然 Failed>0。
	chk, err := CheckCredentials(context.Background(), store, real)
	if err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
	if chk.Accounts != 1 {
		t.Fatalf("禁用账户不该参与自检: %+v", chk)
	}
	if chk.Failed != 0 {
		t.Fatalf("禁用账户的坏凭据不该算失败: %+v", chk)
	}
}
