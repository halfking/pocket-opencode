package email

import (
	"strings"
	"testing"
)

// TestCryptoRoundTrip 验证 AES-GCM 加解密往返，并确保 nonce 随机化让两次
// 加密相同明文得到不同密文。
func TestCryptoRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}
	cases := []string{"hello", "", "中文密码123", strings.Repeat("a", 4096)}
	for _, plaintext := range cases {
		enc, err := c.EncryptString(plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		dec, err := c.DecryptString(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if dec != plaintext {
			t.Fatalf("roundtrip mismatch: want %q, got %q", plaintext, dec)
		}
	}
	enc1, _ := c.EncryptString("same")
	enc2, _ := c.EncryptString("same")
	if enc1 == enc2 {
		t.Fatalf("nonce should be random; got identical ciphertexts")
	}
}

// TestCryptoWrongKey 验证错误密钥解密会失败。
func TestCryptoWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 0xff
	c1, _ := NewCrypto(key1)
	c2, _ := NewCrypto(key2)
	enc, err := c1.EncryptString("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := c2.DecryptString(enc); err == nil {
		t.Fatalf("decrypt with wrong key should fail")
	}
}

// TestCryptoInvalidKeyLength 验证 NewCrypto 拒绝错误长度的 key。
func TestCryptoInvalidKeyLength(t *testing.T) {
	if _, err := NewCrypto(make([]byte, 16)); err == nil {
		t.Fatalf("expected error for 16-byte key")
	}
}

// TestEnsureMasterKeyAutoGen 验证 dataDir 下生成并复用密钥。
func TestEnsureMasterKeyAutoGen(t *testing.T) {
	dir := t.TempDir()
	k1, err := EnsureMasterKey("", dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(k1) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(k1))
	}
	k2, err := EnsureMasterKey("", dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if string(k1) != string(k2) {
		t.Fatalf("auto-generated key should be persisted; got different bytes")
	}
}

func TestEnsureMasterKeyInvalidEnv(t *testing.T) {
	if _, err := EnsureMasterKey("not-32-bytes", t.TempDir()); err == nil {
		t.Fatalf("expected error for invalid env key length")
	}
}