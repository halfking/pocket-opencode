package presentation

import "time"

// Presentation types
const (
	TypePRD      = "prd"
	TypeTechSpec = "tech-spec"
	TypeWeekly   = "weekly"
)

// Presentation statuses
const (
	StatusDraft     = "draft"
	StatusCompleted = "completed"
	StatusArchived  = "archived"
)

// Input validation limits
const (
	MaxTopicLength     = 200
	MaxContextLength   = 10000
	MaxAudienceLength  = 1000
	MaxKeyPointsLength = 5000
)

// Presentation represents a business proposal or PPT document
type Presentation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`       // prd / tech-spec / weekly / quarterly
	Content   string    `json:"content"`    // Markdown content
	Slides    []Slide   `json:"slides,omitempty"` // PPT slides
	Status    string    `json:"status"`     // draft / completed / archived
	Tags      []string  `json:"tags,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Slide represents a single PPT slide
type Slide struct {
	Title   string `json:"title"`
	Content string `json:"content"`        // Plain text content (will be escaped for HTML)
	Note    string `json:"note,omitempty"` // Speaker notes
}

// GenerateRequest represents a request to generate a presentation
type GenerateRequest struct {
	Type      string `json:"type"`                  // prd / tech-spec / weekly
	Topic     string `json:"topic"`                 // Presentation topic
	Context   string `json:"context,omitempty"`     // Context/requirements description
	Audience  string `json:"audience,omitempty"`    // Target audience
	KeyPoints string `json:"key_points,omitempty"`  // Key points
}

// GenerateResponse represents the response from generating a presentation
type GenerateResponse struct {
	Presentation *Presentation `json:"presentation"`
	RawContent   string        `json:"raw_content"`
}