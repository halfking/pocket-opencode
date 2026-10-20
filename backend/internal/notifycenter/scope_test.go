package notifycenter

// scope_test.go — §944 D4 判据：notification_rules 的**归属守卫**。
//
// ⚠⚠ 本节查出来的缺陷（**修复前实测**）：
//
//	`Store.UpsertRule` 的 `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	⇒ ⇒ ⇒ 撞上一个**别的 workspace** 的 rule id 时，它会把那一行的
//	**规则内容整个改写成提交者的**（渠道 / 优先级 / 静默时段 / 开关）
//	⇒ ⇒ ⇒ 而 workspace_id **不在** SET 列表里 ⇒ ⇒⇒⇒ 那条规则**留在
//	受害者的租户里**、却已经**听攻击者的**了
//	⇒ ⇒⇒⇒ 受害者的通知被静默、被改道到别的渠道。
//
// ⚠ 守卫断言 workspace_id（它**不在** SET 列表里 ⇒ 永远不会被搬走
//   ⇒ 它就是「这条规则属于谁」的唯一稳定锚点）⇒ ★ 这正是
//   「只断言不可搬走的归属列」这条通用口径的又一个实例。
//
// ⚠ 判据量的是**行为**（撞号后：报错 + 受害者的规则一字未改），不是源码文本。
//
// ⚠ 负控（同一文件内自带）：**同一个 workspace** 用同一个 id 改规则 ⇒ 照常更新
//   ⇒ ⇒⇒ 这一腿证明「守卫没有把正常路径（改规则）也挡掉」。
import (
	"context"
	"errors"
	"testing"
)

const (
	s944RuleID  = "rule-s944-victim"
	s944RuleWS  = "ws-s944-victim"
	s944RuleWS2 = "ws-s944-attacker"
)

// s944Rule 造一条通知规则。
func s944Rule(id, ws, source, priority string, enabled bool) *Rule {
	return &Rule{
		ID:          id,
		WorkspaceID: ws,
		Source:      source,
		Kind:        "important",
		Channels:    []string{string(ChannelInbox)},
		Priority:    priority,
		Enabled:     enabled,
	}
}

// s944FindRule 在某个 workspace 的规则清单里找一条。
func s944FindRule(t *testing.T, store *Store, ws, id string) *Rule {
	t.Helper()
	rules, err := store.ListRules(context.Background(), ws)
	if err != nil {
		t.Fatalf("列规则失败: %v", err)
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

// TestS944UpsertRuleOfAnotherWorkspaceIsRejected 撞号 ⇒ 报错 + 受害者的规则一字未改。
func TestS944UpsertRuleOfAnotherWorkspaceIsRejected(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertRule(ctx, s944Rule(s944RuleID, s944RuleWS, "email", "high", true)); err != nil {
		t.Fatalf("受害者建规则应成功: %v", err)
	}

	err := store.UpsertRule(ctx, s944Rule(s944RuleID, s944RuleWS2, "attacker-source", "low", false))
	// ⚠ 用 Errorf 而不是 Fatalf ⇒ 守卫被去掉时，**规则被改没改**那条检查
	//   仍然会跑 ⇒ ⇒⇒ 「越权覆盖」这个症状**一定**出现在输出里。
	if err == nil {
		t.Errorf("攻击者撞 id 竟然成功了 ⇒ 归属守卫没生效（受害者的通知规则被改写）")
	} else if !errors.Is(err, ErrRuleScopeConflict) {
		t.Errorf("撞 id 应返回 ErrRuleScopeConflict，实际 %v", err)
	}

	got := s944FindRule(t, store, s944RuleWS, s944RuleID)
	if got == nil {
		t.Fatalf("受害者的规则不见了 ⇒ 归属被搬走了")
	}
	if got.Source != "email" {
		t.Errorf("受害者的规则 source 变成了 %q（want email）⇒ **越权覆盖**", got.Source)
	}
	if got.Priority != "high" {
		t.Errorf("受害者的规则优先级变成了 %q（want high）⇒ **越权覆盖**", got.Priority)
	}
	if !got.Enabled {
		t.Errorf("受害者的规则被关掉了 ⇒ **越权覆盖**")
	}
}

// TestS944UpsertRuleSameWorkspaceStillUpdates 负控：同一个 workspace 改规则 ⇒ 照常更新。
func TestS944UpsertRuleSameWorkspaceStillUpdates(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertRule(ctx, s944Rule(s944RuleID, s944RuleWS, "email", "high", true)); err != nil {
		t.Fatalf("首次建规则应成功: %v", err)
	}
	if err := store.UpsertRule(ctx, s944Rule(s944RuleID, s944RuleWS, "meeting", "normal", false)); err != nil {
		t.Fatalf("同 workspace 改规则应成功，实际: %v", err)
	}
	got := s944FindRule(t, store, s944RuleWS, s944RuleID)
	if got == nil {
		t.Fatalf("规则不见了")
	}
	if got.Source != "meeting" || got.Priority != "normal" || got.Enabled {
		t.Errorf("规则没被更新：source=%q priority=%q enabled=%v", got.Source, got.Priority, got.Enabled)
	}
}
