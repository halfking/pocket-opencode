package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// gatewayKeyCipher implements apiKeyCipher using a deterministic key derived
// from a deployment secret (JWT secret), so that SQLite fallback deployments
// keep at-rest encryption without requiring POCKET_EMAIL_MASTER_KEY to be set.
type gatewayKeyCipher struct {
	key [32]byte
}

// NewGatewayCipher derives a cipher keyed by the provided secret.
// Secret must be a non-empty string; empty input returns nil.
func NewGatewayCipher(secret string) *gatewayKeyCipher {
	if secret == "" {
		return nil
	}
	// Deterministic key derivation (domain-separated HMAC-like expansion of secret)
	digest := sha256.Sum256(append([]byte("gatewayconfig:"), secret...))
	return &gatewayKeyCipher{key: digest}
}

func (g *gatewayKeyCipher) EncryptString(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(g.key[:])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ct := aesgcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

func (g *gatewayKeyCipher) DecryptString(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	if len(data) < 12+16 {
		return "", fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(g.key[:])
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := aesgcm.Open(nil, data[:12], data[12:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
