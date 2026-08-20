package redclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client RedClaw API 客户端
type Client struct {
	cfg    ClientConfig
	httpDo *http.Client
}

// NewClient creates a new RedClaw API client with the given configuration.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("redclaw: BaseURL cannot be empty")
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("redclaw: Secret cannot be empty")
	}
	if cfg.TenantID == "" {
		return nil, fmt.Errorf("redclaw: TenantID cannot be empty")
	}
	
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	return &Client{
		cfg: cfg,
		httpDo: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Health performs a health check against the RedClaw service.
func (c *Client) Health() (*HealthResponse, error) {
	resp, err := c.doRequest(http.MethodGet, "/health", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode health response: %w", err)
	}
	return &result, nil
}

// Chat sends a chat request to the LLM service with tenant isolation enforcement.
func (c *Client) Chat(req ChatRequest) (*ChatResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("redclaw: chat request must contain at least one message")
	}
	
	// Enforce tenant isolation: override with client's tenant ID
	if req.TenantID != "" && req.TenantID != c.cfg.TenantID {
		return nil, fmt.Errorf("redclaw: tenant ID mismatch (request=%s, client=%s)", req.TenantID, c.cfg.TenantID)
	}
	req.TenantID = c.cfg.TenantID

	resp, err := c.doRequest(http.MethodPost, "/api/v1/pocket/llm/chat", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("RedClaw error (code=%d): %s", errResp.Code, errResp.Message)
		}
		return nil, fmt.Errorf("RedClaw HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	return &result, nil
}

// KnowledgeSearch searches the knowledge base with tenant isolation enforcement.
func (c *Client) KnowledgeSearch(req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("redclaw: search query cannot be empty")
	}
	
	// Enforce tenant isolation: override with client's tenant ID
	if req.TenantID != "" && req.TenantID != c.cfg.TenantID {
		return nil, fmt.Errorf("redclaw: tenant ID mismatch (request=%s, client=%s)", req.TenantID, c.cfg.TenantID)
	}
	req.TenantID = c.cfg.TenantID

	resp, err := c.doRequest(http.MethodPost, "/api/v1/pocket/knowledge/search", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("RedClaw error (code=%d): %s", errResp.Code, errResp.Message)
		}
		return nil, fmt.Errorf("RedClaw HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result KnowledgeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode knowledge search response: %w", err)
	}
	return &result, nil
}

// doRequest 执行 HTTP 请求
func (c *Client) doRequest(method, path string, body interface{}) (*http.Response, error) {
	url := c.cfg.BaseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	req.Header.Set("X-Tenant-ID", c.cfg.TenantID)
	req.Header.Set("Content-Type", "application/json")

	return c.httpDo.Do(req)
}