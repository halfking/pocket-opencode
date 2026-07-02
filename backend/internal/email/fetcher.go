package email

import (
	"context"
	"fmt"
)

// Fetcher polls an IMAP mailbox and upserts new messages into the Store.
//
// Skeleton: the actual IMAP connect + FETCH is implemented in Phase 3 using
// github.com/emersion/go-imap. This stub documents the contract so the
// scheduler and handlers can be wired up ahead of the full implementation.
type Fetcher struct {
	store *Store
	// decrypt returns the plaintext credential from credential_encrypted.
	decrypt func(encrypted string) (string, error)
}

func NewFetcher(store *Store, decrypt func(string) (string, error)) *Fetcher {
	return &Fetcher{store: store, decrypt: decrypt}
}

// Sync pulls new mail for one account since its last synced UID.
// Returns the count of newly fetched messages.
func (f *Fetcher) Sync(ctx context.Context, accountID string) (int, error) {
	// TODO Phase 3: implement with go-imap:
	//   1. load account, decrypt credential
	//   2. dial TLS, LOGIN, SELECT INBOX
	//   3. SEARCH UID lastSyncedUID:*
	//   4. FETCH ENVELOPE + BODY[1] (first ~5KB) for new UIDs
	//   5. upsert into emails (dedupe on message_id)
	//   6. update last_synced_uid / last_synced_at
	//   7. enqueue new emails for kxmemory classification
	return 0, fmt.Errorf("email.Fetcher.Sync not implemented (Phase 3)")
}
