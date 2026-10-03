package email

// store_snippet_refresh_test.go — 重跑同步必须能修好已入库的坏摘要。
//
// 需要活的 PostgreSQL；见 store_workspace_test.go 的 POCKET_TEST_POSTGRES_DSN 约定
// 与每测试独立 schema 的夹具（没设 DSN 时自动 skip）。
//
// 缺陷背景（真机 2026-10-01）：
//   InsertEmail 原来是 `ON CONFLICT (id) DO NOTHING`，snippet 因此是**只写一次**的
//   不可自愈字段。DeriveSnippet（防原始 MIME / 字面 HTML 漏进摘要）上线之前写进去的
//   坏摘要永远留在库里 —— 真机 /notifications 100 个正文元素里 46 个溢出、累计
//   12,311px 内容被祖先 overflow-x:hidden 静默裁掉。代码修好了，数据仍然是坏的。
//
// 这三个用例把「只刷 snippet、且不伤用户状态、且不刷空白」钉死。少任何一条都会
// 引入比原缺陷更糟的回归：
//   - 不刷 → 存量坏数据永远好不了（就是本缺陷本身）
//   - 连 is_read 一起刷 → 每轮同步把用户读过的邮件标回未读
//   - 空值直刷 → DeriveSnippet 的「疑似 MIME 但剥不干净就返回空」分支，一次
//     同步就把正常摘要清空

import (
	"context"
	"testing"
	"time"
)

func snippetOf(t *testing.T, store *Store, id string) string {
	t.Helper()
	var got string
	err := store.pool.QueryRow(context.Background(),
		`SELECT snippet FROM emails WHERE id = $1`, id).Scan(&got)
	if err != nil {
		t.Fatalf("read snippet %s: %v", id, err)
	}
	return got
}

// 存量坏摘要（原始 MIME 转储）必须在重跑同步后被换成干净摘要。
func TestInsertEmailRefreshesSnippetOnConflict(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-snip", "u1", "ws-a")

	// 第一轮：模拟 DeriveSnippet 上线前落库的那批坏数据
	const rawMIME = "------=_Part_397111_1624436759.1790214518883\r\nContent-Type: multipart/alternative; boundary=\"x\"\r\n\r\n<html>您好</html>"
	if err := store.InsertEmail(ctx, Email{
		ID: "em-snip-1", AccountID: "acct-snip", WorkspaceID: "ws-a",
		MessageID: "snip1@example.com", FromAddress: "a@example.com",
		Subject: "额度提醒", Snippet: rawMIME, Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("首次入库: %v", err)
	}
	if got := snippetOf(t, store, "em-snip-1"); got != rawMIME {
		t.Fatalf("前置条件不成立：首轮应原样存下 %q，实际 %q", rawMIME, got)
	}

	// 第二轮：修复后的抓取流程重跑同步
	const clean = "您的额度即将用尽，请及时充值。"
	if err := store.InsertEmail(ctx, Email{
		ID: "em-snip-1", AccountID: "acct-snip", WorkspaceID: "ws-a",
		MessageID: "snip1@example.com", FromAddress: "a@example.com",
		Subject: "额度提醒", Snippet: clean, Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("重跑同步: %v", err)
	}
	if got := snippetOf(t, store, "em-snip-1"); got != clean {
		t.Fatalf("重跑同步没刷新坏摘要：仍是 %q", got)
	}
}

// 刷新 snippet 时绝不能碰用户/流程已经算好的状态。
func TestInsertEmailConflictDoesNotClobberUserState(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-state", "u1", "ws-a")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-state-1", AccountID: "acct-state", WorkspaceID: "ws-a",
		MessageID: "state1@example.com", Subject: "季度报告", Snippet: "旧摘要",
		Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("首次入库: %v", err)
	}
	// 用户读过了、还标了星；分类流程也写了结论
	if _, err := store.pool.Exec(ctx,
		`UPDATE emails SET is_read = true, is_starred = true, category = 'work', ai_summary = '已生成的总结' WHERE id = 'em-state-1'`,
	); err != nil {
		t.Fatalf("预置用户状态: %v", err)
	}

	// 重跑同步时这封邮件在信封里是未读未标星的（IMAP 侧的真实情况）
	if err := store.InsertEmail(ctx, Email{
		ID: "em-state-1", AccountID: "acct-state", WorkspaceID: "ws-a",
		MessageID: "state1@example.com", Subject: "季度报告", Snippet: "新摘要",
		Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("重跑同步: %v", err)
	}

	var isRead, isStar bool
	var category, aiSummary string
	if err := store.pool.QueryRow(ctx,
		`SELECT is_read, is_starred, category, ai_summary FROM emails WHERE id = 'em-state-1'`,
	).Scan(&isRead, &isStar, &category, &aiSummary); err != nil {
		t.Fatalf("读回状态: %v", err)
	}
	if !isRead {
		t.Error("重跑同步把用户读过的邮件标回了未读 —— DO UPDATE 不能带 is_read")
	}
	if !isStar {
		t.Error("重跑同步把星标冲掉了 —— DO UPDATE 不能带 is_starred")
	}
	if category != "work" {
		t.Errorf("分类被覆盖成 %q，应保留 work", category)
	}
	if aiSummary != "已生成的总结" {
		t.Errorf("AI 总结被覆盖成 %q", aiSummary)
	}
	if got := snippetOf(t, store, "em-state-1"); got != "新摘要" {
		t.Errorf("snippet 应被刷新为 %q，实际 %q", "新摘要", got)
	}
}

// DeriveSnippet 在「疑似整段 MIME 又剥不干净」时返回空串（宁可空着也不把 MIME
// 转储还给用户）。那次同步不能顺手把库里已有的正常摘要清空。
func TestInsertEmailConflictKeepsOldSnippetWhenNewIsEmpty(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-empty", "u1", "ws-a")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-empty-1", AccountID: "acct-empty", WorkspaceID: "ws-a",
		MessageID: "empty1@example.com", Subject: "公告", Snippet: "原有正常摘要",
		Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("首次入库: %v", err)
	}
	// 本轮抓取没能解析出干净摘要（返回空串）
	if err := store.InsertEmail(ctx, Email{
		ID: "em-empty-1", AccountID: "acct-empty", WorkspaceID: "ws-a",
		MessageID: "empty1@example.com", Subject: "公告", Snippet: "",
		Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("重跑同步: %v", err)
	}
	if got := snippetOf(t, store, "em-empty-1"); got != "原有正常摘要" {
		t.Fatalf("空摘要把旧值冲掉了：现在是 %q，丢了用户已经能看到的正文", got)
	}
}
