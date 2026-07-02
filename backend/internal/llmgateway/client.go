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
