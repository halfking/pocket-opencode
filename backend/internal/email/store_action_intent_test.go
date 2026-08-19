package email

import (
	"context"
	"testing"
	"time"
)

func TestInsertActionIntentIdempotent(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user  = "act-user"
		ws    = "act-ws"
		acct  = "act-acct"
		email = "act-email"
	)
	seedAccount(t, store, acct, user, ws)
	if err := store.InsertEmail(ctx, Email{
		ID: "act-email", AccountID: acct, WorkspaceID: ws,
		MessageID: "act-email@example.com", FromAddress: "sender@example.com", Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	// 同一 (email_id, action) 写两次 — 第二次按 idempotency_key 直接忽略。
	intent := &ActionIntent{
		EmailID:        email,
		AccountID:      acct,
		WorkspaceID:    ws,
		UserID:         user,
		Action:         "archive",
		Folder:         "",
		Reason:         "blacklist",
		IdempotencyKey: "idem-archive",
		Status:         "pending",
	}
	if err := store.InsertActionIntent(ctx, intent); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := store.InsertActionIntent(ctx, intent); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	// 不同 action 同 email：应并存。
	intent2 := *intent
	intent2.ID = ""
	intent2.Action = "route-folder"
	intent2.Folder = "Junk"
	intent2.IdempotencyKey = "idem-route-folder"
	if err := store.InsertActionIntent(ctx, &intent2); err != nil {
		t.Fatalf("third insert: %v", err)
	}

	list, err := store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 intents, got %d", len(list))
	}

	if err := store.UpdateActionIntentStatus(ctx, list[0].ID, user, ws, "applied", "", 123); err != nil {
		t.Fatalf("mark applied: %v", err)
	}

	// pending only 一次 → 现在应只剩一条
	list, err = store.ClaimActionIntents(ctx, user, ws, 10)
	if err != nil {
		t.Fatalf("claim again: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 pending after mark applied, got %d", len(list))
	}

	// 跨 workspace：同一 user 在另一个 ws 不应拿到上述 intent。
	cross, err := store.ClaimActionIntents(ctx, user, "ws-other", 10)
	if err != nil {
		t.Fatalf("cross claim: %v", err)
	}
	if len(cross) != 0 {
		t.Fatalf("cross-workspace leaked %d intents", len(cross))
	}
}
