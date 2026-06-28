package model

type PocketInstance struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Environment     string   `json:"environment"`
	NPSClientID     int      `json:"npsClientId"`
	Capabilities    []string `json:"capabilities"`
	Health          string   `json:"health"`
	LastHeartbeatAt string   `json:"lastHeartbeatAt"`
}

type TaskSummary struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Status            string `json:"status"`
	Priority          string `json:"priority"`
	WorkstreamID      string `json:"workstreamId"`
	SessionCount      int    `json:"sessionCount"`
	PendingApprovals  int    `json:"pendingApprovals"`
}

type SessionResumeBrief struct {
	InstanceID    string   `json:"instanceId"`
	SessionID     string   `json:"sessionId"`
	TaskID        string   `json:"taskId"`
	Title         string   `json:"title"`
	CurrentState  string   `json:"currentState"`
	LastObjective string   `json:"lastObjective"`
	Decisions     []string `json:"decisions"`
	ChangedFiles  []string `json:"changedFiles"`
	Blockers      []string `json:"blockers"`
	NextAction    string   `json:"nextAction"`
}
