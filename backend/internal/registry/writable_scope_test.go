package registry

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

func TestWritableInstanceRequiresWorkspaceOwnership(t *testing.T) {
	reg := NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID: "owned", WorkspaceID: "ws-a", APIBaseURL: "http://owned.example",
	}); err != nil {
		t.Fatalf("register owned: %v", err)
	}
	if err := reg.LoadFromConfig([]InstanceConfig{{ID: "shared", APIBaseURL: "http://shared.example"}}); err != nil {
		t.Fatalf("register shared: %v", err)
	}

	if got, err := reg.GetWritableInstanceAPIBaseForWorkspace("ws-a", "owned"); err != nil || got != "http://owned.example" {
		t.Fatalf("owner write resolver = %q, %v", got, err)
	}
	for _, tc := range []struct {
		workspace string
		instance  string
	}{
		{"", "owned"},
		{"ws-b", "owned"},
		{"ws-a", "shared"},
		{"ws-b", "shared"},
	} {
		if _, err := reg.GetWritableInstanceAPIBaseForWorkspace(tc.workspace, tc.instance); err == nil {
			t.Errorf("workspace %q must not resolve %q for writing", tc.workspace, tc.instance)
		}
	}
}
