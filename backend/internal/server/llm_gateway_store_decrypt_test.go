package server

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// decryptAPIKey 的护栏必须有直接用例钉死。252 实测的 crash-loop 病根就是这条
// 链路：master key 文件丢失 → emailCrypto 为 nil → LoadConfig → decryptAPIKey
// → 在 nil receiver 上解引用 gcm（SIGSEGV）→ 容器 crash-loop。
// 3ea18409 在构造器与调用方加了防线，这里把 store 自身的两道分支也锁住：
//
//	1. cipher 为 nil 时返回 error（按「未配置」降级），绝不 panic；
//	2. 解密失败（key 轮换后 GCM 校验不过）返回 error，把密文行留给
//	   EnsureLLMGatewayDefaults / LoadLLMGatewayFromDB 去告警 + 自愈；
//	3. recover 兜底后必须返回非 nil error：默认返回值 ("", nil) 会把一次
//	   崩溃伪装成「解密成功但 key 为空」的成功加载。
//
// 这三个用例都是纯内存单测，不碰 PG（见 pg_test_isolation_guard_test.go）。

// panickingCipher 复现 cipher 内部字段为 nil 时的 SIGSEGV 路径：
// 接口非 nil，但方法一进来就在 nil gcm 上崩。
type panickingCipher struct{}

func (panickingCipher) EncryptString(string) (string, error) { return "", nil }
func (panickingCipher) DecryptString(string) (string, error) { panic("nil gcm dereference") }

func newTestGatewayCipher(t *testing.T) *email.Crypto {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cr, err := email.NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	return cr
}

func TestDecryptAPIKey_NilCipherReturnsErrorNotPanic(t *testing.T) {
	t.Parallel()
	// 绕过构造器直构（构造器本身已拒绝 nil cipher，见 3ea18409）；
	// 这里锁的是 decryptAPIKey 作为最后一道防线的兜底行为。
	s := &LLMGatewayStore{}
	key, err := s.decryptAPIKey("c2FtcGxlLWNpcGhlcnRleHQ=")
	if err == nil {
		t.Fatal("nil cipher 时 decryptAPIKey 返回了 nil error；" +
			"master key 不可用必须按「未配置」报错，而不是 panic 或返回空 key")
	}
	if key != "" {
		t.Fatalf("nil cipher 时 decryptAPIKey 返回了 key 内容 %q", key)
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v，期望提示未配置，让运维能定位到 master key 问题", err)
	}
}

func TestDecryptAPIKey_CipherPanicBecomesExplicitError(t *testing.T) {
	t.Parallel()
	s := &LLMGatewayStore{cipher: panickingCipher{}}
	key, err := s.decryptAPIKey("c2FtcGxl")
	if err == nil {
		t.Fatal("cipher panic 被 recover 后返回了 (\"\", nil)；" +
			"把崩溃伪装成「解密成功但 key 为空」正好制造空 key 配置，" +
			"调用方的自愈分支（err != nil）也永远不会触发")
	}
	if key != "" {
		t.Fatalf("cipher panic 后 decryptAPIKey 返回了 key 内容 %q", key)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v，期望 panic 被转成显式 error", err)
	}
}

func TestDecryptAPIKey_UndecryptableCiphertextReturnsError(t *testing.T) {
	t.Parallel()
	s := &LLMGatewayStore{cipher: newTestGatewayCipher(t)}
	// 合法 base64 但不是本 cipher 的 GCM 密文（key 轮换后的典型形态：
	// 行还在、格式对，就是解不开）。
	garbage := base64.StdEncoding.EncodeToString([]byte("not a real gcm ciphertext"))
	key, err := s.decryptAPIKey(garbage)
	if err == nil {
		t.Fatal("解不开的密文返回了 nil error；必须报错让调用方走「未配置」降级，" +
			"而不是把垃圾当 key 传播给 OpenCode")
	}
	if key != "" {
		t.Fatalf("解密失败时 decryptAPIKey 返回了 key 内容 %q", key)
	}
}

func TestDecryptAPIKey_EmptyCiphertextAndRoundTrip(t *testing.T) {
	t.Parallel()
	cr := newTestGatewayCipher(t)
	s := &LLMGatewayStore{cipher: cr}

	// 空串 = 「未配置」，直接短路，不需要碰 cipher。
	if key, err := s.decryptAPIKey(""); err != nil || key != "" {
		t.Fatalf("decryptAPIKey(\"\") = (%q, %v)，想要 (\"\", nil)", key, err)
	}

	// 对照组：同一把 cipher 加密→解密必须还原，证明上面「解不开」用例
	// 的失败来自密文本身，而不是测试夹具的 cipher 是坏的。
	enc, err := cr.EncryptString("sk-roundtrip-check")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	got, err := s.decryptAPIKey(enc)
	if err != nil || got != "sk-roundtrip-check" {
		t.Fatalf("round-trip decryptAPIKey = (%q, %v)，想要还原明文", got, err)
	}
}
