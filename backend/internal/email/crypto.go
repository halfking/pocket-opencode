package email

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Crypto 提供 AES-256-GCM 加密/解密（用于 IMAP 密码和 OAuth token）。
type Crypto struct {
	gcm cipher.AEAD
}

// NewCrypto 用 32 字节密钥构造 Crypto。
func NewCrypto(key []byte) (*Crypto, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{gcm: gcm}, nil
}

// EncryptString 加密明文，返回 base64 编码的 ciphertext。
func (c *Crypto) EncryptString(plaintext string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString 解密 base64 编码的 ciphertext。
func (c *Crypto) DecryptString(encrypted string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	nonceSize := c.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// EnsureMasterKey 从环境变量或自动生成/持久化 key。
// envKey 是 POCKET_EMAIL_MASTER_KEY 的值（可以是 32 raw bytes 或 base64）。
// dataDir 是后端数据目录（例如 /data），用于存放 email_master.key。
// 返回 32 字节原始密钥。
func EnsureMasterKey(envKey, dataDir string) ([]byte, error) {
	if envKey != "" {
		// 尝试 base64 解码
		if decoded, err := base64.StdEncoding.DecodeString(envKey); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
		// 否则当作原始 32 字节
		if len(envKey) == 32 {
			return []byte(envKey), nil
		}
		return nil, fmt.Errorf("POCKET_EMAIL_MASTER_KEY must be 32 raw bytes or base64-encoded 32 bytes")
	}
	// 自动生成并持久化到 dataDir/email_master.key
	keyPath := filepath.Join(dataDir, "email_master.key")
	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
		return data, nil
	}
	// 生成新密钥
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dataDir, err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", keyPath, err)
	}
	return key, nil
}
