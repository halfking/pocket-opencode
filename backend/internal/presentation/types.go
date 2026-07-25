package presentation

import "time"

// Presentation 产品方案/PPT
type Presentation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`       // prd / tech-spec / weekly / quarterly
	Content   string    `json:"content"`    // Markdown 方案内容
	Slides    []Slide   `json:"slides,omitempty"` // PPT 分页
	Status    string    `json:"status"`     // draft / completed / archived
	Tags      []string  `json:"tags,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Slide PPT 分页
type Slide struct {
	Title   string `json:"title"`
	Content string `json:"content"`  // HTML 片段
	Note    string `json:"note,omitempty"` // 演讲备注
}

// GenerateRequest 生成请求
type GenerateRequest struct {
	Type      string `json:"type"`       // prd / tech-spec / weekly
	Topic     string `json:"topic"`      // 主题
	Context   string `json:"context,omitempty"` // 上下文/需求描述
	Audience  string `json:"audience,omitempty"` // 目标受众
	KeyPoints string `json:"key_points,omitempty"` // 关键要点
}

// GenerateResponse 生成响应
type GenerateResponse struct {
	Presentation *Presentation `json:"presentation"`
	RawContent   string        `json:"raw_content"`
}