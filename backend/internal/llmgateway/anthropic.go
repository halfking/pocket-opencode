package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// anthropic.go — Anthropic Messages 协议适配（POST /v1/messages）。
//
// 背景（docs/handoff/2026-10-01-llm-gateway-chat-completions-unsupported.md）：
// 生产网关 llm.kxpms.cn **不提供** OpenAI /v1/chat/completions（所有模型挂死
// 0 bytes），但 /v1/messages（Anthropic 形态）实测 3.4s 正常出字。此前 pocketd
// 的客户端只实现了 openai-chat 一种协议，于是邮件翻译、AI 对话在这个网关上
// 全部不可用。这里把请求/响应在 OpenAI chat 形态与 Anthropic messages 形态间
// 互相翻译，让上层调用方（llmbff Provider、/api/llm/chat）无感切换。
//
// 鉴权：Anthropic 形态用 `x-api-key` + `anthropic-version` 头（实测可用）；
// 同时附带 `Authorization: Bearer`，兼容按 bearer 鉴权的网关实现，两种头并存
// 不冲突。

const anthropicVersion = "2023-06-01"

// anthropicMaxTokens 兜底值：Anthropic 协议 max_tokens 必填，而 OpenAI 形态
// 的调用方经常省略。4096 覆盖邮件翻译/对话的绝大多数场景且不会明显超计费。
const anthropicMaxTokens = 4096

// anthropicRequest 对应 POST /v1/messages 请求体。
type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

// anthropicMessage 的 Content 与 OpenAI 不同：system 不是消息角色而是顶层
// 字段；assistant 的 tool_calls 与 tool 角色的结果都折叠进 content 块数组。
type anthropicMessage struct {
	Role    string `json:"role"` // user | assistant
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// anthropicResponse 对应非流式响应。content 是块数组，文本散落在 type=="text"
// 的块里，需拼接；tool_use 块携带结构化工具调用。
type anthropicResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"` // text | tool_use
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"` // tool_use
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason,omitempty"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// toAnthropicRequest 把 OpenAI chat 形态翻译成 Anthropic messages 形态。
//
// 规则：
//   - role=="system" 的消息抽出来拼成顶层 system（Anthropic 无 system 角色）；
//   - OpenAI 多模态 content 数组（ContentParts 的产物）翻译成 content 块：
//     text → {type:text}，image_url → data URI 解出 base64 source / http(s) 用 url source；
//   - assistant 消息的 tool_calls → {type:tool_use} 块（arguments 解析成对象）；
//   - role=="tool" 的消息 → 下一条 user 消息里的 {type:tool_result} 块。
func toAnthropicRequest(req ChatRequest) anthropicRequest {
	out := anthropicRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		Stream:      false,
		MaxTokens:   req.MaxTokens,
	}
	if out.MaxTokens <= 0 {
		out.MaxTokens = anthropicMaxTokens
	}
	var systemParts []string
	// pendingToolResults 收集 tool 角色消息，附着到下一条 user 消息前。
	var pendingToolResults []map[string]any
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: pendingToolResults})
		pendingToolResults = nil
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if s, ok := m.Content.(string); ok && s != "" {
				systemParts = append(systemParts, s)
			}
		case "tool":
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			})
		case "assistant":
			flushToolResults()
			blocks := anthropicContentBlocks(m.Content)
			for _, tc := range m.ToolCalls {
				input := map[string]any{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			out.Messages = append(out.Messages, anthropicMessage{Role: "assistant", Content: blocks})
		default: // user
			flushToolResults()
			out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: anthropicContentBlocks(m.Content)})
		}
	}
	flushToolResults()
	out.System = strings.Join(systemParts, "\n\n")
	for _, t := range req.Tools {
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]interface{}{"type": "object"}
		}
		out.Tools = append(out.Tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return out
}

// anthropicContentBlocks 把 OpenAI 形态的 content（string 或多模态数组）翻译成
// Anthropic content 块数组。纯文本也统一成块数组（Anthropic 两种都收，统一
// 形态让图片分支不需要特判）。
func anthropicContentBlocks(content any) []map[string]any {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": c}}
	case []any:
		var blocks []map[string]any
		for _, p := range c {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "text":
				if s, _ := pm["text"].(string); s != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": s})
				}
			case "image_url":
				// OpenAI 形态 {"type":"image_url","image_url":{"url":...}}；
				// url 可能是 data URI（base64）或 http(s) 链接。
				iu, _ := pm["image_url"].(map[string]any)
				u, _ := iu["url"].(string)
				if u == "" {
					continue
				}
				if mt, data, ok := strings.Cut(u, ","); ok && strings.HasPrefix(mt, "data:") {
					mediaType := strings.TrimPrefix(mt, "data:")
					if i := strings.Index(mediaType, ";"); i >= 0 {
						mediaType = mediaType[:i]
					}
					blocks = append(blocks, map[string]any{
						"type":   "image",
						"source": map[string]any{"type": "base64", "media_type": mediaType, "data": data},
					})
				} else {
					blocks = append(blocks, map[string]any{
						"type":   "image",
						"source": map[string]any{"type": "url", "url": u},
					})
				}
			}
		}
		return blocks
	default:
		if b, err := json.Marshal(content); err == nil {
			return []map[string]any{{"type": "text", "text": string(b)}}
		}
		return nil
	}
}

// anthropicHeaders 组 Anthropic 形态的请求头。x-api-key 是 Anthropic 官方形态，
// Authorization Bearer 一并附上以兼容网关侧只认 bearer 的实现。
func anthropicHeaders(req *http.Request, apiKey, workType string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if workType != "" {
		req.Header.Set("X-Gw-Work-Type", workType)
	}
}

// chatViaMessages 走 /v1/messages 完成一次非流式补全，并把响应翻译回
// OpenAI ChatResponse 形态（调用方零改动）。
func (c *Client) chatViaMessages(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, _ := json.Marshal(toAnthropicRequest(req))
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	anthropicHeaders(httpReq, c.APIKey, req.WorkType)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway anthropic messages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway anthropic messages %d: %s", resp.StatusCode, string(r))
	}
	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("llm-gateway anthropic error %s: %s", out.Error.Type, out.Error.Message)
	}
	chat := &ChatResponse{ID: out.ID, Model: out.Model, Object: "chat.completion"}
	var text strings.Builder
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			chat.Choices = append(chat.Choices, struct {
				Index   int `json:"index"`
				Message struct {
					Role      string     `json:"role"`
					Content   string     `json:"content"`
					ToolCalls []ToolCall `json:"tool_calls,omitempty"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{})
			choice := &chat.Choices[len(chat.Choices)-1]
			choice.Index = len(chat.Choices) - 1
			choice.Message.Role = "assistant"
			choice.Message.ToolCalls = []ToolCall{{
				ID:   block.ID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      block.Name,
					Arguments: args,
				},
			}}
			choice.FinishReason = "tool_calls"
		}
	}
	if len(chat.Choices) == 0 {
		chat.Choices = append(chat.Choices, struct {
			Index   int `json:"index"`
			Message struct {
				Role      string     `json:"role"`
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{})
		choice := &chat.Choices[0]
		choice.Message.Role = "assistant"
		choice.Message.Content = text.String()
		choice.FinishReason = mapStopReason(out.StopReason)
	} else {
		// 文本与工具调用并存时文本放首个 choice 的 content。
		chat.Choices[0].Message.Content = text.String()
	}
	chat.Usage.PromptTokens = out.Usage.InputTokens
	chat.Usage.CompletionTokens = out.Usage.OutputTokens
	chat.Usage.TotalTokens = out.Usage.InputTokens + out.Usage.OutputTokens
	return chat, nil
}

// mapStopReason Anthropic stop_reason → OpenAI finish_reason（尽力映射）。
func mapStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// streamViaMessages 走 /v1/messages 的流式形态（SSE 具名事件），把
// content_block_delta 翻译成 OpenAI delta 形态回调。
//
// 事件形状（Anthropic streaming）：
//   - message_start       → usage.input_tokens
//   - content_block_start → tool_use 块开始（id/name）
//   - content_block_delta → text_delta 增量文本 / input_json_delta 工具参数增量
//   - content_block_stop  → 工具调用收口（此时才下发完整 ToolCall）
//   - message_delta       → stop_reason + usage.output_tokens
//   - message_stop        → 结束
func (c *Client) streamViaMessages(ctx context.Context, req ChatRequest, fn func(StreamDelta) bool) (*StreamDelta, error) {
	ar := toAnthropicRequest(req)
	ar.Stream = true
	body, _ := json.Marshal(ar)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	anthropicHeaders(httpReq, c.APIKey, req.WorkType)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm-gateway anthropic stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("llm-gateway anthropic stream %d: %s", resp.StatusCode, string(r))
	}
	return parseAnthropicSSE(resp.Body, fn)
}

// parseAnthropicSSE 逐行解析 Anthropic 具名事件流。
func parseAnthropicSSE(r io.Reader, fn func(StreamDelta) bool) (*StreamDelta, error) {
	final := &StreamDelta{}
	var (
		stopSeen     bool
		promptTokens int
		outputTokens int
		model        string
		// toolAcc 累积 tool_use 块：content_block_start 记录 id/name，
		// input_json_delta 累积参数，content_block_stop 时整体下发。
		toolAcc = map[int]*toolAccumulator{}
	)
	emit := func(d StreamDelta) bool {
		if d.Model == "" {
			d.Model = model
		}
		if !fn(d) {
			return false
		}
		return true
	}
	forEachSSEEvent(r, func(event, data string) {
		if data == "" || data == "[DONE]" {
			return
		}
		var payload struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		etype := payload.Type
		if etype == "" {
			etype = event
		}
		switch etype {
		case "message_start":
			model = payload.Message.Model
			promptTokens = payload.Message.Usage.InputTokens
		case "content_block_start":
			if payload.ContentBlock.Type == "tool_use" {
				toolAcc[payload.Index] = &toolAccumulator{
					id: payload.ContentBlock.ID, name: payload.ContentBlock.Name,
				}
			}
		case "content_block_delta":
			switch payload.Delta.Type {
			case "text_delta", "text":
				if payload.Delta.Text != "" {
					final.Content += payload.Delta.Text
					emit(StreamDelta{Content: payload.Delta.Text})
				}
			case "input_json_delta":
				if acc := toolAcc[payload.Index]; acc != nil {
					acc.args.WriteString(payload.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			if acc := toolAcc[payload.Index]; acc != nil {
				args := acc.args.String()
				if args == "" {
					args = "{}"
				}
				final.ToolCalls = append(final.ToolCalls, ToolCall{
					ID:   acc.id,
					Type: "function",
					Function: ToolCallFunc{Name: acc.name, Arguments: args},
				})
				delete(toolAcc, payload.Index)
			}
		case "message_delta":
			if payload.Delta.StopReason != "" {
				final.FinishReason = mapStopReason(payload.Delta.StopReason)
			}
			if payload.Usage.OutputTokens > 0 {
				outputTokens = payload.Usage.OutputTokens
			}
			if payload.Usage.InputTokens > 0 {
				promptTokens = payload.Usage.InputTokens
			}
		case "message_stop":
			stopSeen = true
		}
	})
	if len(final.ToolCalls) > 0 && final.FinishReason == "" {
		final.FinishReason = "tool_calls"
	}
	if final.FinishReason != "" || stopSeen {
		final.Done = true
		final.PromptTokens = promptTokens
		final.CompletionTokens = outputTokens
		final.TotalTokens = promptTokens + outputTokens
	}
	return final, nil
}

// toolAccumulator 累积一个 tool_use 块的参数 JSON 分片。
type toolAccumulator struct {
	id   string
	name string
	args strings.Builder
}

// forEachSSEEvent 把 SSE 字节流拆成 (event, data) 事件对回调。
// 兼容 CRLF；跨多行 data: 按 SSE 规范用 \n 连接。
func forEachSSEEvent(r io.Reader, cb func(event, data string)) {
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	var event, data string
	flush := func() {
		if event == "" && data == "" {
			return
		}
		cb(event, data)
		event, data = "", ""
	}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := string(buf[:idx])
				buf = buf[idx+1:]
				line = strings.TrimRight(line, "\r")
				switch {
				case line == "":
					flush()
				case strings.HasPrefix(line, "event:"):
					event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				case strings.HasPrefix(line, "data:"):
					chunk := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
					if data == "" {
						data = chunk
					} else {
						data += "\n" + chunk
					}
				}
			}
		}
		if err != nil {
			flush()
			return
		}
	}
}
