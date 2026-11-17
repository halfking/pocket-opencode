package sync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"time"
)

// Orchestrator manages bidirectional synchronization between local and memora.
type Orchestrator struct {
	syncStateStore   *SyncStateStore
	memoraClient     MemoraClient
	syncInterval     time.Duration
	maxRetries       int
	retryBackoffBase time.Duration
}

// MemoraClient defines the interface for interacting with memora service.
type MemoraClient interface {
	// PushNote pushes a local note to memora.
	// Returns the memora note ID and error.
	PushNote(ctx context.Context, noteID, title, content string) (string, error)

	// GetUpdatedSince retrieves notes updated since lastSync from memora.
	GetUpdatedSince(ctx context.Context, lastSync int64) ([]MemoraNote, error)
}

// MemoraNote represents a note from memora service.
type MemoraNote struct {
	ID string
	Title     string
	Content   string
	UpdatedAt int64
	Hash      string
}

// NewOrchestrator creates a new sync orchestrator.
func NewOrchestrator(
	syncStateStore *SyncStateStore,
	memoraClient MemoraClient,
	syncInterval time.Duration,
) *Orchestrator {
	if syncInterval == 0 {
		syncInterval = 5 * time.Minute // default
	}
	return &Orchestrator{
		syncStateStore:   syncStateStore,
		memoraClient:     memoraClient,
		syncInterval:     syncInterval,
		maxRetries:       3,
		retryBackoffBase: 1 * time.Second,
	}
}

// Run starts the background synchronization loop.
// It should be called in a separate goroutine.
func (o *Orchestrator) Run(ctx context.Context) {
	ticker := time.NewTicker(o.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			o.syncOnce(ctx)
		case <-ctx.Done():
			log.Println("[Sync] Orchestrator shutting down")
			return
		}
	}
}

// syncOnce performs one complete sync cycle:
// 1. Push pending local notes to memora
// 2. Pull updates from memora
func (o *Orchestrator) syncOnce(ctx context.Context) {
	// Phase 1: Sync pending notes to memora
	if err := o.syncPendingNotes(ctx); err != nil {
		log.Printf("[Sync] Failed to sync pending notes: %v", err)
	}

	// Phase 2: Pull updates from memora
	if err := o.pullMemoraUpdates(ctx); err != nil {
		log.Printf("[Sync] Failed to pull memora updates: %v", err)
	}
}

// syncPendingNotes pushes all pending notes to memora.
func (o *Orchestrator) syncPendingNotes(ctx context.Context) error {
	if o.memoraClient == nil {
		return nil // memora not configured
	}

	pending, err := o.syncStateStore.GetPending()
	if err != nil {
		return fmt.Errorf("get pending: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	log.Printf("[Sync] Found %d pending notes", len(pending))

	for _, noteID := range pending {
		if err := o.syncOneNote(ctx, noteID); err != nil {
			log.Printf("[Sync] Failed to sync note %s: %v", noteID, err)
			// Continue with other notes even if one fails
		}
	}

	return nil
}

// syncOneNote syncs a single note to memora with retry logic.
func (o *Orchestrator) syncOneNote(ctx context.Context, noteID string) error {
	state, err := o.syncStateStore.GetByNoteID(noteID)
	if err != nil {
		return fmt.Errorf("get sync state: %w", err)
	}
	if state == nil {
		return nil // No sync state yet, skip
	}

	var lastErr error
	for attempt := 0; attempt < o.maxRetries; attempt++ {
		// TODO: Get note content from notes store
		// For now, this is a placeholder
		memoraID, err := o.memoraClient.PushNote(ctx, noteID, "title", "content")
		if err == nil {
			// Success
			if err := o.syncStateStore.MarkSynced(noteID, memoraID); err != nil {
				log.Printf("[Sync] Failed to mark synced: %v", err)
			}
			log.Printf("[Sync] Synced note %s to memora as %s", noteID, memoraID)
			return nil
		}

		lastErr = err
		if attempt < o.maxRetries-1 {
			// Exponential backoff: 1s, 2s, 4s
			backoff := time.Duration(1<<uint(attempt)) * o.retryBackoffBase
			time.Sleep(backoff)
		}
	}

	// All retries failed
	if err := o.syncStateStore.MarkError(noteID, lastErr.Error()); err != nil {
		log.Printf("[Sync] Failed to mark error: %v", err)
	}
	return fmt.Errorf("sync failed after %d retries: %w", o.maxRetries, lastErr)
}

// pullMemoraUpdates pulls notes updated since last sync from memora.
func (o *Orchestrator) pullMemoraUpdates(ctx context.Context) error {
	if o.memoraClient == nil {
		return nil // memora not configured
	}

	// Get last sync time (use current time - 1 hour as fallback)
	lastSync := time.Now().Add(-1 * time.Hour).UnixMilli()

	updates, err := o.memoraClient.GetUpdatedSince(ctx, lastSync)
	if err != nil {
		return fmt.Errorf("get updates since: %w", err)
	}

	if len(updates) == 0 {
		return nil
	}

	log.Printf("[Sync] Pulled %d updates from memora", len(updates))

	for _, item := range updates {
		if err := o.mergeFromMemora(ctx, item); err != nil {
			log.Printf("[Sync] Failed to merge from memora: %v", err)
		}
	}

	return nil
}

// mergeFromMemora merges a memora note into local storage.
func (o *Orchestrator) mergeFromMemora(ctx context.Context, item MemoraNote) error {
	// Get current local sync state
	state, err := o.syncStateStore.GetByNoteID(item.ID)
	if err != nil {
		return fmt.Errorf("get sync state: %w", err)
	}

	if state == nil {
		// New note from memora, mark as synced
		newState := &SyncState{
			NoteID:      item.ID,
			MemoraID:    item.ID,
			ContentHash: item.Hash,
			SyncStatus:  "synced",
			Version:     1,
		}
		return o.syncStateStore.Upsert(newState)
	}

	// Check for conflict: if local content hash differs from memora hash
	if state.ContentHash != item.Hash {
		// Conflict detected
		if err := o.syncStateStore.MarkConflict(item.ID); err != nil {
			return fmt.Errorf("mark conflict: %w", err)
		}
		log.Printf("[Sync] Conflict detected for note %s", item.ID)
		return nil
	}

	// No conflict, mark as synced
	return o.syncStateStore.MarkSynced(item.ID, item.ID)
}

// MarkForSync marks a note as pending sync (called after local edit).
func (o *Orchestrator) MarkForSync(ctx context.Context, noteID, content string) error {
	contentHash := hashContent(content)

	state := &SyncState{
		NoteID:      noteID,
		ContentHash: contentHash,
		SyncStatus:  "pending",
		Version:     1,
	}

	return o.syncStateStore.Upsert(state)
}

// ResolveConflict resolves a conflict by choosing local or remote version.
func (o *Orchestrator) ResolveConflict(ctx context.Context, noteID, resolution string) error {
	if resolution != "local" && resolution != "remote" && resolution != "merge" {
		return fmt.Errorf("invalid resolution: %s", resolution)
	}

	state, err := o.syncStateStore.GetByNoteID(noteID)
	if err != nil {
		return fmt.Errorf("get sync state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no sync state found for note %s", noteID)
	}

	switch resolution {
	case "local":
		// Mark as pending to push local version again
		return o.syncStateStore.Upsert(&SyncState{
			NoteID:     noteID,
			SyncStatus: "pending",
			Version:    state.Version + 1,
		})
	case "remote":
		// Mark as synced with remote hash (accept remote version)
		return o.syncStateStore.MarkSynced(noteID, state.MemoraID)
	case "merge":
		// TODO: Implement smart merge strategy
		return o.syncStateStore.MarkSynced(noteID, state.MemoraID)
	}

	return nil
}

// GetSyncStatus returns the current sync status overview.
func (o *Orchestrator) GetSyncStatus(ctx context.Context) (map[string]interface{}, error) {
	pending, err := o.syncStateStore.GetPending()
	if err != nil {
		return nil, fmt.Errorf("get pending: %w", err)
	}

	return map[string]interface{}{
		"pendingCount": len(pending),
		"syncInterval": o.syncInterval.String(),
		"memoraConfigured": o.memoraClient != nil,
	}, nil
}

// hashContent computes SHA256 hash of content.
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}
