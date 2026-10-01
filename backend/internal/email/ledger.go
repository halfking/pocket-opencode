package email

// ledger.go — 发票共享台账（需求：「建立共享文档及文件，进行整理，需要整理
// 一个列表，记录必要信息并汇总金额」）。
//
// 原来这条只有 BuildInvoiceSummaryDocs 在设备本地写 CSV/MD——那是**本机文件**，
// 不是共享文档，别人拿不到，也不算「建立共享文档」。这里定义一个发布器接口，
// 由 server 包用飞书电子表格实现：创建表格 → 写表头/明细/合计行 → 返回可分享链接。
// 飞书不可用时上层仍会落本地 CSV/MD，两条路都不丢。

import (
	"context"
	"fmt"
	"time"
)

// LedgerPublisher 把发票清单发布为共享文档（当前实现：飞书电子表格）。
type LedgerPublisher interface {
	// PublishLedger 发布清单并返回可分享链接；不可用返回 error。
	PublishLedger(ctx context.Context, title string, invs []Invoice) (url string, err error)
	Available() bool
	// PublishedURL / RememberPublished 供发布器实现「同一用户复用同一张表」。
	//
	// 读接口（GET 汇总）也会走发布路径：没有复用时每刷新一次就新建一张飞书
	// 表格，把用户云盘刷屏。返回空串表示「本进程内还没建过」，发布器若不支持
	// 复用可原样返回空串，行为退回到每次新建。
	PublishedURL(workspaceID, userID string) string
	RememberPublished(workspaceID, userID, url string)
}

// LedgerRows 把发票清单转成表格二维数组：表头 + 每张票一行 + 合计行。
//
// 合计单独占一行（而不是只在文字里提一句），这样对账时能直接在表里排序/求和。
// 列顺序与 WriteInvoiceSummaryDocs 的 CSV 表头保持一致，方便两处对照。
//
// **合计只统计 status == "downloaded" 的发票**（2026-10-01 修正）。
// 原来是无条件 `total += inv.Amount` 累加全部记录。当前真实数据下两种口径
// 结果相同（3 张：failed 0 + failed 0 + downloaded 3500 = 3500），所以这个
// 问题一直没被暴露。
//
// 但库里确实存在 status=failed 却残留脏字段的记录（两张 QQ Wallet：
// `seller="name:"`、`invoiceNo="Issuance"`）—— 那些字段是从邮件的错误段落
// 里抽出来的（handoff §7o 记的同一批根因），金额当时恰好是 0 才没出事。
// 若将来某张 failed 发票带着一个非零但错误的金额，它会被静默算进合计，
// 让对账总额虚高，而没有任何地方会提示。
//
// 注意：**不计入合计 ≠ 从列表消失**。failed/pending 的行照样在表里、状态列
// 照样写明，用户依然看得到「有几张没拿到」—— 这正是合计能用来对账的前提。
// 若改成只在表里留已下载的，用户会以为「一共就这些」，总额反而更不可信。
func LedgerRows(invs []Invoice) (rows [][]any, total float64) {
	rows = make([][]any, 0, len(invs)+2)
	rows = append(rows, []any{
		"费用类型", "对方单位", "金额", "币种", "发票号", "开票日期", "状态", "文件名", "来源邮件",
	})
	for _, inv := range invs {
		// 判据与 server 层 handleEmailInvoiceSummary 的 downloaded 计数
		// 完全一致（status 属于已下载态 **且** FilePath 非空），否则界面上
		// 「已下载 N 张」和「合计 X 元」会指向两批不同的发票。
		if (inv.Status == "downloaded" || inv.Status == "filed") && inv.FilePath != "" {
			total += inv.Amount
		}
		rows = append(rows, []any{
			inv.Category, inv.Seller, inv.Amount, currencyOrDefault(inv.Currency),
			inv.InvoiceNo, inv.InvoiceDate, inv.Status, inv.FileName, inv.Subject,
		})
	}
	rows = append(rows, []any{"合计", "", total, "", "", "", "", fmt.Sprintf("共 %d 张", len(invs)), ""})
	return rows, total
}

func currencyOrDefault(c string) string {
	if c == "" {
		return "CNY"
	}
	return c
}

// LedgerCellRange 把行数换算成 "<sheetId>!A1:I<n>" 形式的写入范围。
// 列数固定 9（表头宽度），行数 = 表头 + 明细 + 合计。
func LedgerCellRange(sheetID string, rows [][]any) string {
	cols := 9
	return fmt.Sprintf("%s!A1:%s%d", sheetID, columnName(cols), len(rows))
}

// columnName 把列数转成 Excel 风格列名（1→A, 9→I, 27→AA）。
func columnName(n int) string {
	name := ""
	for n > 0 {
		n--
		name = string(rune('A'+n%26)) + name
		n /= 26
	}
	if name == "" {
		return "A"
	}
	return name
}

// LedgerTitle 生成带日期的台账标题，便于在飞书里按日期回溯。
func LedgerTitle(workspaceID string, now time.Time) string {
	ws := defaultWorkspace(workspaceID)
	return fmt.Sprintf("发票台账 %s（%s）", now.Format("2006-01-02"), ws)
}
