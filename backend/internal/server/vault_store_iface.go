package server

import (
	"context"

	"github.com/halfking/pocket-opencode/backend/internal/vault"
)

// vaultSyncStorer 抽象 vault 同步所需的 4 个操作，便于 handler 测试注入
// fake 实现而不依赖 Postgres。生产路径下由 *vault.Store 满足；测试时
// 通过 newServer 的 vaultStore 参数传入 fakeVaultSyncStorer。
type vaultSyncStorer interface {
	PutLatest(ctx context.Context, workspaceID, userID, ciphertext string, version int) error
	GetLatest(ctx context.Context, workspaceID, userID string) (ciphertext string, version int, err error)
	GetByVersion(ctx context.Context, workspaceID, userID string, version int) (string, error)
	MarkCurrent(ctx context.Context, workspaceID, userID string, version int) error
	ListVersions(ctx context.Context, workspaceID, userID string) ([]vault.Version, error)
}

// 编译期校验：*vault.Store 必须满足 vaultSyncStorer。
var _ vaultSyncStorer = (*vault.Store)(nil)
