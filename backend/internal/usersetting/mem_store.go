package usersetting

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// MemStore is an in-memory Repository for handler tests.
type MemStore struct {
	mu   sync.Mutex
	rows map[string]Record
}

func NewMemStore() *MemStore {
	return &MemStore{rows: map[string]Record{}}
}

func memKey(userID, workspaceID, namespace, id string) string {
	return userID + "|" + workspaceID + "|" + namespace + "|" + id
}

func (m *MemStore) List(userID, workspaceID string) ([]Record, error) {
	userID, workspaceID = scope(userID, workspaceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0)
	for _, rec := range m.rows {
		if rec.UserID == userID && rec.WorkspaceID == workspaceID {
			out = append(out, publicCopy(rec))
		}
	}
	return out, nil
}

func (m *MemStore) Get(userID, workspaceID, namespace, id string) (*Record, error) {
	userID, workspaceID = scope(userID, workspaceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[memKey(userID, workspaceID, namespace, id)]
	if !ok {
		return nil, nil
	}
	copy := publicCopy(rec)
	return &copy, nil
}

func (m *MemStore) Put(rec Record) (*PutResult, error) {
	rec.UserID, rec.WorkspaceID = scope(rec.UserID, rec.WorkspaceID)
	if rec.Namespace == "" || rec.ID == "" {
		return nil, fmt.Errorf("usersetting: namespace and id required")
	}
	if rec.UpdatedAt <= 0 {
		rec.UpdatedAt = time.Now().Unix()
	}
	if len(rec.Payload) == 0 {
		rec.Payload = json.RawMessage(`{}`)
	}
	key := memKey(rec.UserID, rec.WorkspaceID, rec.Namespace, rec.ID)
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.rows[key]
	if ok && DecidePut(rec.UpdatedAt, existing.UpdatedAt) == DecisionKeep {
		return &PutResult{
			Applied:  false,
			Conflict: rec.UpdatedAt < existing.UpdatedAt,
			Record:   publicCopy(existing),
		}, nil
	}
	if ok && rec.Secret == "" {
		rec.Secret = existing.Secret
	}
	m.rows[key] = rec
	return &PutResult{Applied: true, Record: publicCopy(rec)}, nil
}

func publicCopy(rec Record) Record {
	out := rec
	out.HasSecret = rec.Secret != ""
	out.Secret = rec.Secret
	return out
}
