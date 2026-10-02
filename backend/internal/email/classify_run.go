package email

import (
	"context"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

type RawClassifyResult struct {
	EmailID    string
	Category   string
	Importance string
	Summary    string
	Action     string
	// Reason 是 AI 判定该分类/重要度的依据。
	//
	// 2026-10-02 补。这个字段原先**根本不存在**：kxmemory 的分类响应契约里
	// 带 action_reason（docs/2026-07-02-kxmemory-api-contract.md），
	// client.Result.ActionReason 也早就解出来了，但 classifyRun 在构造
	// RawClassifyResult 时只搬了 Category/Importance/Summary/SuggestedAction，
	// action_reason 在**这一跳**被丢弃 —— 于是后面的
	// SetClassificationScoped（不写这一列）拿不到它，分类器给出的判定依据
	// 永远进不了库。
	//
	// 实测 blast radius：真库 122 封邮件 action_reason **全为空**，而同一响应里
	// 的 ai_summary **122 封全有值**。两者来自同一个分类结果，这排除了
	// 「分类器没返回」的解释——它返回了，只是被这里扔掉。
	// 后果：提醒卡片无法回答「为什么这封被判为重要」，用户无从判断该不该点开。
	Reason string
}

func BuildClassifyWrites(in []RawClassifyResult) []RawClassifyResult {
	out := make([]RawClassifyResult, 0, len(in))
	for _, r := range in {
		r.Category = NormalizeCategory(r.Category)
		// Importance 也要归一化。修复前这里只处理了 Category：kxmemory
		// 返回什么就原样落库，而下游 splitReminderCandidates 用
		// `case "high"` 精确匹配（pipeline.go:727）——上游返回 "High" 时
		// 重要邮件会**静默漏提醒**，报告里 remindersSent=0 也不报错。
		// DB 层没有 CHECK 约束兜底（实测 pg_constraint 对 emails 返回 0 行），
		// 脏值会一直留着。
		r.Importance = NormalizeImportance(r.Importance)
		if r.EmailID == "" || r.Category == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// NormalizeImportance 把上游（kxmemory / 规则）返回的重要度归一成
// high / medium / low 三档之一；无法识别时返回空串。
//
// 空串的语义是「未分类」——splitReminderCandidates 会把它计入
// unclassified，让报告里的 0 变得可解释；这比落一个匹配不上的脏值好得多
// （脏值既不提醒也不计数，是最坏情况）。
//
// 覆盖上游常见偏差：大小写（"High"）、中文（"高"）、数字档位（"1"）。
func NormalizeImportance(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return ""
	case "high", "h", "1", "高", "重要", "紧急", "urgent", "critical":
		return "high"
	case "medium", "med", "m", "2", "中", "普通", "normal":
		return "medium"
	case "low", "l", "3", "低", "次要", "minor":
		return "low"
	}
	return ""
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
			// 必须一起搬：漏了这一项 action_reason 就止步于此，
			// 后面无论调哪个写库方法都补不回来（真库 122/122 为空的成因）。
			Reason: row.ActionReason,
		}})
		if len(writes) == 0 {
			cancel()
			continue
		}
		w := writes[0]
		// 走带 reason 的写库方法。用 SetClassificationScoped 会静默丢列——
		// 那个方法的签名里就没有它，编译通过、运行不报错、库里永远是空。
		err := store.SetClassificationWithReasonScoped(callCtx, w.EmailID, userID, workspaceID,
			w.Category, w.Importance, w.Summary, w.Action, w.Reason)
		cancel()
		if err != nil {
			continue
		}
		n++
	}
	return n, nil
}
