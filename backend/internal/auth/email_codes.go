package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// CodePurpose 验证码用途。
type CodePurpose string

const (
	PurposeRegister CodePurpose = "register"
	PurposeReset    CodePurpose = "reset"
	PurposeLogin    CodePurpose = "login"
)

// Valid returns true if the purpose is one of the known constants.
func (p CodePurpose) Valid() bool {
	switch p {
	case PurposeRegister, PurposeReset, PurposeLogin:
		return true
	}
	return false
}

// 频控参数：60 秒 1 次（同邮箱 + 同 purpose），每天 10 次。
const (
	codeMinInterval = 60 * time.Second
	codeDailyMax    = 10
	codeTTL         = 5 * time.Minute
	codeLen         = 6
)

// ErrCodeInvalid 表示验证码错误或已过期（统一对外文案）。
var ErrCodeInvalid = errors.New("验证码错误或已过期")

// ErrRateLimited 表示触发频控（60s 内重复发送或每天 10 次上限）。
var ErrRateLimited = errors.New("请求过于频繁，请稍后再试")

// ErrEmailInvalid 表示邮箱格式非法。
var ErrEmailInvalid = errors.New("邮箱格式无效")

// CodeStore 管理 email_verification_codes 表。
type CodeStore struct {
	pool *pgxpool.Pool
}

// NewCodeStore 构造 CodeStore。
func NewCodeStore(pool *pgxpool.Pool) (*CodeStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgxpool is nil")
	}
	return &CodeStore{pool: pool}, nil
}

// Generate 生成 6 位数字验证码，bcrypt hash 入库。
//
//   - 同邮箱 + 同 purpose：60 秒内重复发送返回 ErrRateLimited
//   - 同邮箱 + 同 purpose：每天成功发送次数 ≥ 10 返回 ErrRateLimited
//   - requestIP 用于审计（可空）
//
// 返回 plaintext 仅用于本次发送，handler 决定是否回显（debug 模式）。
func (s *CodeStore) Generate(ctx context.Context, email string, purpose CodePurpose, requestIP string) (plaintext string, ttlSec int, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !looksLikeEmail(email) {
		return "", 0, ErrEmailInvalid
	}
	if !purpose.Valid() {
		return "", 0, fmt.Errorf("invalid purpose: %q", purpose)
	}

	// 频控：上次成功发送 < 60s
	var lastSent *time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT MAX(created_at) FROM email_verification_codes
		WHERE LOWER(email) = $1 AND purpose = $2
	`, email, string(purpose)).Scan(&lastSent)
	if err != nil {
		return "", 0, fmt.Errorf("failed to query last sent: %w", err)
	}
	if lastSent != nil && time.Since(*lastSent) < codeMinInterval {
		return "", 0, ErrRateLimited
	}

	// 频控：今天 0 点起成功发送次数
	startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
	var todayCount int
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM email_verification_codes
		WHERE LOWER(email) = $1 AND purpose = $2 AND created_at >= $3
	`, email, string(purpose), startOfDay).Scan(&todayCount)
	if err != nil {
		return "", 0, fmt.Errorf("failed to query daily count: %w", err)
	}
	if todayCount >= codeDailyMax {
		return "", 0, ErrRateLimited
	}

	// 生成 6 位数字
	code, err := genNumericCode(codeLen)
	if err != nil {
		return "", 0, fmt.Errorf("failed to generate code: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, fmt.Errorf("bcrypt hash: %w", err)
	}

	expires := time.Now().Add(codeTTL)
	ip := strings.TrimSpace(requestIP)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO email_verification_codes (email, purpose, code_hash, expires_at, request_ip)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
	`, email, string(purpose), string(hash), expires, ip)
	if err != nil {
		return "", 0, fmt.Errorf("failed to insert code: %w", err)
	}
	return code, int(codeTTL.Seconds()), nil
}

// Verify 校验验证码，成功则原子标记 used。
// 失败统一返回 ErrCodeInvalid（不暴露过期/错误/已用差异，防探测）。
func (s *CodeStore) Verify(ctx context.Context, email string, purpose CodePurpose, plaintext string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	plaintext = strings.TrimSpace(plaintext)
	if !looksLikeEmail(email) {
		return ErrCodeInvalid
	}
	if !purpose.Valid() || plaintext == "" {
		return ErrCodeInvalid
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, code_hash FROM email_verification_codes
		WHERE LOWER(email) = $1 AND purpose = $2
		  AND used_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
		LIMIT 5
	`, email, string(purpose))
	if err != nil {
		return fmt.Errorf("failed to query codes: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id    int64
		hash  string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			return fmt.Errorf("scan code: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate codes: %w", err)
	}

	for _, c := range candidates {
		if err := bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(plaintext)); err == nil {
			// 命中：原子标记 used
			tag, err := s.pool.Exec(ctx, `
				UPDATE email_verification_codes SET used_at = NOW()
				WHERE id = $1 AND used_at IS NULL
			`, c.id)
			if err != nil {
				return fmt.Errorf("mark used: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return ErrCodeInvalid
			}
			return nil
		}
	}
	return ErrCodeInvalid
}

func genNumericCode(n int) (string, error) {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		// 0-9
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		out[i] = byte('0' + n.Int64())
	}
	// 第一位不能为 0（用户体验）
	if out[0] == '0' {
		out[0] = '1'
	}
	return string(out), nil
}

func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at >= len(s)-2 {
		return false
	}
	if strings.IndexByte(s[at+1:], '.') < 0 {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			return false
		}
	}
	return true
}
