package repohygiene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCorruptGoMod 拦住「0 字节 / 缺 module 声明的 go.mod」。
//
// ## 为什么这道卡口存在（真实事故，不是假想）
//
// 2026-10-02 审计轮在仓库根发现一个 **0 字节的 `go.mod`**（某个被强杀/重定向的
// 命令留下的产物）。它的杀伤力不在于「多了一个文件」，而在于：
//
//	go env GOMOD            → C:\workspace\openpocket\go.mod
//	go list ./...           → go: error reading go.mod: missing module declaration
//
// 只要它躺在根目录，**任何从仓库根执行的 go 命令都会硬失败**——包括
// `go build ./...`、`go vet ./...`、`go env`。而后端 CI 与本地开发都在
// `backend/` 下跑（那里有真正的 `backend/go.mod`），所以**后端测试全绿、
// 没有任何一道现有卡口会红**，只有「在根目录敲一条 go 命令」的人会撞上。
//
// 这正是本包 doc.go 说的那类事故的翻版：缺陷存在、CI 绿、无人察觉。
//
// ## 为什么必须扫**未跟踪**文件（最容易写错的一点）
//
// 旁边那道 `TestNoCommittedSecrets` 用的是 `git ls-files`，只枚举**受跟踪**文件。
// 照抄那个做法会让本卡口**永远绿**：出事的 go.mod 恰恰是命令产物，是**未跟踪**的
// 本地垃圾，`git ls-files` 根本看不到它。
//
// 判据的分量取决于扫不扫得到，所以这里刻意走文件系统 walk（含未跟踪），
// 并在下面用 `TestGoModModuleDirectiveScannerHandlesTheThreeWaysToBeCorrupt`
// 锁住判据本身的语义。
func TestNoCorruptGoMod(t *testing.T) {
	root := repoRoot(t)

	// 仓库里真实存在的模块（本轮实测）：backend / services/zagent-gateway /
	// opencode-manager，以及 .scratch/genpdf。根目录**不在**其中——根目录一旦
	// 出现 go.mod，就会被 `go env GOMOD` 优先选中并遮蔽所有人的直觉。
	var checked int
	var broken []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" ||
				strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() != "go.mod" {
			return nil
		}
		checked++
		ok, why := goModDeclaresModule(path)
		if !ok {
			rel, _ := filepath.Rel(root, path)
			broken = append(broken, filepath.ToSlash(rel)+" —— "+why)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}

	// 少扫到文件 = 判据失明。宁可误报也不能不扫：这道卡口的价值完全取决于覆盖面。
	//
	// 下限定为 3：本轮实测这 3 个是仓库结构的一部分（backend 是后端模块、
	// services/zagent-gateway 是网关、opencode-manager 是管理器），删掉任意一个
	// 都是架构级变更，届时应当同步改这条数字。另有
	// backend/third_party/identity-go 与 .scratch/genpdf（后者被跳过），
	// 它们是**可增可减**的，所以不能拿来当下限——否则删一个第三方模块就误报。
	if checked < 3 {
		t.Errorf("只扫描到 %d 个 go.mod，walk 范围可能不对（本护栏会因此失明）。\n"+
			"  仓库里至少有 backend / services/zagent-gateway / opencode-manager 三个模块。", checked)
	}
	if len(broken) > 0 {
		t.Errorf("发现 %d 个无法解析的 go.mod：\n  %s\n\n"+
			"  后果不是「多一个文件」：只要它在模块搜索路径的根上，"+
			"`go build ./...` / `go list ./...` / `go env` 都会以\n"+
			"  「missing module declaration」硬失败，而后端 CI 在 backend/ 下跑、依旧全绿。\n"+
			"  这类文件通常是命令被强杀时留下的 0 字节产物，直接删掉即可"+
			"（不要试图补 module 行——根目录本来就不该有模块）。",
			len(broken), strings.Join(broken, "\n  "))
	}
	t.Logf("已扫描 %d 个 go.mod（含量身定制的判据，含未跟踪文件）", checked)
}

// goModDeclaresModule 判一个 go.mod 是否声明了 module。
//
// fail-closed：读不出来即视为损坏。理由与 secrets 卡口一致——
// 「扫不到就当通过」比「误报」危险得多。
func goModDeclaresModule(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "读不出来（按损坏处理）：" + err.Error()
	}
	if len(data) == 0 {
		return false, "0 字节：命令被强杀/重定向留下的产物"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// 跳过 BOM 残留与整行注释。
		line = strings.TrimPrefix(line, "\ufeff")
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "module ") || line == "module" {
			return true, ""
		}
		// module 必须是 go.mod 的第一条指令。提前出现别的指令说明文件被截断。
		return false, "第一条指令不是 module（文件疑似被截断或写坏）"
	}
	return false, "没有任何指令：内容不是合法 go.mod"
}

// TestGoModDeclaresModuleHandlesTheThreeWaysToBeCorrupt 锁住判据语义。
//
// 为什么必须单独锁：判据一坏，两种失败方向都**不会**让上面的护栏变红。
//   - 判据太宽（把损坏当合法）→ 护栏永远绿，事故复现。
//   - 判据太窄（把合法当损坏）→ 误报，真 go.mod 全被点名，护栏被忽略。
//
// 两种都只能靠**直接对输入断言**发现，光看护栏整体是绿/红都不够。
//
// 负控：把 cases 里任意一条 want 的实现改坏（例如 len(data)==0 时返回 true），
// 本测试必须转红。
func TestGoModDeclaresModuleHandlesTheThreeWaysToBeCorrupt(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"0 字节（真实事故形态）", "", false},
		{"只有换行", "\n\n", false},
		{"有内容但没写 module", "go 1.24\n", false},
		{"module 被截断成半个词", "modul\ngo 1.24\n", false},
		{"正常模块", "module github.com/halfking/pocket-opencode/backend\n\ngo 1.24\n", true},
		{"注释在前、module 在后（合法）", "// 由脚本生成\nmodule example.com/x\n\ngo 1.24\n", true},
		{"带 BOM 也不能漏判（合法）", "\ufeffmodule example.com/x\n\ngo 1.24\n", true},
	}

	for _, c := range cases {
		p := filepath.Join(dir, strings.ReplaceAll(c.name, "/", "_")+".mod")
		if err := os.WriteFile(p, []byte(c.content), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
		got, why := goModDeclaresModule(p)
		if got != c.want {
			t.Errorf("%s：goModDeclaresModule = %v（%s），want %v。\n"+
				"  输入=%q\n"+
				"  判据太宽会让护栏永远绿（事故复现且无人察觉）；"+
				"判据太窄会误报真模块（护栏被忽略）。",
				c.name, got, why, c.want, c.content)
		}
	}
}
