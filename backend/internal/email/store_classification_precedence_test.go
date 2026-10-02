package email

// store_classification_precedence_test.go — 账户规则与 AI 分类的优先级。
//
// 2026-10-02 修完「重跑同步不刷新 importance」之后发现的**下一环**：
// 规则判出的 importance=high 会被 AI 分类悄悄抹掉，于是配了规则照样没提醒。
//
// 链路是这样接起来的（每一环都实测过，不是推演）：
//
//  1. 账户只配了 mark-important（没有 label-category）→ 入库时
//     importance='high' 而 **category 仍为空**；
//  2. ListUnclassifiedScoped 挑待分类邮件时过滤的是 **category**（不是
//     importance）⇒ 这封规则标重要的邮件照常进 AI 分类队列；
//  3. BuildClassifyWrites 只要求 category 非空，LLM 没给 importance 时
//     Importance 就是空串；
//  4. SetClassificationScoped 是**全量覆盖**（category/importance/ai_summary/
//     suggested_action 四个字段一起写）⇒ importance 被写成 '' 或 'normal'。
//
// 结果：用户明确配了「这个发件人的邮件标重要」，AI 一句话就把它降级，提醒
// 永远不发，而且没有任何报错。症状与 81704531 修的那个一模一样，排查方向却
// 完全相反（那个是规则没落到库，这个是落了又被抹掉）。
//
// 修法：importance='high' 对 AI 免疫（规则显式配置优先），其余情况 AI 照旧。
// category 刻意不动 —— 它归 AI 拥有，这是有意保留的语义边界。
import (
	"context"
	"testing"
)

// 可达性：只标了重要、没标分类的邮件**确实会进** AI 分类队列。
// 没有这条，上面那条链路的第 2 环就只是读代码读出来的结论。
func TestRuleMarkedEmailStillEntersClassifyQueue(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-rule", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-rule", "acct-rule", "ws-a", "high", "") // 只有 mark-important

	items, err := store.ListUnclassifiedScoped(ctx, "user-1", "ws-a", 20)
	if err != nil {
		t.Fatalf("list unclassified: %v", err)
	}
	if len(items) != 1 || items[0].ID != "em-rule" {
		t.Fatalf("待分类队列 = %+v，want 只有 em-rule（category 为空就会入队）", items)
	}
}

// AI 没给 importance 时，不得抹掉规则判出的 high；但它给的 category 要照写。
func TestClassifyDoesNotDowngradeRuleImportance(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-rule", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-rule", "acct-rule", "ws-a", "high", "")

	// LLM 回了一个 category，但没给 importance。
	if err := store.SetClassificationScoped(ctx, "em-rule", "user-1", "ws-a", "work", "", "摘要", ""); err != nil {
		t.Fatalf("SetClassificationScoped: %v", err)
	}

	var importance, category string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(importance,''), COALESCE(category,'') FROM emails WHERE id='em-rule'`).
		Scan(&importance, &category); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if importance != "high" {
		t.Fatalf("importance=%q，want high —— AI 把它降级了，规则配了也没用", importance)
	}
	if category != "work" {
		t.Fatalf("category=%q，want work（AI 的分类必须照常写入）", category)
	}
}

// 反向：AI 给 normal 时同样不该降掉 high；但 AI 给 high 时要能写进去。
// 没有这条，一个「importance 一律保持 high」的错实现也能通过上一条。
func TestClassifyKeepsHighButAcceptsHighAndNormal(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-rule", "user-1", "ws-a")
	seedReminderEmail(t, store, "em-high", "acct-rule", "ws-a", "high", "")
	seedReminderEmail(t, store, "em-normal", "acct-rule", "ws-a", "normal", "")
	seedReminderEmail(t, store, "em-empty", "acct-rule", "ws-a", "", "")

	// AI 把 high 判成 normal —— 规则优先，仍是 high。
	if err := store.SetClassificationScoped(ctx, "em-high", "user-1", "ws-a", "work", "normal", "s", ""); err != nil {
		t.Fatalf("classify em-high: %v", err)
	}
	// AI 把 normal 判成 high —— AI 可以升级。
	if err := store.SetClassificationScoped(ctx, "em-normal", "user-1", "ws-a", "work", "high", "s", ""); err != nil {
		t.Fatalf("classify em-normal: %v", err)
	}
	// AI 把空判成 normal —— 没有规则保护，按 AI 的写。
	if err := store.SetClassificationScoped(ctx, "em-empty", "user-1", "ws-a", "work", "normal", "s", ""); err != nil {
		t.Fatalf("classify em-empty: %v", err)
	}

	for id, want := range map[string]string{"em-high": "high", "em-normal": "high", "em-empty": "normal"} {
		var got string
		if err := store.pool.QueryRow(ctx, `SELECT COALESCE(importance,'') FROM emails WHERE id=$1`, id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got != want {
			t.Fatalf("%s importance=%q，want %q", id, got, want)
		}
	}
}
