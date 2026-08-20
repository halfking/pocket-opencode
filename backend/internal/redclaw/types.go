package redclaw

import "time"

// ClientConfig RedClaw 客户端配置
type ClientConfig struct {
	BaseURL    string `json:"base_url"`    // RedClaw Gateway 地址，如 http://localhost:8092
	Secret     string `json:"secret"`      // 共享密钥
	TenantID   string `json:"tenant_id"`   // 当前租户 ID
	TimeoutSec int    `json:"timeout_sec"` // HTTP 超时秒数，默认 30
}

// ChatRequest LLM 对话请求
type ChatRequest struct {
	TenantID string    `json:"tenant_id"`
	UserID   string    `json:"user_id"`
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
}

// Message 对话消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse LLM 对话响应
type ChatResponse struct {
	Message   Message `json:"message"`
	ModelUsed string  `json:"model_used"`
	LatencyMs int64   `json:"latency_ms"`
}

// KnowledgeSearchRequest 知识库检索请求
type KnowledgeSearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	TopK     int    `json:"top_k,omitempty"`
}

// KnowledgeSearchResponse 知识库检索响应
type KnowledgeSearchResponse struct {
	Results []KnowledgeResult `json:"results"`
}

// KnowledgeResult 知识库条目
type KnowledgeResult struct {
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// BridgeEvent 桥接事件 (WebSocket 推送)
type BridgeEvent struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}