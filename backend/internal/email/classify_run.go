package email

import (
	"context"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

type RawClassifyResult struct {
	EmailID    string
	Category   string
	Importance string
	Summary    string
	Action     string
}

func BuildClassifyWrites(in []RawClassifyResult) []RawClassifyResult {
	out := make([]RawClassifyResult, 0, len(in))
	for _, r := range in {
		r.Category = NormalizeCategory(r.Category)
		if r.EmailID == "" || r.Category == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func ShouldProcessAfterFetch(syncedAccounts, newEmails int) bool {
	_ = newEmails
	return syncedAccounts > 0
}

// ClassifyUnclassified 委托 kxmemory 逐封处理未归类邮件。IMAP 在 Fetcher /
// Scheduler 里跑，WebView 不碰邮箱协议。
func ClassifyUnclassified(ctx context.Context, store *Store, kx kxmemory.Client, userID, workspaceID string, limit int) (int, error) {
	if store == nil || kx == nil || userID == "" {
		return 0, nil
	}
	items, err := store.ListUnclassifiedScoped(ctx, userID, workspaceID, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		resp, cerr := kx.ClassifyEmails(callCtx, kxmemory.ClassifyEmailsRequest{
			Emails: []kxmemory.EmailForClassification{{
				EmailID: it.ID, Subject: it.Subject, Snippet: it.Snippet,
				FromAddress: it.FromAddress, FromName: it.FromName,
			}},
		})
		if cerr != nil || resp == nil || len(resp.Results) == 0 {
			cancel()
			continue
		}
		row := resp.Results[0]
		writes := BuildClassifyWrites([]RawClassifyResult{{
			EmailID: it.ID, Category: row.Category, Importance: row.Importance,
			Summary: row.Summary, Action: row.SuggestedAction,
		}})
		if len(writes) == 0 {
			cancel()
			continue
		}
		w := writes[0]
		err := store.SetClassificationScoped(callCtx, w.EmailID, userID, workspaceID, w.Category, w.Importance, w.Summary, w.Action)
		cancel()
		if err != nil {
			continue
		}
		n++
	}
	return n, nil
}
