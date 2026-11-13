package notelinks

import (
	"database/sql"
	"fmt"
	"time"
)

// LinkType defines the type of relationship between notes.
type LinkType string

const (
	LinkTypeRelated       LinkType = "related"
	LinkTypeReference     LinkType = "reference"
	LinkTypeParent        LinkType = "parent"
	LinkTypeChild         LinkType = "child"
	LinkTypeBidirectional LinkType = "bidirectional"
)

// NoteLink represents a link between two notes.
type NoteLink struct {
	ID           string   `json:"id"`
	SourceNoteID string   `json:"sourceNoteId"`
	TargetNoteID string   `json:"targetNoteId"`
	LinkType     LinkType `json:"linkType"`
	Confidence   float64  `json:"confidence"`   // 0-1, for AI-generated links
	CreatedBy    string   `json:"createdBy"`    // "user" | "ai"
	Reason       string   `json:"reason,omitempty"` // AI recommendation reason
	CreatedAt    int64    `json:"createdAt"`
}

// Store handles note link persistence.
type Store struct {
	db *sql.DB
}

// NewStore creates a new note link store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create creates a new note link.
func (s *Store) Create(link *NoteLink) error {
	link.CreatedAt = time.Now().UnixMilli()

	query := `
		INSERT INTO note_links (id, source_note_id, target_note_id, link_type, confidence, created_by, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_note_id, target_note_id, link_type) DO NOTHING
	`
	_, err := s.db.Exec(query, link.ID, link.SourceNoteID, link.TargetNoteID,
		string(link.LinkType), link.Confidence, link.CreatedBy, stringOrNull(link.Reason), link.CreatedAt)
	return err
}

// Delete deletes a note link by ID.
func (s *Store) Delete(id string) error {
	query := `DELETE FROM note_links WHERE id = ?`
	result, err := s.db.Exec(query, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("link not found: %s", id)
	}

	return nil
}

// GetOutgoingLinks retrieves all links from a source note.
func (s *Store) GetOutgoingLinks(sourceNoteID string) ([]NoteLink, error) {
	query := `
		SELECT id, source_note_id, target_note_id, link_type, confidence, created_by, reason, created_at
		FROM note_links
		WHERE source_note_id = ?
		ORDER BY confidence DESC, created_at DESC
	`
	return s.queryLinks(query, sourceNoteID)
}

// GetIncomingLinks retrieves all links to a target note (backlinks).
func (s *Store) GetIncomingLinks(targetNoteID string) ([]NoteLink, error) {
	query := `
		SELECT id, source_note_id, target_note_id, link_type, confidence, created_by, reason, created_at
		FROM note_links
		WHERE target_note_id = ?
		ORDER BY confidence DESC, created_at DESC
	`
	return s.queryLinks(query, targetNoteID)
}

// GetRelatedNotes retrieves both outgoing and incoming links for a note.
func (s *Store) GetRelatedNotes(noteID string) (outgoing, incoming []NoteLink, err error) {
	outgoing, err = s.GetOutgoingLinks(noteID)
	if err != nil {
		return nil, nil, err
	}

	incoming, err = s.GetIncomingLinks(noteID)
	if err != nil {
		return nil, nil, err
	}

	return outgoing, incoming, nil
}

// DeleteByNoteID deletes all links associated with a note (used when deleting a note).
func (s *Store) DeleteByNoteID(noteID string) error {
	query := `DELETE FROM note_links WHERE source_note_id = ? OR target_note_id = ?`
	_, err := s.db.Exec(query, noteID, noteID)
	return err
}

// CreateBatch creates multiple links in a transaction.
func (s *Store) CreateBatch(links []NoteLink) error {
	if len(links) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO note_links (id, source_note_id, target_note_id, link_type, confidence, created_by, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_note_id, target_note_id, link_type) DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	for _, link := range links {
		link.CreatedAt = now
		_, err := stmt.Exec(link.ID, link.SourceNoteID, link.TargetNoteID,
			string(link.LinkType), link.Confidence, link.CreatedBy, stringOrNull(link.Reason), link.CreatedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// queryLinks is a helper to execute link queries.
func (s *Store) queryLinks(query string, args ...interface{}) ([]NoteLink, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []NoteLink
	for rows.Next() {
		link := NoteLink{}
		var reason sql.NullString
		var linkType string

		if err := rows.Scan(&link.ID, &link.SourceNoteID, &link.TargetNoteID,
			&linkType, &link.Confidence, &link.CreatedBy, &reason, &link.CreatedAt); err != nil {
			return nil, err
		}

		link.LinkType = LinkType(linkType)
		link.Reason = reason.String

		links = append(links, link)
	}

	return links, rows.Err()
}

// stringOrNull converts empty string to sql.NullString.
func stringOrNull(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// LinkInput is the input for creating links.
type LinkInput struct {
	TargetNoteID string   `json:"targetNoteId"`
	LinkType     LinkType `json:"linkType"`
}

// Validate validates link input.
func (input *LinkInput) Validate() error {
	if input.TargetNoteID == "" {
		return fmt.Errorf("targetNoteId is required")
	}

	validTypes := map[LinkType]bool{
		LinkTypeRelated:       true,
		LinkTypeReference:     true,
		LinkTypeParent:        true,
		LinkTypeChild:         true,
		LinkTypeBidirectional: true,
	}

	if !validTypes[input.LinkType] {
		return fmt.Errorf("invalid link type: %s", input.LinkType)
	}

	return nil
}
