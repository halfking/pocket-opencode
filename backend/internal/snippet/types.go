// internal/snippet/types.go
package snippet

import "time"

// Snippet 代码片段
type Snippet struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Title       string    `json:"title"`
	Language    string    `json:"language"`
	Code        string    `json:"code"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	Source      string    `json:"source,omitempty"`      // manual / session / import
	SourceID    string    `json:"source_id,omitempty"`   // 来源会话 ID
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateSnippetRequest 创建片段请求
type CreateSnippetRequest struct {
	Title       string   `json:"title"`
	Language    string   `json:"language"`
	Code        string   `json:"code"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
}

// ListSnippetsRequest 列表查询参数
type ListSnippetsRequest struct {
	Language  string `json:"language,omitempty"`
	Tag       string `json:"tag,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Search    string `json:"search,omitempty"`
	Limit     int    `json:"limit,omitempty"` // 默认 50
	Offset    int    `json:"offset,omitempty"`
}