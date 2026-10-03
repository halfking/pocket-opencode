// marshal — 需求 6 A 路线的**最后一个未知数**：wasm 堆 ↔ 宿主数据结构的传递成本。
//
// ## 状态：native 与三种 wasm 形态**都已跑通**（2026-10-02 更新）
//
// 见 docs/handoff 的 §7ce 与 §7cv。
//
//	· `go run ./cmd/wasmprobe/marshal/`              -> 正常输出 JSON
//	· `go run ./cmd/wasmprobe/marshal/ -emit-corpus` -> 正常输出 120 封语料
//	· `GOOS=js GOARCH=wasm go build`                -> 正常产出 wasm
//	· `node scripts/marshal-probe.mjs`              -> 四条路径数字 + 一致性校验
//
// §7ce 记的那个「driver 退出码 0、stderr 全空、stdout 0 字节」已定位并修掉，
// 根因是 wasm 侧的 `js.Global().Get("Array").New(arr)`（JS 语义坑：
// `new Array(x)` 在 x 非数字时得到**长度为 1、元素就是 x 本身**的数组），
// 不是 driver 的 stdout 捕获问题。修法与两条负控见 §7cv。
//
// **注意 `go vet` 在 host 上不编译 `//go:build js` 的文件** —— 改完
// main_wasm.go 必须 `GOOS=js GOARCH=wasm go vet` 才算验过。本轮就被
// 「host vet 绿、wasm build 红（undefined: arr）」坑过一次。
//
// 保留这套代码的价值：native 基线与三种 wasm 形态的 wasm 侧实现都已就位，
// 驱动一旦修好即可直接产出数据；而且 §7ce 里那些排查结论本身是可复用的知识。
//
// ## 为什么值得测（问题本身仍然有效）
//
// §7cb/§7cc/§7cd 证明了「纯逻辑编到 wasm 后与原生行为完全一致」，
// 但那只覆盖**计算**。设备端 SQLite 在 JS 侧（仓库里已有
// `frontend/public/assets/sql-wasm.wasm` = sql.js 的先例），wasm 只做纯函数，
// 于是**每封邮件都要穿过 wasm 边界**。
//
// 关键在于 AI 分类的 HTTP **不能**在 wasm 里发（js/wasm 没有 fetch，net 也不可用），
// 必须由 JS 侧发起，于是真实架构是
//
//	JS(sql.js 取行) → wasm(判是否要分类) → JS(fetch LLM) → wasm(解析结果)
//
// 每封邮件**至少两次跨界**。bulk 形态（一次 JSON 串过边界）在这个架构里
// 根本不会出现 —— 它假设数据一次性进出，而中途要调外部服务时这个假设就破了。
// 拿 bulk 的数字论证 A 路线会**系统性低估**边界成本。
//
// ## 三种形态共用 classifyAll，保证差值纯粹来自边界
//
//	bulk    整个数组一个 JSON 字符串过边界   一次跨界
//	struct  逐字段 syscall/js 属性访问       N×M 次跨界
//	permail 每封一次 JSON 往返               2N 次跨界  ← 真实模型
//
// 计算部分调用 email 包的真实函数（§7cd 已验证它们在 wasm 下与原生一致），
// 否则测出来的数字对 A 路线无效。
package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// corpusSize 对齐生产现状：opencode_pocket.emails 实测 120 行（2026-10-02）。
// 用真实规模而不是「测起来快」的规模，否则边界成本会被低估 ——
// 跨界次数与数据量成正比。
const corpusSize = 120

// mailIn 是一封邮件进入分类所需的全部字段。
// 刻意只放**分类真正用到的**：真实 SQLite 查询也是投影，不会把
// body_path / attachments 之类的大字段拉出来。
type mailIn struct {
	From       string `json:"from"`
	Subject    string `json:"subject"`
	Snippet    string `json:"snippet"`
	Importance string `json:"importance"`
	Category   string `json:"category"`
}

// mailOut 是分类结果。
type mailOut struct {
	From       string `json:"from"`
	Category   string `json:"category"`
	Importance string `json:"importance"`
}

// corpus 造一批有区分度的邮件：空/脏/正常/外文各占一些，
// 避免「输入太整齐让分支全中」把计算时间压得 unrealistically 低。
func corpus(n int) []mailIn {
	subjects := []string{
		"增值税专用发票已开具", "限时优惠 快来抢购", "Your invoice is ready",
		"", "季度对账单（见附件）", "【广告】新人专享 5 折",
		"发票重发 Invoice Reissued", "会议纪要：请确认",
	}
	snippets := []string{
		"发票号码 12345678 金额 128.00 开票日期 2026年09月24日",
		"点击立即领取优惠券 http://shop.example.com/promo",
		"Invoice No. INV-2026-0001 Total: 1,280.00 CNY",
		"", "详见附件，对账周期 2026-08",
		"限时秒杀，全场 1 折起 http://a.example.com/x",
		"重开发票，原发票作废 Invoice reissued",
		"各位好，会议纪要如下，请确认",
	}
	out := make([]mailIn, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, mailIn{
			From:       fmt.Sprintf("sender%d@%s", i%17, []string{"x.com", "qq.com", "163.com", "corp.cn"}[i%4]),
			Subject:    subjects[i%len(subjects)],
			Snippet:    snippets[i%len(snippets)],
			Importance: []string{"", "low", "normal", "medium", "high"}[i%5],
			Category:   []string{"", "marketing", "finance"}[i%3],
		})
	}
	return out
}

// classifyAll 是**计算**部分，三条路径（native / wasm-bulk / wasm-struct）共用，
// 保证差值纯粹来自边界而不是业务逻辑。
//
// 用的是 email 包里真实存在的函数：NormalizeCategory（§7af 修过归一化）、
// NormalizeImportance、NeedsClassification。没有自己造同类实现 —— 那样
// 测出来的边界成本虽然没错，但会让人误以为计算部分也是现成的。
func classifyAll(in []mailIn) []mailOut {
	out := make([]mailOut, 0, len(in))
	for i := range in {
		m := &in[i]
		cat := email.NormalizeCategory(m.Category)
		if email.NeedsClassification(m.Category) {
			// 真实链路这里会调 kxmemory 的 AI 分类；本探针**不调**，
			// 因为那是一次网络往返，会把边界成本彻底淹没。
			// 因此这里只跑本地能确定的那一半，并在结果里标明。
			cat = email.NormalizeCategory(m.Snippet)
		}
		out = append(out, mailOut{
			From:       m.From,
			Category:   cat,
			Importance: email.NormalizeImportance(m.Importance),
		})
	}
	return out
}

// report 输出：耗时 + 结果摘要。摘要用 digest 而不是全量，
// 免得 120 条记录把时间数字淹没；同时 digest 也能用来校验三条路径结果一致。
func report(label string, n int, total time.Duration, in []mailIn, out []mailOut) {
	enc := json.NewEncoder(mustStdout())
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"label":         label,
		"count":         n,
		"elapsed_ns":    total.Nanoseconds(),
		"ns_per_mail":   total.Nanoseconds() / int64(maxInt(n, 1)),
		"result_digest": digest(out),
		"input_bytes":   approxJSONSize(in),
	})
}

// digest 折叠结果集：按 category / importance 计数。
func digest(out []mailOut) map[string]map[string]int {
	cat := map[string]int{}
	imp := map[string]int{}
	for _, o := range out {
		cat[o.Category]++
		imp[o.Importance]++
	}
	return map[string]map[string]int{"category": cat, "importance": imp}
}

// approxJSONSize 量出这批输入序列化后的字节数 —— 「边界成本」的分母得有个数。
func approxJSONSize(in []mailIn) int {
	b, err := json.Marshal(in)
	if err != nil {
		return -1
	}
	return len(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
