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
}

// VacationReply represents a configured auto-reply window. SMTP delivery
// is intentionally not yet implemented; the table currently stores
// configuration only so the UI can save and audit.
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
