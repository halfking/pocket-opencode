package email

// 发票「准入门」到底放行了什么——给「什么算一张发票」这个待拍板问题取证。
//
// 背景（2026-10-02，q11/q12 之后）：
//
//	真库 email_invoices 有一行 amount=58000.00 date=2026-10-25 kind=bill
//	extracted_by=rule invoice_no='' ，来源是「中国工商银行信用卡对账单」。
//	q12 把它量化成：127 封里 2 封被抽成发票，1 封假账占合计 94%，虚高 17.6 倍。
//
// q12 把根因记在 invoice.go 的金额兜底（无关键词 + 取全文最大值）。本文件
// 补上**更靠前的一层**：ExtractInvoiceLoose 的第一道门
// `if !invoiceKeywordHit(joined) { return nil, false }`（invoice.go:539）。
// invoiceKeywordHit 的词表里有「账单」「对账单」「扣款」「支付成功」
// 「订单确认」——信用卡对账单因此**合法通过**准入门。
//
// 两层缺陷是叠加关系，且顺序很重要：
//
//	第 1 层  准入门放行（对账单不是发票，是债务通知）
//	第 2 层  金额兜底取全文最大值（把「信用额度 58,000」当成发票金额）
//
// 只修第 2 层（让 reAmountTotal 能吃下「合计人民币(本位币)12,838.93」）
// **不会**让这封邮件不再建档：第 617 行的兜底门槛是
// `Amount == 0 && InvoiceNo == "" && !hasInvoiceAttachment` 才丢弃，
// 拿到 12,838.93 照样建档——只是把一个一眼荒谬的 5.8 万换成一个
// 貌似合理的 1.28 万（而它其实是「应还款额」，仍然不是发票金额）。
// 错误数字改对了一点，比错误数字更危险。
//
// 所以本文件要回答的是第 1 层：**准入门放行的邮件里，有多少真的
// 带着发票的语义信号。** 这个比例决定该怎么改，而怎么改是产品决定。
//
// 本文件只读真实库，不写任何东西。

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// invoiceSignalProbe 把「像不像发票」拆成几个**互相独立**的语义探针。
//
// 刻意**不**复制 invoiceKeywordHit 的词表：复制一份就会漂移，届时
// 报告里的「准入门命中」和真实生产词表说的不是同一件事，结论就废了。
// 这里只做独立的旁证——生产函数负责说「门放没放行」，本探针负责说
// 「文本里到底有没有发票的语义」。
func invoiceSignalProbe(joined string) map[string]bool {
	low := strings.ToLower(joined)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, strings.ToLower(w)) {
				return true
			}
		}
		return false
	}
	return map[string]bool{
		// 发票本体：只有这几个词出现，才谈得上「这封邮件在开/送一张发票」。
		"发票词": has("发票", "invoice", "e-invoice", "增值税", "vat", "专用发票"),
		// 票据的其他形态：收据、票据。
		"票据词": has("票据", "收据", "receipt"),
		// 对账单：这是**债务通知**，不是支出凭证。
		"对账单词": has("对账单", "账单", "statement", "billing", "还款"),
		// 交易通知：扣款/支付成功同样不是凭证，只是「钱动了」的通知。
		"交易通知词": has("扣款", "支付成功", "订单确认", "交易", "支付"),
		// 发票的硬标识：没有号码的「发票」在实务上不可核销。
		"有发票号": reInvoiceNo.MatchString(joined),
		// 税号：开票方必须披露，没有税号基本可以断定不是发票。
		"有税号": has("纳税人识别号", "统一社会信用代码", "税号", "tax id"),
	}
}

// TestDiagRealInvoiceGateProbeSelfCheck —— 探针自己的对照实验。
//
// 判据必须先证明自己有区分力，否则报告里的 0/1 分布可能是恒值。
// 这里的两个样本是**合成**的（不含真实邮件内容），所以它在普通
// `go test` 里就会跑，不依赖真实库。
func TestDiagRealInvoiceGateProbeSelfCheck(t *testing.T) {
	realInvoice := "增值税电子普通发票 发票号码 25312000000123456789 " +
		"纳税人识别号 91310000MA1FL0P51K 价税合计：1280.00"
	statement := "信 用 卡 对 账 单 账单周期 2026年09月01日—2026年09月30日 " +
		"应还款额 12,838.93 最低还款额 1,605.56 信用额度 58,000.00 合计人民币(本位币)12,838.93"

	a := invoiceSignalProbe(realInvoice)
	b := invoiceSignalProbe(statement)

	if !a["发票词"] {
		t.Fatalf("探针无区分力：连真发票都判不出「发票词」。got=%v", a)
	}
	if b["发票词"] {
		t.Fatalf("探针无区分力：把对账单判成含「发票词」。got=%v", b)
	}
	if !b["对账单词"] {
		t.Fatalf("探针无区分力：认不出对账单。got=%v", b)
	}
	if !a["有发票号"] {
		t.Fatalf("探针无区分力：认不出真发票的发票号码。got=%v", a)
	}
	// 对账单样本里绝不该出现税号/发票号——它就不是一张发票。
	if b["有发票号"] || b["有税号"] {
		t.Fatalf("对账单样本竟带上了发票号/税号信号，样本选错了。got=%v", b)
	}
}

// TestDiagRealInvoiceAdmissionGate —— 把真实库全部邮件过一遍准入门，
// 报出交叉表：门放行了多少、其中多少真的带发票语义、金额走的哪条路。
//
// 只读。bodyText 传空串（只用 主题+摘要），这样把「准入门」与
// 「读缓存正文/带附件」两条放宽路径分开，不把它们的功劳算到门上。
func TestDiagRealInvoiceAdmissionGate(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; 跳过真实准入门诊断")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	ctx := context.Background()
	cfg, perr := pgxpool.ParseConfig(dsn)
	if perr != nil {
		t.Fatalf("parse dsn: %v", perr)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 2026-10-02 补：当场验证 search_path 确实落在目标 schema 上。
	//
	// 覆盖式设置（RuntimeParams）只有单一来源，不像 DSN 拼接那样有
	// 「pgx 取第一个同名参数」的歧义；但「以为钉住了」正是本仓库
	// search_path 缺陷家族的特征（见 diag_merge_exec_test.go 与
	// reminder_notified_diag_test.go 的注释），所以读回来确认一次。
	//
	// 代价是每次诊断多一个 round trip；收益是打错库时**立刻**报出
	// schema 名，而不是扫到空集后输出误导性结论。
	var resolvedSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolvedSchema != schema {
		t.Fatalf("search_path 未生效：期望 %q，连接实际落在 %q。**拒绝继续**"+
			"——本诊断的全部价值在于「和实现看到同一批数据」，"+
			"打到别的库会输出误导性结论。", schema, resolvedSchema)
	}
	t.Logf("search_path verified: current_schema() = %q", resolvedSchema)
	rows, qerr := pool.Query(ctx, `
		SELECT id, COALESCE(subject,''), COALESCE(snippet,'')
		FROM emails
		WHERE COALESCE(deleted_at,0)=0
		ORDER BY date DESC`)
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	defer rows.Close()

	type row struct {
		id, subj, joined string
		sig              map[string]bool
		gate             bool
		archived         bool
		amt              float64
		date             string
		no               string
		amountFrom       string
	}
	var all []row
	var gateOnly, archived int
	for rows.Next() {
		var e Email
		if serr := rows.Scan(&e.ID, &e.Subject, &e.Snippet); serr != nil {
			t.Fatalf("scan: %v", serr)
		}
		joined := e.Subject + "\n" + e.Snippet
		r := row{
			id:     e.ID,
			subj:   e.Subject,
			joined: joined,
			sig:    invoiceSignalProbe(joined),
			gate:   invoiceKeywordHit(joined),
		}
		if r.gate {
			gateOnly++
		}
		inv, ok := ExtractInvoiceLoose(e, "", false)
		if ok {
			r.archived = true
			r.amt = inv.Amount
			r.date = inv.InvoiceDate
			r.no = inv.InvoiceNo
			archived++
			// 金额是从哪条路来的：reAmountTotal 命中 = 关键词路径，
			// 没命中就落到「取全文最大值」的兜底。两条路的可信度天差地别。
			if reAmountTotal.MatchString(joined) {
				r.amountFrom = "关键词"
			} else {
				r.amountFrom = "兜底取全文最大值"
			}
		}
		all = append(all, r)
	}
	if rerr := rows.Err(); rerr != nil {
		t.Fatalf("rows: %v", rerr)
	}

	// 门放行了、但没有发票语义的——这批就是误建档的候选。
	var noInvoiceWord []row
	for _, r := range all {
		if r.archived && !r.sig["发票词"] {
			noInvoiceWord = append(noInvoiceWord, r)
		}
	}
	sort.Slice(noInvoiceWord, func(i, j int) bool { return noInvoiceWord[i].amt > noInvoiceWord[j].amt })

	t.Logf("=== 准入门交叉表（真实库，主题+摘要，不含正文/附件放宽）===")
	t.Logf("邮件总数            : %d", len(all))
	t.Logf("准入门放行          : %d", gateOnly)
	t.Logf("最终建档            : %d", archived)
	t.Logf("建档但**无发票语义** : %d  ← 误建档候选", len(noInvoiceWord))
	t.Logf("")
	t.Logf("=== 建档明细 ===")
	for _, r := range all {
		if !r.archived {
			continue
		}
		t.Logf("%12.2f  金额来源=%-16s 发票号=%-6s 发票词=%v 对账单词=%v 发票号信号=%v 税号信号=%v",
			r.amt, r.amountFrom, emptyAsDash(r.no),
			r.sig["发票词"], r.sig["对账单词"], r.sig["有发票号"], r.sig["有税号"])
		t.Logf("    主题: %s", emptyAsDash(r.subj))
	}
	t.Logf("")
	t.Logf("=== 准入门放行、但最终**未**建档（被 invoice.go:617 门槛丢弃）===")
	t.Logf("（这一段是收紧准入门的风险面：其中可能藏着「金额只印在附件 PDF 里」")
	t.Logf("　的真发票——那种邮件正是 ExtractInvoiceLoose 放宽路径要救的，见")
	t.Logf("　invoice.go:520-527。收紧前必须先看这里。）")
	for _, r := range all {
		if !r.gate || r.archived {
			continue
		}
		t.Logf("发票词=%v 对账单词=%v 交易通知词=%v 有发票号=%v 有税号=%v",
			r.sig["发票词"], r.sig["对账单词"], r.sig["交易通知词"],
			r.sig["有发票号"], r.sig["有税号"])
		t.Logf("    主题: %s", emptyAsDash(r.subj))
	}
	t.Logf("")
	t.Logf("=== 误建档候选（按金额降序）===")
	for _, r := range noInvoiceWord {
		t.Logf("%12.2f  %s", r.amt, emptyAsDash(r.subj))
		// 把命中「非发票」信号的词原样打出来，让「为什么判它不是发票」可核查。
		var why []string
		for _, k := range []string{"对账单词", "交易通知词"} {
			if r.sig[k] {
				why = append(why, k)
			}
		}
		t.Logf("    命中信号: %v   主题: %s", why, emptyAsDash(r.subj))
	}
	// 只读诊断，不对真实数据下「必须为 0」的结论——真实环境误建档是需要
	// 人来处理的事，测试只保证它会被看见。上面的行就是可见性本身。
	_ = archived
}
