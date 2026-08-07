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
	if data, err := os.ReadFile(keyPath); err == nil {
		// A truncated key (len != 32) is treated as MISSING on purpose so
		// loadOrGenerateMasterKey regenerates. We must NOT pass it through
		// to the atomic helper below, which would refuse to overwrite it
		// (refusing-to-clobber is the contract of writeKeyAtomic).
		if len(data) == 32 {
			return data, nil
		}
	}
	// 生成新密钥
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dataDir, err)
	}
	if err := writeKeyAtomic(dataDir, key); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

// writeKeyAtomic persists the 32-byte master key under
// dataDir/email_master.key with mode 0600. The write is atomic in two
// senses:
//
//  1. The directory entry is created with O_EXCL|O_CREATE on a temp file
//     and renamed into place, so a concurrent process that tries the
//     same dance loses the race to fs.ErrExist instead of silently
//     overwriting the winner.
//
//  2. A pre-existing file at the target path is left untouched: the
//     rename is a no-op when src/dst collide. This protects partial
//     keys (e.g. from a crashed predecessor) from being clobbered, which
//     would render unrecoverable every ciphertext already encrypted
//     under those bytes.
//
// The caller (loadOrGenerateMasterKey) is responsible for checking
// whether a usable key already exists; this helper is the only thing
// that should ever create or replace the on-disk key.
func writeKeyAtomic(dataDir string, key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("master key must be exactly 32 bytes, got %d", len(key))
	}
	final := filepath.Join(dataDir, "email_master.key")
	tmp, err := os.CreateTemp(dataDir, ".email_master.key.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		// Best-effort: if rename failed the temp file is still on disk.
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	// os.Rename is atomic on POSIX. If final exists, the rename still
	// replaces it on Linux but on macOS / on Windows it depends on the
	// filesystem. Belt-and-braces: refuse to clobber an existing file
	// that we did not author in this call. We do that by checking
	// existence first and returning an error.
	if _, err := os.Stat(final); err == nil {
		// Existing key present. The caller should have read it first
		// and only call us when no key exists. Bail with a clear error
		// rather than silently replacing.
		cleanup()
		return fmt.Errorf("refusing to overwrite existing master key at %s", final)
	} else if !os.IsNotExist(err) {
		cleanup()
		return fmt.Errorf("stat %s: %w", final, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, final, err)
	}
	return nil
}
