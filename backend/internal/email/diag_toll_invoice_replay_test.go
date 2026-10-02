package email

// diag_toll_invoice_replay_test.go — 决定性实验：两封「通行费电子发票」为什么没有台账行。
//
// ## 背景与本文件要推翻/坐实的东西
//
// 上一轮（提交 b55d0703）把根因记成「reAmountTotal 匹配不到『金额共计19元』」，
// 并写进 diag_amount_gap_test.go:25-26：
//
//	若成立，ExtractInvoiceLoose 拿到的 Amount=0、InvoiceNo=""、hasInvoiceAttachment=false，
//	命中 invoice.go:617 的丢弃门槛 ⇒ 不建档。
//
// **那个前提是未经验证的假设，不是实测。** 它只解释了「为什么第 1 趟不建档」，
// 完全没有解释「为什么第 2 趟（那条专门为救这类邮件而写的放宽路径）也没建档」。
//
// 而第 2 趟存在只有一个门控（pipeline.go:567）：`p.Fetcher != nil && e.UID > 0`。
// 排队条件是 `invoiceBodyReason` 返回非空（pipeline.go:739：!hit && InvoiceCandidate
// ⇒ "candidate"，这两封 subject 含「发票」⇒ 会排队）。
// 取原文走 `Fetcher.FetchMessageRaw`，而它 **无条件 dial(acc.IMAPHost:acc.IMAPPort)**
// （mime.go:98）——**IMAP 专用**。这两封是 `em-pop3-…`，POP3 账户。
//
// 所以存在两个互相独立的阻断点，只查一个会得出「改了正则就好了」的错误结论：
//
//	A（主）：POP3 账户的第 2 趟取不到原文 ⇒ 放宽路径根本没机会生效
//	B（次）：就算拿到原文，reAmountTotal 是否真匹配不上「金额共计19元」
//
// 本文件把 A、B 一起测掉。
//
// ## 方法：用**生产的**函数，不复制判定
//
// 只读磁盘上的加密原文缓存（data/email-bodies-raw/<id>.bin），解密 + ParseMIMEMessage，
// 然后调**生产函数本身**：
//
//	ExtractInvoiceLoose(e, body, false)  ← 模拟「第 2 趟跑不了」的现状
//	ExtractInvoiceLoose(e, body, true)   ← 模拟「第 2 趟能跑且拿到附件」
//
// 复制一份判定就会漂移，届时本文件说的就不是生产会做的事了。
//
// ## 门控（只读，不连 PG、不连 IMAP、不写任何数据）
//
//	POCKET_DIAG_TOLL_REPLAY=1
//	POCKET_DIAG_QP_DATADIR=<含 email_master.key 的数据目录>
//
// 语料 id 写死在下面的 tollTargets 里（不是靠扫目录），避免「扫到什么算什么」
// 让结论随磁盘内容漂移。

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// tollTargets 是本次要解释的两封。它们是**从库里查出来的**，不是猜的：
// opencode_pocket.emails 中 subject ILIKE '%发票%' 的 3 封里，2 封无台账行。
var tollTargets = []string{
	"em-pop3-acct-1790870162047413500-2-ZL0014_NzbN7QSM14kuaWoAEvTJP10",
	"em-pop3-acct-1790870162047413500-2-ZL0014_FhfN7AWM14kuaWoAA7LLi10",
}

// reGongJi 只用来回答「正文里到底有没有『共计』这两个字」——
// 上一轮的结论是拿一个手写夹具推出来的，本文件要确认它出现在**真实原文**里。
var reGongJi = regexp.MustCompile(`共计`)

// reAmountTotalWithGongJi 是「假设采纳方案①：把『共计』加进分隔符」后的正则，
// 构造方式与生产一致（同一份 reCurrency），避免手抄走样。
var reAmountTotalWithGongJi = regexp.MustCompile(`(?i)(?:价税合计|合计金额|合计|总额|金额|amount|Amount\s*(?:Due|Total)?)[:：（(]?(?:小写[)）]?|共计)?[:：\s]*(` + reCurrency + `)([0-9][0-9,]*(?:\.[0-9]{1,2})?)\s*(?:元|[圆])?`)

func TestDiagTollInvoiceReplay(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_REPLAY") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_REPLAY=1 to replay the two toll invoices from disk")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if dataDir == "" {
		t.Fatal("POCKET_DIAG_QP_DATADIR 未设置（需要含 email_master.key 的数据目录）")
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey: %v", err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	t.Logf("=== 现状（第 2 趟跑不了：POP3 账户）vs 假设（第 2 趟能跑）===")
	t.Logf("")

	for _, id := range tollTargets {
		path := dataDir + string(os.PathSeparator) + "email-bodies-raw" + string(os.PathSeparator) + id + ".bin"
		blob, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: 读原文缓存失败 %v", id, err)
			continue
		}
		format, payload, err := locateCiphertext(blob)
		if err != nil {
			t.Errorf("%s: locateCiphertext: %v", id, err)
			continue
		}
		dec, err := cr.DecryptString(payload)
		if err != nil {
			t.Errorf("%s: 解密失败: %v", id, err)
			continue
		}
		parsed, err := ParseMIMEMessage([]byte(dec))
		if err != nil {
			t.Errorf("%s: ParseMIMEMessage: %v", id, err)
			continue
		}
		body := parsed.TextBody + "\n" + parsed.HTMLBody
		e := Email{ID: id, Subject: "通行费电子发票"}

		hasAtt := HasInvoiceAttachment(parsed.Attachments)
		curHit := reAmountTotal.MatchString(body)
		gongJiHit := reGongJi.MatchString(body)
		withGJ := reAmountTotalWithGongJi.MatchString(body)

		t.Logf("--- %s ---", id)
		t.Logf("  format=0x%02x  解密 %d 字节  附件 %d 个  HasInvoiceAttachment=%v",
			format, len(dec), len(parsed.Attachments), hasAtt)
		t.Logf("  正文里出现「共计」            : %v", gongJiHit)
		t.Logf("  reAmountTotal（现）匹配        : %v", curHit)
		t.Logf("  加上「共计」后匹配            : %v", withGJ)
		t.Logf("  ExtractInvoiceLoose(body,false): hit=%v  ← 现状", looseHit(t, e, body, false))
		if hasAtt {
			inv, hit := ExtractInvoiceLoose(e, body, true)
			t.Logf("  ExtractInvoiceLoose(body,true) : hit=%v  ← 假设第 2 趟能跑", hit)
			if hit && inv != nil {
				// 建档后金额是多少，直接决定「台账合计会不会还是少这 24.61 元」。
				t.Logf("      ⇒ 建档结果 amount=%v invoiceNo=%q date=%q kind=%q",
					inv.Amount, inv.InvoiceNo, inv.InvoiceDate, inv.Kind)
			}
		} else {
			t.Logf("  ExtractInvoiceLoose(body,true) : （跳过：HasInvoiceAttachment=false，" +
				"放宽路径本来就不会因附件放行）")
		}
		t.Logf("")
	}
}

func looseHit(t *testing.T, e Email, body string, hasAtt bool) bool {
	t.Helper()
	_, hit := ExtractInvoiceLoose(e, body, hasAtt)
	return hit
}

// 判据自检：确认「共计」确实在正文里出现过。
// 若没有，本文件就只是又一次拿手写夹具说事，不构成对 b55d0703 的验证或推翻。
func TestDiagTollReplaySawTheRealBody(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_REPLAY") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_REPLAY=1")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if dataDir == "" {
		t.Fatal("POCKET_DIAG_QP_DATADIR 未设置")
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey: %v", err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	sawGongJi := 0
	for _, id := range tollTargets {
		blob, err := os.ReadFile(dataDir + string(os.PathSeparator) + "email-bodies-raw" +
			string(os.PathSeparator) + id + ".bin")
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		_, payload, err := locateCiphertext(blob)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		dec, err := cr.DecryptString(payload)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		parsed, err := ParseMIMEMessage([]byte(dec))
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		body := parsed.TextBody + "\n" + parsed.HTMLBody
		hit := reGongJi.MatchString(body)
		t.Logf("%s  len(body)=%d  含「共计」=%v", id, len(body), hit)
		if hit {
			sawGongJi++
		}
		// 打印金额附近的原文，让「19.00 / 5.61」这两个数有出处
		for _, kw := range []string{"共计", "金额"} {
			idx := strings.Index(body, kw)
			if idx < 0 {
				continue
			}
			a := idx - 40
			if a < 0 {
				a = 0
			}
			b := idx + 60
			if b > len(body) {
				b = len(body)
			}
			t.Logf("    …%s…", body[a:b])
		}
	}
	if sawGongJi == 0 {
		t.Fatalf("两封正文的可见文本里都没有「共计」——上一轮 b55d0703 用的「金额共计19元」" +
			"很可能来自手写夹具而非真实原文，本文件的所有结论都要重新定性")
	}
}
