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

func (p *llmGatewayBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	msgs := make([]llmgateway.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llmgateway.ChatMessage{Role: string(m.Role), Content: m.Content}
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
	out.Usage = llmbff.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	return out, nil
}

func (p *llmGatewayBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	msgs := make([]llmgateway.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llmgateway.ChatMessage{Role: string(m.Role), Content: m.Content}
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
			u := llmbff.Usage{
				PromptTokens:     d.PromptTokens,
				CompletionTokens: d.CompletionTokens,
				TotalTokens:      d.TotalTokens,
			}
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
		Usage: llmbff.Usage{
			PromptTokens: resp.Usage.PromptTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}, nil
}
