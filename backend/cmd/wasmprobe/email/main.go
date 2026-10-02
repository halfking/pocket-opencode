// email — 需求 6 A 路线 wasm 探针的 **email 变体**。
//
// ## 为什么必须独立成一个包（而不是给 wasmprobe 加 -mode）
//
// 第一版把两种模式塞进同一个 `cmd/wasmprobe/main.go`，用 `-mode` 切换。
// **这个设计量不出体积**，实测两个 mode 编出来都是 8.78 MB、净增 0.00 MB ——
// 因为 `main.go` 顶层同时 import 了 `internal/email` 和 `internal/email/rules`，
// 而 `probeEmail()` 在 switch 分支里可达，所以**无论 `-mode` 传什么，
// 两个包的代码都进产物**。两个 mode 是同一个二进制，对比毫无意义。
//
// 教训和「判据要能失败」是同一条：**当两个样本的差值恰好是 0.00 MB 时，
// 先怀疑测量装置，而不是急着得出「死代码被剔除了」这种漂亮结论。**
//
// 拆成独立 main 之后，链接器才能看到「这个二进制的可达集合到底是什么」。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

type emailProbe struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Want  bool   `json:"want,omitempty"`
	Got   bool   `json:"got"`
}

func main() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(probe()); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}

// probeEmail 调用 internal/email 里**不触 socket** 的导出纯函数。
//
// 挑这些的理由：它们分别对应需求 1/2/3/4/7 的判定逻辑，且都是
// 「输入 → 输出」的纯计算，正是在 wasm 里应该原样工作的那类。
//
// 刻意**不含** ExportInvoiceGrid（需求 5 的 A4 网格）：它写文件，
// js/wasm 没有文件系统，这条路径在设备端必须换成别的实现。
// 这里把边界显式写下来，免得方案里误以为「纯逻辑全都能搬」。
//
// 全部使用**固定**时间戳：一旦引入 time.Now()，两次运行必然不同，
// 逐字节比对就失去意义。
func probe() []emailProbe {
	const since, until int64 = 1780000000, 1780600000

	return []emailProbe{
		// 需求 7：分类归一化
		{Name: "NormalizeCategory:工作", Value: email.NormalizeCategory(" 工作 ")},
		{Name: "NormalizeCategory:空", Value: email.NormalizeCategory("")},
		{Name: "NeedsClassification:空", Got: email.NeedsClassification(""), Want: true},
		{Name: "NeedsClassification:已分类", Got: email.NeedsClassification("财务"), Want: false},
		// 需求 4：重要性归一化（§7af 修过 importance 未归一化）
		{Name: "NormalizeImportance:HIGH", Value: email.NormalizeImportance("HIGH")},
		{Name: "NormalizeImportance:unknown", Value: email.NormalizeImportance("urgent")},
		{Name: "NormalizeImportance:empty", Value: email.NormalizeImportance("")},
		// 需求 1：拉取后是否继续处理。
		// newEmails 被**显式丢弃**（classify_run.go:62 的 `_ = newEmails`），
		// 只看 syncedAccounts > 0。这是**有意设计**且已有测试钉住
		//（classify_run_test.go:22「synced account must process even with 0 new」）：
		// 0 新邮件时仍要跑一遍，好让上一轮分类失败的邮件被重试。
		// 函数名读起来像「有新的才处理」，容易误判成 bug。
		{Name: "ShouldProcessAfterFetch:2acct0new", Got: email.ShouldProcessAfterFetch(2, 0), Want: true},
		{Name: "ShouldProcessAfterFetch:0acct3new", Got: email.ShouldProcessAfterFetch(0, 3), Want: false},
		// 需求 2：垃圾匹配
		{Name: "MatchCleanup:命中", Got: email.MatchCleanup(
			email.Email{Subject: "限时优惠", FromAddress: "ad@shop.com", Date: since + 86400},
			email.CleanupFilter{Subject: "优惠", Since: since, Until: until},
		), Want: true},
		{Name: "MatchCleanup:不命中", Got: email.MatchCleanup(
			email.Email{Subject: "发票已开具", FromAddress: "billing@x.com", Date: since + 86400},
			email.CleanupFilter{Subject: "优惠", Since: since, Until: until},
		), Want: false},
		{Name: "MatchCleanup:窗口外", Got: email.MatchCleanup(
			email.Email{Subject: "限时优惠", Date: since - 99999},
			email.CleanupFilter{Subject: "优惠", Since: since, Until: until},
		), Want: false},
		// 需求 2：已移走的不能再删（防重复 UID）
		{Name: "SelectDeletable:跳过已移动", Value: fmt.Sprint(email.SelectDeletable(
			[]email.CleanupItem{
				{ID: "a", AccountID: "acct-1", UID: 10},
				{ID: "b", AccountID: "acct-1", UID: 11},
				{ID: "c", AccountID: "acct-2", UID: 12},
			},
			map[string][]int64{"acct-1": {10}},
		))},
		// 需求 3：发票日期解析 + 内容哈希
		{Name: "ParseInvoiceDate:中文", Value: email.ParseInvoiceDate("开票日期：2026年09月24日")},
		{Name: "ParseInvoiceDate:斜杠", Value: email.ParseInvoiceDate("Invoice date 2026/09/24")},
		{Name: "ParseInvoiceDate:无", Value: email.ParseInvoiceDate("no date here")},
		{Name: "ParseInvoiceDateFromBytes", Value: email.ParseInvoiceDateFromBytes([]byte("date 2026-09-24"))},
		{Name: "InvoiceContentHash:稳定", Value: email.InvoiceContentHash([]byte("%PDF-1.7 x"))},
		// 需求 3：分类写回构造
		{Name: "BuildClassifyWrites", Value: fmt.Sprint(email.BuildClassifyWrites([]email.RawClassifyResult{
			{EmailID: "e1", Category: "", Importance: "high"},
			{EmailID: "e2", Category: "财务", Importance: ""},
		}))},
	}
}
