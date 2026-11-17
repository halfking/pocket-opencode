package sync

import (
	"database/sql"
	"time"
)

// SyncState tracks the synchronization status of a note between local and memora.
type SyncState struct {
	NoteID       string `json:"noteId"`
	MemoraID     string `json:"memoraId,omitempty"` // memora 侧的 ID
	ContentHash  string `json:"contentHash"`        // SHA256(content)
	LastSyncedAt int64  `json:"lastSyncedAt,omitempty"`
	SyncStatus   string `json:"syncStatus"` // 'pending', 'synced', 'conflict', 'error'
	ErrorMessage string `json:"errorMessage,omitempty"`
	Version      int    `json:"version"`
}

// SyncStateStore handles sync state persistence in SQLite.
type SyncStateStore struct {
	db *sql.DB
}

// NewSyncStateStore creates a new sync state store.
func NewSyncStateStore(db *sql.DB) (*SyncStateStore, error) {
	s := &SyncStateStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrate creates the sync_state table if it doesn't exist.
func (s *SyncStateStore) migrate() error {
	query := `
		CREATE TABLE IF NOT EXISTS sync_state (
			note_id TEXT PRIMARY KEY,
			memora_id TEXT,
			content_hash TEXT NOT NULL,
			last_synced_at INTEGER,
			sync_status TEXT NOT NULL,
			error_message TEXT,
			version INTEGER DEFAULT 1,
			FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_sync_state_status ON sync_state(sync_status);
	`
	_, err := s.db.Exec(query)
	return err
}

// Create or update a sync state record.
func (s *SyncStateStore) Upsert(state *SyncState) error {
	now := time.Now().UnixMilli()
	if state.LastSyncedAt == 0 {
		state.LastSyncedAt = now
	}

	query := `
		INSERT INTO sync_state (note_id, memora_id, content_hash, last_synced_at, sync_status, error_message, version)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			memora_id = excluded.memora_id,
			content_hash = excluded.content_hash,
			last_synced_at = excluded.last_synced_at,
			sync_status = excluded.sync_status,
			error_message = excluded.error_message,
			version = excluded.version
	`
	_, err := s.db.Exec(query, state.NoteID, stringOrNull(state.MemoraID),
		state.ContentHash, state.LastSyncedAt, state.SyncStatus,
		stringOrNull(state.ErrorMessage), state.Version)
	return err
}

// GetByNoteID retrieves sync state for a note.
func (s *SyncStateStore) GetByNoteID(noteID string) (*SyncState, error) {
	query := `
		SELECT note_id, memora_id, content_hash, last_synced_at, sync_status, error_message, version
		FROM sync_state
		WHERE note_id = ?
	`
	state := &SyncState{}
	var memoraID, errorMsg sql.NullString

	err := s.db.QueryRow(query, noteID).Scan(
		&state.NoteID, &memoraID, &state.ContentHash, &state.LastSyncedAt,
		&state.SyncStatus, &errorMsg, &state.Version,
	)
	if err == sql.ErrNoRows {
		return nil, nil // No sync state yet
	}
	if err != nil {
		return nil, err
	}

	state.MemoraID = memoraID.String
	state.ErrorMessage = errorMsg.String
	return state, nil
}

// GetPending retrieves all notes with 'pending' sync status.
func (s *SyncStateStore) GetPending() ([]string, error) {
	query := `
		SELECT note_id FROM sync_state WHERE sync_status = 'pending'
		ORDER BY last_synced_at ASC
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var noteIDs []string
	for rows.Next() {
		var noteID string
		if err := rows.Scan(&noteID); err != nil {
			return nil, err
		}
		noteIDs = append(noteIDs, noteID)
	}
	return noteIDs, rows.Err()
}

// MarkSynced marks a note as successfully synced.
func (s *SyncStateStore) MarkSynced(noteID, memoraID string) error {
	query := `
		UPDATE sync_state
		SET sync_status = 'synced', last_synced_at = ?, error_message = NULL, memora_id = ?
		WHERE note_id = ?
	`
	_, err := s.db.Exec(query, time.Now().UnixMilli(), stringOrNull(memoraID), noteID)
	return err
}

// MarkError marks a note as having a sync error.
func (s *SyncStateStore) MarkError(noteID, errorMsg string) error {
	query := `
		UPDATE sync_state
		SET sync_status = 'error', error_message = ?
		WHERE note_id = ?
	`
	_, err := s.db.Exec(query, errorMsg, noteID)
	return err
}

// MarkConflict marks a note as having a conflict.
func (s *SyncStateStore) MarkConflict(noteID string) error {
	query := `
		UPDATE sync_state
		SET sync_status = 'conflict'
		WHERE note_id = ?
	`
	_, err := s.db.Exec(query, noteID)
	return err
}

// Delete removes a sync state record.
func (s *SyncStateStore) Delete(noteID string) error {
	query := `DELETE FROM sync_state WHERE note_id = ?`
	_, err := s.db.Exec(query, noteID)
	return err
}

// stringOrNull converts empty string to sql.NullString.
func stringOrNull(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
