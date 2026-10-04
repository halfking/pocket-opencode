package email

// invoice_filename_fabricated_date_test.go —— 文件名里的日期不许是编的。
//
// ## 缺陷（2026-10-04 08:00 真实产物）
//
// `InvoiceFileName` 原本在 `inv.InvoiceDate == ""` 时填 `time.Now()`，
// 也就是**下载当天**。真实落盘的那一份：
//
//	data/email-invoices/ws_user-admin/通信-X-8.00-2026-10-04.pdf
//	data/email-invoices/exports/ws_user-admin/invoices-summary-20261004-080022.md
//	  | 通信 | X | 8.00 USD |  |  | downloaded | 已核验 |      ← 「日期」列是空的
//
// 同一份数据，文件名里有个像模像样的日期，汇总单里明说没有。
// 财务看文件名会以为这是 10-04 开出的票 —— 而库里根本没解析出日期。
//
// 与 round37 §35（金额=信用额度、日期=到期还款日）同类：
// **一个看起来权威的错值，比留空危险得多。**
//
// ## 判别式
//
// 「值是不是编的」要问「换一个时间它会不会变」。当天日期就是这种值：
// 隔天跑一次，同一张票会得到另一个文件名。顺带那也是非确定性缺陷——
// `pickFreeInvoicePath` 按「目标名 + 内容相同」去重，名字变了目标就不存在，
// 于是写出第二份副本。所以这里同时钉住「确定性」和「不含当天日期」。
import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// 缺陷复现：日期未知时不得出现任何具体日期。
func TestInvoiceFileName_NoFabricatedDateWhenInvoiceDateEmpty(t *testing.T) {
	inv := &Invoice{
		Category: "通信", Seller: "X", Amount: 8, Currency: "USD",
		InvoiceNo: "", InvoiceDate: "",
	}
	got := InvoiceFileName(inv)

	// 真实产物里出现过的那一段。
	if strings.Contains(got, "2026-10-04") {
		t.Errorf("文件名带上了下载日 2026-10-04：%q\n"+
			"  台账「日期」列是空的，文件名不该替它编一个。", got)
	}
	// 更强的一条：不带**任何** YYYY-MM-DD 形态的日期。
	if mm := regexpDate.FindString(got); mm != "" {
		t.Errorf("文件名里出现日期形态 %q：%q\n"+
			"  发票日期未知时只能是显式占位，不能是任何具体日子。", mm, got)
	}
	if !strings.Contains(got, "未知日期") {
		t.Errorf("文件名=%q，want 含显式占位「未知日期」——留空会被误读成解析失败", got)
	}
}

// 确定性：同一个发票（日期未知）反复取名必须完全一致。
//
// 这一条与「不含当天日期」是两件事，但都由同一个 `time.Now()` 造成。
// 只钉「不含当天日期」的话，一个改成 `time.Now().AddDate(0,0,-1)`
// 之类的实现照样能过。
func TestInvoiceFileName_IsDeterministicWhenInvoiceDateEmpty(t *testing.T) {
	inv := &Invoice{Category: "通信", Seller: "X", Amount: 8, Currency: "USD"}
	first := InvoiceFileName(inv)
	for i := 0; i < 5; i++ {
		if got := InvoiceFileName(inv); got != first {
			t.Fatalf("第 %d 次取名不一致：%q vs %q —— 名字不稳定会让同一张票重试时写出副本",
				i, got, first)
		}
	}
	// 换一天也必须一样。直接断言「名字里不含今天的日期」是最容易读懂的形式。
	today := time.Now().Format("2006-01-02")
	if strings.Contains(first, today) {
		t.Errorf("文件名 %q 含今天（%s）的日期，取名依赖了当前时间", first, today)
	}
}

// 反向保护：日期**已知**时必须原样用日期，不能被占位符吞掉。
func TestInvoiceFileName_KeepsRealInvoiceDate(t *testing.T) {
	cases := []struct{ date, want string }{
		{"2026-09-15", "2026-09-15"},
		{"2026/09/15", "2026-09-15"}, // 斜杠会被换成连字符
	}
	for _, c := range cases {
		inv := &Invoice{Category: "其他", Seller: "云服务开票中心", Amount: 1280, InvoiceDate: c.date}
		got := InvoiceFileName(inv)
		if !strings.Contains(got, c.want) {
			t.Errorf("InvoiceDate=%q → 文件名 %q，want 含 %q", c.date, got, c.want)
		}
		if strings.Contains(got, "未知日期") {
			t.Errorf("InvoiceDate=%q 却用了占位符：%q", c.date, got)
		}
	}
}

// regexpDate 只为「文件名里有没有 YYYY-MM-DD 形态的日期」服务。
var regexpDate = regexpMustCompileDate()

func regexpMustCompileDate() *regexp.Regexp {
	return regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
}
