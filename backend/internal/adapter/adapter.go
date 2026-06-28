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

type OpenCodeAdapter interface {
	ListSessions(ctx context.Context, instanceBaseURL string) ([]OpenCodeSession, error)
	GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error)
}

type NotificationAdapter interface {
	ScheduleTaskReminder(ctx context.Context, taskID string) error
	SendTaskCompleted(ctx context.Context, taskID string) error
}

type ShellBridge interface {
	NotifyTaskCompletion(taskID string, title string) error
	OpenTask(taskID string) error
}
