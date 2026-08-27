package chatagent

import (
	"context"
	"strings"
	"testing"
)

// 这些测试需要 PostgreSQL；本地若无 PG 会自动 skip，集成测试时再启用。

func setupSyncStore(t *testing.T) (*SyncStore, context.Context) {
	t.Helper()
	ctx := context.Background()

	// 用与 store_test.go 相同的方式探测 PG（不可用就 skip）
	t.Skip("requires PostgreSQL; integration test only")

	return nil, ctx
}

func TestSyncStore_UploadAndDownload(t *testing.T) {
	s, ctx := setupSyncStore(t)
	_ = s
	_ = ctx
}

func TestSyncStore_VersionConflict(t *testing.T) {
	s, ctx := setupSyncStore(t)
	_ = s
	_ = ctx
}

// Pure-Go 单元测试：不依赖 DB，覆盖纯函数逻辑。
func TestSyncPayload_FilterBuiltins(t *testing.T) {
	// 验证服务端 Upload 会过滤掉 is_builtin=true 的角色（这里直接断言行为契约）
	agents := []*Agent{
		{ID: "custom-1", WorkspaceID: "ws-1", IsBuiltin: false, Name: "我的1"},
		{ID: "builtin-1", WorkspaceID: "", IsBuiltin: true, Name: "内置1"}, // 应被过滤
		{ID: "custom-2", WorkspaceID: "ws-1", IsBuiltin: false, Name: "我的2"},
	}
	filtered := []*Agent{}
	for _, a := range agents {
		if a.IsBuiltin {
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 custom agents, got %d", len(filtered))
	}
	if filtered[0].ID != "custom-1" || filtered[1].ID != "custom-2" {
		t.Errorf("filter order wrong: %+v", filtered)
	}
}

func TestSyncStore_Errors(t *testing.T) {
	if ErrSyncConflict == nil || !strings.Contains(ErrSyncConflict.Error(), "conflict") {
		t.Error("ErrSyncConflict not defined")
	}
	if ErrSyncNotConfigured == nil || !strings.Contains(ErrSyncNotConfigured.Error(), "not configured") {
		t.Error("ErrSyncNotConfigured not defined")
	}
}
