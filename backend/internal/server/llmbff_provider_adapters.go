package server

// llmbff_provider_adapters.go — adapts the existing llmgateway.Client (and the
// legacy aigate clients) to the llmbff.Provider interface defined in S0-B.
//
// This keeps llmbff free of any HTTP-client dependency and lets the BFF be
// unit-tested with a fake provider. The server package owns the wiring.
//
// Two adapters:
//   - llmGatewayBFFProvider: forwards to llm-gateway-go-3 (Chat / Stream / Embed)
//   - (future) aigateBFFProvider: forwards to the legacy direct-OpenAI/Groq clients

import (
	"context"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/quota"
)

// Compile-time checks that the adapters satisfy the Provider interface.
var (
	_ llmbff.Provider = (*llmGatewayBFFProvider)(nil)
)

// llmGatewayBFFProvider adapts llmgateway.Client to llmbff.Provider.
type llmGatewayBFFProvider struct {
	client *llmgateway.Client
}

// NewLLMGatewayBFFProvider wraps a gateway client into a BFF Provider.
func NewLLMGatewayBFFProvider(c *llmgateway.Client) llmbff.Provider {
	if c == nil {
		return nil
	}
	return &llmGatewayBFFProvider{client: c}
}

// GatewayConfig 是 LLM BFF 在请求时解析出的网关连接信息（baseURL + apiKey +
// 已知模型列表）。它由 env 默认值与运行时 /api/llm-gateway/config 配置合并得到，
// 因此用户在「设置 → AI 模型」里修改网关后，对话功能无需重启即可生效。
type GatewayConfig struct {
	BaseURL string
	APIKey  string
	Models  []string
}

// GatewayResolver 按 workspace 返回当前生效的 GatewayConfig。
type GatewayResolver func(workspaceID string) GatewayConfig

// NewDynamicLLMGatewayBFFProvider 构造一个「每次请求时按 workspace 解析网关配置」
// 的 Provider。这样对话流量既可使用启动时的环境变量（POCKET_LLM_GATEWAY_URL/_
// API_KEY），也可使用运行时通过 /api/llm-gateway/config 保存的配置，无需重启
// pocketd 即可让新配置的网关服务于对话。
func NewDynamicLLMGatewayBFFProvider(resolve GatewayResolver) llmbff.Provider {
	if resolve == nil {
		return nil
	}
	return &dynamicGatewayBFFProvider{resolve: resolve}
}

// dynamicGatewayBFFProvider 在每次调用时按 workspace 现拉网关配置并构造客户端，
// 委托给 llmGatewayBFFProvider 完成实际的协议转换。
type dynamicGatewayBFFProvider struct {
	resolve GatewayResolver
}

func (p *dynamicGatewayBFFProvider) clientFor(wsID string) (*llmgateway.Client, error) {
	cfg := p.resolve(wsID)
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return nil, llmbff.ErrNotConfigured
	}
	return llmgateway.NewClient(cfg.BaseURL, cfg.APIKey), nil
}

func (p *dynamicGatewayBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	c, err := p.clientFor(req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return (&llmGatewayBFFProvider{client: c}).Chat(ctx, req)
}

func (p *dynamicGatewayBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	c, err := p.clientFor(req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return (&llmGatewayBFFProvider{client: c}).Stream(ctx, req, fn)
}

func (p *dynamicGatewayBFFProvider) Embed(ctx context.Context, req llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	c, err := p.clientFor(req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return (&llmGatewayBFFProvider{client: c}).Embed(ctx, req)
}

func (p *llmGatewayBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	msgs := make([]llmgateway.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llmgateway.ChatMessage{Role: string(m.Role), Content: llmgateway.ContentParts(m.Content, m.Images)}
	}
	resp, err := p.client.Chat(ctx, llmgateway.ChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		User:        req.User,
	})
	if err != nil {
		return nil, err
	}
	out := &llmbff.ChatResponse{Model: resp.Model}
	if len(resp.Choices) > 0 {
		out.Content = resp.Choices[0].Message.Content
	}
	out.Usage = quota.ApplyCost(resp.Model, llmbff.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	})
	return out, nil
}

func (p *llmGatewayBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	msgs := make([]llmgateway.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llmgateway.ChatMessage{Role: string(m.Role), Content: llmgateway.ContentParts(m.Content, m.Images)}
	}
	var finalUsage *llmbff.Usage
	_, err := p.client.Stream(ctx, llmgateway.ChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		User:        req.User,
	}, func(d llmgateway.StreamDelta) bool {
		delta := llmbff.Delta{
			Content:      d.Content,
			FinishReason: d.FinishReason,
			Done:         d.FinishReason != "" || d.TotalTokens > 0,
		}
		if d.TotalTokens > 0 {
			u := quota.ApplyCost(req.Model, llmbff.Usage{
				PromptTokens:     d.PromptTokens,
				CompletionTokens: d.CompletionTokens,
				TotalTokens:      d.TotalTokens,
			})
			delta.Usage = &u
			finalUsage = &u
		}
		return fn(delta)
	})
	if err != nil {
		return nil, err
	}
	if finalUsage == nil {
		return &llmbff.Usage{}, nil
	}
	return finalUsage, nil
}

func (p *llmGatewayBFFProvider) Embed(ctx context.Context, req llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	resp, err := p.client.Embed(ctx, llmgateway.EmbeddingRequest{
		Model: req.Model,
		Input: req.Input,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("empty embedding from gateway")
	}
	return &llmbff.EmbedResponse{
		Embedding: resp.Data[0].Embedding,
		Model:     resp.Model,
		Usage: quota.ApplyCost(resp.Model, llmbff.Usage{
			PromptTokens: resp.Usage.PromptTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}),
	}, nil
}
