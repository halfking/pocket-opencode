package email

// ledger_total_test.go — 共享台账「汇总金额」的口径。
//
// 目标原文：「需要整理一个列表，记录必要信息并汇总金额」。汇总金额是对账
// 的关键数字，口径必须明确且稳定，否则「对不上账」时没人说得清差在哪。
//
// 现状：LedgerRows 直接 `total += inv.Amount` 累加**全部**发票，不看 status。
// 当前真实数据下两种口径结果相同（3 张：failed 0 + failed 0 + downloaded 3500
// = 3500），所以这个选择一直没被暴露出来。
//
// 但它是真风险：库里已经存在 status=failed 却残留脏字段的记录
// （handoff §7o 记的两张 QQ Wallet：`seller="name:"`、`invoiceNo="Issuance"`、
// `amount=0.00`；金额恰好是 0 才没出事）。若将来某张 failed 发票带着一个
// 非零但错误的金额（例如从错误段落里抽到数字），它会被静默算进合计，
// 让台账总额虚高，而**没有任何地方会提示**。
//
// 本文件固定的口径：**只把 downloaded 的发票计入合计**，其余状态既不计入
// 也不丢弃（行仍在，状态列写明）—— 行留着让人追，金额不进账。
import "testing"

func ledgerInv(id, status string, amount float64) Invoice {
	// FilePath 非空是计入合计的前提之一（与 server 层 downloaded 计数对齐）：
	// 只有真正落了盘的发票才算「拿到了这张票的钱」。
	fp := "email-invoices/ws/" + id + ".pdf"
	// Seller / InvoiceNo 一并填上：台账行**不含 ID 列**，断言某张发票在不在
	// 表里只能按业务列（销售方/发票号）找。留空的话「这张票还在不在」
	// 根本无从判断 —— 那正是这个负控要守住的东西。
	return Invoice{
		ID: id, Status: status, Amount: amount, Currency: "CNY", FilePath: fp,
		Seller: "seller-" + id, InvoiceNo: "no-" + id,
		Category: "其他", FileName: id + ".pdf", Subject: "发票 " + id,
	}
}

// TestLedgerRows_TotalCountsOnlyDownloaded 固定合计口径。
func TestLedgerRows_TotalCountsOnlyDownloaded(t *testing.T) {
	invs := []Invoice{
		ledgerInv("a", "downloaded", 3500),
		// 一张 failed 却带着脏金额的发票：行要留着（能追），金额不进合计。
		ledgerInv("b", "failed", 999.99),
		ledgerInv("c", "pending", 1280),
	}
	rows, total := LedgerRows(invs)
	if total != 3500 {
		t.Fatalf("合计 = %v, want 3500 —— 只应计入 downloaded；"+
			"failed/pending 的金额可能来自错误抽取，静默计入会让总额虚高", total)
	}
	// 行数：表头 + 3 张发票 + 合计行，一张都不能少。
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5（表头 + 3 张 + 合计）", len(rows))
	}
	// 合计行必须存在且带数值 —— 目标要求「汇总金额」能被直接求和，
	// 只在正文里提一句不算数。
	last := rows[len(rows)-1]
	if len(last) < 3 || last[0] != "合计" {
		t.Fatalf("最后一行 = %v, want 以「合计」开头的汇总行", last)
	}
	if got, ok := last[2].(float64); !ok || got != 3500 {
		t.Fatalf("合计行金额 = %v（%T）, want float64(3500)", last[2], last[2])
	}
}

// TestLedgerRows_FailedInvoiceStillGetsARow 负控：口径收紧后，
// failed 发票**仍必须占一行**并带状态 —— 不能因为不计入合计就把它从
// 台账里抹掉，否则用户看不到「有两张没拿到」，合计也就失去了对账意义。
func TestLedgerRows_FailedInvoiceStillGetsARow(t *testing.T) {
	invs := []Invoice{
		ledgerInv("a", "downloaded", 3500),
		ledgerInv("b", "failed", 999.99),
	}
	rows, total := LedgerRows(invs)
	if total != 3500 {
		t.Fatalf("合计 = %v, want 3500", total)
	}
	// 按销售方列（第 2 列，索引 1）找 failed 那张 —— 台账行里没有 ID 列。
	const sellerCol = 1
	found := false
	for _, r := range rows {
		if len(r) > sellerCol && r[sellerCol] == "seller-b" {
			found = true
			// 状态列必须写明「失败」：不计入合计已经隐含在总额里，
			// 但行里不写状态，用户会以为这行也参与了求和。
			if r[6] != "failed" {
				t.Fatalf("failed 行的状态列 = %v, want \"failed\"", r[6])
			}
		}
	}
	if !found {
		t.Fatalf("failed 发票 b 没有出现在台账里：%v —— 不计入合计 ≠ 从列表消失", rows)
	}
}

// TestLedgerRows_DownloadedButNoFileIsNotCounted 钉住两处判据一致性的关键点：
// status 是 downloaded 但 FilePath 为空（文件被外部删了、或落盘失败却已改状态）
// 的发票**不计入合计**。它与 server 层 `handleEmailInvoiceSummary` 里
// `downloaded++` 的条件一模一样 —— 若两边判据不一致，界面上「已下载 N 张」
// 和「合计 X 元」会指向两批不同的发票。
func TestLedgerRows_DownloadedButNoFileIsNotCounted(t *testing.T) {
	invs := []Invoice{
		ledgerInv("a", "downloaded", 3500),
		// 状态说下好了，文件却不在（被清理脚本删掉/落盘失败）。
		{ID: "ghost", Status: "downloaded", Amount: 777, Currency: "CNY", FilePath: ""},
	}
	_, total := LedgerRows(invs)
	if total != 3500 {
		t.Fatalf("合计 = %v, want 3500 —— 没有落盘文件的发票不该计入", total)
	}
}

// TestLedgerRows_EmptyLedger 对照组：空清单下合计必须是 0，且**形状不变**。
//
// 这里原本断言「空清单返回空 rows」，是写测试时的错误猜测 —— 实现返回
// 表头 + 「合计 0 / 共 0 张」两行，而这是更安全的形状，不是缺陷：
//   - 飞书写入方和 LedgerCellRange 都假定 rows[0] 是表头、rows[len-1] 是合计，
//     空清单若特判成空表，读取方就会出现「表头/合计取不到」的分叉；
//   - 空清单根本不会被发布出去：PublishLedgerScoped 提前返回空 URL，
//     不建飞书表（ledger_test.go 的 TestPublishLedgerScoped_SkipsEmptyLedger 钉着），
//     所以「合计 0」不会被任何人误读成「查不到数据」。
//
// 真正要守住的是：**空输入不能凭空造出金额**（合计 0），且不能出现明细行。
func TestLedgerRows_EmptyLedger(t *testing.T) {
	rows, total := LedgerRows(nil)
	if total != 0 {
		t.Fatalf("total = %v, want 0", total)
	}
	if len(rows) != 2 { // 表头 + 合计行
		t.Fatalf("rows = %v, want 表头 + 合计行两行", rows)
	}
	if rows[0][0] != "费用类型" {
		t.Fatalf("第一行必须是表头（写单元格方按行 0 写表头）：%v", rows[0])
	}
	if rows[len(rows)-1][0] != "合计" {
		t.Fatalf("最后一行必须是合计行：%v", rows[len(rows)-1])
	}
	// 合计行里的「共 N 张」必须是 0，不能带上一轮残留的计数。
	if got := rows[len(rows)-1][7]; got != "共 0 张" {
		t.Fatalf("合计行张数 = %v, want \"共 0 张\"", got)
	}
}
