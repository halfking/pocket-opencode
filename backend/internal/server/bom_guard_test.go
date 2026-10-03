package server

// bom_guard_test.go — 全模块扫描：任何 .go 源文件都不许以 UTF-8 BOM 开头。
//
// ## 为什么需要这条护栏
//
// 2026-10-02 实测：`internal/server` **整包无法做覆盖率插桩**，报
//
//	internal\server\server_email_invoice.go:1:1: invalid BOM in the middle of the file
//
// 而 `go vet` 和不带 `-coverprofile` 的 `go test` **都通过** —— 也就是说
// 「这个包有没有测试」和「这个包能不能被测量」是两件事，而后者坏了没人会发现。
// 后果是 `internal/server` 在此之前**从来没有过覆盖率数字**，于是
// `delegatePipeline`（需求 6 服务端委托腿）零覆盖这种事能长期躺着（§7db）。
//
// BOM 的来源在 Windows 上很常见：PowerShell 5.1 的 `Set-Content -Encoding UTF8`
// 与 `>` 重定向都会写 BOM。这不是谁不小心，是环境默认行为，所以要机器拦。
//
// ## 判据
//
// 扫模块根下所有 .go 文件，报告**以 EF BB BF 开头**的那些。
// 不检查文件中间/结尾的 BOM —— 那是另一类问题，且 Go 对文首 BOM 是容忍的；
// 真正会炸的是覆盖率插桩重写文件后 BOM 落到非首位置。

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// findBOMFiles 递归找 root 下所有以 UTF-8 BOM 开头的 .go 文件。
// 单独抽出来是为了能被 TestFindBOMFilesDetectsBOM 在临时目录上验证 ——
// 一个从没被验证过的扫描器，等于没有护栏。
func findBOMFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// 跳过构建产物与依赖目录：里面的 .go 不受本仓库规则约束，
			// 而且 vendor/node_modules 可能有十万个文件。
			if path != root && (name == "node_modules" || name == "vendor" || name == ".git" ||
				strings.HasPrefix(name, ".") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		head := make([]byte, 3)
		n, err := f.Read(head)
		if err != nil && n == 0 {
			return nil // 空文件读不到字节，不算 BOM
		}
		if n == 3 && bytes.Equal(head, utf8BOM) {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// moduleRoot 从本包目录向上找 go.mod。
// 找不到就 fail 而不是 skip：Go module 里的包必然在模块根之下，找不到
// 说明目录结构不对，那种情况下「静默跳过」正好会放行一个已经坏掉的仓库。
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("从 %s 向上找不到 go.mod —— 护栏不能靠 skip 生效", dir)
		}
		dir = parent
	}
}

func TestNoGoFileHasUTF8BOM(t *testing.T) {
	root := moduleRoot(t)
	offenders, err := findBOMFiles(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	if len(offenders) == 0 {
		return
	}
	t.Errorf("%d 个 .go 文件以 UTF-8 BOM 开头，会让覆盖率插桩构建失败：", len(offenders))
	for _, f := range offenders {
		t.Errorf("  %s", f)
	}
	t.Errorf("修法：用 UTF-8 **无 BOM** 重存（PowerShell 5.1 的 `Set-Content -Encoding UTF8` " +
		"与 `>` 重定向都会写 BOM，用 [System.IO.File]::WriteAllText($p, $t, " +
		"New-Object System.Text.UTF8Encoding($false))）")
}

// 扫描器自身的对照：临时目录里放一个带 BOM、一个不带，必须只报出带 BOM 的那个。
// 没有这条，上面那条护栏可能因为「什么都没扫到」而永远绿。
func TestFindBOMFilesDetectsBOM(t *testing.T) {
	dir := t.TempDir()
	withBOM := filepath.Join(dir, "with_bom.go")
	clean := filepath.Join(dir, "clean.go")
	nested := filepath.Join(dir, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withBOM, append(append([]byte{}, utf8BOM...), []byte("package x\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clean, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested_bom.go"),
		append(append([]byte{}, utf8BOM...), []byte("package y\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	// 非 .go 文件即使带 BOM 也不该报 —— 判据限定在 Go 源文件。
	if err := os.WriteFile(filepath.Join(dir, "data.json"),
		append(append([]byte{}, utf8BOM...), []byte("{}")...), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findBOMFiles(dir)
	if err != nil {
		t.Fatalf("findBOMFiles: %v", err)
	}
	want := map[string]bool{"with_bom.go": false, filepath.Join("sub", "nested_bom.go"): false}
	for _, g := range got {
		if _, ok := want[g]; !ok {
			t.Errorf("unexpected offender %q", g)
			continue
		}
		want[g] = true
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("missed offender %q (got %v)", f, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d offenders, want %d: %v", len(got), len(want), got)
	}
}
