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
	"context"
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

// 已 downloaded/filed 的发票**不得**被重新采集。
//
// 这是「文件名格式从 4 段变 5 段」不需要迁移存量文件的前提：
// 采集器只处理 status IN ('new','pending')，已落盘的发票永远不会被再次
// 写入，因此旧名文件不会被改名、不会被新名文件顶掉、也不会产生孤儿。
// 一旦将来把 downloaded 也纳入重跑（例如「重新下载」功能），这条立刻失效，
// 存量文件就必须迁移——所以这里用测试钉住这个前提。
//
// 需要真库（无 POCKET_TEST_POSTGRES_DSN 时 skip）。
func TestListHarvestableInvoices_ExcludesDownloadedAndFiled(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedInvoiceForStats(t, store, "inv-h-new", "CNY", "new", 10, "email-invoices/inv-h-new.pdf")
	seedInvoiceForStats(t, store, "inv-h-pending", "CNY", "pending", 10, "email-invoices/inv-h-pending.pdf")
	seedInvoiceForStats(t, store, "inv-h-done", "CNY", "downloaded", 10, "email-invoices/inv-h-done.pdf")
	seedInvoiceForStats(t, store, "inv-h-filed", "CNY", "filed", 10, "email-invoices/inv-h-filed.pdf")
	seedInvoiceForStats(t, store, "inv-h-failed", "CNY", "failed", 10, "email-invoices/inv-h-failed.pdf")

	got, err := store.ListHarvestableInvoices(ctx, 50)
	if err != nil {
		t.Fatalf("ListHarvestableInvoices: %v", err)
	}
	ids := map[string]bool{}
	for _, inv := range got {
		ids[inv.ID] = true
	}
	if !ids["inv-h-new"] || !ids["inv-h-pending"] {
		t.Fatalf("new/pending 应被采集，实际拿到 %v", ids)
	}
	for _, id := range []string{"inv-h-done", "inv-h-filed", "inv-h-failed"} {
		if ids[id] {
			t.Errorf("%s 不应被重新采集 —— 否则已落盘的旧名文件会被新名顶掉", id)
		}
	}
}

// 无发票号时（采集早期/XML 未解析出）不加那一段，避免留下 `-` 空段。
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

// ─────────────────────────────────────────────────────────────────────
// 第二层防线（2026-10-02 补）：落盘选名。
//
// 上面那一组全部作用在 InvoiceFileName 上——**让名字唯一**。但发票号段
// 只有在**规则层从邮件正文/主题解析出了发票号**时才有值：harvestOne 的
// attachment 与 pdf-url 两条路径拿的都是邮件文本里那个号，
// 发票号只印在 PDF 内部时它是空的。于是仍然会算出同名，
// 而 os.Rename 是替换语义 → 凭证静默丢失。
//
// 本组不试图让 InvoiceFileName 更聪明（那要往名字里塞更多字段，
// 见该函数注释的建议），而是在**落盘那一刻**发现名字已被别的内容占用
// 就换名。判据是**内容**而不是存在性，这样同一张票的重跑仍然幂等。
// ─────────────────────────────────────────────────────────────────────

func writeCollT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// 目标未占用 → 原名。
func TestPickFreeInvoicePath_UnusedName(t *testing.T) {
	dir := t.TempDir()
	got := pickFreeInvoicePath(dir, "其他-某公司-100.00-2026-09-24.pdf", []byte("A"))
	if filepath.Base(got) != "其他-某公司-100.00-2026-09-24.pdf" {
		t.Fatalf("未占用的名字应原样返回，得到 %q", filepath.Base(got))
	}
}

// 内容相同 → 仍用原名。重跑必须幂等，否则每次补跑多出 -2/-3，台账被副本淹没。
func TestPickFreeInvoicePath_SameContentIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	name := "其他-某公司-100.00-2026-09-24.pdf"
	writeCollT(t, filepath.Join(dir, name), "PDF-A")
	for i := 0; i < 3; i++ {
		got := pickFreeInvoicePath(dir, name, []byte("PDF-A"))
		if filepath.Base(got) != name {
			t.Fatalf("第 %d 次重跑改了名：%q → %q（会产生副本）", i+1, name, filepath.Base(got))
		}
	}
}

// 内容不同 → 换名，且原文件分毫不动。**这是本组的核心断言。**
func TestPickFreeInvoicePath_DifferentContentGetsNewName(t *testing.T) {
	dir := t.TempDir()
	name := "其他-某公司-100.00-2026-09-24.pdf"
	writeCollT(t, filepath.Join(dir, name), "PDF-A")

	got := pickFreeInvoicePath(dir, name, []byte("PDF-B"))
	if filepath.Base(got) != "其他-某公司-100.00-2026-09-24-2.pdf" {
		t.Fatalf("撞名时应加序号，得到 %q", filepath.Base(got))
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(raw) != "PDF-A" {
		t.Fatalf("原文件被改动（这正是要防的凭证丢失）：err=%v content=%q", err, string(raw))
	}
}

// 连续三张同名不同内容 → 序号递增，互不覆盖。
func TestPickFreeInvoicePath_ChainsSequenceNumbers(t *testing.T) {
	dir := t.TempDir()
	name := "其他-某公司-100.00-2026-09-24.pdf"
	var got []string
	for _, content := range []string{"A", "B", "C"} {
		p := pickFreeInvoicePath(dir, name, []byte(content))
		got = append(got, filepath.Base(p))
		writeCollT(t, p, content)
	}
	want := []string{
		"其他-某公司-100.00-2026-09-24.pdf",
		"其他-某公司-100.00-2026-09-24-2.pdf",
		"其他-某公司-100.00-2026-09-24-3.pdf",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 张得到 %q，应为 %q", i+1, got[i], want[i])
		}
		raw, err := os.ReadFile(filepath.Join(dir, want[i]))
		if err != nil || string(raw) != string([]string{"A", "B", "C"}[i]) {
			t.Fatalf("第 %d 个文件内容不对：err=%v content=%q", i+1, err, string(raw))
		}
	}
}

// 崩溃恢复：上一轮已写成 -2（写盘后 DB 更新前崩了），重跑必须认回 -2。
func TestPickFreeInvoicePath_ResumesExistingSequenceSlot(t *testing.T) {
	dir := t.TempDir()
	name := "其他-某公司-100.00-2026-09-24.pdf"
	writeCollT(t, filepath.Join(dir, name), "PDF-A")
	writeCollT(t, filepath.Join(dir, "其他-某公司-100.00-2026-09-24-2.pdf"), "PDF-B")

	got := pickFreeInvoicePath(dir, name, []byte("PDF-B"))
	if filepath.Base(got) != "其他-某公司-100.00-2026-09-24-2.pdf" {
		t.Fatalf("应认回已存在的 -2，得到 %q", filepath.Base(got))
	}
}

// 两张**真实形态**的票（发票号只印在 PDF 里、规则层没解析出）算出同一个名字，
// 必须被分开。承重用例：用的就是 InvoiceFileName 的真实输出。
func TestPickFreeInvoicePath_TwoInvoicesWithoutParsedNumberDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	a := &Invoice{Category: "其他", Seller: "某服务商", Amount: 100,
		InvoiceDate: "2026-09-24", Subject: "发票 A"}
	b := &Invoice{Category: "其他", Seller: "某服务商", Amount: 100,
		InvoiceDate: "2026-09-24", Subject: "发票 B"}

	na, nb := InvoiceFileName(a), InvoiceFileName(b)
	if na != nb {
		t.Skipf("InvoiceFileName 已能区分这两张（%q vs %q），本用例前提不成立", na, nb)
	}
	pa := pickFreeInvoicePath(dir, na, []byte("PDF-A"))
	writeCollT(t, pa, "PDF-A")
	pb := pickFreeInvoicePath(dir, nb, []byte("PDF-B"))
	writeCollT(t, pb, "PDF-B")

	if pa == pb {
		t.Fatalf("两张不同内容的发票落在同一路径 %q —— 凭证会被静默覆盖", pa)
	}
	rawA, _ := os.ReadFile(pa)
	rawB, _ := os.ReadFile(pb)
	if string(rawA) != "PDF-A" || string(rawB) != "PDF-B" {
		t.Fatalf("落盘内容不对：A=%q B=%q", string(rawA), string(rawB))
	}
}

// 图片发票走 .jpg，序号必须插在扩展名**之前**（否则变成 x.pdf-2）。
func TestWithInvoiceSeq_InsertsBeforeExtension(t *testing.T) {
	cases := map[string]string{
		"a-b-1.00-2026-09-24.pdf": "a-b-1.00-2026-09-24-2.pdf",
		"a-b-1.00-2026-09-24.jpg": "a-b-1.00-2026-09-24-2.jpg",
		"noext":                   "noext-2",
	}
	for in, want := range cases {
		if got := withInvoiceSeq(in, 2); got != want {
			t.Fatalf("withInvoiceSeq(%q) = %q，应为 %q", in, got, want)
		}
	}
}
