package email

// Account mirrors the email_accounts table. CredentialEncrypted holds the
// IMAP password/OAuth token encrypted with the server master key
// (POCKET_EMAIL_MASTER_KEY); plaintext is never persisted.
//
// SMTP credentials live in a separate `smtp_credential_encrypted` column so
// SMTP can be configured independently from IMAP.
type Account struct {
	ID              string `json:"id"`
	UserID          string `json:"userId"`
	WorkspaceID     string `json:"workspaceId,omitempty"`
	DisplayName     string `json:"displayName"`
	EmailAddress    string `json:"emailAddress"`
	IMAPHost        string `json:"imapHost"`
	IMAPPort        int    `json:"imapPort"`
	SMTPHost        string `json:"smtpHost,omitempty"`
	SMTPPort        int    `json:"smtpPort,omitempty"`
	AuthType        string `json:"authType"` // password | oauth2
	SyncIntervalMin int    `json:"syncIntervalMin"`
	LastSyncedUID   int64  `json:"lastSyncedUid,omitempty"`
	LastSyncedAt    int64  `json:"lastSyncedAt,omitempty"`
	Rules           string `json:"rules,omitempty"` // JSON
	Enabled         bool   `json:"enabled"`
	CreatedAt       int64  `json:"createdAt"`
}

// Email is the cached envelope + AI classification result.
type Email struct {
	ID              string `json:"id"`
	AccountID       string `json:"accountId"`
	MessageID       string `json:"messageId,omitempty"`
	UID             int64  `json:"uid,omitempty"`
	WorkspaceID     string `json:"workspaceId,omitempty"`
	FromAddress     string `json:"fromAddress"`
	FromName        string `json:"fromName,omitempty"`
	Subject         string `json:"subject"`
	Snippet         string `json:"snippet"`
	Date            int64  `json:"date"`
	IsRead          bool   `json:"isRead"`
	IsStarred       bool   `json:"isStarred"`
	Category        string `json:"category,omitempty"`
	Importance      string `json:"importance,omitempty"`
	AISummary       string `json:"aiSummary,omitempty"`
	SuggestedAction string `json:"suggestedAction,omitempty"`
	ActionReason    string `json:"actionReason,omitempty"`
	HasAttachments  bool   `json:"hasAttachments"`

	// BodyPath 是完整正文的加密缓存相对路径（见 MarkEmailBodyCached）。仅内部用于
	// 判断缓存命中，不回显前端（json:"-"）。空 = 尚未缓存。
	BodyPath string `json:"-"`
}

// VacationReply represents a configured auto-reply window.
type VacationReply struct {
	ID          string `json:"id"`
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId"`
	Enabled     bool   `json:"enabled"`
	StartAt     int64  `json:"startAt"`
	EndAt       int64  `json:"endAt"`
	Subject     string `json:"subject"`
	BodyText    string `json:"bodyText"`
	LastSentAt  *int64 `json:"lastSentAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type VacationDelivery struct {
	VacationID              string
	EmailID                 string
	AccountID               string
	WorkspaceID             string
	UserID                  string
	Recipient               string
	OriginalMessageID       string
	OriginalSubject         string
	VacationSubject         string
	VacationBody            string
	SMTPHost                string
	SMTPPort                int
	SenderAddress           string
	SMTPEncryptedCredential string
	ClaimedAt               int64
}

// OutgoingMessage is the transport-neutral payload passed to an injected SMTP sender.
type OutgoingMessage struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	Subject  string
	Body     string
	Headers  map[string]string
}

// ActionIntent 记录规则建议的副作用动作（archive / route-folder / trigger-autoreply）。
//
// 由 fetcher 在评估规则后落表，后续 job 按 status='pending' 顺序消费并标记 applied/failed。
// IdempotencyKey 由 email_id + action 派生，保证同一邮件同一动作只产生一行。
type ActionIntent struct {
	ID             string `json:"id"`
	EmailID        string `json:"emailId"`
	AccountID      string `json:"accountId"`
	WorkspaceID    string `json:"workspaceId,omitempty"`
	UserID         string `json:"userId,omitempty"`
	Action         string `json:"action"`
	Folder         string `json:"folder,omitempty"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
	AppliedAt      *int64 `json:"appliedAt,omitempty"`
}

// DailySummary is the LLM-generated end-of-day digest.
type DailySummary struct {
	ID             string `json:"id"`
	UserID         string `json:"userId"`
	WorkspaceID    string `json:"workspaceId,omitempty"`
	SummaryDate    string `json:"summaryDate"`
	TotalCount     int    `json:"totalCount"`
	ImportantCount int    `json:"importantCount"`
	Content        string `json:"content"`
	ActionItems    string `json:"actionItems,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
}

// ListFilter parameterizes ListEmails queries.
type ListFilter struct {
	AccountID  string
	Category   string
	Importance string
	UnreadOnly bool
}

// AccountSyncStatus reports per-account sync state for the front-end
// EmailAccountSetup / status panel.
type AccountSyncStatus struct {
	AccountID     string `json:"accountId"`
	DisplayName   string `json:"displayName"`
	EmailAddress  string `json:"emailAddress"`
	LastSyncedAt  int64  `json:"lastSyncedAt,omitempty"`
	LastSyncedUID int64  `json:"lastSyncedUid,omitempty"`
	Enabled       bool   `json:"enabled"`
	PendingCount  int    `json:"pendingCount"`
}
