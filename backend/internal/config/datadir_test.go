package config

// datadir_test.go — 数据目录解析（ResolveDataDir）的回归测试。
//
// 背景见 ResolveDataDir 的注释：`dataDir` 原本只有 `filepath.Dir(cfg.DBPath)`
// 一条路，而 DBPath 在 Postgres 迁移后已不再真的开 SQLite 库、默认值又是相对
// 的 `./data/pocket.sqlite`。于是**数据目录取决于从哪个目录启动二进制**，这个
// 根因咬过两次且症状毫无关联（master key 换目录 → 全部账户解不开；发票写 A
// 目录、按 B 目录读 → 单张 404 / A4 导出 400）。
//
// 这里钉三件事：
//  1. 返回值**一定是绝对路径** —— 绝对路径在进程运行期间不受 CWD 影响；
//  2. POCKET_DATA_DIR 显式指定时**优先于** DBPath；
//  3. 两者都空时报错，而不是悄悄落到某个隐式目录。
//
// 反面教材写在 TestResolveDataDir_NotAffectedByCWD 里：只断言「不同 CWD 下
// 结果不同」是不够的 —— 那样反而把缺陷固化成了期望。真正的契约是
// **给了绝对路径的 POCKET_DATA_DIR 后，结果与 CWD 完全无关**。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDataDir_ExplicitOverrideWins(t *testing.T) {
	want := filepath.Join(t.TempDir(), "explicit-data")
	// DBPath 故意给一个完全不同的相对值：override 必须压过它。
	got, err := ResolveDataDir("./data/pocket.sqlite", want)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if got != want {
		t.Fatalf("dataDir = %q, want %q —— POCKET_DATA_DIR 是显式指定，必须优先", got, want)
	}
}

func TestResolveDataDir_FallsBackToDBPath(t *testing.T) {
	got, err := ResolveDataDir("./data/pocket.sqlite", "")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("dataDir = %q, want 绝对路径 —— 相对路径会随启动目录漂移", got)
	}
	if filepath.Base(got) != "data" {
		t.Fatalf("dataDir = %q, want 以 data 结尾（Dir(\"./data/pocket.sqlite\")）", got)
	}
}

func TestResolveDataDir_AlwaysAbsolute(t *testing.T) {
	// 覆盖各种形态：空 override + 相对/绝对 DBPath、有/无尾斜杠。
	cases := []struct{ name, dbPath, override string }{
		{"relative dbpath", "./data/pocket.sqlite", ""},
		{"bare relative", "data/pocket.sqlite", ""},
		{"absolute dbpath", filepath.Join(t.TempDir(), "x", "pocket.sqlite"), ""},
		{"relative override", "", "somewhere/data"},
		{"dot override", "", "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveDataDir(c.dbPath, c.override)
			if err != nil {
				t.Fatalf("ResolveDataDir(%q, %q): %v", c.dbPath, c.override, err)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("dataDir = %q, want 绝对路径", got)
			}
		})
	}
}

func TestResolveDataDir_NotAffectedByCWD(t *testing.T) {
	// 核心契约：给了**绝对**的 POCKET_DATA_DIR 之后，从哪个目录启动都落到
	// 同一个地方。这正是原来那两次故障的解药。
	want := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	seen := map[string]bool{}
	for _, dir := range []string{t.TempDir(), t.TempDir(), t.TempDir()} {
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir %s: %v", dir, err)
		}
		got, err := ResolveDataDir("./data/pocket.sqlite", want)
		if err != nil {
			t.Fatalf("ResolveDataDir: %v", err)
		}
		if got != want {
			t.Fatalf("CWD=%s 时 dataDir = %q, want %q —— 显式指定后不该受 CWD 影响", dir, got, want)
		}
		seen[got] = true
	}
	if len(seen) != 1 {
		t.Fatalf("不同 CWD 得到了 %d 个不同的 dataDir: %v", len(seen), seen)
	}
}

// 保留这段是为了说明「相对 DBPath 仍然依赖 CWD」这个**已知且可接受的**行为：
// 没有显式指定 POCKET_DATA_DIR 时，我们无法凭空知道调用方想要哪个目录，
// 所以保持旧语义（Dir(DBPath)，但转成绝对路径）。测试把这条行为写死，
// 免得以后有人误以为它已经与 CWD 无关。
func TestResolveDataDir_RelativeDBPathStillDependsOnCWD(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	dirA, dirB := t.TempDir(), t.TempDir()
	var gotA, gotB string
	if err := os.Chdir(dirA); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if gotA, err = ResolveDataDir("./data/pocket.sqlite", ""); err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if err := os.Chdir(dirB); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if gotB, err = ResolveDataDir("./data/pocket.sqlite", ""); err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if gotA == gotB {
		t.Skip("本平台上 CWD 未生效，跳过「相对路径依赖 CWD」的行为记录")
	}
	for _, got := range []string{gotA, gotB} {
		if !filepath.IsAbs(got) {
			t.Fatalf("dataDir = %q, want 绝对路径（即使它依赖 CWD 也要是绝对的）", got)
		}
	}
	t.Logf("未显式指定时依赖 CWD：%s vs %s —— 这就是必须显式设 POCKET_DATA_DIR 的原因", gotA, gotB)
}

func TestResolveDataDir_TrimsWhitespace(t *testing.T) {
	// 环境变量很容易带上首尾空格（env 文件、脚本拼接）。空白值应视为未设置，
	// 否则会静默把 dataDir 指到一个名为 " " 的目录上。
	got, err := ResolveDataDir("./data/pocket.sqlite", "   ")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if strings.TrimSpace(got) != got {
		t.Fatalf("dataDir = %q, want 已去空白", got)
	}
	if filepath.Base(got) != "data" {
		t.Fatalf("dataDir = %q, want 退回 Dir(DBPath)（空白 override 视为未设置）", got)
	}
}

func TestResolveDataDir_ErrorsWhenNothingConfigured(t *testing.T) {
	if _, err := ResolveDataDir("", ""); err == nil {
		t.Fatal("两者都空时必须报错，不能悄悄落到某个隐式目录")
	}
}
