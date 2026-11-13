package notelinks

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	schema := `
	CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		title TEXT,
		content TEXT,
		is_latest INTEGER DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE note_links (
		id TEXT PRIMARY KEY,
		source_note_id TEXT NOT NULL,
		target_note_id TEXT NOT NULL,
		link_type TEXT NOT NULL,
		confidence REAL DEFAULT 1.0,
		created_by TEXT NOT NULL,
		reason TEXT,
		created_at INTEGER NOT NULL,
		FOREIGN KEY (source_note_id) REFERENCES notes(id) ON DELETE CASCADE,
		FOREIGN KEY (target_note_id) REFERENCES notes(id) ON DELETE CASCADE,
		UNIQUE(source_note_id, target_note_id, link_type)
	);
	CREATE INDEX idx_note_links_source ON note_links(source_note_id);
	CREATE INDEX idx_note_links_target ON note_links(target_note_id);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// 创建测试笔记
	now := time.Now().UnixMilli()
	_, err = db.Exec(`
		INSERT INTO notes (id, user_id, title, content, is_latest, created_at, updated_at)
		VALUES 
		('note-1', 'user-1', 'Note 1', 'Content 1', 1, ?, ?),
		('note-2', 'user-1', 'Note 2', 'Content 2', 1, ?, ?),
		('note-3', 'user-1', 'Note 3', 'Content 3', 1, ?, ?)
	`, now, now, now, now, now, now)
	if err != nil {
		t.Fatalf("failed to insert test notes: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

func TestNoteLinkStore_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	link := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		Confidence:   1.0,
		CreatedBy:    "user",
	}

	err := store.Create(link)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if link.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
}

func TestNoteLinkStore_GetOutgoingLinks(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建链接
	link1 := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}
	link2 := &NoteLink{
		ID:           "link-2",
		SourceNoteID: "note-1",
		TargetNoteID: "note-3",
		LinkType:     LinkTypeReference,
		CreatedBy:    "ai",
		Confidence:   0.85,
		Reason:       "Similar content",
	}

	if err := store.Create(link1); err != nil {
		t.Fatalf("Create link1 failed: %v", err)
	}
	if err := store.Create(link2); err != nil {
		t.Fatalf("Create link2 failed: %v", err)
	}

	// 获取外链
	links, err := store.GetOutgoingLinks("note-1")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}

	if len(links) != 2 {
		t.Errorf("expected 2 links, got %d", len(links))
	}

	// 验证按置信度排序（AI链接应该排在前面，因为有置信度）
	if links[0].CreatedBy == "ai" && links[0].Confidence != 0.85 {
		t.Error("AI link should be sorted by confidence")
	}
}

func TestNoteLinkStore_GetIncomingLinks(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建链接（note-1 和 note-2 都指向 note-3）
	link1 := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-3",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}
	link2 := &NoteLink{
		ID:           "link-2",
		SourceNoteID: "note-2",
		TargetNoteID: "note-3",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}

	store.Create(link1)
	store.Create(link2)

	// 获取入链（backlinks）
	links, err := store.GetIncomingLinks("note-3")
	if err != nil {
		t.Fatalf("GetIncomingLinks failed: %v", err)
	}

	if len(links) != 2 {
		t.Errorf("expected 2 backlinks, got %d", len(links))
	}
}

func TestNoteLinkStore_GetRelatedNotes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// note-1 -> note-2 (outgoing)
	// note-3 -> note-1 (incoming)
	link1 := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}
	link2 := &NoteLink{
		ID:           "link-2",
		SourceNoteID: "note-3",
		TargetNoteID: "note-1",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}

	store.Create(link1)
	store.Create(link2)

	outgoing, incoming, err := store.GetRelatedNotes("note-1")
	if err != nil {
		t.Fatalf("GetRelatedNotes failed: %v", err)
	}

	if len(outgoing) != 1 {
		t.Errorf("expected 1 outgoing link, got %d", len(outgoing))
	}
	if len(incoming) != 1 {
		t.Errorf("expected 1 incoming link, got %d", len(incoming))
	}

	if outgoing[0].TargetNoteID != "note-2" {
		t.Errorf("expected target note-2, got %s", outgoing[0].TargetNoteID)
	}
	if incoming[0].SourceNoteID != "note-3" {
		t.Errorf("expected source note-3, got %s", incoming[0].SourceNoteID)
	}
}

func TestNoteLinkStore_Delete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	link := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}

	if err := store.Create(link); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 删除
	if err := store.Delete("link-1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 验证已删除
	links, err := store.GetOutgoingLinks("note-1")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected 0 links after delete, got %d", len(links))
	}
}

func TestNoteLinkStore_DeleteByNoteID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// 创建多个链接
	store.Create(&NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	})
	store.Create(&NoteLink{
		ID:           "link-2",
		SourceNoteID: "note-2",
		TargetNoteID: "note-3",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	})
	store.Create(&NoteLink{
		ID:           "link-3",
		SourceNoteID: "note-3",
		TargetNoteID: "note-1",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	})

	// 删除所有与 note-1 相关的链接
	if err := store.DeleteByNoteID("note-1"); err != nil {
		t.Fatalf("DeleteByNoteID failed: %v", err)
	}

	// 验证 note-1 的链接已删除
	outgoing, incoming, err := store.GetRelatedNotes("note-1")
	if err != nil {
		t.Fatalf("GetRelatedNotes failed: %v", err)
	}
	if len(outgoing) != 0 || len(incoming) != 0 {
		t.Error("all links to/from note-1 should be deleted")
	}

	// 验证其他链接还在
	links, err := store.GetOutgoingLinks("note-2")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("expected 1 link from note-2, got %d", len(links))
	}
}

func TestNoteLinkStore_CreateBatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	links := []NoteLink{
		{
			ID:           "link-1",
			SourceNoteID: "note-1",
			TargetNoteID: "note-2",
			LinkType:     LinkTypeRelated,
			CreatedBy:    "ai",
			Confidence:   0.9,
		},
		{
			ID:           "link-2",
			SourceNoteID: "note-1",
			TargetNoteID: "note-3",
			LinkType:     LinkTypeRelated,
			CreatedBy:    "ai",
			Confidence:   0.8,
		},
	}

	if err := store.CreateBatch(links); err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	// 验证
	retrieved, err := store.GetOutgoingLinks("note-1")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if len(retrieved) != 2 {
		t.Errorf("expected 2 links, got %d", len(retrieved))
	}
}

func TestNoteLinkStore_UniqueConstraint(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	link := &NoteLink{
		ID:           "link-1",
		SourceNoteID: "note-1",
		TargetNoteID: "note-2",
		LinkType:     LinkTypeRelated,
		CreatedBy:    "user",
	}

	// 第一次创建应该成功
	if err := store.Create(link); err != nil {
		t.Fatalf("First Create failed: %v", err)
	}

	// 第二次创建相同的链接应该被忽略（ON CONFLICT DO NOTHING）
	link.ID = "link-2" // 不同的ID
	if err := store.Create(link); err != nil {
		t.Fatalf("Second Create failed: %v", err)
	}

	// 应该只有一个链接
	links, err := store.GetOutgoingLinks("note-1")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("expected 1 link due to unique constraint, got %d", len(links))
	}
}

func TestLinkInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   LinkInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: LinkInput{
				TargetNoteID: "note-123",
				LinkType:     LinkTypeRelated,
			},
			wantErr: false,
		},
		{
			name: "empty target",
			input: LinkInput{
				TargetNoteID: "",
				LinkType:     LinkTypeRelated,
			},
			wantErr: true,
		},
		{
			name: "invalid link type",
			input: LinkInput{
				TargetNoteID: "note-123",
				LinkType:     "invalid",
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
