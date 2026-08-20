package registry

import (
	"context"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// 本机适配器实例（disk 会话聚合，APIBaseURL 是 disk:// locator）不参与
// 端口扫描发现，也没有 /global/health 端点：一轮「什么都没发现」之后不能被
// 判离线，否则移动端的实例列表会把本机磁盘会话显示成掉线。
func TestLocalInstancesSurviveDiscoverySweep(t *testing.T) {
	r := NewRegistry()

	local := &model.PocketInstance{
		ID:          "disk-claude",
		APIBaseURL:  "disk://claude",
		Origin:      "disk",
		Health:      "healthy",
		WorkspaceID: "",
	}
	if err := r.RegisterInstance(local); err != nil {
		t.Fatalf("register disk instance: %v", err)
	}
	r.SetInstanceAPIBase(local.ID, local.APIBaseURL)

	remote := &model.PocketInstance{
		ID:         "http-1",
		APIBaseURL: "http://127.0.0.1:4096",
		Origin:     "discovered",
		Health:     "healthy",
	}
	if err := r.RegisterInstance(remote); err != nil {
		t.Fatalf("register http instance: %v", err)
	}
	r.SetInstanceAPIBase(remote.ID, remote.APIBaseURL)

	// 发现返回空 = LAN 上一个 OpenCode 都没扫到
	r.discoveryFunc = func(context.Context) ([]InstanceConfig, error) { return nil, nil }
	r.discoverAndUpdate(context.Background())

	got, err := r.GetInstance("disk-claude")
	if err != nil {
		t.Fatalf("GetInstance(disk-claude): %v", err)
	}
	if got.Health == "offline" {
		t.Error("disk instance must not be marked offline by network discovery")
	}

	got, err = r.GetInstance("http-1")
	if err != nil {
		t.Fatalf("GetInstance(http-1): %v", err)
	}
	if got.Health != "offline" {
		t.Errorf("an undiscovered HTTP instance should still go offline, got %q", got.Health)
	}
}

// 健康探测只对 HTTP 实例发请求；disk locator 不能被误判 unhealthy。
func TestHealthCheckSkipsLocalInstances(t *testing.T) {
	r := NewRegistry()
	local := &model.PocketInstance{ID: "disk-codex", APIBaseURL: "disk://codex", Origin: "disk", Health: "healthy"}
	if err := r.RegisterInstance(local); err != nil {
		t.Fatalf("register: %v", err)
	}
	r.SetInstanceAPIBase(local.ID, local.APIBaseURL)

	r.healthCheck(context.Background())

	got, err := r.GetInstance("disk-codex")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got.Health != "healthy" {
		t.Errorf("disk instance health must be left alone, got %q", got.Health)
	}
}

func TestIsLocalAPIBase(t *testing.T) {
	cases := map[string]bool{
		"":                       false,
		"http://127.0.0.1:4096":  false,
		"https://code.kxpms.cn":  false,
		"disk://claude":          true,
		"disk://codex":           true,
		"unix:///tmp/agent.sock": true,
	}
	for in, want := range cases {
		if got := isLocalAPIBase(in); got != want {
			t.Errorf("isLocalAPIBase(%q) = %v, want %v", in, got, want)
		}
	}
}
