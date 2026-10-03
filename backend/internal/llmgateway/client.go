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
	"strings"
	"sync"
	"time"
)

// normalizeBaseURL 把用户/配置里可能带 /v1 后缀的网关地址收敛为「不含 /v1、
// 不含结尾斜杠」的基础地址。llmgateway.Client 在拼接 OpenAI 兼容端点时会自行
// 补上 /v1，因此传入 https://llmgo.kxpms.cn/v1 与 https://llmgo.kxpms.cn
// 都应得到 https://llmgo.kxpms.cn，避免拼出 /v1/v1/chat/completions 这类双
// /v1 错误路径（见 internal/opencode/config_writer.go 的默认值）。
func normalizeBaseURL(baseURL string) string {
	u := strings.TrimSpace(baseURL)
	u = strings.TrimRight(u, "/")
	if strings.HasSuffix(u, "/v1") {
		u = strings.TrimRight(u[:len(u)-3], "/")
	}
	return u
}

// Client 是 llm-gateway-go 的 HTTP 客户端，OpenAI 兼容协议。
type Client struct {
	BaseURL string // 如 https://llm-gateway.example.com
	APIKey  string // 租户 API key（llm-gateway 签发）
	Client  *http.Client
	// Format 决定对话走哪种协议形态：
	//   - "" / "openai-chat"：POST /v1/chat/completions（默认）；
	//   - "anthropic-messages"：POST /v1/messages（见 anthropic.go）。
	// 若 openai-chat 形态在传输层挂死/端点缺失，客户端会**自动回退**尝试
	// anthropic-messages 一次，并把成功形态记进进程级 discoveredFormats，
	// 后续请求直接走可用形态（详见 chatWithFormatFallback）。
	Format string
}

// discoveredFormats 记录「某网关哪种协议形态实测可用」。key 是归一化后的
// BaseURL。进程级缓存：第一次回退探测要付出一次 openai-chat 的失败代价
// （ResponseHeaderTimeout 30s），之后同进程内直接走可用形态，不再重复付。
var discoveredFormats sync.Map // baseURL(normalized) -> string

// NewClient 构造 llm-gateway 客户端。baseURL 会自动归一化（剥离结尾的 /v1 与
// 斜杠），详见 normalizeBaseURL。
//
// 超时策略：
//   - 整体 Timeout 90s，留给长回答/流式首 token 充分时间；
//   - Transport.ResponseHeaderTimeout 60s。
//
// ## 为什么是 60s（2026-10-02 人工拍板）
//
// 这个值换过一次，两次的理由都留在下面，**别只改数字不改这里**。
//
// **30s 那一版（2026-08-31 加的）**：llm.kxpms.cn 在 preferred 模型里有两个
// model 对 /v1/chat/completions 既不返结果也不返错误（连接挂死），此前无该
// 上限时前端要等满 60s；加上 30s 后能 30s 内即触发 client 错误，handler 再把
// 错误作为 SSE error 事件写回。
//
// **它的代价（也是本次拍板的原因）**：ResponseHeaderTimeout 约束的是「响应头
// 何时到达」，而非流式调用要等上游**真正开始回包**才发头。对推理模型（本项目
// 网关自动路由到 glm-5.2）这很致命：它先把 token 花在 reasoning_content 上，
// 正文 content 最后才吐（2026-10-02 实测：一句 63 字的总结，reasoning 用了
// 985 token）。于是任何给这类模型留了 >30s 预算的 handler，实际预算都被压到
// 30s——例如 handleNoteSummarize 的 `context.WithTimeout(..., 60*time.Second)`
// 只有前 30 秒是真能用的，30~60 秒是**死预算**：handler 以为有 60s，用户看到
// 的是 30s 就「总结失败」。
//
// 取舍：把失败时间从 30s 拉长到最多 60s（整体 Timeout 仍是 90s，不会无界
// 挂死），换取推理模型那 30~60s 真的可用。**上游真挂死时用户要多等，最坏
// 60s 而不是 30s** —— 这是本次拍板接受的代价。
//
// 改动落地时 client_test.go 的 TestNewClient_TransportTimeouts 同步改成 60s；
// 那条测试锁的是**数字**，并且额外锁住「ResponseHeaderTimeout 必须小于整体
// Timeout」这条不变量（60 < 90 成立）。
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: normalizeBaseURL(baseURL),
		APIKey:  apiKey,
		Client: &http.Client{
			Timeout: 90 * time.Second,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 60 * time.Second,
			},
		},
	}
}

// Tool 对应 OpenAI function calling 的工具定义。
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 是工具的函数定义（名称 + 描述 + JSON Schema 参数）。
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"` // JSON Schema
}

// ToolCall 对应 assistant 消息携带的工具调用。
type ToolCall struct {
	Index    int          `json:"index,omitempty"` // only in streaming deltas
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc 是工具调用的函数部分（名称 + 参数 JSON 字符串）。
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatMessage 兼容 OpenAI chat completion 消息格式。
//
// Content 是 any：纯文本时为 string；多模态（带图）时由 ContentParts 生成
// OpenAI 的 [{type:text},{type:image_url}] 数组。旧调用方继续传 string 即可。
//
// ToolCalls 用于 assistant 消息携带工具调用（function calling）。
// ToolCallID 用于 tool role 消息关联到之前的工具调用。
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ContentParts 把"文本 + 图片列表"组装成 OpenAI 多模态 content 数组。
// images 为空时返回原文本，保持纯文本请求的 wire format 不变。
func ContentParts(text string, images []string) any {
	if len(images) == 0 {
		return text
	}
	parts := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, img := range images {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": img},
		})
	}
	return parts
}

// ChatRequest 对应 POST /v1/chat/completions（OpenAI 兼容）
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []Tool        `json:"tools,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	User        string        `json:"user,omitempty"` // 用户标识（审计用）
	WorkType    string        `json:"work_type,omitempty"`
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
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat 调用 llm-gateway 的 chat completion（非流式）。按 Format 分派协议形态，
// openai-chat 失败时自动回退 anthropic-messages（见 chatWithFormatFallback）。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	if c.Format == "anthropic-messages" {
		return c.chatViaMessages(ctx, req)
	}
	if v, ok := discoveredFormats.Load(c.BaseURL); ok && v == "anthropic-messages" {
		return c.chatViaMessages(ctx, req)
	}
	resp, err := c.chatOpenAI(ctx, req)
	if err == nil {
		return resp, nil
	}
	return c.chatWithFormatFallback(ctx, req, err, func() (*ChatResponse, error) {
		return c.chatViaMessages(ctx, req)
	})
}

// chatOpenAI 是原始的 /v1/chat/completions 调用（Chat 拆出的内核）。
func (c *Client) chatOpenAI(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	if req.WorkType != "" {
		httpReq.Header.Set("X-Gw-Work-Type", req.WorkType)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway chat %d: %s", resp.StatusCode, string(r))
	}

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	return &out, nil
}

// chatWithFormatFallback 统一处理「openai-chat 形态不可用 → anthropic-messages
// 兜底」的回退决策与成功后的形态记忆。retry 执行 anthropic 形态的调用。
//
// 只在这些情况回退（保守，避免掩盖真实配置错误）：
//   - 传输层失败（超时/连接重置/ResponseHeaderTimeout）——llm.kxpms.cn 对
//     /chat/completions 的已知症状就是「收下请求既不回结果也不回错误」；
//   - HTTP 404/405/501——端点不存在/方法不对/未实现。
//
// 鉴权失败(401/403)、配额(429)、模型无 provider(503 no_candidate) 等业务
// 错误**不**回退：那些在 anthropic 形态下同样会失败，多打一次只会掩盖根因。
func (c *Client) chatWithFormatFallback(ctx context.Context, req ChatRequest, openAIErr error, retry func() (*ChatResponse, error)) (*ChatResponse, error) {
	if !shouldFallbackToAnthropic(openAIErr) {
		return nil, openAIErr
	}
	resp, err := retry()
	if err != nil {
		// 回退也失败：返回原始错误，让上层看到 openai-chat 的真实症状。
		return nil, openAIErr
	}
	discoveredFormats.Store(c.BaseURL, "anthropic-messages")
	return resp, nil
}

// shouldFallbackToAnthropic 判定 openai-chat 的错误是否值得换 anthropic-messages
// 形态重试一次。
func shouldFallbackToAnthropic(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range []string{"404", "405", "501"} {
		// 错误格式固定为 "llm-gateway chat <code>: ..."。
		if strings.Contains(msg, " chat "+code+":") || strings.Contains(msg, " stream "+code+":") {
			return true
		}
	}
	// 传输层错误（超时/连接被重置等）没有状态码前缀，按「非 HTTP 状态错误」
	// 处理：chatOpenAI 的传输错误消息以 "llm-gateway chat: " 开头（无数字）。
	return strings.HasPrefix(msg, "llm-gateway chat: ") || strings.HasPrefix(msg, "llm-gateway stream: ")
}

// StreamDelta is one chunk of a streaming chat completion (OpenAI SSE shape).
// Content is the incremental text; ToolCalls carries incremental tool call deltas
// (OpenAI 增量模式: index + id/type/function.name/arguments 分帧到达); Usage is
// only present on the final chunk when the request set stream_options.include_usage.
type StreamDelta struct {
	Content          string     `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	FinishReason     string     `json:"finish_reason"`
	Done             bool       `json:"done"`
	Model            string     `json:"model,omitempty"`
	PromptTokens     int        `json:"prompt_tokens,omitempty"`
	CompletionTokens int        `json:"completion_tokens,omitempty"`
	TotalTokens      int        `json:"total_tokens,omitempty"`
}

// Stream 调用 llm-gateway 的 chat completion（流式 SSE）。
//
// 对每个 SSE data 块解析 OpenAI delta 并调用 fn(delta)。fn 返回 false 时
// 提前终止流（客户端断连）。返回最终 usage（若 provider 在末帧返回）。
//
// 请求自动设置 stream=true 和 stream_options.include_usage=true。
// 协议分派与回退策略同 Chat：anthropic-messages 直接走 /v1/messages 流式；
// openai-chat 首帧未出即失败时换 anthropic 形态重试一次（已向客户端输出过
// 内容的尝试不重试，避免同一气泡里重复作答）。
func (c *Client) Stream(ctx context.Context, req ChatRequest, fn func(StreamDelta) bool) (*StreamDelta, error) {
	req.Stream = true
	if c.Format == "anthropic-messages" {
		return c.streamViaMessages(ctx, req, fn)
	}
	if v, ok := discoveredFormats.Load(c.BaseURL); ok && v == "anthropic-messages" {
		return c.streamViaMessages(ctx, req, fn)
	}
	answered := false
	wrapped := func(d StreamDelta) bool {
		if d.Content != "" || len(d.ToolCalls) > 0 {
			answered = true
		}
		return fn(d)
	}
	final, err := c.streamOpenAI(ctx, req, wrapped)
	if err == nil {
		return final, nil
	}
	if answered || !shouldFallbackToAnthropic(err) {
		return final, err
	}
	resp, rerr := c.streamViaMessages(ctx, req, fn)
	if rerr != nil {
		return final, err
	}
	discoveredFormats.Store(c.BaseURL, "anthropic-messages")
	return resp, nil
}

// streamOpenAI 是原始的 /v1/chat/completions 流式调用（Stream 拆出的内核）。
func (c *Client) streamOpenAI(ctx context.Context, req ChatRequest, fn func(StreamDelta) bool) (*StreamDelta, error) {
	payload := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"temperature":    req.Temperature,
		"max_tokens":     req.MaxTokens,
		"stream":         true,
		"user":           req.User,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	if req.WorkType != "" {
		payload["work_type"] = req.WorkType
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	if req.WorkType != "" {
		httpReq.Header.Set("X-Gw-Work-Type", req.WorkType)
	}

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
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway embed %d: %s", resp.StatusCode, string(r))
	}

	var out EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return &out, nil
}

// ModelInfo 是 GET /v1/models 返回的单个模型元数据（OpenAI 兼容）。
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ListModels 调用网关的 OpenAI 兼容模型列表接口（GET /v1/models）。
// 用于前端模型选择器动态填充，避免硬编码可用模型。
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	u := c.BaseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway models %d: %s", resp.StatusCode, string(r))
	}
	var out struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return out.Data, nil
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
	Messages   []json.RawMessage `json:"messages,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	ExportedAt string            `json:"exported_at,omitempty"`
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
