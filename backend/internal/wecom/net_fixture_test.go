package wecom

import (
	"os"
	"testing"
)

// TestDecryptFromNetFixture 用 .NET 独立实现的产物验证 Go 侧解密。
//
// 存在的理由：本地打真进程时出现过「签名过了但解密报 PKCS#7 非法」。
// 那次要区分的是两种完全不同的原因：
//
//	(a) Go 侧解密与 .NET 加解密口径不同（真 bug）
//	(b) 跑着的进程根本没拿到同一个 EncodingAESKey（环境问题）
//
// 单元测试里 Encrypt/Decrypt 同源，两者会互为自证；把 .NET 的产物当固定
// 输入钉在这里，(a) 会红而 (b) 不会——这是唯一能分开它们的判据。
//
// 固定输入由 scripts\wecom-live-check.ps1 里同样的 .NET 逻辑产出，
// 密钥 jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAJHu8vY6NnH2k / receiveid ww5823bf96d3bd5549。
func TestDecryptFromNetFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_ciphertext.txt")
	if err != nil {
		t.Skipf("缺少 .NET fixture（先跑 scripts/wecom-net-fixture.ps1 生成）: %v", err)
	}
	cipherText := trimSpace(string(raw))
	const want = "1616140317555161061"
	got, err := Decrypt("jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAJHu8vY6NnH2k", cipherText, "ww5823bf96d3bd5549")
	if err != nil {
		t.Fatalf("Decrypt(.NET 产物): %v", err)
	}
	if got != want {
		t.Fatalf("明文 = %q, want %q", got, want)
	}
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == '\n' || s[start] == '\r' || s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
