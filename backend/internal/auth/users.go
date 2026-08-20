package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// User 表示一个用户账户。
type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"` // "admin" | "user"
	CreatedAt int64  `json:"created_at"`
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
func (s *UserStore) InsertUser(ctx context.Context, u *User, password string) error {
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
	_, err = s.pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, u.ID, u.Username, string(hash), u.Role, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}
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
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, created_at
		FROM users WHERE username = $1
	`, username).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt)
	if err != nil {
		// Generic error to prevent username enumeration
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		// Same error message to prevent username enumeration
		return nil, fmt.Errorf("invalid credentials")
	}
	return &u, nil
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
