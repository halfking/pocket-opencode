// wasmprobe — 需求 6 A 路线的可行性探针（不是产品代码）。
//
// ## 它回答一个问题
//
// §7cb 证明 Go 编到 js/wasm 后**所有 socket syscall 直接 ENOSYS**，所以
// IMAP/POP3 不可能在 WebView 里跑。但那结论只否掉了「有 socket 的那一面」。
//
// 这一半没验证：**纯计算的那部分能不能真的编成 wasm 并在 JS 宿主里跑出正确结果**。
// 「`go build` exit 0」证明不了这一点 —— 上一轮实测 `internal/email`（含 socket）
// 和 `rules`（零 socket）**都** exit 0，判据毫无区分度。
// 唯一有区分度的判据是**编出可执行产物、在宿主里真跑一遍、比对结果**。
//
// ## 为什么选 rules 作为样本
//
// `internal/email/rules` 只有 293 行，import 只有
// regexp/strings/sort/json/time/bytes/fmt —— **零 net 依赖**（实测）。
// 它同时是需求 2（垃圾清理）的规则引擎，搬对了就直接少一块 Java 重写。
//
// ## 用法
//
//	cd backend
//	GOOS=js GOARCH=wasm go build -o rulesprobe.wasm ./cmd/wasmprobe/
//	node -e "require('C:/tools/go/lib/wasm/wasm_exec_node.js');" # 由 run.js 包装
//	node ../scripts/wasmprobe-run.mjs
//
// 输出是 JSON，便于与 windows/amd64 下同源逻辑逐字段比对。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email/rules"
)

// cases 是固定的一组输入：必须覆盖每种 actionSpec 形态，
// 否则「跑通了」也只说明一部分逻辑对。
var cases = []struct {
	name string
	raw  string
	in   rules.EmailInput
}{
	{
		name: "sender-blacklist",
		raw:  `{"rules":[{"type":"sender-blacklist","pattern":"spam@bad.com","actions":["archive"]}]}`,
		in:   rules.EmailInput{From: "spam@bad.com", Subject: "hi", Importance: "low"},
	},
	{
		name: "subject-keyword",
		raw:  `{"rules":[{"type":"subject-keyword","pattern":"发票","actions":["mark-important"]}]}`,
		in:   rules.EmailInput{From: "a@x.com", Subject: "增值税发票已开具", Importance: "normal"},
	},
	{
		name: "label-category",
		raw:  `{"rules":[{"type":"sender-whitelist","pattern":"@x.com","actions":[{"name":"label-category","category":"work"}]}]}`,
		in:   rules.EmailInput{From: "alice@x.com", Subject: "s", Importance: "high"},
	},
	{
		name: "route-folder",
		raw:  `{"rules":[{"type":"domain-match","pattern":"qq.com","actions":[{"name":"route-folder","folder":"QQ"}]}]}`,
		in:   rules.EmailInput{From: "a@qq.com", Subject: "s", Importance: "normal"},
	},
	{
		name: "importance-min",
		raw:  `{"rules":[{"type":"importance-min","pattern":"high","actions":["mark-important"]}]}`,
		in:   rules.EmailInput{From: "a@x.com", Subject: "s", Importance: "high"},
	},
	{
		name: "legacy-blacklist",
		raw:  `{"blacklist":["spam@bad.com"]}`,
		in:   rules.EmailInput{From: "spam@bad.com", Subject: "s", Importance: "normal"},
	},
	{
		name: "no-match",
		raw:  `{"rules":[{"type":"sender-blacklist","pattern":"spam@bad.com","actions":["archive"]}]}`,
		in:   rules.EmailInput{From: "ok@good.com", Subject: "s", Importance: "normal"},
	},
}

type probeResult struct {
	Name    string               `json:"name"`
	Actions []rules.ActionResult `json:"actions"`
	Err     string               `json:"err,omitempty"`
	Rank    int                  `json:"importance_rank"`
}

func main() {
	// time 会被 JSON 序列化碰到：ReceivedAt 零值必须能被 marshal。
	// 带上**固定**时间是为了保证 native 与 wasm 的输出逐字节可比 ——
	// 一旦这里用 time.Now()，两次运行必然不同，比对就失去意义。
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(probeRules(base)); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}

func probeRules(base time.Time) []probeResult {
	out := make([]probeResult, 0, len(cases))
	for _, c := range cases {
		in := c.in
		in.ReceivedAt = base
		rs, err := rules.ParseRules(c.raw)
		if err != nil {
			out = append(out, probeResult{Name: c.name, Err: err.Error()})
			continue
		}
		out = append(out, probeResult{
			Name:    c.name,
			Actions: rules.Evaluate(rs, in),
			Rank:    rules.ImportanceRank(c.in.Importance),
		})
	}
	out = append(out, probeResult{
		Name:    "supported-actions",
		Actions: toActions(rules.SupportedActions()),
		Rank:    rules.ImportanceRank("normal"),
	})
	return out
}


func toActions(names []string) []rules.ActionResult {
	a := make([]rules.ActionResult, 0, len(names))
	for _, n := range names {
		a = append(a, rules.ActionResult{Action: rules.Action(n)})
	}
	return a
}
