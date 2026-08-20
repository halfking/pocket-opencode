package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
)

// Cursor represents a pagination cursor encoding {id, created_at} for keyset
// pagination. Base64-encoded JSON so it's opaque to clients and safe in URLs.
type Cursor struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"` // unix timestamp
}

// EncodeCursor serializes a cursor to a base64 string.
func EncodeCursor(id string, createdAt int64) string {
	data, _ := json.Marshal(Cursor{ID: id, CreatedAt: createdAt})
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeCursor parses a base64 cursor string. Returns nil if the cursor is
// empty or invalid.
func DecodeCursor(s string) *Cursor {
	if s == "" {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	if c.ID == "" {
		return nil
	}
	return &c
}

// ParseLimit parses a "limit" query param with a default and max.
func ParseLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

// PaginatedResponse is the standard paginated API response envelope.
type PaginatedResponse struct {
	Data       any    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Total      int    `json:"total,omitempty"`
}

// FormatCursorPage builds a PaginatedResponse. lastID/lastCreatedAt are from
// the last item in data (used to build next_cursor). hasMore indicates whether
// there are more items beyond this page.
func FormatCursorPage(data any, lastID string, lastCreatedAt int64, hasMore bool) PaginatedResponse {
	resp := PaginatedResponse{
		Data:    data,
		HasMore: hasMore,
	}
	if hasMore && lastID != "" {
		resp.NextCursor = EncodeCursor(lastID, lastCreatedAt)
	}
	return resp
}

// cursorWhereClause returns a SQL WHERE clause fragment and args for keyset
// pagination. It filters rows where (created_at, id) < (cursor.created_at,
// cursor.id) for DESC ordering. Returns ("", nil) if cursor is nil.
func cursorWhereClause(cursor *Cursor, createdAtCol, idCol string, paramOffset int) (string, []any) {
	if cursor == nil {
		return "", nil
	}
	// For DESC: (created_at < $N) OR (created_at = $N AND id < $N+1)
	clause := fmt.Sprintf("((%s < $%d) OR (%s = $%d AND %s < $%d))",
		createdAtCol, paramOffset,
		createdAtCol, paramOffset, idCol, paramOffset+1)
	return clause, []any{cursor.CreatedAt, cursor.ID}
}
