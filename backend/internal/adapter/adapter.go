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
}

type NotificationAdapter interface {
	ScheduleTaskReminder(ctx context.Context, taskID string) error
	SendTaskCompleted(ctx context.Context, taskID string) error
}

type ShellBridge interface {
	NotifyTaskCompletion(taskID string, title string) error
	OpenTask(taskID string) error
}
