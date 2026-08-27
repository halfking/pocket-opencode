package chatagent

import "context"

// StoreIface 是智能体角色存储的接口契约。
//
// PG 模式（*Store）和 SQLite 模式（未来扩展）都会实现该接口。Server
// 端只依赖接口，避免在 main 包里硬编码具体实现。
//
// 设计要点：
//   - 所有方法以 workspaceID 隔离；内置角色（workspaceID=""）全局可见；
//   - Init/CountCustom 让 quota 与启动装配有明确钩子；
//   - Update/Delete 语义：只允许修改/删除自定义角色（is_builtin=false），
//     内置角色由 importer 单独管理。
type StoreIface interface {
	Init(ctx context.Context) error
	Create(ctx context.Context, a *Agent) error
	Get(ctx context.Context, workspaceID, id string) (*Agent, error)
	List(ctx context.Context, workspaceID, department string) ([]*Agent, error)
	Update(ctx context.Context, workspaceID string, a *Agent) error
	Delete(ctx context.Context, workspaceID, id string) error
	CountCustom(ctx context.Context, workspaceID string) (int, error)
	// ImportBuiltinAgents 从 markdown 仓库导入内置角色（幂等）。
	ImportBuiltinAgents(ctx context.Context, repoPath string) error
}

// 编译期断言：*Store 满足 StoreIface（SQLiteStore 在合入后再追加）。
var _ StoreIface = (*Store)(nil)
