package server

import (
	"os"
	"path/filepath"
	"testing"
)

// loadVersionConfig 的默认路径是相对**进程工作目录**的，不是相对可执行文件。
// 这不是学术问题：两个启动脚本（start-pocketd-pg.ps1 /
// start-pocketd-email-verify.ps1）都从仓库根启 pocketd，原来的实现因此读不到
// backend/config/version.json，而失败分支**静默回落默认值**——App 报 1.2.0，
// 日志里只有一行 Warning，没有任何东西指向「路径不对」。
//
// 所以这里钉住三件事：默认按 CWD 解析、环境变量优先于一切猜测、
// 真的找不到时才回落默认值。
func TestLoadVersionConfig_DefaultPathIsCWDRelative(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "version.json"),
		[]byte(`{"version":"9.9.9-cwd","buildNumber":77}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 环境变量必须为空，否则测的是另一条分支。
	t.Setenv("POCKET_VERSION_CONFIG_PATH", "")
	t.Chdir(root)

	v, err := (&Server{}).loadVersionConfig()
	if err != nil {
		t.Fatalf("loadVersionConfig: %v", err)
	}
	if v.Version != "9.9.9-cwd" {
		t.Fatalf("默认路径应相对 CWD 解析，实际拿到 version=%q（回落到默认值就说明它找的是别处）", v.Version)
	}
}

func TestLoadVersionConfig_EnvVarWins(t *testing.T) {
	// 从一个**有** config/version.json 的目录启动，但显式指向另一个文件：
	// 环境变量必须胜出，否则「显式设置会被默认路径悄悄盖掉」这种回归无从发现。
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "version.json"),
		[]byte(`{"version":"from-cwd","buildNumber":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(root, "elsewhere.json")
	if err := os.WriteFile(explicit, []byte(`{"version":"from-env","buildNumber":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("POCKET_VERSION_CONFIG_PATH", explicit)
	t.Chdir(root)

	v, err := (&Server{}).loadVersionConfig()
	if err != nil {
		t.Fatalf("loadVersionConfig: %v", err)
	}
	if v.Version != "from-env" {
		t.Fatalf("显式 POCKET_VERSION_CONFIG_PATH 必须优先，实际 version=%q", v.Version)
	}
}

func TestLoadVersionConfig_MissingFallsBackToDefaults(t *testing.T) {
	// 这一条钉住「静默回落」这个已知行为本身：拿不到文件时**不报错**，
	// 返回内置默认值。若将来有人改成返回 error，App 的更新检查会整个 500，
	// 那比现在更难排查——所以要么保持现状、要么连同调用方一起改，并更新本用例。
	t.Setenv("POCKET_VERSION_CONFIG_PATH", filepath.Join(t.TempDir(), "nope.json"))

	v, err := (&Server{}).loadVersionConfig()
	if err != nil {
		t.Fatalf("缺失时按现状应回落默认值而不是返回 error，实际 err=%v", err)
	}
	if v == nil || v.Version == "" {
		t.Fatal("回落时必须返回一个非空 VersionInfo")
	}
}
