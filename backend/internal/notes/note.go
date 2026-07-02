// Package notes provides local SQLite caching of voice-note metadata for
// the personal-assistant module. AI processing (classification, SSOT, graph)
// happens in the kxmemory FastAPI service; pocketd only caches metadata for
// offline browsing and proxies note content to/from kxmemory.
//
// The full notes schema (workspaces, knowledge_blocks, smart_links, etc.)
// lives in kxmemory; this package intentionally stores a thin local mirror.
package notes

// Note is the local cached metadata of a voice note. The full body and AI
// fields are fetched from kxmemory on demand; only fields needed for list
// rendering offline are cached here.
type Note struct {
	ID            string `json:"id"`
	UserID        string `json:"userId"`
	WorkspaceID   string `json:"workspaceId,omitempty"`
	Title         string `json:"title,omitempty"`
	Snippet       string `json:"snippet"`              // first ~200 chars of content
	ContentType   string `json:"contentType"`          // voice | text | mixed
	Domain        string `json:"domain,omitempty"`     // work | study | life | idea
	Tags          string `json:"tags,omitempty"`       // JSON array
	AudioPath     string `json:"audioPath,omitempty"`
	AudioDuration int    `json:"audioDuration,omitempty"`
	CreatedByVoice bool  `json:"createdByVoice"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}
