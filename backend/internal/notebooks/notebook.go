package notebooks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Notebook represents a notebook for organizing notes.
type Notebook struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	WorkspaceID string     `json:"workspaceId,omitempty"`
	Name        string     `json:"name"`
	ParentID    string     `json:"parentId,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	Color       string     `json:"color,omitempty"`
	SortOrder   int        `json:"sortOrder"`
	CreatedAt   int64      `json:"createdAt"`
	UpdatedAt   int64      `json:"updatedAt"`
	NoteCount   int        `json:"noteCount,omitempty"`   // computed field
	Children    []Notebook `json:"children,omitempty"`    // for tree view
}

// Store handles notebook persistence in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore creates a new notebook store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create creates a new notebook.
func (s *Store) Create(nb *Notebook) error {
	now := time.Now().UnixMilli()
	nb.CreatedAt = now
	nb.UpdatedAt = now

	query := `
		INSERT INTO notebooks (id, user_id, workspace_id, name, parent_id, icon, color, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, nb.ID, nb.UserID, stringOrNull(nb.WorkspaceID), nb.Name,
		stringOrNull(nb.ParentID), stringOrNull(nb.Icon), stringOrNull(nb.Color),
		nb.SortOrder, nb.CreatedAt, nb.UpdatedAt)
	return err
}

// GetByID retrieves a notebook by ID with note count.
func (s *Store) GetByID(id string) (*Notebook, error) {
	query := `
		SELECT nb.id, nb.user_id, nb.workspace_id, nb.name, nb.parent_id, nb.icon, nb.color,
		       nb.sort_order, nb.created_at, nb.updated_at, 
		       COALESCE(COUNT(DISTINCT n.id), 0) as note_count
		FROM notebooks nb
		LEFT JOIN notes n ON n.notebook_id = nb.id AND n.is_latest = 1
		WHERE nb.id = ?
		GROUP BY nb.id
	`
	nb := &Notebook{}
	var workspaceID, parentID, icon, color sql.NullString

	err := s.db.QueryRow(query, id).Scan(
		&nb.ID, &nb.UserID, &workspaceID, &nb.Name, &parentID, &icon, &color,
		&nb.SortOrder, &nb.CreatedAt, &nb.UpdatedAt, &nb.NoteCount,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("notebook not found: %s", id)
	}
	if err != nil {
		return nil, err
	}

	nb.WorkspaceID = workspaceID.String
	nb.ParentID = parentID.String
	nb.Icon = icon.String
	nb.Color = color.String

	return nb, nil
}

// ListByUser retrieves all notebooks for a user in tree structure.
func (s *Store) ListByUser(userID, workspaceID string) ([]Notebook, error) {
	query := `
		SELECT nb.id, nb.user_id, nb.workspace_id, nb.name, nb.parent_id, nb.icon, nb.color,
		       nb.sort_order, nb.created_at, nb.updated_at,
		       COALESCE(COUNT(DISTINCT n.id), 0) as note_count
		FROM notebooks nb
		LEFT JOIN notes n ON n.notebook_id = nb.id AND n.is_latest = 1
		WHERE nb.user_id = ? AND (nb.workspace_id = ? OR (nb.workspace_id IS NULL AND ? = ''))
		GROUP BY nb.id
		ORDER BY nb.parent_id NULLS FIRST, nb.sort_order, nb.name
	`

	rows, err := s.db.Query(query, userID, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []Notebook
	for rows.Next() {
		nb := Notebook{}
		var workspaceIDVal, parentID, icon, color sql.NullString

		if err := rows.Scan(
			&nb.ID, &nb.UserID, &workspaceIDVal, &nb.Name, &parentID, &icon, &color,
			&nb.SortOrder, &nb.CreatedAt, &nb.UpdatedAt, &nb.NoteCount,
		); err != nil {
			return nil, err
		}

		nb.WorkspaceID = workspaceIDVal.String
		nb.ParentID = parentID.String
		nb.Icon = icon.String
		nb.Color = color.String

		all = append(all, nb)
	}

	// Build tree structure
	return buildNotebookTree(all), nil
}

// Update updates a notebook.
func (s *Store) Update(nb *Notebook) error {
	nb.UpdatedAt = time.Now().UnixMilli()

	query := `
		UPDATE notebooks 
		SET name = ?, parent_id = ?, icon = ?, color = ?, sort_order = ?, updated_at = ?
		WHERE id = ?
	`
	result, err := s.db.Exec(query, nb.Name, stringOrNull(nb.ParentID),
		stringOrNull(nb.Icon), stringOrNull(nb.Color), nb.SortOrder, nb.UpdatedAt, nb.ID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("notebook not found: %s", nb.ID)
	}

	return nil
}

// Delete deletes a notebook (notes are moved to parent via trigger).
func (s *Store) Delete(id string) error {
	query := `DELETE FROM notebooks WHERE id = ?`
	result, err := s.db.Exec(query, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("notebook not found: %s", id)
	}

	return nil
}

// GetNoteCount returns the number of notes in a notebook.
func (s *Store) GetNoteCount(notebookID string) (int, error) {
	query := `SELECT COUNT(*) FROM notes WHERE notebook_id = ? AND is_latest = 1`
	var count int
	err := s.db.QueryRow(query, notebookID).Scan(&count)
	return count, err
}

// buildNotebookTree constructs a tree structure from flat notebook list.
func buildNotebookTree(all []Notebook) []Notebook {
	if len(all) == 0 {
		return nil
	}

	// Build parent-child map
	childrenMap := make(map[string][]Notebook)
	var roots []Notebook

	for _, nb := range all {
		if nb.ParentID == "" {
			roots = append(roots, nb)
		} else {
			childrenMap[nb.ParentID] = append(childrenMap[nb.ParentID], nb)
		}
	}

	// Recursively attach children
	var attachChildren func(*Notebook)
	attachChildren = func(parent *Notebook) {
		if children, ok := childrenMap[parent.ID]; ok {
			parent.Children = children
			for i := range parent.Children {
				attachChildren(&parent.Children[i])
			}
		}
	}

	for i := range roots {
		attachChildren(&roots[i])
	}

	return roots
}

// stringOrNull converts empty string to sql.NullString.
func stringOrNull(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// NotebookInput is the input for creating/updating notebooks.
type NotebookInput struct {
	Name      string `json:"name"`
	ParentID  string `json:"parentId,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Color     string `json:"color,omitempty"`
	SortOrder int    `json:"sortOrder"`
}

// Validate validates notebook input.
func (input *NotebookInput) Validate() error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(input.Name) > 200 {
		return fmt.Errorf("name too long (max 200 characters)")
	}
	return nil
}

// MarshalJSON customizes JSON marshaling to handle empty slices.
func (nb Notebook) MarshalJSON() ([]byte, error) {
	type Alias Notebook
	if nb.Children == nil {
		nb.Children = []Notebook{}
	}
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(&nb),
	})
}
