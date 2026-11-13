package notebooks

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// 创建临时数据库
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// 创建 notebooks 表
	schema := `
	CREATE TABLE notebooks (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		workspace_id TEXT,
		name TEXT NOT NULL,
		parent_id TEXT,
		icon TEXT,
		color TEXT,
		sort_order INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		FOREIGN KEY (parent_id) REFERENCES notebooks(id) ON DELETE CASCADE
	);
	CREATE INDEX idx_notebooks_user_workspace ON notebooks(user_id, workspace_id);
	CREATE INDEX idx_notebooks_parent ON notebooks(parent_id);

	CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		notebook_id TEXT,
		title TEXT,
		content TEXT,
		is_latest INTEGER DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

func TestNotebookStore_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	nb := &Notebook{
		ID:       "nb-test-1",
		UserID:   "user-1",
		Name:     "测试笔记本",
		Icon:     "📚",
		Color:    "#3498db",
		SortOrder: 0,
	}

	err := store.Create(nb)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 验证创建时间已设置
	if nb.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if nb.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}

	// 验证可以读取
	retrieved, err := store.GetByID("nb-test-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if retrieved.Name != "测试笔记本" {
		t.Errorf("expected name '测试笔记本', got '%s'", retrieved.Name)
	}
	if retrieved.Icon != "📚" {
		t.Errorf("expected icon '📚', got '%s'", retrieved.Icon)
	}
}

func TestNotebookStore_ListByUser(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建根笔记本
	root := &Notebook{
		ID:     "nb-root",
		UserID: "user-1",
		Name:   "根笔记本",
	}
	if err := store.Create(root); err != nil {
		t.Fatalf("Create root failed: %v", err)
	}

	// 创建子笔记本
	child := &Notebook{
		ID:       "nb-child",
		UserID:   "user-1",
		Name:     "子笔记本",
		ParentID: "nb-root",
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	// 列出笔记本
	notebooks, err := store.ListByUser("user-1", "")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}

	// 应该返回树形结构（只有根节点）
	if len(notebooks) != 1 {
		t.Errorf("expected 1 root notebook, got %d", len(notebooks))
	}

	// 检查子节点
	if len(notebooks[0].Children) != 1 {
		t.Errorf("expected 1 child, got %d", len(notebooks[0].Children))
	}

	if notebooks[0].Children[0].Name != "子笔记本" {
		t.Errorf("expected child name '子笔记本', got '%s'", notebooks[0].Children[0].Name)
	}
}

func TestNotebookStore_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建笔记本
	nb := &Notebook{
		ID:     "nb-test",
		UserID: "user-1",
		Name:   "原始名称",
	}
	if err := store.Create(nb); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 等待一会儿以确保时间戳不同
	time.Sleep(10 * time.Millisecond)

	// 更新
	nb.Name = "新名称"
	nb.Icon = "🎯"
	if err := store.Update(nb); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// 验证更新
	updated, err := store.GetByID("nb-test")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if updated.Name != "新名称" {
		t.Errorf("expected name '新名称', got '%s'", updated.Name)
	}
	if updated.Icon != "🎯" {
		t.Errorf("expected icon '🎯', got '%s'", updated.Icon)
	}
	if updated.UpdatedAt <= nb.CreatedAt {
		t.Error("UpdatedAt should be greater than CreatedAt")
	}
}

func TestNotebookStore_Delete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建笔记本
	nb := &Notebook{
		ID:     "nb-test",
		UserID: "user-1",
		Name:   "待删除",
	}
	if err := store.Create(nb); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 删除
	if err := store.Delete("nb-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 验证已删除
	_, err := store.GetByID("nb-test")
	if err == nil {
		t.Error("expected error when getting deleted notebook")
	}
}

func TestNotebookStore_GetNoteCount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建笔记本
	nb := &Notebook{
		ID:     "nb-test",
		UserID: "user-1",
		Name:   "测试",
	}
	if err := store.Create(nb); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 添加笔记
	_, err := db.Exec(`
		INSERT INTO notes (id, user_id, notebook_id, title, content, is_latest, created_at, updated_at)
		VALUES ('note-1', 'user-1', 'nb-test', 'Note 1', 'Content 1', 1, ?, ?)
	`, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("failed to insert note: %v", err)
	}

	// 获取笔记数
	count, err := store.GetNoteCount("nb-test")
	if err != nil {
		t.Fatalf("GetNoteCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestBuildNotebookTree(t *testing.T) {
	notebooks := []Notebook{
		{ID: "nb-1", Name: "Root 1", ParentID: ""},
		{ID: "nb-2", Name: "Root 2", ParentID: ""},
		{ID: "nb-1-1", Name: "Child 1-1", ParentID: "nb-1"},
		{ID: "nb-1-2", Name: "Child 1-2", ParentID: "nb-1"},
		{ID: "nb-1-1-1", Name: "GrandChild 1-1-1", ParentID: "nb-1-1"},
	}

	tree := buildNotebookTree(notebooks)

	// 应该有 2 个根节点
	if len(tree) != 2 {
		t.Errorf("expected 2 roots, got %d", len(tree))
	}

	// 检查第一个根的子节点
	root1 := tree[0]
	if len(root1.Children) != 2 {
		t.Errorf("expected 2 children for root1, got %d", len(root1.Children))
	}

	// 检查孙节点
	if len(root1.Children[0].Children) != 1 {
		t.Errorf("expected 1 grandchild, got %d", len(root1.Children[0].Children))
	}
}

func TestNotebookInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   NotebookInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: NotebookInput{
				Name: "Test Notebook",
			},
			wantErr: false,
		},
		{
			name: "empty name",
			input: NotebookInput{
				Name: "",
			},
			wantErr: true,
		},
		{
			name: "name too long",
			input: NotebookInput{
				Name: string(make([]byte, 201)), // > 200 characters
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
