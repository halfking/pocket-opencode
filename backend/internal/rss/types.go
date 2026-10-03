// Package rss contains the RSS/Atom ingestion domain. It deliberately has no
// dependency on the HTTP server or command packages.
package rss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound         = errors.New("rss: not found")
	ErrStoreUnavailable = errors.New("rss: store unavailable")
	ErrInvalidScope     = errors.New("rss: user_id and workspace_id are required")
)

type SourceStatus string

const (
	SourceActive   SourceStatus = "active"
	SourceDisabled SourceStatus = "disabled"
	SourceError    SourceStatus = "error"
)

type ItemStatus string

const (
	ItemUnread   ItemStatus = "unread"
	ItemRead     ItemStatus = "read"
	ItemStarred  ItemStatus = "starred"
	ItemArchived ItemStatus = "archived"
)

type DraftStatus string

const (
	DraftPending   DraftStatus = "pending"
	DraftPublished DraftStatus = "published"
	DraftFailed    DraftStatus = "failed"
)

type PublishStatus string

const (
	PublishPending   PublishStatus = "pending"
	PublishSucceeded PublishStatus = "succeeded"
	PublishFailed    PublishStatus = "failed"
)

type Source struct {
	ID, UserID, WorkspaceID                    string
	URL, Title, Description, SiteURL, Language string
	// Category 决定日报分组：it / finance / news，空值归入 other。
	Category string
	ETag, LastModified                         string
	Status                                     SourceStatus
	Enabled                                    bool
	Error                                      string
	FetchInterval                              time.Duration
	NextFetchAt, LastFetchedAt                 *time.Time
	CreatedAt, UpdatedAt                       time.Time
}

type Item struct {
	ID, SourceID, UserID, WorkspaceID                          string
	GUID, Hash, URL, Title, Author, Summary, Content, Language string
	Categories                                                 []string
	PublishedAt, UpdatedAt, FetchedAt                          *time.Time
	Relevance                                                  float64
	MatchReasons                                               []string
	Status                                                     ItemStatus
}

type FilterRule struct {
	ID, UserID, WorkspaceID, Name               string
	Enabled                                     bool
	IncludeKeywords, ExcludeKeywords, Languages []string
	Since, Until                                *time.Time
	MinRelevance                                float64
	CreatedAt, UpdatedAt                        time.Time
}

type Draft struct {
	ID, ItemID, UserID, WorkspaceID string
	Text                            string
	Status                          DraftStatus
	CreatedAt, UpdatedAt            time.Time
}

type PublishAttempt struct {
	ID, DraftID, ItemID, UserID, WorkspaceID, Platform, RemoteID, Error string
	Status                                                              PublishStatus
	AttemptedAt, CreatedAt                                              *time.Time
}

type CreateSourceRequest struct {
	URL, Title, Description, SiteURL, Language string
	Category                                   string
	Enabled                                    bool
	FetchInterval                              time.Duration
}
type UpdateSourceRequest struct {
	URL, Title, Description, SiteURL, Language *string
	Category                                   *string
	Enabled                                    *bool
	FetchInterval                              *time.Duration
	Status                                     *SourceStatus
}
type ListItemsOptions struct {
	Limit, Offset int
	Status        ItemStatus
	Since, Until  *time.Time
	Query         string
	Unmatched     bool
}
type UpsertDraftRequest struct {
	ID, ItemID, Text string
	Status           DraftStatus
}
type UpdateDraftRequest struct {
	Text   *string
	Status *DraftStatus
}
type PublishAttemptRequest struct {
	DraftID, ItemID, Platform, RemoteID, Error string
	Status                                     PublishStatus
}
type RetentionOptions struct{ ItemsBefore, AttemptsBefore, DraftsBefore *time.Time }

// Scope is used by every persistence operation to make cross-tenant access
// impossible to express accidentally.
type Scope struct{ UserID, WorkspaceID string }

func (s Scope) valid() bool {
	return strings.TrimSpace(s.UserID) != "" && strings.TrimSpace(s.WorkspaceID) != ""
}

func stableHash(guid, link, title, content string) string {
	v := strings.TrimSpace(guid)
	if v == "" {
		v = strings.TrimSpace(link)
	}
	if v == "" {
		v = strings.TrimSpace(title) + "\x00" + strings.TrimSpace(content)
	}
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// itemID 是一条 item 的行主键。
//
// 必须是 (source_id, content hash) 的**组合**，不能只拿其中之一：
//   - 只用 content hash：两个源转载同一篇稿件（中新网财经/要闻、CNBC 的
//     Markets/Technology）会撞主键；两个用户订同一个源也会撞。
//   - 只用 source_id：同一个源的所有条目主键相同 ⇒ 每个源最多只能存进一条，
//     后面每一条都报 duplicate key。真实 38 个源上跑，一分钟就能把这堵死。
//
// 单独写这个函数而不是复用 stableHash：stableHash 的语义是「guid / link /
// 标题+正文 里取第一个非空的」，它**不是**「把这些都拼起来哈希」——
// stableHash(source.ID, h, "", "") 只等于 sha256(source.ID)。
func itemID(sourceID, contentHash string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(sourceID) + "\x00" + strings.TrimSpace(contentHash)))
	return "it_" + hex.EncodeToString(h[:])
}

// StoreAPI is the persistence surface needed by ingestion and scheduling.
type StoreAPI interface {
	Available() bool
	ClaimDueSources(context.Context, Scope, time.Time, int) ([]Source, error)
	UpsertItem(context.Context, Item) error
	SetSourceFetched(context.Context, Scope, string, time.Time, string, string, error) error
}
