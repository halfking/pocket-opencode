package email

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pipeline_a4_test.go — 每日 A4 网格导出阶段的判据。
//
// 这个阶段默认关闭（Pipeline.A4Grid=0），所以判据必须同时钉住两件事：
//   - 关着的时候**什么都不产出**，且报告写明为什么（否则「没导出」与「没票」长得一样）；
//   - 打开的时候产物、页数、入网格张数、坏件跳过、以及「记不上 exported_at 会
//     每天重导」这条隐患都被显式暴露。
//
// 三条用例的输入都刻意落在**不同**的窄边上，避免一条判据同时被好几个缺陷满足。

// writeA4Fixture 在 <root>/email-invoices/<ws>/ 下造 n 张单页合法发票 PDF。
func writeA4Fixture(t *testing.T, root, ws string, n int) {
	t.Helper()
	dir := filepath.Join(root, "email-invoices", ws)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for i := 0; i < n; i++ {
		makeTestPDF(t, filepath.Join(dir, a4FixtureName(i)), 1)
	}
}

func a4FixtureName(i int) string {
	return string(rune('a'+i)) + ".pdf"
}

// 判据一：选片口径。「已下载但没进过 A4」的才入选；已导出过的、文件不存在的、
// 没 FilePath 的都不能入选——尤其「文件不存在」这条：FilePath 是库里的相对
// 路径，落盘件可能已被外部清掉，直接交给 ExportInvoiceGrid 会让它整批失败
// （export_pdf.go:83-87 缺文件即 error）。
func TestPendingA4Files_SelectsOnlyUnexportedWithRealFile(t *testing.T) {
	root := t.TempDir()
	writeA4Fixture(t, root, "ws-a", 2)
	p := &Pipeline{DataDir: root, A4Grid: 2}

	got := []Invoice{
		{ID: "keep-1", FilePath: filepath.Join("email-invoices", "ws-a", a4FixtureName(0))},
		{ID: "already-exported", FilePath: filepath.Join("email-invoices", "ws-a", a4FixtureName(1)), ExportedAt: 1700000000},
		{ID: "file-gone", FilePath: filepath.Join("email-invoices", "ws-a", "zz-not-on-disk.pdf")},
		{ID: "no-path", FilePath: ""},
	}
	files, ids := p.pendingA4Files(got)

	if len(ids) != 1 || ids[0] != "keep-1" {
		t.Fatalf("selected ids = %v, want exactly [keep-1]", ids)
	}
	if len(files) != 1 {
		t.Fatalf("selected files = %v, want exactly 1", files)
	}
	want := filepath.Join(root, "email-invoices", "ws-a", a4FixtureName(0))
	if files[0] != want {
		t.Fatalf("selected file = %q, want %q (must be joined onto DataDir)", files[0], want)
	}
}

// 判据二：默认关闭。零张票落盘，且 A4ExportSkip 非空。
// 断言的是「exports 目录里没有新文件」这个**磁盘事实**，不是只读报告字段——
// 报告字段是这条代码自己写的，用它证明自己没写文件是循环论证。
func TestExportPendingA4_DisabledByDefaultProducesNothing(t *testing.T) {
	root := t.TempDir()
	writeA4Fixture(t, root, "ws-a", 1)
	p := &Pipeline{DataDir: root} // A4Grid 留零值 = 默认关闭

	rep := &PipelineReport{}
	p.exportPendingA4(context.Background(), rep, "user-a", "ws-a", []Invoice{
		{ID: "i1", FilePath: filepath.Join("email-invoices", "ws-a", a4FixtureName(0))},
	})

	if rep.A4ExportPath != "" || rep.A4ExportCount != 0 {
		t.Fatalf("disabled stage still exported: path=%q count=%d", rep.A4ExportPath, rep.A4ExportCount)
	}
	if rep.A4ExportSkip == "" {
		t.Fatal("disabled stage left A4ExportSkip empty: then 'not exported' is indistinguishable from 'nothing to export'")
	}
	if !strings.Contains(rep.A4ExportSkip, "disabled") {
		t.Fatalf("A4ExportSkip = %q, want it to name the disabled stage", rep.A4ExportSkip)
	}
	assertNoExportsDir(t, root)
}

// 判据三：打开时产出真 A4。三张单页票 + grid=2 ⇒ 输出 1 页（4 个格子放 3 张）。
// 页数是独立算出来的期望值（不调用被测函数推导），并且顺带钉住输出目录。
func TestExportPendingA4_OnProducesOnePageA4AndFlagsUnrecordedMarker(t *testing.T) {
	root := t.TempDir()
	writeA4Fixture(t, root, "ws-b", 3)
	p := &Pipeline{DataDir: root, A4Grid: 2}

	rep := &PipelineReport{}
	p.exportPendingA4(context.Background(), rep, "user-b", "ws-b", []Invoice{
		{ID: "i1", FilePath: filepath.Join("email-invoices", "ws-b", a4FixtureName(0))},
		{ID: "i2", FilePath: filepath.Join("email-invoices", "ws-b", a4FixtureName(1))},
		{ID: "i3", FilePath: filepath.Join("email-invoices", "ws-b", a4FixtureName(2))},
	})

	if rep.A4ExportPath == "" {
		t.Fatalf("stage reported no output; errors=%v", rep.Errors)
	}
	if rep.A4ExportCount != 3 {
		t.Fatalf("A4ExportCount = %d, want 3 (all three files are valid)", rep.A4ExportCount)
	}
	// 必须是 1 页：3 张票在 2x2 网格里只占 3 个格子，出 1 页；出 2 页说明
	// 网格化没生效（每张票各占一页），那就不是需求要的「可直接剪裁」。
	if got := pdfPageCount(t, rep.A4ExportPath); got != 1 {
		t.Fatalf("A4 page count = %d, want 1", got)
	}
	outDir := filepath.Join(root, "email-invoices", "exports", "ws-b")
	if filepath.Dir(rep.A4ExportPath) != outDir {
		t.Fatalf("output landed in %q, want under %q", filepath.Dir(rep.A4ExportPath), outDir)
	}

	// 没有 Store ⇒ 记不上 exported_at。这必须**响**地报出来：静默跳过的话，
	// 这 3 张票明天会被重新选中、同一个 A4 明天再出一份。
	if rep.A4ExportMarked != 0 {
		t.Fatalf("A4ExportMarked = %d, want 0 with no store", rep.A4ExportMarked)
	}
	if !errorsMention(rep.Errors, "exported_at not recorded") {
		t.Fatalf("unrecorded exported_at was not reported as an error; errors=%v", rep.Errors)
	}
}

// 判据四：坏件不得被算成「已导出」。一张畸形 PDF + 两张好票 ⇒ 入网格 2、跳过 1，
// 且跳过的名字出现在报告里。手写 69 字节残件（只有 %PDF 头没有页树）当坏件。
func TestExportPendingA4_SkippedFileIsNotCountedAsExported(t *testing.T) {
	root := t.TempDir()
	writeA4Fixture(t, root, "ws-c", 2)
	dir := filepath.Join(root, "email-invoices", "ws-c")
	broken := filepath.Join(dir, "broken.pdf")
	if err := os.WriteFile(broken, []byte("%PDF-1.4\n%%EOF\n"), 0o600); err != nil {
		t.Fatalf("write broken pdf: %v", err)
	}
	p := &Pipeline{DataDir: root, A4Grid: 2}

	rel := func(n string) string { return filepath.Join("email-invoices", "ws-c", n) }
	rep := &PipelineReport{}
	p.exportPendingA4(context.Background(), rep, "user-c", "ws-c", []Invoice{
		{ID: "ok-1", FilePath: rel(a4FixtureName(0))},
		{ID: "broken", FilePath: rel("broken.pdf")},
		{ID: "ok-2", FilePath: rel(a4FixtureName(1))},
	})

	if rep.A4ExportCount != 2 {
		t.Fatalf("A4ExportCount = %d, want 2 (the malformed file must not enter the grid)", rep.A4ExportCount)
	}
	if len(rep.A4ExportSkipped) != 1 || !strings.Contains(rep.A4ExportSkipped[0], "broken") {
		t.Fatalf("A4ExportSkipped = %v, want exactly the broken file", rep.A4ExportSkipped)
	}
}

func assertNoExportsDir(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "email-invoices", "exports")
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("stage created %s while disabled", dir)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat exports dir: %v", err)
	}
}

func errorsMention(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
