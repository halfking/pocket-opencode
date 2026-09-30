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
	"strconv"
	"time"
)

// LedgerPublisher 把发票清单发布为共享文档（当前实现：飞书电子表格）。
type LedgerPublisher interface {
	// PublishLedger 发布清单并返回可分享链接；不可用返回 error。
	PublishLedger(ctx context.Context, title string, invs []Invoice) (url string, err error)
	Available() bool
}

// LedgerRows 把发票清单转成表格二维数组：表头 + 每张票一行 + 合计行。
//
// 合计单独占一行（而不是只在文字里提一句），这样对账时能直接在表里排序/求和。
// 列顺序与 WriteInvoiceSummaryDocs 的 CSV 表头保持一致，方便两处对照。
func LedgerRows(invs []Invoice) (rows [][]any, total float64) {
	rows = make([][]any, 0, len(invs)+2)
	rows = append(rows, []any{
		"费用类型", "对方单位", "金额", "币种", "发票号", "开票日期", "状态", "文件名", "来源邮件",
	})
	for _, inv := range invs {
		total += inv.Amount
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

// LedgerTotalText 是推送文本里的合计行（金额格式化为两位小数，避免 1280.0 这种尾巴）。
func LedgerTotalText(total float64, count int) string {
	return "合计 " + strconv.FormatFloat(total, 'f', 2, 64) + "（" + strconv.Itoa(count) + " 张）"
}
