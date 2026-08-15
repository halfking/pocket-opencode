package facade

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a façade Client.
type Config struct {
	// BaseURL is the façade root, e.g. https://redclaw-api.example.com or a
	// mock server URL. It must be configurable so tests and local dev can
	// point at a mock.
	BaseURL string
	// Token is the service JWT (Bearer). Tenant identity is derived from the
	// token claims by the provider; the client must not send a bare
	// X-User-Id/X-Tenant-ID identity header.
	Token string
	// TimeoutSec is the per-request HTTP timeout (default 30; SSE streams use
	// their own lifecycle, see StreamRunEvents).
	TimeoutSec int
	// HTTPClient overrides the default http.Client (optional, for tests).
	HTTPClient *http.Client
}

// Client is the RedClaw façade API client.
type Client struct {
	cfg            Config
	baseURL        *url.URL
	httpDo         *http.Client
	correlationGen func() string
	idempotencyGen func() string
}

// NewClient builds a façade client. It fails if BaseURL or Token is empty:
// an unauthenticated client is never valid against the façade contract.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("facade: BaseURL is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("facade: Token (service JWT) is required")
	}
	u, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("facade: invalid BaseURL %q", cfg.BaseURL)
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: time.Duration(timeout) * time.Second}
	}
	return &Client{
		cfg:            cfg,
		baseURL:        u,
		httpDo:         httpClient,
		correlationGen: newRequestID,
		idempotencyGen: newRequestID,
	}, nil
}

// TenantID derives the tenant from the JWT `tenant_id` claim WITHOUT
// verification (the provider verifies the signature). It exists for logging
// and local mapping bookkeeping only; the tenant is never sent as a request
// identity header.
func (c *Client) TenantID() string {
	return tenantFromToken(c.cfg.Token)
}

// APIError is the unified error returned for non-2xx façade responses.
type APIError struct {
	Status        int
	Code          string
	Message       string
	Retryable     bool
	RequestID     string
	CorrelationID string
	Body          string // raw body when the envelope could not be parsed
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("facade: HTTP %d code=%s retryable=%v: %s", e.Status, e.Code, e.Retryable, e.Message)
	}
	return fmt.Sprintf("facade: HTTP %d: %s", e.Status, e.Body)
}

// callOption carries per-call header overrides.
type callOption struct {
	idempotencyKey string
	correlationID  string
}

// CallOption customizes a single façade call.
type CallOption func(*callOption)

// WithIdempotencyKey reuses a caller-provided idempotency key (e.g. when
// retrying after a network failure). If omitted on a write, the client
// generates one.
func WithIdempotencyKey(key string) CallOption {
	return func(o *callOption) { o.idempotencyKey = key }
}

// WithCorrelationID reuses a caller-provided correlation ID. If omitted, the
// client generates one.
func WithCorrelationID(id string) CallOption {
	return func(o *callOption) { o.correlationID = id }
}

// do performs a façade request and decodes the JSON envelope into out.
// write=true forces an Idempotency-Key (required for writes per contract).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body interface{}, out interface{}, write bool, opts []CallOption) error {
	var o callOption
	for _, fn := range opts {
		fn(&o)
	}
	if o.correlationID == "" {
		o.correlationID = c.correlationGen()
	}
	if write && o.idempotencyKey == "" {
		o.idempotencyKey = c.idempotencyGen()
	}

	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("facade: marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return fmt.Errorf("facade: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Correlation-ID", o.correlationID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if write {
		if o.idempotencyKey == "" {
			return fmt.Errorf("facade: Idempotency-Key is required for %s %s", method, path)
		}
		req.Header.Set("Idempotency-Key", o.idempotencyKey)
	}

	resp, err := c.httpDo.Do(req)
	if err != nil {
		return fmt.Errorf("facade: do %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("facade: decode %s %s response: %w", method, path, err)
	}
	return nil
}

// parseAPIError decodes the unified error envelope into an *APIError.
func parseAPIError(resp *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &APIError{Status: resp.StatusCode, Body: fmt.Sprintf("read error body: %v", err)}
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Error.Code == "" {
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	return &APIError{
		Status:        resp.StatusCode,
		Code:          env.Error.Code,
		Message:       env.Error.Message,
		Retryable:     env.Error.Retryable,
		RequestID:     env.RequestID,
		CorrelationID: env.CorrelationID,
		Body:          strings.TrimSpace(string(raw)),
	}
}

// newRequestID generates a random request-scoped identifier (used for both
// X-Correlation-ID and auto-generated Idempotency-Key).
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("pk-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// tenantFromToken extracts the tenant_id claim from a JWT without signature
// verification. Returns "" for malformed tokens.
func tenantFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.TenantID
}

// CreateTask POSTs /api/v2/tasks. Write operation: Idempotency-Key required
// (supplied via WithIdempotencyKey or auto-generated). Accepts both 202
// (accepted) and 200 (idempotent replay) success codes.
func (c *Client) CreateTask(ctx context.Context, req CreateTaskRequest, opts ...CallOption) (*TaskCreatedResponse, error) {
	if req.ProjectID == "" || req.Title == "" || req.TaskContract == nil {
		return nil, fmt.Errorf("facade: CreateTask requires project_id, title and task_contract")
	}
	var out TaskCreatedResponse
	if err := c.do(ctx, http.MethodPost, "/api/v2/tasks", nil, req, &out, true, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTasks GETs /api/v2/tasks with cursor pagination.
func (c *Client) ListTasks(ctx context.Context, q ListTasksQuery, opts ...CallOption) (*TaskListResponse, error) {
	query := url.Values{}
	if q.ProjectID != "" {
		query.Set("project_id", q.ProjectID)
	}
	if q.Status != "" {
		query.Set("status", q.Status)
	}
	if q.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Cursor != "" {
		query.Set("cursor", q.Cursor)
	}
	var out TaskListResponse
	if err := c.do(ctx, http.MethodGet, "/api/v2/tasks", query, nil, &out, false, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTask GETs /api/v2/tasks/{task_id}.
func (c *Client) GetTask(ctx context.Context, taskID string, opts ...CallOption) (*TaskDetailResponse, error) {
	if taskID == "" {
		return nil, fmt.Errorf("facade: task_id is required")
	}
	var out TaskDetailResponse
	if err := c.do(ctx, http.MethodGet, "/api/v2/tasks/"+url.PathEscape(taskID), nil, nil, &out, false, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitApprovalDecision POSTs /api/v2/approvals/{gate_id}/decision.
// Write operation: pass WithIdempotencyKey to make offline retries idempotent.
func (c *Client) SubmitApprovalDecision(ctx context.Context, gateID string, req ApprovalDecisionRequest, opts ...CallOption) (*ApprovalDecisionResponse, error) {
	if gateID == "" {
		return nil, fmt.Errorf("facade: gate_id is required")
	}
	if req.Decision == "" || req.ExpectedGateVersion <= 0 {
		return nil, fmt.Errorf("facade: ApprovalDecision requires decision and expected_gate_version")
	}
	var out ApprovalDecisionResponse
	path := "/api/v2/approvals/" + url.PathEscape(gateID) + "/decision"
	if err := c.do(ctx, http.MethodPost, path, nil, req, &out, true, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListNotifications GETs /api/v2/notifications with cursor pagination.
func (c *Client) ListNotifications(ctx context.Context, q ListNotificationsQuery, opts ...CallOption) (*NotificationListResponse, error) {
	query := url.Values{}
	if q.UnreadOnly {
		query.Set("unread_only", "true")
	}
	if q.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Cursor != "" {
		query.Set("cursor", q.Cursor)
	}
	var out NotificationListResponse
	if err := c.do(ctx, http.MethodGet, "/api/v2/notifications", query, nil, &out, false, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// AckNotification POSTs /api/v2/notifications/{notification_id}/ack.
// Write operation: Idempotency-Key supplied or auto-generated, so a retry
// never double-acks.
func (c *Client) AckNotification(ctx context.Context, notificationID string, opts ...CallOption) (*NotificationAckResponse, error) {
	if notificationID == "" {
		return nil, fmt.Errorf("facade: notification_id is required")
	}
	var out NotificationAckResponse
	path := "/api/v2/notifications/" + url.PathEscape(notificationID) + "/ack"
	if err := c.do(ctx, http.MethodPost, path, nil, struct{}{}, &out, true, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchMemory POSTs /api/v2/memory/search. Read-like semantic (no
// Idempotency-Key per contract); X-Correlation-ID always sent.
func (c *Client) SearchMemory(ctx context.Context, req MemorySearchRequest, opts ...CallOption) (*MemorySearchResponse, error) {
	if req.Query == "" || req.ScopeChain == nil || req.ScopeChain.TenantID == "" {
		return nil, fmt.Errorf("facade: SearchMemory requires query and scope_chain.tenant_id")
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}
	var out MemorySearchResponse
	if err := c.do(ctx, http.MethodPost, "/api/v2/memory/search", nil, req, &out, false, opts); err != nil {
		return nil, err
	}
	return &out, nil
}
