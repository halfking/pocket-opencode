package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ErrEmailAlreadyExists 表示 email 已被占用（pg 23505）。
var ErrEmailAlreadyExists = errors.New("email already exists")

// ErrUserNotFound 表示用户不存在。
var ErrUserNotFound = errors.New("user not found")

// User 表示一个用户账户。
type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	Role          string `json:"role"` // "admin" | "user"
	CreatedAt     int64  `json:"created_at"`
}

// UserStore 管理 users 表。
type UserStore struct {
	pool *pgxpool.Pool
}

// NewUserStore 构造 UserStore。
func NewUserStore(pool *pgxpool.Pool) (*UserStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgxpool is nil")
	}
	return &UserStore{pool: pool}, nil
}

// InsertUser 插入新用户。password 是明文，函数内部 bcrypt hash。
// email 可为空（旧用户无邮箱场景）；非空时存小写并校验唯一。
func (s *UserStore) InsertUser(ctx context.Context, u *User, password, email string) error {
	if u == nil {
		return fmt.Errorf("user cannot be nil")
	}
	if u.ID == "" {
		return fmt.Errorf("user ID cannot be empty")
	}
	if u.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if u.Role == "" {
		return fmt.Errorf("role cannot be empty")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt hash generation failed: %w", err)
	}
	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}
	emailLower := strings.ToLower(strings.TrimSpace(email))
	_, err = s.pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role, created_at, email, email_verified)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
	`, u.ID, u.Username, string(hash), u.Role, u.CreatedAt, emailLower, u.EmailVerified)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// unique violation on email_lower_uq
			return fmt.Errorf("%w: %s", ErrEmailAlreadyExists, emailLower)
		}
		return fmt.Errorf("failed to insert user: %w", err)
	}
	u.Email = emailLower
	return nil
}

// VerifyPassword 校验用户名/密码，成功返回 User。
// Returns generic error to prevent username enumeration attacks.
func (s *UserStore) VerifyPassword(ctx context.Context, username, password string) (*User, error) {
	if username == "" {
		return nil, fmt.Errorf("invalid credentials")
	}
	if password == "" {
		return nil, fmt.Errorf("invalid credentials")
	}

	var u User
	var hash string
	var email *string
	var verified bool
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, created_at, email, email_verified
		FROM users WHERE username = $1
	`, username).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt, &email, &verified)
	if err != nil {
		// Generic error to prevent username enumeration
		return nil, fmt.Errorf("invalid credentials")
	}
	if email != nil {
		u.Email = *email
	}
	u.EmailVerified = verified
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		// Same error message to prevent username enumeration
		return nil, fmt.Errorf("invalid credentials")
	}
	return &u, nil
}

// GetUserByEmail 按 email（大小写不敏感）查询用户。
func (s *UserStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	emailLower := strings.ToLower(strings.TrimSpace(email))
	if emailLower == "" {
		return nil, ErrUserNotFound
	}
	var u User
	var hash string
	var emailCol *string
	var verified bool
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, created_at, email, email_verified
		FROM users WHERE LOWER(email) = $1
	`, emailLower).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt, &emailCol, &verified)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}
	if emailCol != nil {
		u.Email = *emailCol
	}
	u.EmailVerified = verified
	return &u, nil
}

// UpdatePasswordByEmail 按 email 更新密码（忘记密码流程）。
func (s *UserStore) UpdatePasswordByEmail(ctx context.Context, email, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	emailLower := strings.ToLower(strings.TrimSpace(email))
	if emailLower == "" {
		return ErrUserNotFound
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt hash generation failed: %w", err)
	}
	cmd, err := s.pool.Exec(ctx, `
		UPDATE users SET password_hash = $1 WHERE LOWER(email) = $2
	`, string(hash), emailLower)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// CountUsers 返回 users 表行数（用于 bootstrap 检测）。
func (s *UserStore) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}
