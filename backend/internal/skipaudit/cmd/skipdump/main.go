// skipdump —— 导出「静态可跳过集合」（JSON）。
//
// 谁用它
// ------
// `scripts/audit-skip-runtime.mjs`（交叉门）：把本输出与 `go test -json` 的
// 运行时跳过集合做比对 —— **运行时跳了、静态看不见** ⇒ 红。
//
// 为什么单独一个程序，而不是在 Node 侧解析 Go 源码
// --------------------------------------------------
// 判据必须与 `internal/skipaudit` 的台账口径**逐字一致**（同一份 go/parser 实现）。
// 在 Node 里重写一遍扫描器 = 两把尺子量同一件事 ⇒ 迟早分叉 ⇒
// 「普查说 276、交叉门说 271」时谁也不知道该信谁。
//
// 输出
// ----
//
//	{
//	  "packages": {
//	    "./internal/email": {"tests": ["TestXxx", ...], "sites": 148, "owners": 96},
//	    ...
//	  }
//	}
//
// ⛔ 只导出**静态能跳过**的测试（保守归因：宁可多算）。
//
//	「静态说能跳、这次没跳」无害；「静态说不能跳、这次跳了」才是危险方向。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

type pkgOut struct {
	Tests  []string `json:"tests"`
	Sites  int      `json:"sites"`
	Owners int      `json:"owners"`
}

func main() {
	root, err := skipaudit.DirOf("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "定位 module root 失败: %v\n", err)
		os.Exit(2)
	}
	scan, err := skipaudit.ScanModule(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描失败: %v\n", err)
		os.Exit(2)
	}
	out := map[string]pkgOut{}
	for pkg, p := range scan.Packages {
		names := []string{}
		for _, t := range p.Tests {
			names = append(names, t.Name)
		}
		out[pkg] = pkgOut{Tests: names, Sites: p.Sites, Owners: len(p.Owners)}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"packages": out}); err != nil {
		fmt.Fprintf(os.Stderr, "输出 JSON 失败: %v\n", err)
		os.Exit(2)
	}
}
