package adapter

import "encoding/json"

// CreateSessionRequest 是 POST /session 的请求体。
type CreateSessionRequest struct {
	ID       *string         `json:"id,omitempty"`
	Agent    *string         `json:"agent,omitempty"`
	Model    *ModelRefHTTP   `json:"model,omitempty"`
	Location *LocationRefRef `json:"location,omitempty"`
}

// ModelRefHTTP 模型引用
type ModelRefHTTP struct {
	ID         string  `json:"id"`
	ProviderID string  `json:"providerID"`
	Variant    *string `json:"variant,omitempty"`
}

// LocationRefRef 位置引用
type LocationRefRef struct {
	Directory   string  `json:"directory"`
	WorkspaceID *string `json:"workspaceID,omitempty"`
}

// SendPromptRequest 是 POST /session/:sessionID/message 的请求体。
// OpenCode V2 真实格式（来自 SDK types.gen.ts SessionPromptData）：
//   { parts: [...], agent?, model?, tools?, system? }
// 注意：没有 prompt 包装，parts 在顶层。V1 的 prompt.text 不再支持。
type SendPromptRequest struct {
	Parts    []PromptPart   `json:"parts"`              // V2 必填：多模态内容数组
	Agent    *string        `json:"agent,omitempty"`
	Model    *ModelRefHTTP  `json:"model,omitempty"`
	MessageID *string       `json:"messageID,omitempty"`
}

// PromptPayload 兼容旧调用方：Text 自动转为 Parts。
type PromptPayload struct {
	Text string `json:"-"`
}

// MarshalJSON 确保 Text 自动同步到 Parts（OpenCode V2 要求 parts 数组非空）。
func (p PromptPayload) MarshalJSON() ([]byte, error) {
	if p.Text != "" {
		return json.Marshal([]PromptPart{{Type: "text", Text: p.Text}})
	}
	return json.Marshal([]PromptPart{})
}

// PromptPart 多模态片段（V2 TextPartInput 等）
type PromptPart struct {
	Type     string         `json:"type"` // "text" | "file" | "image"
	Text     string         `json:"text,omitempty"`
	URL      string         `json:"url,omitempty"`
	Mime     string         `json:"mime,omitempty"`
	Filename string         `json:"filename,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SendPromptResponse 是 POST /session/:sessionID/message 的响应。
type SendPromptResponse struct {
	MessageID string `json:"messageID"`
	Enqueued  bool   `json:"enqueued"`
	Position  *int   `json:"position,omitempty"`
}