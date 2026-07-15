// Package meeting provides PostgreSQL-backed meeting metadata cache for
// cloud sync. Full transcript/segments stay on-device (lobster architecture);
// pocketd stores summary, status, and note linkage for multi-device list.
package meeting

import "time"

type Meeting struct {
	ID                string   `json:"id"`
	UserID            string   `json:"userId"`
	Title             string   `json:"title,omitempty"`
	Location          string   `json:"location,omitempty"`
	Participants      []string `json:"participants,omitempty"`
	StartedAt         int64    `json:"startedAt"`
	DurationMs        int      `json:"durationMs"`
	Summary           string   `json:"summary,omitempty"`
	RefinedTranscript string   `json:"refinedTranscript,omitempty"`
	NoteID            string   `json:"noteId,omitempty"`
	Status            string   `json:"status"` // recording|completed|processing|refined
	CreatedAt         int64    `json:"createdAt"`
	UpdatedAt         int64    `json:"updatedAt"`
}

func NowUnix() int64 { return time.Now().Unix() }
