package sync

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SavedSearch represents a saved search query with filters.
type SavedSearch struct {
	ID        string     `json:"id"`
	UserID    string            `json:"userId"`
	Name      string            `json:"name"`
	Query     string     `json:"query"`
	Filters   map[string]interface{} `json:"filters,omitempty"` // {tags, notebook, dateRange, domain}
	CreatedAt int64      `json:"createdAt"`
}

// SavedSearchStore handles saved search persistence in SQLite.
type SavedSearchStore struct {
	db *sql.DB
}

// NewSavedSearchStore creates a new saved search store.
func NewSavedSearchStore(db *sql.DB) (*SavedSearchStore, error) {
	s := &SavedSearchStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrate creates the saved_searches table if it doesn't exist.
func (s *SavedSearchStore) migrate() error {
	query := `
		CREATE TABLE IF NOT EXISTS saved_searches (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			query TEXT NOT NULL,
			filters TEXT,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_saved_searches_user ON saved_searches(user_id);
	`
	_, err := s.db.Exec(query)
	return err
}

// Create creates a new saved search.
func (s *SavedSearchStore) Create(search *SavedSearch) error {
	now := time.Now().UnixMilli()
	search.CreatedAt = now

	filtersJSON, err := json.Marshal(search.Filters)
	if err != nil {
		return fmt.Errorf("marshal filters: %w", err)
	}

	query := `
		INSERT INTO saved_searches (id, user_id, name, query, filters, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.Exec(query, search.ID, search.UserID, search.Name, search.Query,
		string(filtersJSON), search.CreatedAt)
	return err
}

// GetByID retrieves a saved search by ID.
func (s *SavedSearchStore) GetByID(id string) (*SavedSearch, error) {
	query := `
		SELECT id, user_id, name, query, filters, created_at
		FROM saved_searches
		WHERE id = ?
	`
	search := &SavedSearch{}
	var filtersStr sql.NullString

	err := s.db.QueryRow(query, id).Scan(
		&search.ID, &search.UserID, &search.Name, &search.Query, &filtersStr, &search.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("saved search not found: %s", id)
	}
	if err != nil {
		return nil, err
	}

	if filtersStr.Valid && filtersStr.String != "" {
		if err := json.Unmarshal([]byte(filtersStr.String), &search.Filters); err != nil {
			return nil, fmt.Errorf("unmarshal filters: %w", err)
		}
	}

	return search, nil
}

// ListByUser retrieves all saved searches for a user.
func (s *SavedSearchStore) ListByUser(userID string) ([]SavedSearch, error) {
	query := `
		SELECT id, user_id, name, query, filters, created_at
		FROM saved_searches
		WHERE user_id = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var searches []SavedSearch
	for rows.Next() {
		search := SavedSearch{}
		var filtersStr sql.NullString

		if err := rows.Scan(&search.ID, &search.UserID, &search.Name, &search.Query, &filtersStr, &search.CreatedAt); err != nil {
			return nil, err
		}

		if filtersStr.Valid && filtersStr.String != "" {
			if err := json.Unmarshal([]byte(filtersStr.String), &search.Filters); err != nil {
				return nil, fmt.Errorf("unmarshal filters: %w", err)
			}
		}

		searches = append(searches, search)
	}
	return searches, rows.Err()
}

// Update updates a saved search.
func (s *SavedSearchStore) Update(search *SavedSearch) error {
	filtersJSON, err := json.Marshal(search.Filters)
	if err != nil {
		return fmt.Errorf("marshal filters: %w", err)
	}

	query := `
		UPDATE saved_searches
		SET name = ?, query = ?, filters = ?
		WHERE id = ? AND user_id = ?
	`
	result, err := s.db.Exec(query, search.Name, search.Query, string(filtersJSON), search.ID, search.UserID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("saved search not found: %s", search.ID)
	}

	return nil
}

// Delete deletes a saved search.
func (s *SavedSearchStore) Delete(id, userID string) error {
	query := `DELETE FROM saved_searches WHERE id = ? AND user_id = ?`
	result, err := s.db.Exec(query, id, userID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("saved search not found: %s", id)
	}

	return nil
}
