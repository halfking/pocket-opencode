package email

import (
	"context"
	"fmt"
	"log"
)

// JunkUIDMover 把 INBOX UID 移到 Junk。测试用假实现，生产走 Fetcher。
type JunkUIDMover interface {
	MoveUIDsToJunk(ctx context.Context, accountID string, uids []int64) ([]int64, error)
}

// CleanupReport 是预览或执行后的结果。
type CleanupReport struct {
	Matched    int           `json:"matched"`
	Moved      int           `json:"moved"`
	Deleted    int           `json:"deleted"`
	DeletedIDs []string      `json:"deletedIds,omitempty"`
	Failed     []string      `json:"failed,omitempty"`
	Emails     []CleanupItem `json:"emails,omitempty"`
}

const cleanupPreviewCap = 50

func previewItems(items []CleanupItem) []CleanupItem {
	if len(items) <= cleanupPreviewCap {
		return items
	}
	return items[:cleanupPreviewCap]
}

func groupUIDsByAccount(items []CleanupItem) map[string][]int64 {
	out := map[string][]int64{}
	for _, it := range items {
		if it.UID <= 0 {
			continue
		}
		out[it.AccountID] = append(out[it.AccountID], it.UID)
	}
	return out
}

// RunCleanup dryRun=true 只预览；false 则 IMAP MOVE 后只删成功 UID。
func RunCleanup(ctx context.Context, store *Store, mover JunkUIDMover, f CleanupFilter, userID, workspaceID string, dryRun bool) (CleanupReport, error) {
	var rep CleanupReport
	if err := f.Validate(); err != nil {
		return rep, err
	}
	items, err := store.ListEmailsForCleanupScoped(ctx, f, userID, workspaceID)
	if err != nil {
		return rep, err
	}
	rep.Matched = len(items)
	rep.Emails = previewItems(items)
	if dryRun {
		return rep, nil
	}
	if mover == nil {
		return rep, fmt.Errorf("email fetcher not configured (IMAP disabled)")
	}

	movedByAccount := map[string][]int64{}
	for accountID, uids := range groupUIDsByAccount(items) {
		moved, merr := mover.MoveUIDsToJunk(ctx, accountID, uids)
		if len(moved) > 0 {
			movedByAccount[accountID] = moved
			rep.Moved += len(moved)
		}
		if merr != nil {
			msg := fmt.Sprintf("%s: %v", accountID, merr)
			rep.Failed = append(rep.Failed, msg)
			log.Printf("[email/cleanup] %s", msg)
		}
	}
	for _, it := range items {
		if it.UID <= 0 {
			rep.Failed = append(rep.Failed, it.ID+": missing IMAP uid")
		}
	}

	ids := SelectDeletable(items, movedByAccount)
	n, err := store.DeleteEmailsByIDsScoped(ctx, ids, userID, workspaceID)
	if err != nil {
		return rep, err
	}
	rep.Deleted = int(n)
	rep.DeletedIDs = ids
	return rep, nil
}
