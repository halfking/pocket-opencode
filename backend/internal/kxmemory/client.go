// Package kxmemory provides a Go client for the kxmemory FastAPI service.
//
// kxmemory 负责 AI 编排（笔记分类/SSOT/邮件总结），pocketd 在龙虾架构
// Phase C 后定位为无状态网关，本客户端只做 HTTP 转发，不持久化用户数据。
//
// API 契约见 docs/2026-07-02-kxmemory-api-contract.md
package kxmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Client 是 kxmemory 客户端抽象接口。所有 handler 都通过这个接口访问
// kxmemory，便于单元测试注入 httptest mock。生产实现是 HTTPClient。
//
// 新增方法时同步更新 HTTPClient + kxmemmock + 测试。
type Client interface {
	ClassifyNote(ctx context.Context, req ClassifyNoteRequest) (*ClassifyNoteResponse, error)
	ClassifyEmails(ctx context.Context, req ClassifyEmailsRequest) (*ClassifyEmailsResponse, error)
	DailySummary(ctx context.Context, req DailySummaryRequest) (*DailySummaryResponse, error)
	Stats() Stats
}

// Compile-time assertion: *HTTPClient implements Client.
var _ Client = (*HTTPClient)(nil)

// Paths 包含 kxmemory 的各功能路径。如果为空，调用方应使用 DefaultPaths。
// 通过环境变量覆盖（部署方在 kxmemory 真实端点与代码假设不一致时配置）。
type Paths struct {
	NoteClassify  string
	EmailClassify string
	DailySummary  string
}

// DefaultPaths 与 docs/2026-07-02-kxmemory-api-contract.md 中约定一致。
var DefaultPaths = Paths{
	NoteClassify:  "/v1/notes/classify",
	EmailClassify: "/v1/emails/classify",
	DailySummary:  "/v1/emails/daily-summary",
}

// NormalizePaths 把空字符串回退到默认值，并去掉前导 "/api" 之类的重复前缀。
func NormalizePaths(in Paths) Paths {
	out := in
	if out.NoteClassify == "" {
		out.NoteClassify = DefaultPaths.NoteClassify
	}
	if out.EmailClassify == "" {
		out.EmailClassify = DefaultPaths.EmailClassify
	}
	if out.DailySummary == "" {
		out.DailySummary = DefaultPaths.DailySummary
	}
	return out
}

// HTTPClient 是 kxmemory 的 HTTP 实现，无状态、可并发安全。
type HTTPClient struct {
	BaseURL string
	APIKey  string // 兼容旧 API；优先使用 JWT secret
	Client  *http.Client
	retry   RetryConfig
	paths   Paths // 各功能路径，可在构造时覆盖
	stats   atomicStats

	// JWT 签发：kxmemory-go 期望 Authorization: Bearer <signed-jwt>
	// 用与 kxmemory 共享的 secret（生产 = POCKET_JWT_SECRET）签 HS256。
	jwtSecret string
	jwtTTL    time.Duration // token 有效期；过期前重发
	signMu    sync.Mutex    // 保护 token 字段
	cachedTok string
	cachedExp time.Time
}

// Stats 暴露客户端运行统计（成功/重试/失败次数），用于 /api/diagnostics。
type Stats struct {
	SuccessCount int64  `json:"success_count"`
	RetryCount   int64  `json:"retry_count"`
	FailureCount int64  `json:"failure_count"`
	LastError    string `json:"last_error,omitempty"`
}

// atomicStats 提供 Stats 字段的原子更新（无锁读路径）。
type atomicStats struct {
	success atomic.Int64
	retry   atomic.Int64
	failure atomic.Int64
	// lastError 用 mutex 保护（错误文本不适合用原子操作）
	lastErrMu sync.RWMutex
	lastErr   string
}

func (s *atomicStats) recordSuccess() { s.success.Add(1) }
func (s *atomicStats) recordRetry()   { s.retry.Add(1) }
func (s *atomicStats) recordFailure(e error) {
	s.failure.Add(1)
	s.lastErrMu.Lock()
	if e != nil {
		s.lastErr = e.Error()
	}
	s.lastErrMu.Unlock()
}

func (s *atomicStats) snapshot() Stats {
	s.lastErrMu.RLock()
	lastErr := s.lastErr
	s.lastErrMu.RUnlock()
	return Stats{
		SuccessCount: s.success.Load(),
		RetryCount:   s.retry.Load(),
		FailureCount: s.failure.Load(),
		LastError:    lastErr,
	}
}

// RetryConfig 控制 HTTPClient 的指数退避重试。
//
// 策略参考 docs/2026-07-02-kxmemory-api-contract.md §1：
//   - 5xx / 网络错误 / 超时 → 指数退避重试（baseDelay 起步 × 2^attempt，
//     不超过 maxDelay，带 ±20% jitter 避免 thundering herd）
//   - 4xx → 立即返回（永久错误，不重试）
type RetryConfig struct {
	MaxAttempts int           // 最大尝试次数（含首次），1 = 不重试
	BaseDelay   time.Duration // 第一次重试前的等待
	MaxDelay    time.Duration // 单次重试最大等待
}

// DefaultRetryConfig 是生产推荐配置：3 次尝试，0.5s/2s/8s 退避。
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500 * time.Millisecond,
	MaxDelay:    8 * time.Second,
}

// NoRetry 用于测试或调用方明确不希望重试的场景。
var NoRetry = RetryConfig{MaxAttempts: 1}

// NewClient 构造 kxmemory 客户端。baseURL 如 http://localhost:8000。
// apiKey 当作 JWT secret 使用（kxmemory-go 期望 HS256 签名 token）。
//
// 兼容旧用法：传空 secret 时不发送 Authorization header（kxmemory 在
// dev/内网模式下可能不需要鉴权）。
func NewClient(baseURL, apiKey string) *HTTPClient {
	return NewClientWithPaths(baseURL, apiKey, DefaultRetryConfig, DefaultPaths)
}

// NewClientWithRetry 显式指定重试配置，便于测试和自定义部署。
func NewClientWithRetry(baseURL, apiKey string, retry RetryConfig) *HTTPClient {
	return NewClientWithPaths(baseURL, apiKey, retry, DefaultPaths)
}

// NewClientWithPaths 同时覆盖重试与端点路径，便于对齐不同部署环境的
// kxmemory 真实端点（生产常见 /api 前缀）。
func NewClientWithPaths(baseURL, apiKey string, retry RetryConfig, paths Paths) *HTTPClient {
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryConfig
	}
	if retry.BaseDelay <= 0 {
		retry.BaseDelay = 500 * time.Millisecond
	}
	if retry.MaxDelay <= 0 {
		retry.MaxDelay = 8 * time.Second
	}
	return &HTTPClient{
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Client:    &http.Client{Timeout: 30 * time.Second},
		retry:     retry,
		paths:     NormalizePaths(paths),
		jwtSecret: apiKey, // 同时作为 JWT 签发密钥
		jwtTTL:    1 * time.Hour,
	}
}

// Stats 返回客户端运行统计快照。
func (c *HTTPClient) Stats() Stats { return c.stats.snapshot() }

// ---- 笔记相关 ----

// ClassifyNoteRequest 对应 POST /v1/notes/classify
type ClassifyNoteRequest struct {
	Content     string   `json:"content"`
	Title       string   `json:"title,omitempty"`
	ContentType string   `json:"content_type,omitempty"` // voice / text
	Domain      string   `json:"domain,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type ClassifyNoteResponse struct {
	Status         string          `json:"status"` // success / conflict_detected
	Classification Classification  `json:"classification"`
	SmartLinks     []SmartLink     `json:"smart_links,omitempty"`
	Todos          []ExtractedTodo `json:"todos,omitempty"`
	SSOTConflicts  []SSOTConflict  `json:"ssot_conflicts,omitempty"`
}

type Classification struct {
	Domain         string   `json:"domain"`   // work / study / life / idea
	Category       string   `json:"category"` // meeting / plan / idea / log / ...
	Tags           []string `json:"tags"`
	SuggestedTitle string   `json:"suggested_title"`
	Confidence     float64  `json:"confidence"`
}

type SmartLink struct {
	TargetID   string  `json:"target_id"`
	LinkType   string  `json:"link_type"` // references / updates / contradicts / complements / related_to
	Confidence float64 `json:"confidence"`
}

type ExtractedTodo struct {
	Text     string `json:"text"`
	DueDate  string `json:"due_date,omitempty"`
	Priority string `json:"priority,omitempty"` // low / medium / high / urgent
}

type SSOTConflict struct {
	ExistingNoteID string  `json:"existing_note_id"`
	ConflictType   string  `json:"conflict_type"` // contradiction / update / duplicate
	Snippet        string  `json:"snippet"`
	Confidence     float64 `json:"confidence"`
}

// ClassifyNote 调用 kxmemory 分类接口（笔记创建/更新后调用）。
func (c *HTTPClient) ClassifyNote(ctx context.Context, req ClassifyNoteRequest) (*ClassifyNoteResponse, error) {
	body, _ := json.Marshal(req)
	var out ClassifyNoteResponse
	if err := c.doWithRetry(ctx, c.paths.NoteClassify, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- 邮件相关 ----

// ClassifyEmailsRequest 对应 POST /v1/emails/classify
type ClassifyEmailsRequest struct {
	Emails []EmailForClassification `json:"emails"`
}

type EmailForClassification struct {
	EmailID     string `json:"email_id"`
	Subject     string `json:"subject"`
	Snippet     string `json:"snippet"` // 正文前 ~500 字
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name,omitempty"`
}

type ClassifyEmailsResponse struct {
	Results []EmailClassificationResult `json:"results"`
}

type EmailClassificationResult struct {
	EmailID         string `json:"email_id"`
	Category        string `json:"category"`   // work / bill / notification / personal / marketing / spam
	Importance      string `json:"importance"` // high / medium / low
	Summary         string `json:"summary"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// ClassifyEmails 批量分类邮件（IMAP 抓取后调用）。
func (c *HTTPClient) ClassifyEmails(ctx context.Context, req ClassifyEmailsRequest) (*ClassifyEmailsResponse, error) {
	body, _ := json.Marshal(req)
	var out ClassifyEmailsResponse
	if err := c.doWithRetry(ctx, c.paths.EmailClassify, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DailySummaryRequest 对应 POST /v1/emails/daily-summary
type DailySummaryRequest struct {
	Date   string                   `json:"date"` // YYYY-MM-DD
	Emails []EmailForClassification `json:"emails"`
}

type DailySummaryResponse struct {
	Date      string              `json:"date"`
	Summary   string              `json:"summary"`
	Breakdown []CategoryBreakdown `json:"breakdown"`
	Todos     []ExtractedTodo     `json:"todos"`
}

type CategoryBreakdown struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
	Snippet  string `json:"snippet"`
}

// DailySummary 生成每日邮件总结（定时任务/手动触发）。
func (c *HTTPClient) DailySummary(ctx context.Context, req DailySummaryRequest) (*DailySummaryResponse, error) {
	body, _ := json.Marshal(req)
	var out DailySummaryResponse
	if err := c.doWithRetry(ctx, c.paths.DailySummary, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- HTTP 内核：重试 + 错误分类 ----

// doWithRetry 是所有方法的统一入口：
//  1. 构造请求
//  2. 循环最多 MaxAttempts 次：5xx/网络错误/超时 → 退避后重试；4xx → 立即返回
//  3. 成功后 unmarshal 到 out
func (c *HTTPClient) doWithRetry(ctx context.Context, path string, body []byte, out any) error {
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		err := c.doOnce(ctx, path, body, out)
		if err == nil {
			c.stats.recordSuccess()
			return nil
		}

		lastErr = err

		// 永久错误（4xx、context canceled）→ 立即返回，不重试
		if isPermanentError(err) || ctx.Err() != nil {
			c.stats.recordFailure(err)
			return err
		}

		// 最后一次失败：不再退避
		if attempt == c.retry.MaxAttempts {
			c.stats.recordFailure(err)
			return err
		}

		// 可重试错误：记录重试，退避后继续
		c.stats.recordRetry()
		if waitErr := sleepWithContext(ctx, backoff(c.retry.BaseDelay, c.retry.MaxDelay, attempt)); waitErr != nil {
			// ctx 在退避期间被取消：包装最后一次错误返回
			c.stats.recordFailure(err)
			return err
		}
	}
	c.stats.recordFailure(lastErr)
	return lastErr
}

// doOnce 执行单次 HTTP 调用，分类错误为 transient / permanent。
func (c *HTTPClient) doOnce(ctx context.Context, path string, body []byte, out any) error {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return NewPermanentError("invalid request", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// JWT 鉴权：secret 非空时签名一个 HS256 token（缓存到过期前）。
	if c.jwtSecret != "" {
		tok, err := c.getOrSignToken()
		if err != nil {
			// 签名失败是永久错误（不会因重试变好）
			return NewPermanentError("sign kxmemory jwt: "+err.Error(), err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		// 网络错误 / DNS / 连接拒绝 / 超时 → 可重试
		return NewTransientError("kxmemory request: "+err.Error(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		if out == nil {
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return NewPermanentError("decode response: "+err.Error(), err)
		}
		return nil
	}

	// 4xx → 永久错误（客户端代码错，无重试意义）
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return NewPermanentError(
			fmt.Sprintf("kxmemory %s returned %d: %s", path, resp.StatusCode, truncate(string(bodyBytes), 200)),
			nil,
		).WithStatus(resp.StatusCode)
	}

	// 5xx → 暂时错误（上游故障，重试）
	bodyBytes, _ := io.ReadAll(resp.Body)
	return NewTransientError(
		fmt.Sprintf("kxmemory %s returned %d: %s", path, resp.StatusCode, truncate(string(bodyBytes), 200)),
		nil,
	).WithStatus(resp.StatusCode)
}

// backoff 计算第 N 次重试的等待时间（指数退避 + ±20% jitter）。
//   - attempt 从 1 开始（首次重试前等待 = BaseDelay）
//   - 不超过 MaxDelay
func backoff(base, max time.Duration, attempt int) time.Duration {
	// base * 2^(attempt-1)
	d := base << (attempt - 1)
	if d <= 0 || d > max {
		d = max
	}
	// ±20% jitter
	jitter := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	return jitter
}

// sleepWithContext 在 ctx 取消时立即返回；否则等待 d。
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// truncate 把字符串截断到 maxLen 以内（用于错误日志脱敏）。
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// getOrSignToken 返回一个有效的 JWT（未过期）；过期前 30 秒会续签。
//
// kxmemory-go 期望 HS256 签名 JWT，claims:
//
//	{ "sub": "pocketd", "role": "admin", "exp": ..., "iat": ... }
//
// 我们缓存 token 避免每次请求都签名（每个 handler 调一次分类，30+ 次/s 也
// 不至于把签名变成热点，但 30s 续签窗口能避免时钟漂移导致 401）。
func (c *HTTPClient) getOrSignToken() (string, error) {
	c.signMu.Lock()
	defer c.signMu.Unlock()

	now := time.Now()
	if c.cachedTok != "" && c.cachedExp.Sub(now) > 30*time.Second {
		return c.cachedTok, nil
	}

	exp := now.Add(c.jwtTTL)
	claims := jwt.MapClaims{
		"sub":  "pocketd",
		"role": "admin",
		"iat":  now.Unix(),
		"exp":  exp.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(c.jwtSecret))
	if err != nil {
		return "", err
	}
	c.cachedTok = signed
	c.cachedExp = exp
	return signed, nil
}

// ---- 错误类型 ----

// Error 是 kxmemory 客户端的结构化错误。
//
//   - Permanent=true 表示不可重试（4xx、context canceled、JSON decode 失败）
//   - Permanent=false 表示可重试（5xx、网络错误、超时）
//
// handler 层基于 Permanent 选择 HTTP 状态码：permanent → 502，transient → 503。
type Error struct {
	Code       string // KXMEMORY_UNREACHABLE / KXMEMORY_BAD_REQUEST / KXMEMORY_UPSTREAM
	Message    string
	Cause      error
	Permanent  bool
	StatusCode int // 上游 HTTP 状态码（如果有）
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// Retryable 返回 true 表示客户端可重试。
func (e *Error) Retryable() bool { return !e.Permanent }

// NewTransientError 包装一个可重试错误（5xx、网络、超时）。
func NewTransientError(msg string, cause error) *Error {
	return &Error{Code: "KXMEMORY_UNREACHABLE", Message: msg, Cause: cause, Permanent: false}
}

// NewPermanentError 包装一个不可重试错误（4xx、JSON decode）。
func NewPermanentError(msg string, cause error) *Error {
	return &Error{Code: "KXMEMORY_BAD_REQUEST", Message: msg, Cause: cause, Permanent: true}
}

// WithStatus 补充上游 HTTP 状态码（5xx / 4xx）。
func (e *Error) WithStatus(code int) *Error {
	e.StatusCode = code
	switch {
	case code >= 500:
		e.Code = "KXMEMORY_UPSTREAM"
	case code >= 400:
		e.Code = "KXMEMORY_BAD_REQUEST"
	}
	return e
}

// isPermanentError 判断错误是否永久（不可重试）。
//
// 4xx + context.Canceled + JSON decode 错误都不可重试。
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}
	var kxe *Error
	if errors.As(err, &kxe) {
		return kxe.Permanent
	}
	// context.Canceled / context.DeadlineExceeded 的语义需要区分：
	//   - DeadlineExceeded 通常来自 ctx.WithTimeout，到期意味着上游慢，
	//     理论可重试，但这里保守起见视为 transient（默认走重试逻辑）
	//   - Canceled 表示调用方主动取消（用户离开页面等），无重试意义
	if errors.Is(err, context.Canceled) {
		return true
	}
	return false
}
