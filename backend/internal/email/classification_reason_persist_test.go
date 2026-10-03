package email

// classification_reason_persist_test.go — AI 分类的「判定依据」必须一路落到库里。
//
// 缺陷（2026-10-01 全字段对账时查出）：kxmemory 的分类响应契约里带
// action_reason（docs/2026-07-02-kxmemory-api-contract.md 的响应示例明写
// `"action_reason": "包含截止日期且需回复确认"`），但客户端 DTO
// kxmemory.EmailClassificationResult **没有这个字段**。
//
// Go 的 encoding/json 对未知字段**静默丢弃、不报错**，所以：
//   1. 服务端一切正常，没有任何日志或错误；
//   2. suggested_action / ai_summary 都落库了（162 封真实邮件有值），
//      只有 action_reason 全是空串（真库实测 162/162 为空）。
//
// 后果是需求 4 的可解释性缺口：提醒说「这封重要」，但拿不到「为什么判它重要」，
// 用户无法判断这条提醒该不该信。这是需求 4 的功能，不是可选的增强。
//
// 断点有两处，缺一不可：
//   - DTO 漏字段（kxmemory/client.go）
//   - 写回 SQL 不带这一列（email/store.go 的 SetClassificationScoped）
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip）。
//
// 负控：任去掉一处（DTO 字段或 SQL 的 action_reason 赋值）-> 本文件转红。

import (
	"context"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// TestClassifyRunPersistsActionReason 端到端钉住 ClassifyUncategorized 这条
// **主分类链路**：kxmemory 返回的 action_reason 必须一路落到 emails 表。
//
// 2026-10-02：上面的用例只覆盖了 store 方法本身（断点 2：SQL），断点 1
// （DTO）由 kxmemory 包的用例覆盖。但真实链路上还有一个**第三处断点**，
// 两处旧用例都照不到：
//
//	ClassifyUncategorized 构造 RawClassifyResult 时只搬了 Category/
//	Importance/Summary/SuggestedAction，而 RawClassifyResult **压根没有
//	Reason 字段** —— kxmemory 明明解出了 row.ActionReason，却在构造
//	结构体这一跳被丢掉，随后调用的还是不写该列的 SetClassificationScoped。
//
// 实测 blast radius：真库 122 封邮件 action_reason 全为空，而同一响应里的
// ai_summary 122 封全有值。两者同源 ⇒ 分类器返回了，是这里扔掉的。
//
// 负控（两路互补）：
//
//	A. 把 Reason: row.ActionReason 去掉 -> reason 为空 -> 本用例转红
//	B. 改回调 SetClassificationScoped -> 该列不写 -> 本用例转红
func TestClassifyRunPersistsActionReason(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-run", "u", "ws-run")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ('em-run','acct-run','ws-run','m-run','s@example.com',
		        '周五前确认预算','snippet',1700000000,1700000000)`); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	const wantReason = "包含截止日期且需回复确认"
	kx := &classifyReasonKxmem{reason: wantReason}

	n, err := ClassifyUnclassified(ctx, store, kx, "u", "ws-run", 10)
	if err != nil {
		t.Fatalf("ClassifyUnclassified: %v", err)
	}
	// 前提断言：分类真的跑了、真的写了那一行。不满足就说明后面的结论不成立。
	if n != 1 {
		t.Fatalf("classified=%d want 1 —— 本用例的链路没被走到，结论不成立", n)
	}

	var reason, summary string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(action_reason,''), COALESCE(ai_summary,'') FROM emails WHERE id='em-run'`,
	).Scan(&reason, &summary); err != nil {
		t.Fatalf("read back: %v", err)
	}
	// 对照组：摘要必须同时有值。若它也为空，说明写入整体没生效。
	if summary == "" {
		t.Fatal("ai_summary 也为空：本用例的写入没有生效，结论不成立")
	}
	if reason != wantReason {
		t.Fatalf("action_reason=%q want %q —— 判定依据在 classifyRun 里被丢掉了，"+
			"提醒卡片无法回答「为什么判它重要」", reason, wantReason)
	}
}

// classifyReasonKxmem 复用 fakeKxmem 的其余 6 个方法，只把 ClassifyEmails
// 换成会返回 action_reason 的版本（父类的实现固定返回空结果）。
type classifyReasonKxmem struct {
	fakeKxmem
	reason string
	hit    int
}

func (f *classifyReasonKxmem) ClassifyEmails(_ context.Context, req kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	f.hit++
	results := make([]kxmemory.EmailClassificationResult, 0, len(req.Emails))
	for _, e := range req.Emails {
		results = append(results, kxmemory.EmailClassificationResult{
			EmailID:         e.EmailID,
			Category:        "work",
			Importance:      "high",
			Summary:         "张经理要求周五前确认 Q3 预算",
			SuggestedAction: "reply",
			ActionReason:    f.reason,
		})
	}
	return &kxmemory.ClassifyEmailsResponse{Results: results}, nil
}

// 落库：action_reason 必须真的写进 emails 表（断点 2：SQL）。
func TestSetClassificationWithReasonScoped_PersistsActionReason(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason", "u", "ws-reason")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at)
		VALUES ('em-reason','acct-reason','ws-reason','m-reason','s@example.com',
		        '周五前确认预算','snippet',1700000000,1700000000)`); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	if err := store.SetClassificationWithReasonScoped(ctx, "em-reason", "u", "ws-reason",
		"work", "high", "张经理要求周五前确认 Q3 预算", "reply", "包含截止日期且需回复确认"); err != nil {
		t.Fatalf("SetClassificationWithReasonScoped: %v", err)
	}

	var reason, action string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(action_reason,''), COALESCE(suggested_action,'')
		 FROM emails WHERE id='em-reason'`).Scan(&reason, &action); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if reason != "包含截止日期且需回复确认" {
		t.Fatalf("库中 action_reason=%q —— 判定依据没落库，提醒不可解释", reason)
	}
	if action != "reply" {
		t.Errorf("suggested_action=%q, want reply", action)
	}
}

// 分类器没给理由时不得抹掉已有值（与其它字段同口径的「非空才写」）。
func TestSetClassificationWithReasonScoped_EmptyReasonKeepsExisting(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason2", "u", "ws-reason2")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at, action_reason)
		VALUES ('em-reason2','acct-reason2','ws-reason2','m-reason2','s@example.com',
		        's','snippet',1700000000,1700000000,'原有依据')`); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	// 这轮分类器没返回 action_reason
	if err := store.SetClassificationWithReasonScoped(ctx, "em-reason2", "u", "ws-reason2",
		"work", "high", "摘要", "reply", ""); err != nil {
		t.Fatalf("SetClassificationWithReasonScoped: %v", err)
	}
	var reason string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(action_reason,'') FROM emails WHERE id='em-reason2'`).Scan(&reason); err != nil {
		t.Fatalf("read: %v", err)
	}
	if reason != "原有依据" {
		t.Fatalf("action_reason=%q, want 原有依据（空理由不应抹掉已有值）", reason)
	}
}

// 老方法仍不写 action_reason：避免规则引擎在 InsertEmail 时写入的依据被清空。
func TestSetClassificationScoped_DoesNotClobberActionReason(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason3", "u", "ws-reason3")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, message_id, from_address,
		                    subject, snippet, date, created_at, action_reason)
		VALUES ('em-reason3','acct-reason3','ws-reason3','m-reason3','s@example.com',
		        's','snippet',1700000000,1700000000,'规则命中依据')`); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	if err := store.SetClassificationScoped(ctx, "em-reason3", "u", "ws-reason3",
		"work", "high", "摘要", "reply"); err != nil {
		t.Fatalf("SetClassificationScoped: %v", err)
	}
	var reason string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(action_reason,'') FROM emails WHERE id='em-reason3'`).Scan(&reason); err != nil {
		t.Fatalf("read: %v", err)
	}
	if reason != "规则命中依据" {
		t.Fatalf("action_reason=%q, want 规则命中依据（老方法不得覆盖该列）", reason)
	}
}
