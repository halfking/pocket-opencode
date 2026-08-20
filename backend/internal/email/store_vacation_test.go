package email

import (
	"context"
	"testing"
	"time"
)

func TestVacationDeliveryClaimAndRetry(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()
	const (
		user = "vac-user"
		ws   = "vac-ws"
		acct = "vac-acct"
	)
	seedAccount(t, store, acct, user, ws)
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp.example.com", 587, "enc-smtp", true); err != nil {
		t.Fatalf("smtp: %v", err)
	}
	vacation := &VacationReply{
		AccountID: acct, Enabled: true,
		StartAt: now - 60, EndAt: now + 3600,
		Subject: "Away", BodyText: "I am away", CreatedAt: now - 120,
	}
	if err := store.UpsertVacationScoped(ctx, vacation, user, ws); err != nil {
		t.Fatalf("vacation: %v", err)
	}
	email := Email{
		ID: "vac-email-1", AccountID: acct, WorkspaceID: ws,
		MessageID: "orig-1", UID: 1, FromAddress: "sender@example.net",
		Subject: "Question", Snippet: "hello", Date: now - 30,
	}
	if err := store.InsertEmail(ctx, email); err != nil {
		t.Fatalf("email: %v", err)
	}

	claimed, err := store.ClaimNextVacationDelivery(ctx, now, 15*time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.EmailID != email.ID {
		t.Fatalf("unexpected claim: %#v", claimed)
	}
	duplicate, err := store.ClaimNextVacationDelivery(ctx, now, 15*time.Minute)
	if err != nil {
		t.Fatalf("duplicate claim: %v", err)
	}
	if duplicate != nil {
		t.Fatalf("duplicate claim should be nil: %#v", duplicate)
	}

	if err := store.MarkVacationDeliveryFailed(ctx, claimed.VacationID, claimed.EmailID, "temporary failure", now); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	tooSoon, err := store.ClaimNextVacationDelivery(ctx, now+60, 15*time.Minute)
	if err != nil {
		t.Fatalf("too-soon retry: %v", err)
	}
	if tooSoon != nil {
		t.Fatalf("retry before backoff should be nil: %#v", tooSoon)
	}
	retry, err := store.ClaimNextVacationDelivery(ctx, now+16*60, 15*time.Minute)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry == nil || retry.EmailID != email.ID {
		t.Fatalf("expected retry claim: %#v", retry)
	}
	if err := store.MarkVacationDeliverySent(ctx, retry.VacationID, retry.EmailID, now+16*60); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	terminal, err := store.ClaimNextVacationDelivery(ctx, now+17*60, 15*time.Minute)
	if err != nil {
		t.Fatalf("terminal claim: %v", err)
	}
	if terminal != nil {
		t.Fatalf("sent delivery should remain terminal: %#v", terminal)
	}
}

func TestVacationDeliveryClaimIsWorkspaceScoped(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()
	seedAccount(t, store, "vac-a", "user-a", "ws-a")
	if err := store.UpsertVacationScoped(ctx, &VacationReply{
		AccountID: "vac-a", Enabled: true,
		StartAt: now - 10, EndAt: now + 100,
		Subject: "Away", BodyText: "Away", CreatedAt: now - 20,
	}, "user-a", "ws-a"); err != nil {
		t.Fatalf("vacation: %v", err)
	}
	if err := store.InsertEmail(ctx, Email{
		ID: "vac-a-email", AccountID: "vac-a", WorkspaceID: "ws-a",
		FromAddress: "sender@example.net", Subject: "Question", Date: now - 1,
	}); err != nil {
		t.Fatalf("email: %v", err)
	}
	claimed, err := store.ClaimNextVacationDelivery(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != nil && claimed.WorkspaceID != "ws-a" {
		t.Fatalf("claim crossed workspace: %#v", claimed)
	}
}
