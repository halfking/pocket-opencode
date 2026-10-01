package email

// invoice_filename_collision_test.go — 规范文件名撞名会静默覆盖发票文件。
//
// 缺陷（2026-10-01 实测发现）：InvoiceFileName 只用
// {费用类型}-{对方单位}-{金额}-{日期}，**不含发票号**。实测三张不同的票
// 得到同一个文件名：
//
//	云服务-AWS-100.00-2026-09-15.pdf   <- 票 A（CNY，发票号 CN-1）
//	云服务-AWS-100.00-2026-09-15.pdf   <- 票 B（USD，发票号 US-1）
//	云服务-AWS-100.00-2026-09-15.pdf   <- 票 C（CNY，发票号 CN-2）
//
// saveInvoiceFile 用 `os.Rename(tmp, path)` 落盘，**同名直接覆盖**，不报错。
// 结果：两行 DB 记录都 status='downloaded'、file_path 指向同一个文件，
// 但磁盘上只剩**最后写入的那一张票**。前一张的凭证文件永久丢失，且
// 列表里两张看起来都正常——用户点开看到的是另一张票的内容。
//
// 这不是理论：真实场景里「同一天同一供应商同金额」很常见
// （充值 100 元两次、订阅续费、重开发票），需求原文的命名规则
// `{费用类型}-{对方单位}-{金额}-{日期}` 本身不足以唯一标识一张票。
//
// 需求原文确实没规定发票号，但**文件名冲突导致凭证丢失是缺陷**，
// 修法是让名字唯一（加发票号），不是改需求的格式约定。
//
// 负控：把发票号从文件名里去掉 -> 本文件转红。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// 撞名：不同的票必须得到不同的文件名。
func TestInvoiceFileName_DistinctInvoicesDoNotCollide(t *testing.T) {
	base := Invoice{
		Category: "云服务", Seller: "AWS", Amount: 100,
		InvoiceDate: "2026-09-15",
	}
	cases := []struct {
		name string
		inv  Invoice
	}{
		{"不同币种", Invoice{InvoiceNo: "CN-1", Currency: "CNY"}},
		{"同币种不同发票号", Invoice{InvoiceNo: "CN-2", Currency: "CNY"}},
		{"外币", Invoice{InvoiceNo: "US-1", Currency: "USD"}},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		inv := base
		inv.InvoiceNo = tc.inv.InvoiceNo
		inv.Currency = tc.inv.Currency
		name := InvoiceFileName(&inv)
		if prev, dup := seen[name]; dup {
			t.Fatalf("%s 与 %s 撞名：%q —— 落盘时 os.Rename 会直接覆盖，凭证丢失",
				tc.name, prev, name)
		}
		seen[name] = tc.name
	}
}

// 真实场景：同一天同一供应商同金额的两次充值（不同发票号）。
func TestInvoiceFileName_SameDaySameAmountDifferentInvoiceNo(t *testing.T) {
	a := &Invoice{Category: "云服务", Seller: "AWS", Amount: 100, Currency: "CNY",
		InvoiceNo: "26332000008261110741", InvoiceDate: "2026-09-15"}
	b := &Invoice{Category: "云服务", Seller: "AWS", Amount: 100, Currency: "CNY",
		InvoiceNo: "26332000008261110742", InvoiceDate: "2026-09-15"}
	if InvoiceFileName(a) == InvoiceFileName(b) {
		t.Fatalf("两张不同发票（%s / %s）得到同一文件名 %q —— 后写入的会覆盖前者",
			a.InvoiceNo, b.InvoiceNo, InvoiceFileName(a))
	}
}

// 落盘层面的直接证据：同名写入确实覆盖，不报错。
// 这条把「os.Rename 会覆盖」从推断变成实测。
func TestSaveInvoiceFile_SameNameOverwritesSilently(t *testing.T) {
	dir := t.TempDir()
	inv := &Invoice{
		Category: "云服务", Seller: "AWS", Amount: 100, Currency: "CNY",
		InvoiceNo: "CN-1", InvoiceDate: "2026-09-15", WorkspaceID: "ws-1",
	}
	name := InvoiceFileName(inv)
	path := filepath.Join(dir, name)

	// 第一次写入
	if err := os.WriteFile(path, []byte("FIRST-TICKET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 第二次写入同名文件（模拟另一张票落到同一路径）
	if err := os.WriteFile(path+".tmp", []byte("SECOND-TICKET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "SECOND") {
		t.Fatalf("expected overwrite to happen (this is the bug), got %q", string(got))
	}
	// 这条测试的意义：证明「同名 -> 静默覆盖」是真实行为，
	// 所以 InvoiceFileName 必须保证唯一。上面的用例才是防线。
}

// 超长字段不得把全路径顶过 Windows MAX_PATH(260)。
//
// 回归护栏：加发票号后名字变长，而 sanitizeFileName 对每个字段各截到 60，
// 四段拼起来最坏 ~208 字节，加数据目录前缀实测全路径 269 字节 > 260，
// os.WriteFile 会直接报 "File name too long" 失败，采集器 markRetry 重试白试。
func TestInvoiceFileName_BoundedLengthForWindowsMaxPath(t *testing.T) {
	long := strings.Repeat("长", 60) // sanitizeFileName 的单字段上限
	inv := &Invoice{
		Category: long, Seller: long, Amount: 1234567.89, Currency: "CNY",
		InvoiceNo: long, InvoiceDate: "2026-09-15",
	}
	name := InvoiceFileName(inv)
	const dataDirPrefix = `C:\workspace\openpocket-wt-email\data\email-invoices\default\`
	if full := len(dataDirPrefix) + len(name); full > 260 {
		t.Fatalf("full path %d bytes exceeds Windows MAX_PATH 260 (name=%d): %q", full, len(name), name)
	}
	// 超长时优先砍发票号，必须保住需求约定的可读部分
	if !strings.HasPrefix(name, long[:20]) {
		t.Errorf("超长时应保留开头的费用类型段: %q", name)
	}
	// 切出来的名字必须是合法 UTF-8（不能切在多字节字符中间）
	if !utf8.ValidString(name) {
		t.Errorf("截断后不是合法 UTF-8: %q", name)
	}
	// 扩展名必须还在
	if !strings.HasSuffix(name, ".pdf") {
		t.Errorf("截断后丢了扩展名: %q", name)
	}
	// 不能以连字符或点结尾（避免 `-` 或 `..` 之类的怪名字）
	base := strings.TrimSuffix(name, ".pdf")
	if strings.HasSuffix(base, "-") || strings.HasSuffix(base, ".") {
		t.Errorf("截断后以分隔符结尾: %q", name)
	}
}

// 无发票号时（采集早期/XML 未解析出）不加分隔段，避免 `-` 空段。
func TestInvoiceFileName_NoInvoiceNoStillDeterministic(t *testing.T) {
	inv := &Invoice{Category: "其他", Seller: "某供应商", Amount: 10, Currency: "CNY",
		InvoiceDate: "2026-09-15"}
	got := InvoiceFileName(inv)
	if got == "" {
		t.Fatal("文件名不应为空")
	}
	// 同一输入必须稳定（幂等：重跑采集不应产生不同名字）
	if again := InvoiceFileName(inv); again != got {
		t.Fatalf("同一发票两次生成的名字不同：%q vs %q", got, again)
	}
	// 绝不能出现空段（双连字符）或路径分隔符
	if strings.Contains(got, "--") {
		t.Errorf("文件名含空段: %q", got)
	}
	if strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("文件名含非法字符: %q", got)
	}
}
