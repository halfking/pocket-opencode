// Package facade implements the OpenPocket client for the RedClaw platform
// façade API (`/api/v2/*`, contract: RedClaw docs/全面优化v1/openapi-facade-v1.yaml).
//
// Conventions enforced by this client:
//   - Authentication is always `Authorization: Bearer <service JWT>`; the tenant
//     is derived from the token claims on the provider side. The client never
//     sends a bare X-User-Id / X-Tenant-ID identity header.
//   - Every write operation carries an `Idempotency-Key` (caller-supplied or
//     auto-generated), so retries never create duplicates.
//   - Every request carries an `X-Correlation-ID` (caller-supplied or
//     auto-generated).
//   - Errors are parsed into the unified envelope (`error.code/message/retryable`)
//     and returned as `*APIError`.
//   - Cursor pagination is surfaced via the `Page` struct on list responses.
package facade

// TaskContract is the typed task contract embedded in CreateTaskRequest.
type TaskContract struct {
	Type       string                 `json:"type"`                 // agent_task | workflow | manual
	Inputs     map[string]interface{} `json:"inputs,omitempty"`     // optional free-form inputs
	Acceptance []string               `json:"acceptance,omitempty"` // optional acceptance criteria
	RiskLevel  string                 `json:"risk_level,omitempty"` // low | medium | high
}

// CreateTaskRequest is the body of POST /api/v2/tasks.
type CreateTaskRequest struct {
	ProjectID    string        `json:"project_id"`
	Title        string        `json:"title"`
	Description  string        `json:"description,omitempty"`
	TaskContract *TaskContract `json:"task_contract,omitempty"`
	ClientRef    string        `json:"client_ref,omitempty"`
}

// TaskCreated is the `data` payload of TaskCreatedResponse.
type TaskCreated struct {
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`
	StatusURL   string `json:"status_url"`
	OperationID string `json:"operation_id,omitempty"`
}

// TaskCreatedResponse is the 202 response of POST /api/v2/tasks.
type TaskCreatedResponse struct {
	Data          TaskCreated `json:"data"`
	RequestID     string      `json:"request_id"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}

// TaskItem is one entry of the task list / task detail.
type TaskItem struct {
	TaskID          string `json:"task_id"`
	ProjectID       string `json:"project_id"`
	Title           string `json:"title"`
	Status          string `json:"status"`
	StatusSource    string `json:"status_source,omitempty"`
	ResourceVersion int64  `json:"resource_version"`
	RunID           string `json:"run_id,omitempty"`
	UpdatedAt       string `json:"updated_at"`
}

// TaskDetail is the `data` payload of GET /api/v2/tasks/{task_id}.
type TaskDetail struct {
	TaskItem
	Description  string                 `json:"description,omitempty"`
	TaskContract map[string]interface{} `json:"task_contract,omitempty"`
	Correlation  string                 `json:"correlation_id,omitempty"`
}

// Page is the cursor-pagination envelope shared by all list responses.
// `next_cursor` empty/absent means no more pages.
type Page struct {
	Limit      int    `json:"limit,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// HasMore reports whether another page is available.
func (p Page) HasMore() bool { return p.NextCursor != "" }

// ListTasksQuery is the query of GET /api/v2/tasks.
type ListTasksQuery struct {
	ProjectID string
	Status    string
	Limit     int
	Cursor    string
}

// TaskListResponse is the 200 response of GET /api/v2/tasks.
type TaskListResponse struct {
	Data      []TaskItem `json:"data"`
	Page      Page       `json:"page,omitempty"`
	RequestID string     `json:"request_id"`
}

// TaskDetailResponse is the 200 response of GET /api/v2/tasks/{task_id}.
type TaskDetailResponse struct {
	Data      TaskDetail `json:"data"`
	RequestID string     `json:"request_id"`
}

// CandidateDecision is one candidate-level decision inside an approval decision.
type CandidateDecision struct {
	CandidateID string `json:"candidate_id"`
	Decision    string `json:"decision"` // promote | reject | defer
	Reason      string `json:"reason,omitempty"`
}

// ApprovalDecisionRequest is the body of POST /api/v2/approvals/{gate_id}/decision.
type ApprovalDecisionRequest struct {
	Decision            string              `json:"decision"` // approve | reject
	Reason              string              `json:"reason,omitempty"`
	ExpectedGateVersion int64               `json:"expected_gate_version"`
	CandidateDecisions  []CandidateDecision `json:"candidate_decisions,omitempty"`
}

// ApprovalAccepted is the `data` payload of ApprovalDecisionResponse.
type ApprovalAccepted struct {
	ApprovalID string `json:"approval_id"`
	GateID     string `json:"gate_id"`
	Status     string `json:"status"`
	StatusURL  string `json:"status_url"`
}

// ApprovalDecisionResponse is the 202 response of the approval decision endpoint.
type ApprovalDecisionResponse struct {
	Data          ApprovalAccepted `json:"data"`
	RequestID     string           `json:"request_id"`
	CorrelationID string           `json:"correlation_id,omitempty"`
}

// ListNotificationsQuery is the query of GET /api/v2/notifications.
type ListNotificationsQuery struct {
	UnreadOnly bool
	Limit      int
	Cursor     string
}

// NotificationItem is one entry of the notification list.
type NotificationItem struct {
	NotificationID string `json:"notification_id"`
	Type           string `json:"type"`
	Severity       string `json:"severity"`
	Source         string `json:"source"`
	Title          string `json:"title"`
	Body           string `json:"body,omitempty"`
	DeepLink       string `json:"deep_link,omitempty"`
	ReadAt         string `json:"read_at"`
	AckAt          string `json:"ack_at"`
	CreatedAt      string `json:"created_at"`
}

// NotificationListResponse is the 200 response of GET /api/v2/notifications.
type NotificationListResponse struct {
	Data      []NotificationItem `json:"data"`
	Page      Page               `json:"page,omitempty"`
	RequestID string             `json:"request_id"`
}

// NotificationAck is the `data` payload of NotificationAckResponse.
type NotificationAck struct {
	NotificationID string `json:"notification_id"`
	AckAt          string `json:"ack_at"`
}

// NotificationAckResponse is the 200 response of POST /api/v2/notifications/{id}/ack.
type NotificationAckResponse struct {
	Data      NotificationAck `json:"data"`
	RequestID string          `json:"request_id"`
}

// MemoryScopeChain scopes a memory search.
type MemoryScopeChain struct {
	TenantID       string `json:"tenant_id"`
	ProjectID      string `json:"project_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
}

// MemoryActor identifies the requesting actor.
type MemoryActor struct {
	Type string `json:"type"` // user | service | agent
	ID   string `json:"id"`
}

// MemorySearchFilters filters a memory search.
type MemorySearchFilters struct {
	Tags              []string `json:"tags,omitempty"`
	SourceTypes       []string `json:"source_types,omitempty"`
	ClassificationMax string   `json:"classification_max,omitempty"` // internal | confidential | restricted
}

// MemorySearchPolicy controls degradation behaviour.
type MemorySearchPolicy struct {
	OnDegraded string `json:"on_degraded,omitempty"` // fail_closed | degraded_with_warning
}

// MemorySearchRequest is the body of POST /api/v2/memory/search.
type MemorySearchRequest struct {
	Query       string               `json:"query"`
	ScopeChain  *MemoryScopeChain    `json:"scope_chain"`
	TopK        int                  `json:"top_k"`
	TokenBudget int                  `json:"token_budget,omitempty"`
	Filters     *MemorySearchFilters `json:"filters,omitempty"`
	Policy      *MemorySearchPolicy  `json:"policy,omitempty"`
	Actor       *MemoryActor         `json:"actor,omitempty"`
}

// MemoryItem is one retrieval result.
type MemoryItem struct {
	Source         string                 `json:"source"`
	MemoryID       string                 `json:"memory_id,omitempty"`
	DocumentID     string                 `json:"document_id,omitempty"`
	Scope          string                 `json:"scope"`
	Score          float64                `json:"score"`
	TokenCount     int                    `json:"token_count"`
	Provenance     map[string]interface{} `json:"provenance,omitempty"`
	PolicyDecision string                 `json:"policy_decision"` // allow | redacted
	Snippet        string                 `json:"snippet,omitempty"`
}

// MemorySearchData is the `data` payload of MemorySearchResponse.
type MemorySearchData struct {
	Items           []MemoryItem `json:"items"`
	Degraded        bool         `json:"degraded"`
	DegradedReasons []string     `json:"degraded_reasons,omitempty"`
}

// MemorySearchResponse is the 200 response of POST /api/v2/memory/search.
type MemorySearchResponse struct {
	Data          MemorySearchData `json:"data"`
	RequestID     string           `json:"request_id"`
	CorrelationID string           `json:"correlation_id,omitempty"`
}

// ErrorDetail is the unified error envelope of the façade API.
type ErrorDetail struct {
	Code      string                   `json:"code"`
	Message   string                   `json:"message"`
	Retryable bool                     `json:"retryable"`
	Details   map[string]interface{}   `json:"details,omitempty"`
	Conflicts []map[string]interface{} `json:"conflicts,omitempty"`
}

// ErrorEnvelope is the error response body shape (`error` + `request_id`).
type ErrorEnvelope struct {
	Error         ErrorDetail `json:"error"`
	RequestID     string      `json:"request_id"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}
