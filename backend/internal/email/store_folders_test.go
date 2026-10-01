package email

import (
	"context"
	"testing"
	"time"
)

// 目录 + 操作日志的持久化行为（PG 门控，POCKET_TEST_POSTGRES_DSN）。

func TestFoldersAndOpsLog(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "fld-user"
		ws   = "fld-ws"
		acct = "fld-acct"
	)
	seedAccount(t, store, acct, user, ws)

	// --- 目录登记：upsert 幂等 ---
	f1 := &MailFolder{AccountID: acct, Name: "账单", Source: "user"}
	if err := store.UpsertFolderScoped(ctx, f1, user, ws); err != nil {
		t.Fatalf("upsert folder: %v", err)
	}
	f2 := &MailFolder{AccountID: acct, Name: "账单", Source: "user", ServerSynced: true}
	if err := store.UpsertFolderScoped(ctx, f2, user, ws); err != nil {
		t.Fatalf("upsert folder again: %v", err)
	}
	list, err := store.ListFoldersScoped(ctx, user, ws, "")
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("folders = %d, want 1 (upsert dedupes by account+name)", len(list))
	}
	if !list[0].ServerSynced {
		t.Error("server_synced must survive upsert (OR semantics)")
	}

	// --- 邮件移动：SetEmailsFolderScoped + 列表过滤 ---
	if err := store.InsertEmail(ctx, Email{
		ID: "fld-mail-1", AccountID: acct, WorkspaceID: ws,
		MessageID: "fld-mail-1@x", UID: 101, FromAddress: "noreply@x.com",
		Subject: "验证码 001", Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	refs, err := store.SetEmailsFolderScoped(ctx, []string{"fld-mail-1"}, user, ws, "账单")
	if err != nil {
		t.Fatalf("set folder: %v", err)
	}
	if len(refs) != 1 || refs[0].UID != 101 || refs[0].AccountID != acct {
		t.Fatalf("refs = %+v, want uid 101 in %s", refs, acct)
	}
	inbox, err := store.ListEmailsScoped(ctx, ListFilter{Limit: 50}, user, ws)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(inbox) != 0 {
		t.Fatalf("inbox should hide moved email, got %d", len(inbox))
	}
	inFolder, err := store.ListEmailsScoped(ctx, ListFilter{Limit: 50, Folder: "账单"}, user, ws)
	if err != nil {
		t.Fatalf("list folder view: %v", err)
	}
	if len(inFolder) != 1 || inFolder[0].ID != "fld-mail-1" || inFolder[0].FolderName != "账单" {
		t.Fatalf("folder view = %+v", inFolder)
	}

	// --- 操作日志：幂等去重 + 状态回写 + pending 计数 ---
	n, err := store.InsertOpsLogScoped(ctx, []OpsLogEntry{
		{EmailID: "fld-mail-1", AccountID: acct, UID: 101, Action: "move", TargetFolder: "账单",
			IdempotencyKey: "ops-key-1"},
	}, user, ws)
	if err != nil || n != 1 {
		t.Fatalf("insert op: n=%d err=%v, want 1/nil", n, err)
	}
	// 同幂等键重放：去重。
	if n, err := store.InsertOpsLogScoped(ctx, []OpsLogEntry{
		{EmailID: "fld-mail-1", AccountID: acct, UID: 101, Action: "move", TargetFolder: "账单",
			IdempotencyKey: "ops-key-1"},
	}, user, ws); err != nil || n != 0 {
		t.Fatalf("replay op should dedupe: n=%d err=%v", n, err)
	}
	pending, err := store.ClaimPendingOpsScoped(ctx, user, ws, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("claim pending: %v (%d)", err, len(pending))
	}
	if err := store.UpdateOpsLogStatusScoped(ctx, pending[0].ID, user, ws, "applied", "", 0); err != nil {
		t.Fatalf("mark applied: %v", err)
	}
	if cnt, err := store.CountPendingOps(ctx, user, ws); err != nil || cnt != 0 {
		t.Fatalf("pending count = %d err=%v, want 0", cnt, err)
	}
	// 可选同步路径：按 id 取 pending（applied 的取不到）。
	again, err := store.ListPendingOpsByIdemKeys(ctx, []string{"ops-key-1"}, user, ws)
	if err != nil || len(again) != 0 {
		t.Fatalf("pending-by-ids after applied = %d err=%v, want 0", len(again), err)
	}

	// --- 目录删除：登记删除 + 目录内邮件退回收件箱 ---
	if _, err := store.CleanFolderName(ctx, "账单", user, ws); err != nil {
		t.Fatalf("clean folder name: %v", err)
	}
	if err := store.DeleteFolderScoped(ctx, list[0].ID, user, ws); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	back, err := store.ListEmailsScoped(ctx, ListFilter{Limit: 50}, user, ws)
	if err != nil {
		t.Fatalf("list inbox after delete: %v", err)
	}
	if len(back) != 1 || back[0].FolderName != "" {
		t.Fatalf("email should return to inbox view, got %+v", back)
	}
}
