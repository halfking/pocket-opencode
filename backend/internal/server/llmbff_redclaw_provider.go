package server

// llmbff_redclaw_provider.go — RedClaw LLM 接入统一 BFF（llmbff.Provider）。
//
// RedClaw 企业后端已承载认证（主权威源）/知识库/审计；本文件把它的
// /api/v1/pocket/llm/chat 也接进 BFF Provider 抽象，作为企业网关
// （dynamicGatewayBFFProvider）不可用时的兜底通道：
//
//	用户请求 → dynamicGateway（按 workspace 解析）─失败→ RedClaw 兜底
//
// 装配见 cmd/pocketd/main.go（POCKET_REDCLAW_LLM_FALLBACK=true 且
// RedClaw Bridge 已配置时启用）。RedClaw pocket API 目前只有非流式
// chat，Stream 以「整段单 delta」模式降级实现，前端无感知。

import (
	"context"
	"errors"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// redclawBFFProvider 把 redclaw.Bridge 适配为 llmbff.Provider。
type redclawBFFProvider struct {
	bridge *redclaw.Bridge
}

// NewRedClawBFFProvider 构造 RedClaw Provider。bridge 为 nil 时返回 nil。
func NewRedClawBFFProvider(bridge *redclaw.Bridge) llmbff.Provider {
	if bridge == nil {
		return nil
	}
	return &redclawBFFProvider{bridge: bridge}
}

func toRedClawMessages(msgs []llmbff.Message) []redclaw.Message {
	out := make([]redclaw.Message, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		// 多模态消息折叠为「文本 + [图片 N 张]」占位：RedClaw 网关暂不
		// 接受 image_url parts，至少不让正文丢失。
		if len(m.Images) > 0 {
			content = fmt.Sprintf("%s\n[附件图片 %d 张]", content, len(m.Images))
		}
		out = append(out, redclaw.Message{Role: string(m.Role), Content: content})
	}
	return out
}

func (p *redclawBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("redclaw provider: messages required")
	}
	rcReq := redclaw.ChatRequest{
		UserID:   req.User,
		Model:    req.Model,
		Messages: toRedClawMessages(req.Messages),
	}
	// ctx 取消传播：Bridge.Chat 无 ctx 参数，这里仅做前置检查（HTTP 客户端
	// 自带 30s 超时，长挂风险由 redclaw.Client 超时兜底）。
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	rcResp, err := p.bridge.Chat(rcReq)
	if err != nil {
		return nil, fmt.Errorf("redclaw chat: %w", err)
	}
	content := rcResp.Message.Content
	usage := llmbff.Usage{
		PromptTokens:     estimateTokens(req.Messages),
		CompletionTokens: len([]rune(content)) / 4,
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return &llmbff.ChatResponse{
		Content: content,
		Model:   rcResp.ModelUsed,
		Usage:   usage,
	}, nil
}

func (p *redclawBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	// RedClaw pocket API 非流式：整段作为单 delta 下发，前端行为不变。
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Content != "" && !fn(llmbff.Delta{Content: resp.Content}) {
		return &resp.Usage, nil // 客户端断开
	}
	fn(llmbff.Delta{Done: true, FinishReason: "stop", Usage: &resp.Usage})
	return &resp.Usage, nil
}

func (p *redclawBFFProvider) Embed(ctx context.Context, req llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, errors.New("redclaw provider: embeddings not supported")
}

// estimateTokens 粗估 prompt token（中文≈1.5 char/token，英文≈4 char/token，
// 取保守中间值 3），RedClaw 未回传 usage 时保持记账链路有数。
func estimateTokens(msgs []llmbff.Message) int {
	total := 0
	for _, m := range msgs {
		total += len([]rune(m.Content))
	}
	return total / 3
}

// fallbackBFFProvider 主通道失败时切换备用通道（RedClaw 兜底）。
type fallbackBFFProvider struct {
	primary  llmbff.Provider
	fallback llmbff.Provider
}

// NewFallbackBFFProvider 包装主/备 Provider。任一为 nil 时退化为单通道。
func NewFallbackBFFProvider(primary, fallback llmbff.Provider) llmbff.Provider {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	return &fallbackBFFProvider{primary: primary, fallback: fallback}
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *fallbackBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	resp, err := p.primary.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	// 请求方主动取消/超时不应换通道重试（换通道也救不了，还可能双倍计费）。
	if isContextErr(err) {
		return nil, err
	}
	return p.fallback.Chat(ctx, req)
}

func (p *fallbackBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	usage, err := p.primary.Stream(ctx, req, fn)
	if err == nil {
		return usage, nil
	}
	if isContextErr(err) {
		return nil, err
	}
	// 主通道首帧前失败：把回退进度告知前端（retry 帧语义），再走备用通道。
	fn(llmbff.Delta{Retry: "redclaw"})
	return p.fallback.Stream(ctx, req, fn)
}

func (p *fallbackBFFProvider) Embed(ctx context.Context, req llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	resp, err := p.primary.Embed(ctx, req)
	if err == nil {
		return resp, nil
	}
	if isContextErr(err) {
		return nil, err
	}
	return p.fallback.Embed(ctx, req)
}
