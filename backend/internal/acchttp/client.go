// Package acchttp 提供面向 ACC（acc.kxpms.cn）的轻量级 HTTP 客户端骨架，
// 集中处理身份、超时、请求 ID、取消、重试上限与日志脱敏。
//
// 适用范围：
//   - 任何需要直接访问 ACC REST/HTTP 端点（非 MCP JSON-RPC 路径）的组件。
//   - mcp.Client（internal/mcp）已经封装了 MCP 协议的 HMAC JWT 流程，
//     不在本包范围内。
//
// 设计要点：
//   - API key/secret 仅出现在 Authorization 头，绝不写日志（client.Do 中
//     redacted）。
//   - 每个请求都会自动生成 X-Pocket-Request-ID（如果调用方未提供）。
//   - 重试仅对幂等方法（GET/HEAD）和 5xx/网络错误生效；4xx 立即失败。
//   - context 取消立刻终止；HTTP 客户端超时为硬上限。
package acchttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Config 控制客户端行为。BaseURL 必填；其他字段有合理默认值。
type Config struct {
	BaseURL      string        // 例 https://acc.kxpms.cn
	APIKey       string        // Bearer token；用于 Authorization 头
	TenantID     string        // X-Pocket-Tenant 头
	Timeout      time.Duration // 单次 HTTP 请求超时（默认 15s）
	MaxRetries   int           // 重试上限（默认 2，共 3 次）
	RetryBackoff time.Duration // 重试退避基础（默认 200ms，指数退避）
	UserAgent    string        // 自定义 UA（默认 pocketd-acchttp/1.0）
}

// Validate 确保关键字段非空。返回的 error 字符串不包含 APIKey 等敏感值。
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("acchttp: config is nil")
	}
	if c.BaseURL == "" {
		return errors.New("acchttp: BaseURL is required")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("acchttp: BaseURL must be http(s)")
	}
	return nil
}

// Client 是线程安全的；可被多个 goroutine 共享。
type Client struct {
	cfg       Config
	http      *http.Client
	requestID atomic.Int64
	// breaker 留位：生产可加 circuit breaker；本骨架不实现以保持依赖最少。
}

// New 创建 Client；cfg 不通过 Validate 时返回 error。
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.MaxRetries > 5 {
		cfg.MaxRetries = 5 // 硬上限，避免误配导致长时间重试
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 200 * time.Millisecond
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "pocketd-acchttp/1.0"
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	return &Client{
		cfg:  cfg,
		http: &http.Client{Transport: transport},
	}, nil
}

// Do 执行一次 HTTP 请求（已包含自动重试）。不直接暴露 http.Client.Do。
//
// method 必须是标准 HTTP 动词；body 任意可被 json.Marshal 的对象；nil 表示无 body。
func (c *Client) Do(ctx context.Context, method, path string, headers map[string]string, body interface{}) (*Response, error) {
	if c == nil {
		return nil, errors.New("acchttp: client is nil")
	}
	// 不在共享 cfg 上原地改写 MaxRetries:之前实现并发调用时 goroutine A 写
	// POST 把 cfg.MaxRetries 改 0,goroutine B 并发的 GET 会拿到改后的值,
	// 导致 GET 永远不重试。改为局部变量。
	maxRetries := c.cfg.MaxRetries
	if !isSafeMethod(method) {
		maxRetries = 0
	}

	fullURL, err := c.resolveURL(path)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避，但尊重 ctx 取消。
			delay := c.cfg.RetryBackoff * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := c.buildRequest(ctx, method, fullURL, headers, body)
		if err != nil {
			return nil, err
		}

		start := time.Now()
		resp, err := c.http.Do(req)
		duration := time.Since(start)
		if err != nil {
			lastErr = err
			// 网络错误：除 ctx 取消外都重试。
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			log.Printf("[acchttp] %s %s attempt=%d error=%v duration=%s",
				method, redactPath(fullURL), attempt+1, err, duration)
			continue
		}

		// 4xx：失败但不重试；5xx：可重试。
		if resp.StatusCode >= 400 {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			lastErr = fmt.Errorf("acchttp: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
			if resp.StatusCode < 500 {
				return nil, lastErr
			}
			log.Printf("[acchttp] %s %s attempt=%d status=%d duration=%s",
				method, redactPath(fullURL), attempt+1, resp.StatusCode, duration)
			continue
		}

		return &Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       resp.Body,
			RequestID:  req.Header.Get("X-Pocket-Request-ID"),
		}, nil
	}
	return nil, fmt.Errorf("acchttp: retries exhausted: %w", lastErr)
}

// Get / Post 便捷方法。
func (c *Client) Get(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.Do(ctx, http.MethodGet, path, headers, nil)
}

func (c *Client) Post(ctx context.Context, path string, headers map[string]string, body interface{}) (*Response, error) {
	return c.Do(ctx, http.MethodPost, path, headers, body)
}

// Response 包装 http.Response，让调用方只读关键字段。
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	RequestID  string
}

// Decode 把 body 解析为 dst；关闭 body。
func (r *Response) Decode(dst interface{}) error {
	if r == nil || r.Body == nil {
		return errors.New("acchttp: response body is nil")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	return dec.Decode(dst)
}

// buildRequest 构造一次 HTTP 请求；自动注入 Authorization / Tenant / Request ID 头。
func (c *Client) buildRequest(ctx context.Context, method, fullURL string, headers map[string]string, body interface{}) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("acchttp: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}

	reqID := c.nextRequestID()
	req.Header.Set("X-Pocket-Request-ID", reqID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		// 仅设置 Authorization 头；调用方不应该自己再加一份。
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	if c.cfg.TenantID != "" {
		req.Header.Set("X-Pocket-Tenant", c.cfg.TenantID)
	}
	for k, v := range headers {
		// 不允许覆盖鉴权头与 request id；HTTP header 大小写不敏感，
		// 必须用 EqualFold 比对,否则小写的 "authorization" 会绕开检查并
		// 注入第二个 Authorization 头。
		if isReservedHeader(k) {
			continue
		}
		req.Header.Set(k, v)
	}
	return req, nil
}

// isReservedHeader 列出不允许调用方覆盖的 header(大小写不敏感)。
func isReservedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "x-pocket-request-id", "x-pocket-tenant":
		return true
	default:
		return false
	}
}

func (c *Client) resolveURL(path string) (string, error) {
	base, err := url.Parse(c.cfg.BaseURL)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	// 显式拒绝绝对 URL — 若 caller 误把外部 URL 当 path 传入,
	// url.ResolveReference 会原样返回该 URL,绕过 BaseURL 沙箱,导致
	// SSRF / 跨域走私。强制 path 必须为相对路径。
	if rel.IsAbs() {
		return "", errors.New("acchttp: path must be relative")
	}
	return base.ResolveReference(rel).String(), nil
}

// nextRequestID 生成形如 "req-<base36>-<hex>" 的请求 ID，
// 用于全链路追踪。
func (c *Client) nextRequestID() string {
	seq := c.requestID.Add(1)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("req-%d-%s", seq, hex.EncodeToString(b))
}

func isSafeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// redactPath 把 query、UserInfo、Fragment 全部剥离再写日志,
// 避免误把 tenant / api_key 之类的明文参数、URL 内嵌凭据落盘。
func redactPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<unparseable-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	if u.Path == "" {
		return "/"
	}
	return u.Path
}
