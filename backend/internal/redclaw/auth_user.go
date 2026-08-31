// Package redclaw — auth_user.go 提供 RedClaw auth-agent 镜像客户端。
//
// 设计目标：本地 (pocketd) 用户/验证码生命周期事件 → 异步镜像到 RedClaw auth-agent，
// 使 RedClaw 企业侧能感知本地账号变更。**严格 fail-soft**：RedClaw 不可用、DNS 不通、
// 超时、4xx/5xx 全部仅记日志，绝不阻塞本地业务路径。
//
// 配置：base_url（必填，鉴权共享密钥可空）+ TenantID（默认 "default"）+ TimeoutSec（默认 5）。
package redclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// AuthClientConfig 描述 RedClaw auth-agent 镜像客户端配置。
type AuthClientConfig struct {
	BaseURL    string
	Secret     string
	TenantID   string
	TimeoutSec int
}

// AuthClient 调用 RedClaw auth-agent 的镜像客户端。
// 所有方法失败均仅 log、不返回 error 给调用方；调用方无需检查。
type AuthClient struct {
	cfg    AuthClientConfig
	httpDo *http.Client
}

// NewAuthClient 构造 AuthClient。BaseURL 为空返回 nil（调用方须 nil-safe）。
func NewAuthClient(cfg AuthClientConfig) (*AuthClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("redclaw auth: BaseURL cannot be empty")
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "default"
	}
	return &AuthClient{
		cfg: AuthClientConfig{
			BaseURL:    cfg.BaseURL,
			Secret:     cfg.Secret,
			TenantID:   tenant,
			TimeoutSec: timeout,
		},
		httpDo: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// RegisterUserRequest 镜像 register 事件载荷。
type RegisterUserRequest struct {
	TenantID  string `json:"tenant_id"`
	Email     string `json:"email"`
	Username  string `json:"username,omitempty"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// RegisterUser 异步通知 RedClaw auth-agent：本地有新用户注册。
// 任何错误均仅 log。ctx 用于上游 cancel 透传。
func (c *AuthClient) RegisterUser(ctx context.Context, req RegisterUserRequest) {
	if c == nil {
		return
	}
	if req.Email == "" || req.UserID == "" {
		log.Printf("WARN: [RedClaw auth mirror] RegisterUser missing email/user_id, skip")
		return
	}
	if req.TenantID == "" {
		req.TenantID = c.cfg.TenantID
	}
	c.fire(ctx, "/api/v1/auth/users/register", req, "RegisterUser")
}

// ResetPasswordRequest 镜像 forgot-password 事件载荷。
type ResetPasswordRequest struct {
	TenantID  string `json:"tenant_id"`
	Email     string `json:"email"`
	UserID    string `json:"user_id"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

// ResetPassword 异步通知 RedClaw auth-agent：本地某用户密码已更新。
// 任何错误均仅 log。
func (c *AuthClient) ResetPassword(ctx context.Context, req ResetPasswordRequest) {
	if c == nil {
		return
	}
	if req.Email == "" || req.UserID == "" {
		log.Printf("WARN: [RedClaw auth mirror] ResetPassword missing email/user_id, skip")
		return
	}
	if req.TenantID == "" {
		req.TenantID = c.cfg.TenantID
	}
	c.fire(ctx, "/api/v1/auth/users/reset-password", req, "ResetPassword")
}

// VerifyCodeLoginRequest 镜像 code-login 事件载荷。
type VerifyCodeLoginRequest struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	UserID   string `json:"user_id"`
	LoginAt  int64  `json:"login_at,omitempty"`
}

// VerifyCodeLogin 异步通知 RedClaw auth-agent：本地某用户通过验证码登录。
// 任何错误均仅 log。
func (c *AuthClient) VerifyCodeLogin(ctx context.Context, req VerifyCodeLoginRequest) {
	if c == nil {
		return
	}
	if req.Email == "" || req.UserID == "" {
		log.Printf("WARN: [RedClaw auth mirror] VerifyCodeLogin missing email/user_id, skip")
		return
	}
	if req.TenantID == "" {
		req.TenantID = c.cfg.TenantID
	}
	c.fire(ctx, "/api/v1/auth/users/code-login", req, "VerifyCodeLogin")
}

// fire 内部统一 POST 实现：marshal → POST → 检查 status → log。
// 不返回 error：失败一律 log，调用方已承诺 fail-soft。
func (c *AuthClient) fire(ctx context.Context, path string, payload any, opName string) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("WARN: [RedClaw auth mirror] %s marshal: %v", opName, err)
		return
	}
	url := c.cfg.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARN: [RedClaw auth mirror] %s request: %v", opName, err)
		return
	}
	if c.cfg.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	}
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpDo.Do(req)
	if err != nil {
		log.Printf("WARN: [RedClaw auth mirror] %s POST %s: %v", opName, url, err)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("INFO: [RedClaw auth mirror] %s ok (%d)", opName, resp.StatusCode)
		return
	}
	log.Printf("WARN: [RedClaw auth mirror] %s POST %s -> HTTP %d (RedClaw auth-agent unavailable; ignored)", opName, url, resp.StatusCode)
}
