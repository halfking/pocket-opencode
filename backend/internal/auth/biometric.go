package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BiometricStore 管理用户的生物识别凭证（WebAuthn 风格的
// challenge/credential 注册与登录）。
//
// 业务边界：
//   - 每个用户可在多个设备上注册多个凭证（每条独立 credential_id）；
//   - 服务端只持久化 public_key + counter + 设备元数据，绝不存储私钥；
//   - credential_id 与 user_id + workspace_id 复合定位，删除/重命名按
//     credential_id 操作。
//
// 本文件为最小可编译骨架（Schema + CRUD），challenge/verify 签名验证
// 在 handler 侧 stub 返回 501；后续 sprint 接入 identity-go 的
// webauthn helper 即可平滑升级，存储层无需迁移。
type BiometricStore struct {
	pool *pgxpool.Pool
}

// BiometricCredential 是单条已注册凭证。
type BiometricCredential struct {
	ID           string `json:"id"`            // credential_id（COSE base64url）
	UserID       string `json:"user_id"`
	WorkspaceID  string `json:"workspace_id"`
	DeviceName   string `json:"device_name"`   // 用户给的别名（如 "iPhone 15"）
	PublicKey    []byte `json:"public_key"`    // COSE-encoded public key
	Counter      uint32 `json:"counter"`       // signature counter（防重放）
	Transports   string `json:"transports"`    // JSON 数组字符串
	CreatedAt    int64  `json:"created_at"`
	LastUsedAt   int64  `json:"last_used_at"`
}

// NewBiometricStore 构造 BiometricStore。pool 为 nil 时返回 noop（CRUD 返 not configured）。
func NewBiometricStore(pool *pgxpool.Pool) *BiometricStore {
	return &BiometricStore{pool: pool}
}

const biometricSchema = `
CREATE TABLE IF NOT EXISTS biometric_credentials (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    workspace_id  TEXT NOT NULL,
    device_name   TEXT NOT NULL DEFAULT '',
    public_key    BYTEA NOT NULL,
    counter       BIGINT NOT NULL DEFAULT 0,
    transports    TEXT NOT NULL DEFAULT '[]',
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_biometric_user ON biometric_credentials(user_id, workspace_id);
`

// Init 创建表。pool 为 nil 返回 not configured。
func (s *BiometricStore) Init(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("biometric store not configured")
	}
	_, err := s.pool.Exec(ctx, biometricSchema)
	return err
}

// ErrBiometricNotConfigured 表示 store 未初始化（pool 为 nil）。
var ErrBiometricNotConfigured = errors.New("auth: biometric store not configured")

// ErrBiometricNotFound 表示找不到指定 credential_id。
var ErrBiometricNotFound = errors.New("auth: biometric credential not found")

// Register 写入一条新凭证。同 credential_id 已存在则覆盖（升级场景）。
func (s *BiometricStore) Register(ctx context.Context, c *BiometricCredential) error {
	if s.pool == nil {
		return ErrBiometricNotConfigured
	}
	if c.ID == "" || c.UserID == "" || c.WorkspaceID == "" {
		return fmt.Errorf("id, user_id, workspace_id are required")
	}
	if len(c.PublicKey) == 0 {
		return fmt.Errorf("public_key is required")
	}
	now := time.Now().Unix()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO biometric_credentials (id, user_id, workspace_id, device_name, public_key, counter, transports, created_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			device_name  = EXCLUDED.device_name,
			public_key   = EXCLUDED.public_key,
			counter      = EXCLUDED.counter,
			transports   = EXCLUDED.transports
	`, c.ID, c.UserID, c.WorkspaceID, c.DeviceName, c.PublicKey, int64(c.Counter), c.Transports, c.CreatedAt, c.LastUsedAt)
	return err
}

// Get 按 credential_id 查一条凭证。
func (s *BiometricStore) Get(ctx context.Context, id string) (*BiometricCredential, error) {
	if s.pool == nil {
		return nil, ErrBiometricNotConfigured
	}
	var c BiometricCredential
	var counter int64
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, device_name, public_key, counter, transports, created_at, last_used_at
		FROM biometric_credentials WHERE id = $1
	`, id).Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.DeviceName, &c.PublicKey, &counter, &c.Transports, &c.CreatedAt, &c.LastUsedAt)
	if err != nil {
		return nil, ErrBiometricNotFound
	}
	c.Counter = uint32(counter)
	return &c, nil
}

// ListByUser 列出指定 user + workspace 下的所有凭证。
func (s *BiometricStore) ListByUser(ctx context.Context, userID, workspaceID string) ([]*BiometricCredential, error) {
	if s.pool == nil {
		return nil, ErrBiometricNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, device_name, public_key, counter, transports, created_at, last_used_at
		FROM biometric_credentials
		WHERE user_id = $1 AND workspace_id = $2
		ORDER BY created_at DESC
	`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BiometricCredential
	for rows.Next() {
		var c BiometricCredential
		var counter int64
		if err := rows.Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.DeviceName, &c.PublicKey, &counter, &c.Transports, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		c.Counter = uint32(counter)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// Delete 删除一条凭证。返回是否实际删除（false = credential_id 不存在）。
func (s *BiometricStore) Delete(ctx context.Context, id string) (bool, error) {
	if s.pool == nil {
		return false, ErrBiometricNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM biometric_credentials WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Rename 修改凭证的设备别名。
func (s *BiometricStore) Rename(ctx context.Context, id, deviceName string) (bool, error) {
	if s.pool == nil {
		return false, ErrBiometricNotConfigured
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE biometric_credentials SET device_name = $2 WHERE id = $1`,
		id, deviceName)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Touch 更新凭证的 counter + last_used_at（登录成功后调用）。
func (s *BiometricStore) Touch(ctx context.Context, id string, counter uint32) error {
	if s.pool == nil {
		return ErrBiometricNotConfigured
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE biometric_credentials SET counter = $2, last_used_at = $3 WHERE id = $1`,
		id, int64(counter), time.Now().Unix())
	return err
}

// NewChallengeID 生成一次性的 challenge id（base64url 32 字节）。
//
// handler 注册阶段生成并发回客户端；登录阶段回传时与 session 绑定校验。
func NewChallengeID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
