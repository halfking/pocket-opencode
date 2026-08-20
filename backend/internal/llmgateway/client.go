// Package llmgateway provides a Go client for llm-gateway-go multi-tenant LLM gateway.
//
// llm-gateway-go 是超级智能网关，提供 OpenAI 兼容 API + 智能路由 + 语义缓存 + 凭据池。
// pocketd 可选地把 LLM 请求代理到 llm-gateway 而非直接调 OpenAI/Groq，享受企业级
// 流量治理（限流/审计/DLP）。
//
// 架构：pocketd 无状态网关 → llm-gateway-go 多租户路由 → OpenAI/Anthropic/etc
package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 是 llm-gateway-go 的 HTTP 客户端，OpenAI 兼容协议。
type Client struct {
	BaseURL string // 如 https://llm-gateway.example.com
	APIKey  string // 租户 API key（llm-gateway 签发）
	Client  *http.Client
}

// NewClient 构造 llm-gateway 客户端。
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// ChatMessage 兼容 OpenAI chat completion 消息格式。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 对应 POST /v1/chat/completions（OpenAI 兼容）
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	User        string        `json:"user,omitempty"` // 用户标识（审计用）
}

// ChatResponse 对应 chat completion 响应（非流式）
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat 调用 llm-gateway 的 chat completion（非流式）。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm-gateway chat %d: %s", resp.StatusCode, string(r))
	}

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	return &out, nil
}

// StreamDelta is one chunk of a streaming chat completion (OpenAI SSE shape).
// Content is the incremental text; Usage is only present on the final chunk
// when the request set stream_options.include_usage.
type StreamDelta struct {
	Content      string `json:"content"`
	FinishReason string `json:"finish_reason"`
	Done         bool   `json:"done"`
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// Stream 调用 llm-gateway 的 chat completion（流式 SSE）。
//
// 对每个 SSE data 块解析 OpenAI delta 并调用 fn(delta)。fn 返回 false 时
// 提前终止流（客户端断连）。返回最终 usage（若 provider 在末帧返回）。
//
// 请求自动设置 stream=true 和 stream_options.include_usage=true。
func (c *Client) Stream(ctx context.Context, req ChatRequest, fn func(StreamDelta) bool) (*StreamDelta, error) {
	req.Stream = true
	// 注：ChatRequest 没有 stream_options 字段；这里通过包装 body 注入。
	body, _ := json.Marshal(map[string]any{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"stream":      true,
		"user":        req.User,
		"stream_options": map[string]bool{"include_usage": true},
	})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway stream %d: %s", resp.StatusCode, string(r))
	}

	// 逐行解析 SSE。每行 "data: {...}"；以 "data: [DONE]" 结束。
	return parseSSEStream(resp.Body, fn)
}

// EmbeddingRequest 对应 POST /v1/embeddings（OpenAI 兼容）
type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
	User  string `json:"user,omitempty"`
}

// EmbeddingResponse 对应 embeddings 响应
type EmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed 调用 llm-gateway 的 embeddings 接口。
func (c *Client) Embed(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm-gateway embed %d: %s", resp.StatusCode, string(r))
	}

	var out EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return &out, nil
}

// =============================================================================
// 会话跨主机迁移：导出/导入/拉取（对接 llm-gateway-go /api/admin/session-export）
// =============================================================================

// SessionPack 是会话迁移包的客户端镜像（与 admin.SessionExport 对齐）。
// json tag 与 llm-gateway-go 的 wire format 一致，可直接反序列化。
type SessionPack struct {
	SessionMeta struct {
		ID        string `json:"id"`
		Title     string `json:"title,omitempty"`
		Directory string `json:"directory,omitempty"`
		Instance  string `json:"instance,omitempty"`
		TaskID    string `json:"taskId,omitempty"`
	} `json:"session_meta"`
	ResumeBrief struct {
		CurrentState  string   `json:"currentState,omitempty"`
		LastObjective string   `json:"lastObjective,omitempty"`
		Decisions     []string `json:"decisions,omitempty"`
		ChangedFiles  []string `json:"changedFiles,omitempty"`
		Blockers      []string `json:"blockers,omitempty"`
		NextAction    string   `json:"nextAction,omitempty"`
	} `json:"resume_brief"`
	Messages    []json.RawMessage `json:"messages,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	ExportedAt  string            `json:"exported_at,omitempty"`
}

// ExportSession 从 llm-gateway-go 导出指定会话的完整迁移包。
// 对应 GET /api/admin/session-export?id=<gw_session_id>&tenant=<t>。
func (c *Client) ExportSession(ctx context.Context, gwSessionID, tenantID string) (*SessionPack, error) {
	u := fmt.Sprintf("%s/api/admin/session-export?id=%s&tenant=%s", c.BaseURL, gwSessionID, tenantID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("export session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("export session %d: %s", resp.StatusCode, string(r))
	}

	var pack SessionPack
	if err := json.NewDecoder(resp.Body).Decode(&pack); err != nil {
		return nil, fmt.Errorf("decode session pack: %w", err)
	}
	return &pack, nil
}

// ImportPackResp 是 ImportPack 的响应。
type ImportPackResp struct {
	PackID    string `json:"pack_id"`
	SessionID string `json:"session_id"`
}

// ImportPack 把迁移包上传到 llm-gateway-go staging，返回 pack_id 供目标主机拉取。
// 对应 POST /api/admin/session-export/import?tenant=<t>。
func (c *Client) ImportPack(ctx context.Context, pack *SessionPack, tenantID string) (*ImportPackResp, error) {
	body, _ := json.Marshal(pack)
	u := fmt.Sprintf("%s/api/admin/session-export/import?tenant=%s", c.BaseURL, tenantID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("import pack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("import pack %d: %s", resp.StatusCode, string(r))
	}

	var out ImportPackResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode import resp: %w", err)
	}
	return &out, nil
}

// FetchPack 按 pack_id 从 llm-gateway-go 拉取已导入的迁移包（目标主机调用）。
// 对应 GET /api/admin/session-export/pack?id=<pack_id>&tenant=<t>。
func (c *Client) FetchPack(ctx context.Context, packID, tenantID string) (*SessionPack, error) {
	u := fmt.Sprintf("%s/api/admin/session-export/pack?id=%s&tenant=%s", c.BaseURL, packID, tenantID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch pack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch pack %d: %s", resp.StatusCode, string(r))
	}

	var pack SessionPack
	if err := json.NewDecoder(resp.Body).Decode(&pack); err != nil {
		return nil, fmt.Errorf("decode pack: %w", err)
	}
	return &pack, nil
}
