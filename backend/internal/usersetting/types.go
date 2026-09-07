package usersetting

import "encoding/json"

// Record is one user-scoped configuration document.
type Record struct {
	UserID      string          `json:"userId"`
	WorkspaceID string          `json:"workspaceId"`
	Namespace   string          `json:"namespace"`
	ID          string          `json:"id"`
	Payload     json.RawMessage `json:"payload"`
	UpdatedAt   int64           `json:"updatedAt"`
	HasSecret   bool            `json:"hasSecret,omitempty"`
	Secret      string          `json:"-"`
}

// PutResult is the LWW outcome of a PUT.
type PutResult struct {
	Applied  bool   `json:"applied"`
	Conflict bool   `json:"conflict"`
	Record   Record `json:"record"`
}

// Repository is the persistence seam used by HTTP handlers and tests.
type Repository interface {
	List(userID, workspaceID string) ([]Record, error)
	Get(userID, workspaceID, namespace, id string) (*Record, error)
	Put(rec Record) (*PutResult, error)
}
