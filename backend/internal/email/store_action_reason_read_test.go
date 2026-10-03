package email

// store_action_reason_read_test.go — action_reason 的**读路径**（q2）。
//
// 背景：action_reason 的**写**路径早就齐了（applyInlineRules /
// SetClassificationWithReasonScoped / InsertEmail 的 ON CONFLICT 都有
// CASE WHEN ... <> '' THEN ... ELSE 旧值 END），但**读**路径从来没读过它：
//
//   - scanEmail（ListEmailsScoped 的唯一扫描函数）不 SELECT 这一列；
//   - GetEmailByIDScoped 的 SELECT 里也没有。
//
// 于是无论库里有没有值，列表页与详情页拿到的 ActionReason 恒为空串。
// 这类缺口没有任何错误信号：接口返回 200、字段少一个、界面少一行文案。
//
// 「真库 122/122 为空」是**另一处**问题（上游 kxmemory DTO 曾丢字段，
// 已由 classify_action_reason_test.go 覆盖）。读路径缺失是独立的第二处：
// 即使有值也显示不出来。两条都要修，缺一条功能都不成立。
//
// 负控：
//   - 把 ListEmailsScoped 的 SELECT 里的 COALESCE(e.action_reason,'') 去掉
//     -> TestListEmailsScoped_ReturnsActionReason 转红（列数与 Scan 不匹配）。
//   - 把 GetEmailByIDScoped 的 SELECT 里的该列去掉 -> 详情用例转红。
//   - 把 scanEmail 的 &actionReason 换成别的变量 -> 列错位，has_attachments
//     会拿到 action_reason 的值，下面那条断言正是为此设的。
import (
	"context"
	"strings"
	"testing"
)

const q2Reason = "包含截止日期且需回复确认"

// seedReasonEmail 插一封带 action_reason 的邮件。
func seedReasonEmail(t *testing.T, store *Store, id, accountID, workspaceID, importance, reason string) {
	t.Helper()
	ctx := context.Background()
	if err := store.InsertEmail(ctx, Email{
		ID: id, AccountID: accountID, WorkspaceID: workspaceID,
		FromAddress: "vendor@example.com", Subject: "季度对账与开票确认 " + id,
		Snippet: "请于本周五前完成对账确认。", Date: 1750000000, UID: 7,
		Importance: importance, Category: "work", ActionReason: reason,
	}); err != nil {
		t.Fatalf("InsertEmail %s: %v", id, err)
	}
}

func TestListEmailsScoped_ReturnsActionReason(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason", "u1", "ws-1")
	seedReasonEmail(t, store, "em-r1", "acct-reason", "ws-1", "high", q2Reason)
	seedReasonEmail(t, store, "em-r2", "acct-reason", "ws-1", "low", "营销群发")

	// 前置断言：库里的值必须真的在。读不到时如果直接断言读路径，本用例
	// 会在「写路径坏了」和「读路径坏了」之间无法分辨。
	var want string
	if err := store.pool.QueryRow(ctx,
		`SELECT COALESCE(action_reason,'') FROM emails WHERE id='em-r1'`).Scan(&want); err != nil {
		t.Fatalf("读前置: %v", err)
	}
	if want != q2Reason {
		t.Fatalf("前置失败：库中 action_reason=%q，want %q —— 写路径坏了，不是读路径", want, q2Reason)
	}

	items, err := store.ListEmailsScoped(ctx, ListFilter{Folder: "__all__"}, "u1", "ws-1")
	if err != nil {
		t.Fatalf("ListEmailsScoped: %v", err)
	}
	byID := map[string]Email{}
	for _, it := range items {
		byID[it.ID] = it
	}
	got, ok := byID["em-r1"]
	if !ok {
		t.Fatalf("列表里没有 em-r1，实际 %d 封: %v", len(items), items)
	}
	if got.ActionReason != q2Reason {
		t.Errorf("列表读到的 action_reason=%q，want %q —— scanEmail 仍在漏读这一列",
			got.ActionReason, q2Reason)
	}
	// 防列错位：SELECT 少一列而 Scan 多一个目标，不会报错，只会让后面的
	// 字段整体串位。has_attachments 紧跟在 action_reason 之后，是最好的探针。
	// 夹具的 has_attachments 是 false（InsertEmail 没给附件），所以这里断言
	// 它**仍是 false**——若被读成了 action_reason 的非空值就说明串位了。
	if got.HasAttachments {
		t.Errorf("em-r1 的 has_attachments=true —— SELECT 与 Scan 的列数/列序可能已错位" +
			"（action_reason 被读进了 has_attachments）")
	}
	if got.Importance != "high" || got.Category != "work" {
		t.Errorf("相邻列串位了：importance=%q category=%q", got.Importance, got.Category)
	}
	// 第二封也必须读到自己的理由，不能全是第一封的。
	if r2 := byID["em-r2"]; r2.ActionReason != "营销群发" {
		t.Errorf("em-r2 的 action_reason=%q，want 营销群发", r2.ActionReason)
	}
}

func TestGetEmailByIDScoped_ReturnsActionReason(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason2", "u1", "ws-1")
	seedReasonEmail(t, store, "em-r3", "acct-reason2", "ws-1", "high", q2Reason)

	em, err := store.GetEmailByIDScoped(ctx, "em-r3", "u1", "ws-1")
	if err != nil {
		t.Fatalf("GetEmailByIDScoped: %v", err)
	}
	if em == nil {
		t.Fatal("详情返回 nil，邮件不存在或作用域不匹配")
	}
	if em.ActionReason != q2Reason {
		t.Errorf("详情读到的 action_reason=%q，want %q —— GetEmailByIDScoped 的 SELECT 漏了这一列",
			em.ActionReason, q2Reason)
	}
	// 夹具无附件，has_attachments 应仍为 false；为 true 说明列序错位。
	if em.HasAttachments {
		t.Error("em-r3 的 has_attachments=true —— SELECT 与 Scan 列数可能已错位")
	}
}

// 空理由必须读成空串而不是报错或「undefined」。
// NULL 与空串在语义上不同：前者是「上游没给理由」，后者是「理由是空的」。
// 两者都映射到 Go 的 string，无法区分 —— 但至少不能因此让整条查询失败。
func TestActionReasonNullReadsAsEmptyString(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-reason3", "u1", "ws-1")
	// 直接插 NULL，绕开 InsertEmail 的「非空才写」CASE。
	// created_at / updated_at 是 NOT NULL 且无默认值，必须显式给。
	if _, err := store.pool.Exec(ctx, `INSERT INTO emails
		(id, account_id, workspace_id, from_address, subject, date, action_reason, created_at, updated_at)
		VALUES ('em-null', 'acct-reason3', 'ws-1', 'a@example.com', 'x', 1750000000, NULL, 0, 0)`); err != nil {
		t.Fatalf("seed NULL: %v", err)
	}

	em, err := store.GetEmailByIDScoped(ctx, "em-null", "u1", "ws-1")
	if err != nil {
		t.Fatalf("NULL 的 action_reason 不该让查询失败: %v", err)
	}
	if em.ActionReason != "" {
		t.Errorf("NULL 应读成空串，实际 %q", em.ActionReason)
	}

	items, err := store.ListEmailsScoped(ctx, ListFilter{Folder: "__all__"}, "u1", "ws-1")
	if err != nil {
		t.Fatalf("ListEmailsScoped: %v", err)
	}
	for _, it := range items {
		if it.ID == "em-null" && it.ActionReason != "" {
			t.Errorf("列表里 NULL 读成 %q，want 空串", it.ActionReason)
		}
	}
}

// 作用域隔离不能因为多读一列而失效：别人的邮件仍然读不到。
func TestActionReasonDoesNotLeakAcrossScope(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-mine", "u1", "ws-1")
	seedAccount(t, store, "acct-theirs", "u2", "ws-1")
	seedReasonEmail(t, store, "em-theirs", "acct-theirs", "ws-1", "high", "别人的判定依据")

	items, err := store.ListEmailsScoped(ctx, ListFilter{Folder: "__all__"}, "u1", "ws-1")
	if err != nil {
		t.Fatalf("ListEmailsScoped: %v", err)
	}
	for _, it := range items {
		if strings.Contains(it.ActionReason, "别人的") {
			t.Fatalf("读到了别人的邮件 %s —— 作用域守卫失效", it.ID)
		}
	}
	if em, err := store.GetEmailByIDScoped(ctx, "em-theirs", "u1", "ws-1"); err != nil {
		t.Fatalf("GetEmailByIDScoped: %v", err)
	} else if em != nil {
		t.Fatalf("跨作用域读到了 %s", em.ID)
	}
}
