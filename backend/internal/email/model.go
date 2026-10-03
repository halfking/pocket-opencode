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
	// UpdatedAt 是配置的最后修改时间（Unix 秒）。服务端为 SSOT：任何写路径
	// （创建/更新/凭证变更）都会刷新该值；客户端本地库按它与服务端做
	// LWW（last-write-wins）同步，时间新的一方覆盖旧的一方。
	UpdatedAt int64 `json:"updatedAt"`
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
	UpdatedAt       int64  `json:"updatedAt,omitempty"`
	IsRead          bool   `json:"isRead"`
	IsStarred       bool   `json:"isStarred"`
	Category        string `json:"category,omitempty"`
	Importance      string `json:"importance,omitempty"`
	AISummary       string `json:"aiSummary,omitempty"`
	SuggestedAction string `json:"suggestedAction,omitempty"`
	ActionReason    string `json:"actionReason,omitempty"`
	HasAttachments  bool   `json:"hasAttachments"`
	// FolderName 是邮件当前所在目录（IMAP mailbox 名）。空串 = INBOX（历史
	// 行默认值）。用户把邮件移进自定义目录后这里记目标目录，列表按它过滤，
	// IMAP MOVE 之后邮件不再出现在服务器 INBOX，行本身保留作为目录视图的数据源。
	FolderName string `json:"folderName,omitempty"`

	// BodyPath 是完整正文的加密缓存相对路径（见 MarkEmailBodyCached）。仅内部用于
	// 判断缓存命中，不回显前端（json:"-"）。空 = 尚未缓存。
	BodyPath string `json:"-"`
	// DeletedAt > 0 表示伪删除；BodyPurged 表示正文已清空且禁止回源。
	DeletedAt  int64 `json:"deletedAt,omitempty"`
	BodyPurged bool  `json:"bodyPurged,omitempty"`
}

// MailFolder 是用户可见的邮件目录（IMAP mailbox 的本地登记）。
//
// 服务端能力对齐：目录既可以是用户自建的（source=user），也可以是从服务器
// LIST 发现的系统/服务商目录（source=server，如 INBOX、垃圾箱）。邮件的
// 归属由 emails.folder_name 指向目录名（不是 id）——目录名是 IMAP 侧的
// 真实主键，服务器目录被重命名/删除时行随之失效而邮件数据不受损。
type MailFolder struct {
	ID          string `json:"id"`
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	UserID      string `json:"userId,omitempty"`
	// Name 是完整 IMAP 信箱名（可含层级分隔符）。
	Name string `json:"name"`
	// DisplayName 是界面展示名（Name 的最后一段或用户输入）。
	DisplayName string `json:"displayName"`
	// Special 标记特殊用途：inbox/trash/junk/sent/drafts/archive/...，空 = 普通目录。
	Special string `json:"special,omitempty"`
	// Source: user = 本产品创建；server = IMAP LIST 发现。
	Source string `json:"source,omitempty"`
	// ServerSynced 表示目录已在 IMAP 服务器上真实存在（user 目录创建后置真）。
	ServerSynced bool  `json:"serverSynced"`
	CreatedAt    int64 `json:"createdAt"`
	UpdatedAt    int64 `json:"updatedAt"`
	// Extra 承载查询期的派生字段（如目录内邮件数），不入库。
	Extra map[string]any `json:"extra,omitempty"`
}

// OpsLogEntry 是一条「本地邮件迁移操作」的服务端日志。
//
// 用户在前端把邮件移目录/删除时，本地先生效，同时在这里记一条 pending；
// 「同步到服务器」按钮把 pending 逐条经 IMAP 执行（MOVE/移入垃圾箱），
// 成功置 applied、失败置 failed 并带原因。它就是用户要求的「操作 log」，
// 也是断网先操作、联网再同步的可靠队列。
type OpsLogEntry struct {
	ID          string `json:"id"`
	UserID      string `json:"userId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	AccountID   string `json:"accountId"`
	EmailID     string `json:"emailId"`
	UID         int64  `json:"uid,omitempty"`
	// Action: move | delete。delete 的落点是账户垃圾箱（不直接 EXPUNGE）。
	Action string `json:"action"`
	// TargetFolder 是 move 的目标目录名；delete 时留空（由 FindTrashMailbox 决定）。
	TargetFolder string `json:"targetFolder,omitempty"`
	// Subject/Snippet 只为日志可读性留存（邮件正文永不入日志）。
	Subject string `json:"subject,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	// IdempotencyKey 幂等键：客户端 op id（offline 队列重放不会记两行）。
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
	AppliedAt      *int64 `json:"appliedAt,omitempty"`
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
	// Uncategorized 只返回 category 为空的行（收件箱「未分类」+ 归类批次）。
	Uncategorized bool
	// Folder 过滤："" = 只看 INBOX（folder_name 为空的行，收件箱默认视图）；
	// 具体名字 = 只看该目录；"__all__" = 不过滤目录（垃圾清理等全量场景）。
	Folder string
	// Limit 默认 200，上限 500。5 个真实账户各拉一批后，100 会截断收件箱。
	Limit int
	// Since 只返回 GREATEST(date, processed_at, created_at) 更大的行。
	// Unix 秒；若传入毫秒（>1e12）会先除以 1000。
	Since int64
}

// AccountSyncStatus reports per-account sync state for the front-end
// EmailAccountSetup / status panel.
//
// LastAttemptAt / LastSyncError / SyncFailures（2026-10-02 加）是**可观测性**
// 字段，不是顺手加的：last_synced_at 只在成功时写，所以只读它无法区分
// 「没被调度到」与「每分钟被轮询一次但每次都失败」。详见 store.go 的
// email_accounts 迁移注释。
type AccountSyncStatus struct {
	AccountID     string `json:"accountId"`
	DisplayName   string `json:"displayName"`
	EmailAddress  string `json:"emailAddress"`
	LastSyncedAt  int64  `json:"lastSyncedAt,omitempty"`
	LastSyncedUID int64  `json:"lastSyncedUid,omitempty"`
	// LastAttemptAt 是最近一次**尝试**同步的时刻（成功或失败都推进）。
	// 它与 LastSyncedAt 一起看才能判断「卡住了」还是「没在跑」。
	LastAttemptAt int64 `json:"lastAttemptAt,omitempty"`
	// LastSyncError 是最近一次失败的错误摘要；最近一次成功时为空串。
	LastSyncError string `json:"lastSyncError,omitempty"`
	// SyncFailures 是**连续**失败次数（任一次成功即归零）。
	SyncFailures int  `json:"syncFailures,omitempty"`
	Enabled      bool `json:"enabled"`
	PendingCount int  `json:"pendingCount"`
}
