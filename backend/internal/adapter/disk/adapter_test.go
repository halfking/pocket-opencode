package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// 客户端不可控 locator：任何不是内置常量的取值都必须被拒绝，绝不当路径/URL 用。
func TestResolveRejectsUntrustedLocators(t *testing.T) {
	a := NewWithHome(writeClaudeHome(t, "ses-1", sampleClaudeJSONL))
	ctx := context.Background()

	for _, bad := range []string{
		"",
		"http://127.0.0.1:4096",
		"file:///etc/passwd",
		"disk://../../etc",
		"disk://claude/../codex",
		"/Users/x/.claude/projects",
	} {
		if _, err := a.ListSessions(ctx, bad); err == nil {
			t.Errorf("locator %q must be rejected", bad)
		}
		if IsLocator(bad) {
			t.Errorf("IsLocator(%q) must be false", bad)
		}
	}
	if !IsLocator(LocatorClaude) || !IsLocator(LocatorCodex) {
		t.Error("built-in locators must be recognised")
	}
}

// 数据目录不存在时报错而不是返回空列表（便于 registry 只注册检测到的 agent）。
func TestResolveRequiresDataDirectory(t *testing.T) {
	a := NewWithHome(t.TempDir())
	if err := a.HealthCheck(context.Background(), LocatorClaude); err == nil {
		t.Error("HealthCheck must fail when ~/.claude/projects is missing")
	}
	if len(a.DetectedInstances()) != 0 {
		t.Error("no instances should be detected on an empty home")
	}
}

// 写路径必须显式不支持，且错误可用 errors.Is 判定。DeleteSession 尤其重要：
// 磁盘数据只读，删除等于抹掉 agent 的真实转录。
func TestWritePathsAreNotSupported(t *testing.T) {
	a := NewWithHome(writeClaudeHome(t, "ses-1", sampleClaudeJSONL))
	ctx := context.Background()

	if _, err := a.CreateSession(ctx, LocatorClaude, nil); !errors.Is(err, ErrNotSupported) {
		t.Errorf("CreateSession: %v", err)
	}
	if _, err := a.SendPrompt(ctx, LocatorClaude, "ses-1", nil); !errors.Is(err, ErrNotSupported) {
		t.Errorf("SendPrompt: %v", err)
	}
	if err := a.InterruptSession(ctx, LocatorClaude, "ses-1"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("InterruptSession: %v", err)
	}
	if err := a.DeleteSession(ctx, LocatorClaude, "ses-1"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("DeleteSession: %v", err)
	}
	if _, _, err := a.SubscribeEvents(ctx, LocatorClaude, "", ""); !errors.Is(err, ErrNotSupported) {
		t.Errorf("SubscribeEvents: %v", err)
	}
	if err := a.ReplyPermission(ctx, LocatorClaude, "ses-1", "per_1", "once", ""); !errors.Is(err, ErrNotSupported) {
		t.Errorf("ReplyPermission: %v", err)
	}
	if err := a.ReplyQuestion(ctx, LocatorClaude, "ses-1", "q_1", nil); !errors.Is(err, ErrNotSupported) {
		t.Errorf("ReplyQuestion: %v", err)
	}
	if err := a.RejectQuestion(ctx, LocatorClaude, "ses-1", "q_1"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("RejectQuestion: %v", err)
	}

	// 只读历史数据的「待审批列表」是空而非错误，避免管理器轮询刷日志
	if reqs, err := a.GetAllPendingPermissionRequests(ctx, LocatorClaude, "", ""); err != nil || len(reqs) != 0 {
		t.Errorf("pending permissions should be empty without error, got %d, %v", len(reqs), err)
	}
	if reqs, err := a.GetAllPendingQuestionRequests(ctx, LocatorClaude, "", ""); err != nil || len(reqs) != 0 {
		t.Errorf("pending questions should be empty without error, got %d, %v", len(reqs), err)
	}

	// 磁盘文件仍在（没有任何写/删发生）
	if _, err := os.Stat(filepath.Join(a.readers[LocatorClaude].dataPath(), "-Users-x-proj", "ses-1.jsonl")); err != nil {
		t.Fatalf("transcript must remain untouched: %v", err)
	}
}

// 注册进 registry：instance_id → locator 走 registry 解析；workspace 留空时是
// 共享只读资源（可读、不可写），指定 workspace 时对其它租户不可见。
func TestRegisterSharedReadOnlyInstances(t *testing.T) {
	home := writeClaudeHome(t, "ses-1", sampleClaudeJSONL)
	if _, err := writeCodexHomeInto(t, home, sampleRollout); err != nil {
		t.Fatalf("prepare codex home: %v", err)
	}
	a := NewWithHome(home)

	reg := registry.NewRegistry()
	ids, err := a.Register(reg, "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("both detected agents should register, got %v", ids)
	}

	for _, id := range []string{InstanceClaude, InstanceCodex} {
		inst, err := reg.GetInstance(id)
		if err != nil {
			t.Fatalf("GetInstance(%s): %v", id, err)
		}
		if inst.Origin != "disk" || inst.Health != "healthy" {
			t.Errorf("%s: origin/health = %q/%q", id, inst.Origin, inst.Health)
		}
		// 共享实例：任意 workspace 可读
		base, err := reg.GetInstanceAPIBaseForWorkspace("ws-any", id)
		if err != nil || !IsLocator(base) {
			t.Errorf("%s must resolve to a disk locator for any workspace: %q %v", id, base, err)
		}
		// 但写解析必须拒绝（只读语义由既有安全模型兜住）
		if _, err := reg.GetWritableInstanceAPIBaseForWorkspace("ws-any", id); err == nil {
			t.Errorf("%s must not be writable for tenants", id)
		}
	}
}

func TestRegisterScopedToWorkspace(t *testing.T) {
	a := NewWithHome(writeClaudeHome(t, "ses-1", sampleClaudeJSONL))
	reg := registry.NewRegistry()
	if _, err := a.Register(reg, "ws-a"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := reg.ListInstancesForWorkspace("ws-b"); len(got) != 0 {
		t.Errorf("ws-b must not see a ws-a scoped disk instance, got %+v", got)
	}
	if _, err := reg.GetInstanceAPIBaseForWorkspace("ws-b", InstanceClaude); err == nil {
		t.Error("ws-b must not resolve a ws-a scoped disk instance")
	}
	if _, err := reg.GetInstanceAPIBaseForWorkspace("ws-a", InstanceClaude); err != nil {
		t.Errorf("ws-a must resolve its own disk instance: %v", err)
	}
}

// writeCodexHomeInto 把 codex 样本写进已有的 home（供多 agent 注册用例复用）。
func writeCodexHomeInto(t *testing.T, home, rollout string) (string, error) {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "08", "20")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rollout-2026-08-20T09-00-00-"+sampleRolloutUUID+".jsonl")
	return path, os.WriteFile(path, []byte(rollout), 0o644)
}
