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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/quota"
)

// llmGatewayNoCandidateBody 是 llm-gateway-go 在「没有可用 provider」时返回的
// 错误体（HTTP 503 + JSON）。需要稳定识别——dynamicGatewayBFFProvider 依赖它
// 触发「auto → preferred 下一个 model」回退。
//
// 实际线上抓到的 body：
//
//	{"error":{"code":"no_candidate","kind":"no_candidate",
//	          "message":"No available provider for model '...'",
//	          "request_id":"...","type":"server_error"}}
type llmGatewayNoCandidateBody struct {
	Error struct {
		Code string `json:"code"`
		Kind string `json:"kind"`
	} `json:"error"`
}

// isNoCandidateError 探测 llmgateway 返回的 error 是否对应 gateway 的
// no_candidate（HTTP 503 + JSON 中 code=="no_candidate"）。
//
// Stream 路径上 503 在写任何 SSE chunk 之前就返回，所以出错的那次尝试
// 本身没有通过 fn 输出过内容；收到此错误即可安全地用下一个候选 model
// 重试（重试间隙由 dynamicGatewayBFFProvider.Stream 经 fn 发一帧 Retry
// 进度，见该函数注释）。
func isNoCandidateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	idx := strings.Index(msg, "{")
	if idx < 0 {
		return false
	}
	body := llmGatewayNoCandidateBody{}
	if jerr := json.Unmarshal([]byte(msg[idx:]), &body); jerr != nil {
		return false
	}
	return body.Error.Code == "no_candidate"
}

// isModelUnavailableError 在 no_candidate 之外再覆盖 invalid_model
// （HTTP 400 + code=="invalid_model"）：网关对「模型 id 不存在/未上架」返回的
// 是 400 invalid_model 而非 503 no_candidate（2026-09-05 E2E 实测：preferred
// 首位设为不存在模型时整链直接报错、不回退）。二者对用户语义等价——
// 「这个 model 现在没货」，都应触发 preferred 下一候选的回退重试。
func isModelUnavailableError(err error) bool {
	if isNoCandidateError(err) {
		return true
	}
	if err == nil {
		return false
	}
	msg := err.Error()
	idx := strings.Index(msg, "{")
	if idx < 0 {
		return false
	}
	body := llmGatewayNoCandidateBody{}
	if jerr := json.Unmarshal([]byte(msg[idx:]), &body); jerr != nil {
		return false
	}
	return body.Error.Code == "invalid_model"
}

// pickFallbackModel 从 workspace 的 preferred 列表里挑一个「在 catalog 内且
// 不是 original」的 model id，作为 auto / no_candidate 时的下一候选。
//
// 返回空字符串表示没有可回退的候选——此时调用方应把原始 no_candidate 错误
// 直接返给前端，让用户感知到「网关确实没货」。
func pickFallbackModel(original string, preferred, catalog []string) string {
	if len(preferred) == 0 {
		return ""
	}
	inCatalog := func(id string) bool {
		if len(catalog) == 0 {
			// catalog 为空（/v1/models 还没拉过）时不强制过滤，
			// 避免因 catalog 陈旧把可用的 preferred 也挡掉。
			return true
		}
		for _, c := range catalog {
			if c == id {
				return true
			}
		}
		return false
	}
	for _, m := range preferred {
		if m == "" || m == original {
			continue
		}
		if inCatalog(m) {
			return m
		}
	}
	return ""
}

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
	// Format：网关调用协议（openai-chat 当前唯一实现；见 gatewayFormats）。
	Format string
	// PreferredModels：用户勾选的常用模型（设置页维护）；/api/llm/models
	// 透传给前端做模型选择器过滤，空 = 不过滤。
	PreferredModels []string
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

// resolveChatModel 处理前端传过来的 model：
//   - 空 / "auto" → 用 workspace 的 preferred 列表第一个；
//   - "auto" 且 preferred 为空 → 仍发 "auto"，由网关自行路由（兜底）。
//
// 设计动机：网关侧 "auto" 当前会路由到 claude-sonnet-4.5，但该 model 在我们的
// 默认网关上没有可用 provider，直接发 "auto" 会得到 no_candidate。本地按
// preferred 解析可以保证对话"开箱即用"，并与设置页的常用模型一致。
func (p *dynamicGatewayBFFProvider) resolveChatModel(req llmbff.ChatRequest) llmbff.ChatRequest {
	if req.Model != "" && req.Model != "auto" {
		return req
	}
	cfg := p.resolve(req.WorkspaceID)
	if len(cfg.PreferredModels) > 0 && cfg.PreferredModels[0] != "" {
		req.Model = cfg.PreferredModels[0]
	} else if len(cfg.Models) > 0 && cfg.Models[0] != "" {
		req.Model = cfg.Models[0]
	} else {
		req.Model = "auto" // 兜底：让网关尝试它的内置路由
	}
	return req
}

func (p *dynamicGatewayBFFProvider) Chat(ctx context.Context, req llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	c, err := p.clientFor(req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	req = p.resolveChatModel(req)
	resp, err := (&llmGatewayBFFProvider{client: c}).Chat(ctx, req)
	if err != nil && isModelUnavailableError(err) {
		// 用户显式选了一个 model 但当前没 provider（no_candidate）或模型 id
		// 不存在/未上架（invalid_model）：用 preferred 的下一个兜底。
		if fallback := p.fallbackModel(req.WorkspaceID, req.Model); fallback != "" {
			req.Model = fallback
			return (&llmGatewayBFFProvider{client: c}).Chat(ctx, req)
		}
	}
	return resp, err
}

// auto 回退链的超时预算。网关客户端自身有 90s 总超时 / 30s 响应头超时，
// 但回退链是逐候选串行重试的：N 个候选都挂住时最坏 N×30s，前端一直转圈。
// 这里限制：单次尝试 20s（覆盖首 token 正常延迟），整链预算 45s。
// var 而非 const：测试用短预算驱动超时回退路径。
var (
	autoFallbackAttemptTimeout = 20 * time.Second
	autoFallbackTotalBudget    = 45 * time.Second
)

// Stream 实现 BFF 的流式 chat。auto / no_candidate 时按 preferred 列表
// 本地回退重试；每次尝试与整链都受超时预算约束，保证错误及时浮出
// （2026-09-05 移动端 E2E：上游挂死时 SSE 长时间零字节，前端空气泡转圈）。
func (p *dynamicGatewayBFFProvider) Stream(ctx context.Context, req llmbff.ChatRequest, fn func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	c, err := p.clientFor(req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	req = p.resolveChatModel(req)
	deadline := time.Now().Add(autoFallbackTotalBudget)
	for {
		attemptCtx := ctx
		var cancel context.CancelFunc
		if remaining := time.Until(deadline); remaining < autoFallbackAttemptTimeout {
			attemptCtx, cancel = context.WithTimeout(ctx, remaining)
		} else {
			attemptCtx, cancel = context.WithTimeout(ctx, autoFallbackAttemptTimeout)
		}
		// 记录本次尝试是否已向客户端写出正文：已写出正文的尝试不能换候选
		// 重试（会在同一气泡里重复作答），错误只能原样上抛。
		wroteContent := false
		attemptFn := func(d llmbff.Delta) bool {
			if d.Content != "" {
				wroteContent = true
			}
			return fn(d)
		}
		usage, err := (&llmGatewayBFFProvider{client: c}).Stream(attemptCtx, req, attemptFn)
		cancel()
		if err == nil || !streamAttemptFallbackEligible(err, wroteContent) {
			return usage, err
		}
		// 单次尝试内 no_candidate(503)/invalid_model(400) 都出现在任何 chunk
		// 之前（该次尝试没有通过 fn 写过内容），可以安全地以另一个 model
		// 重试。注意：切换到新候选前会先经 fn 发一帧 Retry 进度（见下），
		// 所以整条链上 fn 可能已被调用过——只要它返回 true（客户端仍在线）
		// 就继续重试。
		fallback := p.fallbackModel(req.WorkspaceID, req.Model)
		if fallback == "" || ctx.Err() != nil || !time.Now().Before(deadline) {
			log.Printf("[llm-auto] stop fallback chain: model=%s err=%v wrote_content=%v "+
				"fallback=%q ctx_err=%v budget_left=%s",
				req.Model, err, wroteContent, fallback, ctx.Err(),
				time.Until(deadline).Round(time.Millisecond))
			return usage, err
		}
		log.Printf("[llm-auto] %s -> fallback %s (%v)", req.Model, fallback, err)
		// 候选切换间隙发一帧回退重试进度（无 content、非终态）：让前端在
		// 整链重试期间能看到「已切换到 <model> 重试」，而不是零字节转圈
		// （2026-09-05 移动端 E2E 反馈）。fn 返回 false 说明客户端已断开，
		// 直接放弃重试。
		if !fn(llmbff.Delta{Retry: fallback}) {
			return usage, err
		}
		req.Model = fallback
	}
}

// streamAttemptFallbackEligible 判断一次 Stream 尝试失败后能否换候选重试：
// ① isModelUnavailableError（no_candidate/invalid_model，出现在任何 chunk
// 之前，该次尝试未写过正文）；② 尝试级超时（context.DeadlineExceeded）且
// 本次尝试未写出任何正文——上游对无 provider 的模型在流式路径上可能挂死
// 而非快速失败（2026-09-05 实测：glm-5.2 无 provider 时 chat 路径 503
// 600ms 快速返回、stream 路径挂满 20s 尝试超时），deadline 错误若不回退，
// auto 整链必死在首位挂死候选上、永远到不了可用候选。已写出正文后超时
// 视为答案中断，不可重试（避免重复作答）。
func streamAttemptFallbackEligible(err error, wroteContent bool) bool {
	if isModelUnavailableError(err) {
		return true
	}
	return !wroteContent && errors.Is(err, context.DeadlineExceeded)
}

// fallbackModel 在 no_candidate 触发时挑下一个候选 model（不含 current）。
func (p *dynamicGatewayBFFProvider) fallbackModel(wsID, current string) string {
	cfg := p.resolve(wsID)
	return pickFallbackModel(current, cfg.PreferredModels, cfg.Models)
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
