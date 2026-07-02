package adapter

// CreateSessionRequest 是 POST /api/session 的请求体。
// 对应 OpenCode: ~/workspace/ai/opencode/packages/server/src/groups/session.ts
//   payload: Schema.Struct({
//     id: SessionV2.ID.pipe(Schema.optional),
//     agent: AgentV2.ID.pipe(Schema.optional),
//     model: ModelV2.Ref.pipe(Schema.optional),
//     location: Location.Ref.pipe(Schema.optional),
//   })
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

// SendPromptRequest 是 POST /api/session/:sessionID/prompt 的请求体。
//   payload: Schema.Struct({
//     id: SessionMessage.ID.pipe(Schema.optional),
//     prompt: Prompt,
//     delivery: SessionInput.Delivery.pipe(Schema.optional),
//     resume: Schema.Boolean.pipe(Schema.optional),
//   })
type SendPromptRequest struct {
	ID       *string            `json:"id,omitempty"`
	Prompt   PromptPayload      `json:"prompt"`
	Delivery *string            `json:"delivery,omitempty"` // "queue" | "single"
	Resume   *bool              `json:"resume,omitempty"`
	Metadata map[string]any     `json:"metadata,omitempty"`
}

// PromptPayload 是 Prompt 结构
// 对应 ~/workspace/ai/opencode/packages/core/src/session/prompt.ts
type PromptPayload struct {
	Text     string         `json:"text"`     // 文本内容
	Parts    []PromptPart   `json:"parts,omitempty"` // 多模态内容
	Agent    *string        `json:"agent,omitempty"`
	Model    *ModelRefHTTP  `json:"model,omitempty"`
}

// PromptPart 多模态片段
type PromptPart struct {
	Type     string         `json:"type"` // "text" | "file" | "image"
	Text     string         `json:"text,omitempty"`
	URL      string         `json:"url,omitempty"`
	Mime     string         `json:"mime,omitempty"`
	Filename string         `json:"filename,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SendPromptResponse 是 POST /api/session/:sessionID/prompt 的响应 data 部分。
// 对应 success: Schema.Struct({ data: SessionInput.Admitted })
type SendPromptResponse struct {
	MessageID string `json:"messageID"` // 已接收的 Message ID
	Enqueued  bool   `json:"enqueued"`  // 是否已加入队列
	Position  *int   `json:"position,omitempty"` // 队列位置
}