package registry

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// 边端注册的实例归属注册它的 workspace：其它租户既看不到，也解析不到 API 地址。
func TestRegisteredInstanceIsScopedToItsWorkspace(t *testing.T) {
	reg := NewRegistry()

	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-a",
		WorkspaceID: "ws-a",
		APIBaseURL:  "http://10.0.0.10:4096",
	}); err != nil {
		t.Fatalf("register ws-a instance: %v", err)
	}

	if got := reg.ListInstancesForWorkspace("ws-b"); len(got) != 0 {
		t.Fatalf("ws-b must not see ws-a instance, got %#v", got)
	}
	if _, err := reg.GetInstanceAPIBaseForWorkspace("ws-b", "inst-a"); err == nil {
		t.Fatal("ws-b must not resolve the API base of a ws-a instance")
	}

	owned := reg.ListInstancesForWorkspace("ws-a")
	if len(owned) != 1 || owned[0].ID != "inst-a" {
		t.Fatalf("ws-a should see its own instance, got %#v", owned)
	}
	base, err := reg.GetInstanceAPIBaseForWorkspace("ws-a", "inst-a")
	if err != nil || base != "http://10.0.0.10:4096" {
		t.Fatalf("ws-a should resolve its own instance, got %q err=%v", base, err)
	}
}

// 静态配置/网络发现的实例没有归属，属于运维共享资源，所有 workspace 可见。
func TestOperatorProvisionedInstancesRemainShared(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadFromConfig([]InstanceConfig{
		{ID: "static-1", APIBaseURL: "http://127.0.0.1:4096"},
	}); err != nil {
		t.Fatalf("load static config: %v", err)
	}

	for _, workspaceID := range []string{"ws-a", "ws-b"} {
		got := reg.ListInstancesForWorkspace(workspaceID)
		if len(got) != 1 || got[0].ID != "static-1" {
			t.Fatalf("workspace %s should see the shared instance, got %#v", workspaceID, got)
		}
		if _, err := reg.GetInstanceAPIBaseForWorkspace(workspaceID, "static-1"); err != nil {
			t.Fatalf("workspace %s should resolve shared instance: %v", workspaceID, err)
		}
	}
}
