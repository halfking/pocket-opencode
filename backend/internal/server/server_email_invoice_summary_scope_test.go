package server

// server_email_invoice_summary_scope_test.go — 汇总接口的**合计口径**只有一处。
//
// ## 这组用例守的是什么
//
// 发票页要交给财务的「合计 X 元」和「已下载 N 张」必须指的是**同一批**发票，
// 而「这一张算不算进合计」这条规则在 `internal/email/ledger.go` 里明写
// 只有 `email.InvoiceCountsTowardTotal` 一处（"为什么必须只有一处"）。
//
// 2026-10-02 真实库上出过一次 3,500 vs 61,500（17.6 倍）：同一份数据，
// `LedgerRows` 走了判据、`InvoiceListStats` 没过，页面上摆着后者。
// 修完那三处之后，`handleEmailInvoiceSummary` 里**仍然留着一份手写的**
// `case "downloaded","filed": if inv.FilePath != ""` —— 今天等价、没有错账，
// 但同一个病换了个位置潜伏着。
//
// ## 判据要指向**行为**，不是"函数被调用过"
//
// 如果新代码写成 `if email.InvoiceCountsTowardTotal(inv) {`，那么
// 「用例调用的就是那个函数」是同义反复、恒真。所以这组用例断言的是
// **每个 (status, FilePath) 组合下应该得到的具体数字**——这些期望值与实现
// 写法无关：谁把条件改回内联的、或者只判 status 不判 FilePath、
// 或者反过来两个都判，都会转红。
//
// ## 负控（实测）
//
//  - 把 `email.InvoiceCountsTowardTotal(inv)` 换成 `inv.Status != ""`
//    → `TestSummarizeInvoiceRows_DownloadedCountMatchesCountedSet` 转红
//    （status=downloaded 但 FilePath 为空那张会被误计入）
//  - 把 `out.Downloaded++` 挪到 if 外面 → 同用例转红（两者不再同长）
//  - 把 pending/failed 计数整段删掉 → `TestSummarizeInvoiceRows_ClassCountsAreExhaustive` 转红

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func inv(id, status, filePath string, amount float64) email.Invoice {
	return email.Invoice{ID: id, Status: status, FilePath: filePath, Amount: amount, Currency: "CNY"}
}

// 真值表：四类身份 × 有无落盘文件。
// wantDownloaded / wantCounted 的依据是「拿到凭证才算数」这条业务口径，
// 不是某个函数长什么样。
func TestSummarizeInvoiceRows_ScopeTruthTable(t *testing.T) {
	cases := []struct {
		name          string
		in            email.Invoice
		wantCounted   bool
		wantPending   int
		wantFailed    int
		wantAllInRows bool
	}{
		{"downloaded with file", inv("a", "downloaded", "a.pdf", 5.61), true, 0, 0, true},
		{"downloaded without file", inv("b", "downloaded", "", 7.00), false, 0, 0, true},
		{"filed with file", inv("c", "filed", "c.pdf", 19.00), true, 0, 0, true},
		{"filed without file", inv("d", "filed", "", 3.00), false, 0, 0, true},
		{"pending", inv("e", "pending", "", 1.00), false, 1, 0, true},
		{"new", inv("f", "new", "", 2.00), false, 1, 0, true},
		// failed 却带着金额 —— handoff §7o 记的那两张 QQ Wallet 就是这个形状。
		// 它绝不能进合计：金额是从邮件错误段落抽出来的，财务含义是 0。
		{"failed carrying a nonzero amount", inv("g", "failed", "", 58000), false, 0, 1, true},
		{"unknown status", inv("h", "weird", "h.pdf", 9.00), false, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarizeInvoiceRows([]email.Invoice{c.in})
			if counted := len(got.Counted) == 1; counted != c.wantCounted {
				t.Errorf("计入合计 = %v, want %v（status=%q FilePath=%q）",
					counted, c.wantCounted, c.in.Status, c.in.FilePath)
			}
			if got.Downloaded != 0 && !c.wantCounted {
				t.Errorf("已下载计数 = %d，但它不该计入合计", got.Downloaded)
			}
			if got.Pending != c.wantPending {
				t.Errorf("pending 计数 = %d, want %d", got.Pending, c.wantPending)
			}
			if got.Failed != c.wantFailed {
				t.Errorf("failed 计数 = %d, want %d", got.Failed, c.wantFailed)
			}
			if len(got.Rows) != 1 {
				t.Errorf("明细行数 = %d, want 1 —— 不计入合计 ≠ 从列表消失", len(got.Rows))
			}
		})
	}
}

// 「已下载 N 张」与「合计所依据的那批」必须同长。分开数就可能出现
// 页面上写着「已下载 3 张、合计 5.61 元」，而 5.61 其实只来自其中 1 张。
func TestSummarizeInvoiceRows_DownloadedCountMatchesCountedSet(t *testing.T) {
	in := []email.Invoice{
		inv("a", "downloaded", "a.pdf", 5.61),
		inv("b", "downloaded", "", 7.00), // 声称已下载却没有落盘文件
		inv("c", "filed", "c.pdf", 19.00),
		inv("d", "pending", "", 1.00),
		inv("e", "failed", "", 58000),
	}
	got := summarizeInvoiceRows(in)
	if got.Downloaded != len(got.Counted) {
		t.Fatalf("已下载 %d 张，但计入合计的是 %d 张 —— 这两个数字会被并排渲染在页面上",
			got.Downloaded, len(got.Counted))
	}
	if got.Downloaded != 2 {
		t.Errorf("已下载 = %d, want 2（b 没有落盘文件，不该算已下载）", got.Downloaded)
	}
	// 合计必须与 email 包里那条唯一判据算出的一致。
	want := 0.0
	for _, iv := range in {
		if email.InvoiceCountsTowardTotal(iv) {
			want += iv.Amount
		}
	}
	amounts := email.SumByCurrency(got.Counted)
	if len(amounts) != 1 || amounts[0].Amount != want {
		t.Errorf("合计 = %+v, want %.2f", amounts, want)
	}
	// 最关键的一条：与台账表 LedgerRows 对同一批发票给出的合计必须相等。
	// 不等 ⇒ 同一批发票在台账里是一个数、在汇总接口里是另一个数。
	_, totals := email.LedgerRows(in)
	if len(totals) != 1 || totals[0].Amount != amounts[0].Amount || totals[0].Count != amounts[0].Count {
		t.Errorf("汇总接口 %+.2f/%d 张 与台账 LedgerRows %+v 不一致 —— 交付财务时这两个数会打架",
			amounts[0].Amount, amounts[0].Count, totals)
	}
}

func TestSummarizeInvoiceRows_ClassCountsAreExhaustive(t *testing.T) {
	in := []email.Invoice{
		inv("a", "downloaded", "a.pdf", 1),
		inv("b", "pending", "", 1),
		inv("c", "new", "", 1),
		inv("d", "failed", "", 1),
	}
	got := summarizeInvoiceRows(in)
	if got.Downloaded+got.Pending+got.Failed != len(in) {
		t.Errorf("三类计数 %d+%d+%d = %d, want %d（每张票都要有归属，"+
			"漏掉一类就意味着这类发票在界面上彻底隐形）",
			got.Downloaded, got.Pending, got.Failed,
			got.Downloaded+got.Pending+got.Failed, len(in))
	}
	if len(got.Rows) != len(in) {
		t.Errorf("明细行数 = %d, want %d", len(got.Rows), len(in))
	}
}

func TestSummarizeInvoiceRows_EmptyIsNotAnError(t *testing.T) {
	got := summarizeInvoiceRows(nil)
	if len(got.Rows) != 0 || len(got.Counted) != 0 {
		t.Errorf("空清单应得到空结果，实得 rows=%d counted=%d", len(got.Rows), len(got.Counted))
	}
	if amounts := email.SumByCurrency(got.Counted); len(amounts) != 0 {
		t.Errorf("空清单的合计组 = %+v, want 空（前端据此隐藏合计区）", amounts)
	}
}
