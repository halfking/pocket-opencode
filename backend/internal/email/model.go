package email

// Account mirrors the email_accounts table. CredentialEncrypted holds the
// IMAP password/OAuth token encrypted with the server master key
// (POCKET_EMAIL_MASTER_KEY); plaintext is never persisted.
type Account struct {
	ID              string `json:"id"`
	UserID          string `json:"userId"`
	WorkspaceID     string `json:"workspaceId,omitempty"`
	DisplayName     string `json:"displayName"`
	EmailAddress    string `json:"emailAddress"`
	IMAPHost        string `json:"imapHost"`
	IMAPPort        int    `json:"imapPort"`
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
	HasAttachments  bool   `json:"hasAttachments"`
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
