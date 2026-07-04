package adapter

import "context"

// NPSClient describes the discovery and routing data fetched from nps_new.
type NPSClient struct {
	ID   int
	Name string
}

type NPSTunnel struct {
	ID       int
	ClientID int
	Type     string
	Remark   string
	Host     string
	Target   string
}

type NPSAdapter interface {
	ListClients(ctx context.Context) ([]NPSClient, error)
	ListTunnels(ctx context.Context) ([]NPSTunnel, error)
}

type OpenCodeSession struct {
	ID     string
	Title  string
	Status string
}

// RemoteTask 从 OpenCode 实例的 Session API 获取的任务（一个 Session = 一个开发任务）
type RemoteTask struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

type OpenCodeAdapter interface {
	ListSessions(ctx context.Context, instanceBaseURL string) ([]OpenCodeSession, error)
	GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error)
	// ListRemoteTasks 从指定 OpenCode 实例的 /session API 获取开发会话列表，
	// 将每个 Session 映射为一个 RemoteTask。instanceBaseURL 是实例的 HTTP API 根地址。
	ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]RemoteTask, error)
	// ---- Phase V3: 真实会话交互（Phase 2 新增） ----
	// CreateSession 在 OpenCode 实例上新建 session。
	// payload 字段：title?, parentID?(fork), agent?, model?。
	CreateSession(ctx context.Context, instanceBaseURL string, payload *CreateSessionRequest) (*OpenCodeSessionInfo, error)
	// GetMessages 拉取 session 历史消息（用于 SSE 断线后回填）。
	GetMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string) ([]OpenCodeMessage, error)
	// SendPrompt 向 session 发送用户 prompt（异步触发 agent 循环）。
	// 返回 messageID 用于客户端追踪。
	SendPrompt(ctx context.Context, instanceBaseURL, sessionID string, payload *SendPromptRequest) (*SendPromptResponse, error)
	// InterruptSession 中断 session 当前的 agent 循环。
	InterruptSession(ctx context.Context, instanceBaseURL, sessionID string) error
	// SubscribeEvents 订阅 OpenCode 上游 SSE 事件流。
	// 返回的 channel 在 ctx cancel 或 cleanup 调用后关闭。
	SubscribeEvents(ctx context.Context, instanceBaseURL, directory, workspaceID string) (<-chan OpenCodeEvent, func(), error)
	// HealthCheck 检查 OpenCode 实例是否存活。
	HealthCheck(ctx context.Context, instanceBaseURL string) error
}

type NotificationAdapter interface {
	ScheduleTaskReminder(ctx context.Context, taskID string) error
	SendTaskCompleted(ctx context.Context, taskID string) error
}

type ShellBridge interface {
	NotifyTaskCompletion(taskID string, title string) error
	OpenTask(taskID string) error
}
