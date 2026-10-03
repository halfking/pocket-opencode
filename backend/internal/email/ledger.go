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
	"math"
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

// CurrencyTotal 是单个币种的合计。跨币种的「总额」不是金额，所以本函数
// 只能按币种分别返回合计，调用方拿不到一个可以随手相加的标量。
//
// json tag 不是装饰，删掉它等于把发票页的合计金额打成「¥NaN」。
//
// 2026-10-03 真机实测（Redmi，/email/invoices）：没有 tag 时
// encoding/json 按字段名原样输出 amounts[] = {"Currency","Amount","Count"}，
// 而前端 resolveSummaryGroups（invoice-money.ts）读的是 a.currency / a.amount
// —— 两个都读到 undefined，round2(undefined) 得到 NaN，页面顶部合计金额
// 显示 **¥NaN**（同一屏的「共 N 张」正常，因为那个数走另一个字段）。
//
// 为什么整条链上一条用例都没红：invoice-totals-chain.test.mjs 的夹具是
// **手写的 camelCase**，invoice_total_parity_test.go 则三处都在 Go 内部
// 比对、从不出 JSON。数值一致 ≠ 线上字段名一致——这条边界此前无人断言。
// 护栏见 server_email_invoice_wire_keys_test.go（真 handler 出线上的字节），
// 跨语言那一半在 frontend/src/features/email/__tests__/invoice-totals-wire-keys.test.mjs。
type CurrencyTotal struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Count    int     `json:"count"`
}

// SumByCurrency 按币种分组求和，返回每币种的合计与张数。
//
// 为什么单独暴露一个函数：这条规则（「跨币种的算术和不是金额」）在仓库里已经
// 有三处实现（LedgerRows、WriteInvoiceSummaryDocs、InvoiceListStats），三处都各
// 自聚合、各自配了用例。第四处（server 层 handleEmailInvoiceSummary 的 amountTotal）
// 此前是裸 `total += inv.Amount` —— 前三处修的时候审计范围只在 internal/email，
// server 层的手写求和没被看到。
//
// 汇总端点已经**持有**发票切片（要输出 rows），所以按切片聚合而不是再查一次库，
// 计数与合计也就来自同一份数据、不会一边被 500 上限截断一边没有。
func SumByCurrency(invs []Invoice) []CurrencyTotal {
	centsByCur := map[string]int64{}
	countByCur := map[string]int{}
	var order []string
	for _, inv := range invs {
		cur := currencyOrDefault(inv.Currency)
		if _, seen := centsByCur[cur]; !seen {
			order = append(order, cur)
		}
		// 整数分累加，保证 total 与 sum(round2(每行)) 恒等（见 round2 处的说明）。
		centsByCur[cur] += int64(math.Round(round2(inv.Amount) * 100))
		countByCur[cur]++
	}
	out := make([]CurrencyTotal, 0, len(order))
	for _, cur := range order {
		out = append(out, CurrencyTotal{
			Currency: cur, Amount: float64(centsByCur[cur]) / 100, Count: countByCur[cur],
		})
	}
	return out
}

// InvoiceCountsTowardTotal 是**唯一**的「这一张算不算进合计」判据。
//
// ## 为什么必须只有一处
//
// 2026-10-02 实测（真实库 2 行发票）：同一份数据，两条消费路径给出两个数——
//
//	LedgerRows（本文件）            → CNY 3,500  （只计 downloaded/filed 且有文件）
//	InvoiceListStats（invoice_list.go）→ CNY 61,500（**完全没有过滤**）
//
// 而发票页显示的是后者：`frontend/src/features/email/invoice-money.ts` 的
// `resolveSummaryGroups` 优先读列表 API 的 `amounts`，只有它缺失时才退回
// 客户端自己按全部行重算的 `sumByCurrency(list)`。于是页面上摆着 61,500，
// 飞书台账里是 3,500，差 17.6 倍——而需求要的就是「汇总金额」。
//
// 病根不是某一处写错，是**同一条规则被手写了三遍**（LedgerRows、
// WriteInvoiceSummaryDocs、InvoiceListStats），前两处逐字符相同、
// 第三处干脆漏了。三处里任何一处漂移都不会被任何测试发现，因为每处
// 只测自己。
//
// ## 判据本身
//
// 只统计**已经拿到凭证**的票：状态属于已下载态，且服务端磁盘上确有落盘文件。
// 只有邮件正文里一个自称的金额（对账单/扣款通知这类没有发票号也没有附件的
// 邮件）不计入——那不是发票金额，是对账单金额，两者的财务含义不同。
//
// 这类行**不删除**，仍出现在明细里并标记为「未核验」（见 InvoiceVerifiedLabel）：
// 删掉就再也看不见「有一封 58,000 的东西需要人去追」，而保留但不标记
// 则会让用户以为表里的每一行都参与了合计。
func InvoiceCountsTowardTotal(inv Invoice) bool {
	return (inv.Status == "downloaded" || inv.Status == "filed") && inv.FilePath != ""
}

// InvoiceVerifiedLabel 是明细行里「核验」列的取值。
//
// 口径必须与 InvoiceCountsTowardTotal 严格一致：判据说不计的，这里就写
// 「未核验」。两处若各写各的，会出现「标着已核验却不计入合计」的行，
// 那比没有这一列更难排查。
func InvoiceVerifiedLabel(inv Invoice) string {
	if InvoiceCountsTowardTotal(inv) {
		return "已核验"
	}
	return "未核验"
}

// LedgerRows 把发票清单转成表格二维数组：表头 + 每张票一行 + 每币种一行合计。
//
// 合计单独占行（而不是只在文字里提一句），这样对账时能直接在表里排序/求和。
// 列顺序与 WriteInvoiceSummaryDocs 的 CSV 表头保持一致，方便两处对照。
//
// 合计用**整数分**累加而不是裸 `total += inv.Amount`。实测（2026-10-01）：
// 100 张 0.07 的发票，float64 累加得 7.00000000000000888178，
// json.Marshal 后以字面量 `7.000000000000009` 写进飞书表格——
// 金额是财务数据，表格里出现这种数字就是错账。整数分累加后
// `float64(cents)/100` 的最短表示恰好是 `7`。
//
// 第二个返回值是**按币种分组的合计**，不是一个标量总额（2026-10-01 改）。
// 原先这里返回 `total float64` 并把所有币种直接相加，于是 USD + CNY
// 会得到一个看起来完全正常的数字——100 USD + 50 CNY 返回 150，
// 调用方无从察觉这是两种货币。唯一生产调用方
// （server_email_pipeline.go 的 PublishLedger）恰好写作 `rows, _ :=`
// 丢掉了它，所以这个缺陷一直潜伏；但它是个地雷：下一个人只要接住这个
// 返回值并写进通知/报表，就是一处静默错账。改成按币种返回后，
// 想犯这个错必须先自己把 map 加起来，跨币种的语义问题会被显式暴露。
func LedgerRows(invs []Invoice) (rows [][]any, totals []CurrencyTotal) {
	rows = make([][]any, 0, len(invs)+2)
	rows = append(rows, []any{
		"费用类型", "对方单位", "金额", "币种", "发票号", "开票日期", "状态", "核验", "文件名", "来源邮件",
	})
	// 按币种分组累加。单一币种（当前真实数据 7 张全是 CNY）时只出一行合计，
	// 与旧行为完全一致；混入外币时每个币种各出一行——USD 与 CNY 直接相加
	// 没有财务意义，需求要求「汇总金额」，而跨币种的和不是金额。
	centsByCur := map[string]int64{}
	countByCur := map[string]int{}
	var order []string // 保持首次出现顺序，合计行跟着明细顺序走
	for _, inv := range invs {
		cur := currencyOrDefault(inv.Currency)
		// 明细行：所有发票都列出来。**不计入合计 ≠ 从列表消失** ——
		// failed/pending 的行照样在表里、状态列照样写明，用户依然看得到
		//「有几张没拿到」，这正是合计能用来对账的前提。
		//
		// 「核验」列（2026-10-02 加）：此前只靠「状态」列暗示某行没计入，
		// 而 failed 与「未核验」在状态列上长得一样（都是 new/failed），
		// 用户无法分辨「这张失败了」和「这张只是个自称的金额」——前者
		// 该重试，后者该去追对账单，两种跟进动作不一样。
		rows = append(rows, []any{
			inv.Category, inv.Seller, round2(inv.Amount), cur,
			inv.InvoiceNo, inv.InvoiceDate, inv.Status, InvoiceVerifiedLabel(inv), inv.FileName, inv.Subject,
		})
		// 合计：判据是唯一的 InvoiceCountsTowardTotal，与
		// WriteInvoiceSummaryDocs、InvoiceListStats 共用同一个函数。
		// 此前这里是手写的内联表达式，与 pipeline.go 那份逐字符相同——
		// 两份相同代码就是两份可以各自漂移的代码。
		if !InvoiceCountsTowardTotal(inv) {
			continue
		}
		// 四舍五入到分再累加：发票金额本身是两位小数，
		// 但解析器可能产出 126.005 这类值，直接转 int64 会截断。
		if _, seen := centsByCur[cur]; !seen {
			order = append(order, cur)
		}
		centsByCur[cur] += int64(math.Round(round2(inv.Amount) * 100))
		countByCur[cur]++
	}
	multi := len(order) > 1
	if len(order) == 0 {
		// 空清单也必须有合计行（需求：「整理一个列表…并汇总金额」）：
		// 只有表头 + 一行 0 合计，下游按行数算写入范围的逻辑才不用特判。
		rows = append(rows, ledgerTotalRow(0.0, "", 0, len(invs)))
		return rows, nil
	}
	totals = make([]CurrencyTotal, 0, len(order))
	for _, cur := range order {
		sum := round2(float64(centsByCur[cur]) / 100)
		// 单币种不标币种标签（与旧输出一致，下游按列位取值）；多币种必须标，
		// 否则两行「合计」加起来仍然没有意义。
		label := ""
		rowTotal := len(invs)
		if multi {
			label = cur
			// 多币种时每行只覆盖本币种，「共 N 张」必须也是本币种的张数。
			// 三行各写「共 5 张」会让读者以为这一行是全部 5 张的合计——
			// 而它其实只覆盖其中 2 张。
			rowTotal = countByCur[cur]
		}
		rows = append(rows, ledgerTotalRow(sum, label, countByCur[cur], rowTotal))
		totals = append(totals, CurrencyTotal{Currency: cur, Amount: sum, Count: countByCur[cur]})
	}
	return rows, totals
}

// ledgerTotalRow 拼出合计行，列数与 LedgerRows 的表头严格一致（10 列）。
//
// 单独抽出来是因为合计行此前散在三个分支里各写一遍字面量，加「核验」列时
// 漏改一处就会让飞书表格按错误的列数写入、或把张数写进「核验」列。
//
// 张数必须写「计入 N 张 / 共 M 张」而不是「共 M 张」：真实库 2 行发票里只有
// 1 行进了合计，写「共 2 张」会让读者以为 3,500 是那 2 张的总额——纸面上
// 看不出错，账上就是错的。
func ledgerTotalRow(sum float64, currency string, counted, total int) []any {
	return []any{
		"合计", "", sum, currency, "", "", "",
		"",
		fmt.Sprintf("计入 %d 张 / 共 %d 张", counted, total),
		"",
	}
}

// round2 把金额规整到分（2 位小数），消除二进制浮点的表示误差。
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func currencyOrDefault(c string) string {
	if c == "" {
		return "CNY"
	}
	return c
}

// LedgerCellRange 把行数换算成 "<sheetId>!A1:J<n>" 形式的写入范围。
// 列数固定 10（表头宽度，2026-10-02 加「核验」列后由 9 变 10），
// 行数 = 表头 + 明细 + 合计。
func LedgerCellRange(sheetID string, rows [][]any) string {
	cols := 10
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
