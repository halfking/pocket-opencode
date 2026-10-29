package identity

import (
	"context"
	"time"
)

// ConsumeDelegation is shared across replicas through PostgreSQL's unique
// constraint. Tokens are consumed before the protected action; an uncertain
// response needs a newly signed request, not a replay of the old credential.
func (s *Store) ConsumeDelegation(ctx context.Context, claims *DelegationClaims, now time.Time) error {
	if claims == nil || claims.ExpiresAt == nil || claims.ID == "" || !claims.ExpiresAt.Time.After(now) {
		return ErrDelegationInvalid
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM workspace_delegation_nonces WHERE expires_at <= $1`, now.Unix()); err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `INSERT INTO workspace_delegation_nonces (issuer,jti,expires_at) VALUES ($1,$2,$3) ON CONFLICT (issuer,jti) DO NOTHING`, claims.Issuer, claims.ID, claims.ExpiresAt.Unix())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrDelegationReplay
	}
	return nil
}

func (s *Store) ListActiveWorkspacesForUser(ctx context.Context, userID string) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `SELECT w.id,w.owner_id,w.name,w.type,w.created_at FROM workspaces w JOIN workspace_members m ON m.workspace_id=w.id WHERE m.user_id=$1 AND (m.expires_at=0 OR m.expires_at>$2) ORDER BY w.created_at,w.id`, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workspaces := []Workspace{}
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(&ws.ID, &ws.OwnerID, &ws.Name, &ws.Type, &ws.CreatedAt); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}
	return workspaces, rows.Err()
}
