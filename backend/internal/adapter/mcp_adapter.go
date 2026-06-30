package adapter

import (
	"context"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// MCPAdapter 实现 OpenCodeAdapter 接口，使用 MCP 协议
type MCPAdapter struct {
	mcpClient *mcp.Client
}

// NewMCPAdapter 创建新的 MCP 适配器
func NewMCPAdapter(baseURL, apiKey string) *MCPAdapter {
	return &MCPAdapter{
		mcpClient: mcp.NewClient(baseURL, apiKey),
	}
}

// ListSessions 获取会话列表
func (a *MCPAdapter) ListSessions(ctx context.Context, instanceBaseURL string) ([]OpenCodeSession, error) {
	return []OpenCodeSession{}, nil
}

// GetSessionSummary 获取会话摘要
func (a *MCPAdapter) GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error) {
	return "", nil
}

// ListRemoteTasks MCP adapter 不支持按实例获取 OpenCode 会话。
// 任务数据应由 HTTP adapter 从各 OpenCode 实例的 /session API 获取。
// 此处返回空列表以避免将 ACC 业务任务混入开发任务列表。
func (a *MCPAdapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]RemoteTask, error) {
	return nil, nil
}
