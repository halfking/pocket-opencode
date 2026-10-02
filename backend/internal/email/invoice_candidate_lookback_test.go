package email

// invoice_candidate_lookback_test.go — 发票候选建档的**回看窗口**回归测试。
//
// 2026-10-02 在真实库上发现：真实库 120 封邮件里只有 2 封落在 24h 窗口内，
// 唯一一张 envelope 就能识别的真实发票
// （「…的发票，发票号码：2633…，金额：3500.00元…」）在窗口之外，
// 于是 email_invoices 一直是 0 行。整条链路（流水线、采集、A4 网格导出、
// 飞书推送、共享台账）看起来都实现了，实际对历史邮件/积压邮件**从不触发**。
//
// 这里锁住两件事：
//  1. 窗口必须能看到「超过 24 小时」入库的发票邮件（缺陷本身的回归锁）；
//  2. 放宽窗口**不能**顺带把非发票邮件也建档（防过度放宽的另一面）。
//
// 断言一律用「新值真的出现」，不用「旧值消失」——后者会被「什么都没做」满足。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// readPipelineSource 读本包的 pipeline.go 源码（源码级护栏用）。
func readPipelineSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("pipeline.go")
	if err != nil {
		t.Fatalf("read pipeline.go: %v", err)
	}
	return string(b)
}

// extractFuncBody 截出某个函数的源码：到**列 0 的收尾花括号**为止，
// 且先剥掉注释。
//
// 剥注释是必须的，不是洁癖：本文件第 520 行的注释里就写着
// `rep.StartedAt-86400`（记录旧实现），不剥就会被当成「调用点仍写死 24h」
// 而误报。判据匹配注释里的字面文本，等于给退化开了后门——
// 仓库里已经因此栽过多次（见 handoff 里的护栏教训）。
func extractFuncBody(t *testing.T, src, header string) string {
	t.Helper()
	noComment := stripGoComments(src)
	i := strings.Index(noComment, header)
	if i < 0 {
		t.Fatalf("源码里找不到 %s", header)
	}
	rest := noComment[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		rest = rest[:j+3]
	}
	return rest
}

// stripGoComments 去掉 // 行注释与 /* */ 块注释（保留换行以免把两行粘成一行）。
func stripGoComments(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		if src[i] == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				continue
			}
			if src[i+1] == '*' {
				j := strings.Index(src[i+2:], "*/")
				if j < 0 {
					break
				}
				for _, c := range src[i : i+2+j+2] {
					if c == '\n' {
						b.WriteByte('\n')
					}
				}
				i += 2 + j + 2
				continue
			}
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

func firstLineContaining(body, needle string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, needle) {
			return strings.TrimSpace(ln)
		}
	}
	return "(没找到 " + needle + ")"
}

// 真实数据里那张发票的 subject/snippet 原样照抄（em-10435）：
// ExtractInvoice 靠主题与摘要里的「发票号码 / 金额 / 开票日期」判定。
// 摘要是从真实邮件上摘的，保留了「开票日期：2026-09-24」——
// 少了这段，规范文件名会退化成下载当天，而这正是文件名格式需求的一环。
const lookbackInvoiceSubject = "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元，请注意查收！"

const lookbackInvoiceSnippet = "您申请的 电子发票（普通发票），已通过 亿企赢 平台成功开具。" +
	"PDF发票下载 OFD发票下载 XML发票下载 " +
	"销方名称：杭州创客家投资管理有限公司 " +
	"购方名称：杭州开轩科技有限公司 " +
	"开票日期：2026-09-24 金额合计：3500.00 " +
	"发票号码：26332000008261110741"

func seedCandidateEmail(t *testing.T, store *Store, id, accountID, wsID, subject, snippet string, date int64) {
	t.Helper()
	if err := store.InsertEmail(context.Background(), Email{
		ID: id, AccountID: accountID, WorkspaceID: wsID,
		MessageID:   id + "@example.com",
		FromAddress: "billing@vendor.example",
		Subject:     subject, Snippet: snippet,
		Date: date,
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestExtractInvoiceCandidates_SeesInvoiceOlderThan24h(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-lookback", "user-lookback", "ws-lookback")

	// 10 天前入库：原实现的 24h 窗口看不见它，定时任务每天都漏。
	tenDaysAgo := time.Now().AddDate(0, 0, -10).Unix()
	seedCandidateEmail(t, store, "old-invoice", "acct-lookback", "ws-lookback",
		lookbackInvoiceSubject, lookbackInvoiceSnippet, tenDaysAgo)

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	// Fetcher 留 nil：这一步只为验证「窗口能否看见」，不该碰 IMAP。
	p := &Pipeline{Store: store, DataDir: t.TempDir()}
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, accounts, rep)

	if rep.InvoiceCandidatesScanned == 0 {
		t.Fatalf("窗口仍把 10 天前的邮件挡在外面：scanned=%d（回看天数=%d）",
			rep.InvoiceCandidatesScanned, invoiceCandidateLookbackDays)
	}
	inv, err := store.GetInvoiceByEmailID(ctx, "old-invoice")
	if err != nil {
		t.Fatalf("10 天前的发票邮件没有被建档（这是缺陷本身）: %v", err)
	}
	if rep.InvoiceCandidatesCreated < 1 {
		t.Errorf("报告里的 created 计数没反映真实建档：got %d", rep.InvoiceCandidatesCreated)
	}
	if inv.Seller != "杭州创客家投资管理有限公司" {
		t.Errorf("对方单位没抽出来：got %q", inv.Seller)
	}
	if inv.Amount != 3500 {
		t.Errorf("金额没抽出来：got %.2f", inv.Amount)
	}
	if inv.InvoiceDate == "" {
		t.Errorf("开票日期为空，规范文件名会退化成下载当天")
	}
}

// 反向锁：窗口放宽后，非发票邮件绝不能被建档。否则「多扫 90 天」会把
// newsletter/账单提醒灌进发票列表，需求要的「整理」就变成了噪音。
func TestExtractInvoiceCandidates_DoesNotFileNonInvoice(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-noninv", "user-noninv", "ws-noninv")

	old := time.Now().AddDate(0, 0, -10).Unix()
	seedCandidateEmail(t, store, "plain-news", "acct-noninv", "ws-noninv",
		"【九月小树报】哪间 here 最适合你？", "新店登场 × 天下学习 30 元学习金", old)
	seedCandidateEmail(t, store, "plain-verify", "acct-noninv", "ws-noninv",
		"你的 OpenAI 临时验证码", "验证码 123456，请勿泄露", old)

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	p := &Pipeline{Store: store, DataDir: t.TempDir()}
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, accounts, rep)

	for _, id := range []string{"plain-news", "plain-verify"} {
		if inv, err := store.GetInvoiceByEmailID(ctx, id); err == nil {
			t.Errorf("非发票邮件被建档了 %s：seller=%q amount=%.2f（放宽窗口不等于放宽判定）",
				id, inv.Seller, inv.Amount)
		}
	}
	if rep.InvoiceCandidatesCreated != 0 {
		t.Errorf("created 应为 0，实际 %d", rep.InvoiceCandidatesCreated)
	}
}

// 幂等：同一封邮件跑两轮不应产生第二条发票记录，也不该重复计数。
func TestExtractInvoiceCandidates_IsIdempotent(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	seedAccount(t, store, "acct-idem", "user-idem", "ws-idem")
	seedCandidateEmail(t, store, "idem-invoice", "acct-idem", "ws-idem",
		lookbackInvoiceSubject, lookbackInvoiceSnippet, time.Now().AddDate(0, 0, -10).Unix())

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	p := &Pipeline{Store: store, DataDir: t.TempDir()}

	rep1 := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, accounts, rep1)
	if rep1.InvoiceCandidatesCreated != 1 {
		t.Fatalf("第一轮应建档 1 条，实际 %d", rep1.InvoiceCandidatesCreated)
	}
	rep2 := &PipelineReport{StartedAt: time.Now().Unix()}
	p.extractInvoiceCandidates(ctx, accounts, rep2)
	if rep2.InvoiceCandidatesCreated != 0 {
		t.Errorf("第二轮重复建档了 %d 条（GetInvoiceByEmailID 的幂等跳过失效）", rep2.InvoiceCandidatesCreated)
	}
}

// 护栏：回看窗口必须显著大于 24 小时，且扫描上限与窗口配套。
// 这条防的是「有人为了省事把窗口调回一天」——那正好把缺陷装回去。
func TestInvoiceCandidateWindowIsWiderThanOneDay(t *testing.T) {
	if invoiceCandidateLookbackDays <= 1 {
		t.Errorf("回看窗口 %d 天 ≤ 1 天，等于退回原缺陷", invoiceCandidateLookbackDays)
	}
	if invoiceCandidateScanLimit <= 500 {
		t.Errorf("扫描上限 %d：ORDER BY date DESC 下 500 行几乎必然被最近邮件占满，宽窗口形同虚设",
			invoiceCandidateScanLimit)
	}
	if invoiceCandidateScanLimit > 2000 {
		t.Errorf("扫描上限 %d 超过 Store.ListEmailsSince 的硬上限 2000，会被静默重置成 500",
			invoiceCandidateScanLimit)
	}
}

// 护栏（接线层）：上面那条只读**常量**，锁不住调用点——实测把
// ListEmailsSince 的实参改回 rep.StartedAt-86400 时，常量仍是 90、
// 上面那条照样绿，而缺陷已经装回去了。所以这里直接锁调用点的写法。
//
// 用源码级匹配而不是「再跑一遍集成测试」：集成测试需要 PG，
// 而这条护栏必须在没有数据库的环境里也成立。
func TestExtractInvoiceCandidates_UsesLookbackConstantAtCallSite(t *testing.T) {
	src := readPipelineSource(t)
	body := extractFuncBody(t, src, "func (p *Pipeline) extractInvoiceCandidates(")
	if !strings.Contains(body, "int64(invoiceCandidateLookbackDays)*86400") {
		t.Errorf("调用点没有用 invoiceCandidateLookbackDays —— 窗口又被写死成别的值了。\n"+
			"实际调用：%s", firstLineContaining(body, "ListEmailsSince"))
	}
	if !strings.Contains(body, "invoiceCandidateScanLimit") {
		t.Errorf("调用点没有用 invoiceCandidateScanLimit —— 扫描上限与回看窗口不配套。\n"+
			"实际调用：%s", firstLineContaining(body, "ListEmailsSince"))
	}
	if strings.Contains(body, "StartedAt-86400") {
		t.Errorf("调用点里仍残留硬编码的 24h 窗口（StartedAt-86400）")
	}
}
