// email-fetcher — §7cd 的**反向对照**：证明 socket 依赖没有混进 wasm 产物。
//
// ## 为什么需要它
//
// §7cd 用「email 纯逻辑产物比 rules 版大 3.84 MB」推断「未被调用的 socket 代码
// 被链接器剔掉了」。这是**间接证据** —— 3.84 MB 看起来合理，但合理不等于证明。
//
// 这个包只做一件事：调用 `email.NewFetcher` —— 它**构造** fetcher 但不建连
// （真正的 socket 在 imapDialWithTimeout 里），所以这个探针能跑通，
// 但会把 fetcher.go 及其**依赖闭包**（net / crypto/tls / go-imap / go-sasl…）
// 全部拉进编译单元。
//
// 两种结果的含义：
//   · 体积暴涨        → 死代码**没有**被剔除，§7cd 的结论要推翻
//   · 体积变化很小    → 剔除成立，且「搬纯逻辑」的包体账是可信的
//
// 用法由 scripts/wasmprobe-run.mjs 统一驱动，不要手工编。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func main() {
	// NewFetcher 只赋值字段，不做任何网络动作（store/crypto 传 nil 是安全的，
	// 因为构造体里没有解引用）。它的存在足以让 fetcher.go 及其依赖进入编译单元。
	f := email.NewFetcher(nil, nil)

	out := map[string]any{
		"probe":      "email-fetcher",
		"constructed": f != nil,
		// 顺带确认一个不触 socket 的导出函数仍然可用，
		// 以便和 wasmprobe/email 的输出做交叉参照。
		"isPortPlainFallback": email.NormalizeCategory(" 工作 ") == "personal",
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
