package marketplace

// signing.go — marketplace 签名链路（ADR: docs/handoff/2026-09-05-marketplace-signing-chain-design.md）。
//
// 协议：payload = canonicalJSON(manifest) || 0x00 || ascii(digest)，
// 签名/验签双方都必须经由本文件的 CanonicalizeManifest / SigningPayload
// 生成字节，禁止各自 json.Marshal——否则 key 序与空白差异会导致验签失败。
//
// 密钥层级：root key（平台级，Store 内存配置）与 publisher key
//（publisher_signing_keys 表内 active 行）。

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// RootKeyID 是平台级 root 密钥的固定 key_id。
	RootKeyID = "root"
	// AlgEd25519 是本签名链路唯一支持的算法标识。
	AlgEd25519 = "ed25519"
)

// 签名链路 sentinel errors。命名风格与 marketplace.go 既有 sentinel 一致，
// 调用方用 errors.Is 区分失败原因（缺签名 / 验签失败 / 密钥问题 / 未启用）。
var (
	// ErrSignatureMissing 版本未签名（signature 或 signing_key_id 为空）。
	ErrSignatureMissing = errors.New("marketplace: signature missing")
	// ErrSignatureInvalid 验签失败（签名解码失败、载荷不匹配或公钥不符）。
	ErrSignatureInvalid = errors.New("marketplace: signature invalid")
	// ErrSigningKeyNotFound 指定 publisher/key_id 的密钥不存在。
	ErrSigningKeyNotFound = errors.New("marketplace: signing key not found")
	// ErrSigningKeyNotFound 的孪生：密钥存在但已吊销（fail-closed，吊销即拒签）。
	ErrSigningKeyRevoked = errors.New("marketplace: signing key revoked")
	// ErrSigningUnavailable 版本由 root 签名但 root 公钥未配置，无法验证平台签名。
	ErrSigningUnavailable = errors.New("marketplace: signing unavailable")
	// ErrInvalidPublicKey 公钥解析失败（编码不支持或长度不是 32 字节）。
	ErrInvalidPublicKey = errors.New("marketplace: invalid public key")
	// ErrUnsupportedAlg 非 ed25519 的算法标识，一律拒绝。
	ErrUnsupportedAlg = errors.New("marketplace: unsupported signing alg")
)

// CanonicalizeManifest 生成 Manifest 的 canonical JSON：递归排序 object key、
// 紧凑分隔符、不做 HTML 转义。中间必须经过 struct 往返（Marshal → Unmarshal →
// interface{}），这样签名只覆盖真正会被序列化的字段，且与 JSONB 落库格式解耦。
func CanonicalizeManifest(m Manifest) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marketplace: marshal manifest: %w", err)
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("marketplace: unmarshal manifest: %w", err)
	}
	return canonicalJSONValue(decoded)
}

// canonicalJSONValue 递归地把解码后的 JSON 值写成 canonical 字节：
// object 按 key 字典序展开，数组保持原序，标量经 json.Encoder 序列化。
func canonicalJSONValue(v interface{}) ([]byte, error) {
	switch typed := v.(type) {
	case map[string]interface{}:
		// key 排序是 canonical 化的核心：消除 map 遍历/落库的 key 序歧义。
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf := bytes.NewBuffer(nil)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyBytes, err := canonicalScalar(k)
			if err != nil {
				return nil, err
			}
			buf.Write(keyBytes)
			buf.WriteByte(':')
			valBytes, err := canonicalJSONValue(typed[k])
			if err != nil {
				return nil, err
			}
			buf.Write(valBytes)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	case []interface{}:
		buf := bytes.NewBuffer(nil)
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			itemBytes, err := canonicalJSONValue(item)
			if err != nil {
				return nil, err
			}
			buf.Write(itemBytes)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		return canonicalScalar(v)
	}
}

// canonicalScalar 用 json.Encoder 序列化标量（字符串/bool/数字/null）。
// SetEscapeHTML(false)：canonical 字节不做 HTML 转义，避免 <,>,& 被改写；
// Encoder 会在末尾追加换行符，必须去掉，否则签名字节两端不一致。
func canonicalScalar(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("marketplace: canonical encode: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// SigningPayload 构造待签名字节：canonical(manifest) ‖ 0x00 ‖ digest(ASCII)。
// 0x00 分隔符防止 canonical JSON 与 digest 拼接产生歧义（JSON 文本不会含 0x00）。
func SigningPayload(m Manifest, digest string) ([]byte, error) {
	canonical, err := CanonicalizeManifest(m)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, 0, len(canonical)+1+len(digest))
	payload = append(payload, canonical...)
	payload = append(payload, 0x00)
	payload = append(payload, digest...)
	return payload, nil
}

// ParsePublicKey 解析 base64（std/raw/url）或 hex 编码的 ed25519 公钥。
// 依次尝试各编码，取第一个恰好解码出 32 字节的结果——hex 串同时也是合法
// base64 字母表，必须靠"32 字节"这个硬约束排除错误解码分支。
func ParsePublicKey(raw string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(raw)
	decoders := []struct {
		name   string
		decode func(string) ([]byte, error)
	}{
		{"base64", base64.StdEncoding.DecodeString},
		{"base64 raw", base64.RawStdEncoding.DecodeString},
		{"base64 url", base64.URLEncoding.DecodeString},
		{"hex", hex.DecodeString},
	}
	for _, d := range decoders {
		decoded, err := d.decode(trimmed)
		if err != nil {
			continue
		}
		if len(decoded) == ed25519.PublicKeySize {
			return ed25519.PublicKey(decoded), nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not a %d-byte ed25519 public key (base64/hex)", ErrInvalidPublicKey, trimmed, ed25519.PublicKeySize)
}

// SignVersion 对 SigningPayload 做 ed25519 签名，返回 base64 std 字符串
// （marketplace_versions.signature 的存储格式）。测试与签名工具共用。
func SignVersion(priv ed25519.PrivateKey, m Manifest, digest string) (string, error) {
	payload, err := SigningPayload(m, digest)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifySignature 验证 base64 签名。任何失败（解码失败、公钥长度不对、
// ed25519.Verify 不通过）统一收敛为 ErrSignatureInvalid，避免调用方逐类处理。
func VerifySignature(pub ed25519.PublicKey, m Manifest, digest, sigB64 string) error {
	if sigB64 == "" {
		return fmt.Errorf("%w: empty signature", ErrSignatureInvalid)
	}
	// 存储格式是 base64 std，但容忍 URL 变体（'-'/'_'），减少无谓的格式摩擦。
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		sig, err = base64.URLEncoding.DecodeString(sigB64)
	}
	if err != nil {
		return fmt.Errorf("%w: decode signature: %v", ErrSignatureInvalid, err)
	}
	payload, err := SigningPayload(m, digest)
	if err != nil {
		return err
	}
	// ed25519.Verify 对错误长度的公钥会 panic，必须先自行校验长度。
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key has %d bytes", ErrSignatureInvalid, len(pub))
	}
	if !ed25519.Verify(pub, payload, sig) {
		return fmt.Errorf("%w: ed25519 verify failed", ErrSignatureInvalid)
	}
	return nil
}

// SetRootPublicKey 配置平台级 root 公钥（base64/hex 编码）。
// raw 为空 → 清除配置，签名强制回到 disabled（"仅记录"语义）；
// enabled 的判定即 RootKeyConfigured()。
func (s *Store) SetRootPublicKey(raw string) error {
	if strings.TrimSpace(raw) == "" {
		s.rootPubKey = nil
		s.rootKeyConfigured = false
		return nil
	}
	pub, err := ParsePublicKey(raw)
	if err != nil {
		return err
	}
	s.rootPubKey = pub
	s.rootKeyConfigured = true
	return nil
}

// RootKeyConfigured 报告签名强制是否启用（root 公钥已配置）。
func (s *Store) RootKeyConfigured() bool {
	return s.rootKeyConfigured
}

// verifyWithKeyID 按 signing_key_id 选择信任锚并验签：
//   - root → 使用内存配置的平台公钥（不查库，未配置即 ErrSigningUnavailable）；
//   - 其他 → 查 publisher_signing_keys，无行 / 非 active / 非 ed25519 分别报错，
//     确保吊销与算法白名单都 fail-closed。
func (s *Store) verifyWithKeyID(ctx context.Context, publisher, keyID string, m Manifest, digest, sigB64 string) error {
	if keyID == RootKeyID {
		if !s.rootKeyConfigured {
			return fmt.Errorf("%w: root public key not configured", ErrSigningUnavailable)
		}
		return VerifySignature(s.rootPubKey, m, digest, sigB64)
	}

	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	var (
		rawPub []byte
		alg    string
		status string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT public_key, alg, status FROM publisher_signing_keys
		WHERE publisher_id = $1 AND key_id = $2
	`, publisher, keyID).Scan(&rawPub, &alg, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: publisher %q key %q", ErrSigningKeyNotFound, publisher, keyID)
	}
	if err != nil {
		return err
	}
	if status != "active" {
		// 吊销行保留作审计，但验签一律拒绝。
		return fmt.Errorf("%w: key %q status %q", ErrSigningKeyRevoked, keyID, status)
	}
	if alg != AlgEd25519 {
		return fmt.Errorf("%w: key %q alg %q", ErrUnsupportedAlg, keyID, alg)
	}
	if len(rawPub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: stored key has %d bytes", ErrInvalidPublicKey, len(rawPub))
	}
	return VerifySignature(ed25519.PublicKey(rawPub), m, digest, sigB64)
}

// VerifySubmission 校验一次提交（Submit 强制点）携带的签名。
// 签名或 key_id 缺失即 ErrSignatureMissing，其余交给 verifyWithKeyID。
func (s *Store) VerifySubmission(ctx context.Context, publisher string, m Manifest, digest, sigB64, keyID string) error {
	if sigB64 == "" || keyID == "" {
		return fmt.Errorf("%w: signature and signing_key_id are required", ErrSignatureMissing)
	}
	return s.verifyWithKeyID(ctx, publisher, keyID, m, digest, sigB64)
}

// VerifyVersion 基于存储态校验一个版本的签名（Publish 强制点，防止
// 提交后换 key / 吊销窗口）。manifest 必须经 struct 往返再规范化：
// JSONB 落库字节与提交方字节不保证一致，struct 往返 + CanonicalizeManifest
// 才能与 SignVersion 的协议字节对齐。
func (s *Store) VerifyVersion(ctx context.Context, versionID string) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	var (
		manifestJSON []byte
		digest       string
		signature    *string
		keyID        *string
		publisher    string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT v.manifest, v.digest, v.signature, v.signing_key_id, p.publisher
		FROM marketplace_versions v
		JOIN marketplace_packages p ON v.package_id = p.package_id
		WHERE v.version_id = $1
	`, versionID).Scan(&manifestJSON, &digest, &signature, &keyID, &publisher)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMarketplaceNotFound
	}
	if err != nil {
		return err
	}
	if signature == nil || *signature == "" || keyID == nil || *keyID == "" {
		return fmt.Errorf("%w: version %q is not signed", ErrSignatureMissing, versionID)
	}

	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return fmt.Errorf("marketplace: unmarshal manifest: %w", err)
	}
	return s.verifyWithKeyID(ctx, publisher, *keyID, m, digest, *signature)
}

// PublisherKey 是 publisher_signing_keys 的一行投影（HTTP 列表用）。
// PublicKey 为 base64 std 编码的原始 32 字节公钥——公钥非机密，回传便于
// 客户端在本地自行验签；吊销行保留（status=revoked），供审计展示。
type PublisherKey struct {
	PublisherID string     `json:"publisher_id"`
	KeyID       string     `json:"key_id"`
	Alg         string     `json:"alg"`
	Status      string     `json:"status"`
	PublicKey   string     `json:"public_key"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// ListPublisherKeys 列出一个 publisher 的全部签名公钥（含已吊销行，
// 按 created_at、key_id 排序）。publisher 无任何密钥 → 空切片而非错误。
func (s *Store) ListPublisherKeys(ctx context.Context, publisherID string) ([]PublisherKey, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("marketplace: pool not configured")
	}
	if publisherID == "" {
		return nil, errors.New("marketplace: publisher_id required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT publisher_id, key_id, alg, status, public_key, created_at, revoked_at
		FROM publisher_signing_keys
		WHERE publisher_id = $1
		ORDER BY created_at, key_id
	`, publisherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []PublisherKey{}
	for rows.Next() {
		var (
			k         PublisherKey
			rawPub    []byte
			createdAt int64
			revokedAt *int64
		)
		if err := rows.Scan(&k.PublisherID, &k.KeyID, &k.Alg, &k.Status, &rawPub, &createdAt, &revokedAt); err != nil {
			return nil, err
		}
		k.PublicKey = base64.StdEncoding.EncodeToString(rawPub)
		k.CreatedAt = time.Unix(createdAt, 0)
		if revokedAt != nil {
			t := time.Unix(*revokedAt, 0)
			k.RevokedAt = &t
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RegisterPublisherKey 注册 publisher 公钥（active 状态）。重复注册同一
// (publisher_id, key_id) 触发唯一键冲突，收敛为 ErrMarketplaceConflict。
func (s *Store) RegisterPublisherKey(ctx context.Context, publisherID, keyID, publicKeyRaw, alg string) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	if publisherID == "" || keyID == "" || publicKeyRaw == "" {
		return errors.New("marketplace: publisher_id, key_id and public_key are required")
	}
	// 算法白名单在解析公钥前校验：非 ed25519 一律拒绝，不做隐式转换。
	if alg != AlgEd25519 {
		return fmt.Errorf("%w: alg %q", ErrUnsupportedAlg, alg)
	}
	pub, err := ParsePublicKey(publicKeyRaw)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO publisher_signing_keys (publisher_id, key_id, public_key, alg, status, created_at)
		VALUES ($1, $2, $3, $4, 'active', $5)
	`, publisherID, keyID, []byte(pub), alg, time.Now().Unix())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: publisher %q already has key %q", ErrMarketplaceConflict, publisherID, keyID)
		}
		return err
	}
	return nil
}

// RevokePublisherKey 吊销 publisher 密钥：行保留（审计），status 置 revoked。
// 只允许从 active 吊销；没有可吊销的 active 行（不存在或已吊销）→ NotFound。
func (s *Store) RevokePublisherKey(ctx context.Context, publisherID, keyID string) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE publisher_signing_keys SET status = 'revoked', revoked_at = $3
		WHERE publisher_id = $1 AND key_id = $2 AND status = 'active'
	`, publisherID, keyID, time.Now().Unix())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMarketplaceNotFound
	}
	return nil
}
