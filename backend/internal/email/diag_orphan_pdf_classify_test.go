package email

// diag_orphan_pdf_classify_test.go — classifyOrphanPDF 的判据必须指向 PDF 的
// **内容**，而不是它的字节。
//
// ## 为什么要单独测
//
// classifyOrphanPDF 唯一的职责是防止「台账漏了一张 1280 的真发票」这种
// **假结论**：那两份 1280 的 PDF 是 `gen_fixture_invoice_test.go` 生成的
// IMAP 测试夹具，正文里写着 "VAT E-INVOICE (IMAP fixture)"，但那段文字在
// /FlateDecode 压缩流里。
//
// 第一版实现只扫裸字节，于是把两份夹具报成「疑似真实、值得看一眼」——
// 也就是这份诊断自己产出了它本该防止的那个假结论。缺陷形态是「判据指向了
// 容器而不是内容」。
//
// ## 样本为什么是固定载荷
//
// 夹具标记必须**只存在于压缩流中**，否则本用例在缺陷存在时也会通过。
// 但这个性质不能交给 deflate：短文本会被原样存成字面量（标记仍出现在压缩
// 字节里），只有定长 Huffman 把短码位级交织时才藏得住——那是实现细节，会
// 随 zlib 版本变。所以这里嵌一份**事先验证过**的定长载荷：它的裸字节里
// 搜不到 "IMAP fixture"，解压后才有。前提由构造保证，不依赖压缩器行为。
// 用例里仍然断言这个前提，这样哪天载荷被误换，测试会立刻报出来。

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fixtureStreamB64 是一段 zlib 压缩的 PDF 内容流。解压后首行含
// "VAT E-INVOICE (IMAP fixture) by Fixture Cloud Services Co., Ltd."；
// **压缩字节本身不含这些字**（生成时已验证，见文件头注释）。
const fixtureStreamB64 = "eJztzN0KgkAQBeD7nuJc7kLZbER6q7KC0B+0+AC5KxqaYBr19unqU8RezHBg5nyRwjYREDuoAntCQASlwbJQQW7Sc3ZJYwmWnsIriurTD53huH+RzBlx3Q4aN9O9q9y8ELfeGsdee1w9INUqWnhaeD+YeQKhwQH1OHOaMtldcoxlgelH2JJPyBuwunoae3Owgx38b/AP"

// fixtureMarker 是只存在于压缩流里的那个标记。
const fixtureMarker = "IMAP FIXTURE"

// writeStreamPDF 用给定（已压缩的）内容流造一份最小 PDF。
func writeStreamPDF(t *testing.T, dir, name string, stream []byte) string {
	t.Helper()
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	pdf.WriteString("1 0 obj<</Type/Catalog>>endobj\n")
	pdf.WriteString("3 0 obj<</Type/Page/Parent 1 0 R>>endobj\n")
	pdf.WriteString("4 0 obj<</Filter /FlateDecode /Length " +
		strconv.Itoa(len(stream)) + ">>\nstream\n")
	pdf.Write(stream)
	pdf.WriteString("\nendstream\nendobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pdf.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestClassifyOrphanPDF(t *testing.T) {
	dir := t.TempDir()

	stream, err := base64.StdEncoding.DecodeString(fixtureStreamB64)
	if err != nil {
		t.Fatalf("内置载荷解不开 base64: %v", err)
	}

	fixturePDF := writeStreamPDF(t, dir, "fixture.pdf", stream)

	t.Run("前提：标记只在压缩流里，裸字节搜不到", func(t *testing.T) {
		raw, err := os.ReadFile(fixturePDF)
		if err != nil {
			t.Fatal(err)
		}
		// 前提一：整份 PDF 的裸字节里搜不到标记——判据若只扫它就会失手。
		if strings.Contains(strings.ToUpper(string(raw)), fixtureMarker) {
			t.Fatal("整份 PDF 的裸字节里就能搜到标记——本用例在缺陷存在时也会通过，" +
				"需要换一份真正位级交织的载荷")
		}
		// 前提二：解压整份 PDF 后能搜到它（走的就是 classifyOrphanPDF 那条路）。
		if !strings.Contains(strings.ToUpper(inflateAllStreams(raw)), fixtureMarker) {
			t.Fatalf("解压后反而找不到 %q：载荷坏了，用例前提不成立", fixtureMarker)
		}
	})

	t.Run("夹具：标记只在压缩流里也必须判成夹具", func(t *testing.T) {
		if got := classifyOrphanPDF(fixturePDF, 4096); got != "夹具" {
			t.Fatalf("classifyOrphanPDF = %q，want 夹具。判据只扫了裸字节、没解压内容流——"+
				"它会把测试夹具报成「真实候选」，也就是它本该防止的那个假结论", got)
		}
	})

	t.Run("真实发票判成真实候选", func(t *testing.T) {
		// 同样的容器形状，但解压后是正常发票文本。
		real := buildStream(t, "BT /F1 12 Tf 40 800 Td (Invoice No 25332000000123456789 Seller 杭州开轩科技) Tj ET")
		p := writeStreamPDF(t, dir, "real.pdf", real)
		if got := classifyOrphanPDF(p, int64(len(real))+200); got != "真实候选" {
			t.Fatalf("classifyOrphanPDF = %q，want 真实候选", got)
		}
	})

	t.Run("69 字节 0 页的退化件判成退化件", func(t *testing.T) {
		// 与真实库里那两个 0.00 文件同形：有 %PDF 头、有 Catalog、
		// 没有 /Type/Page，且极小。
		p := filepath.Join(dir, "degenerate.pdf")
		body := "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := classifyOrphanPDF(p, int64(len(body))); got != "退化件" {
			t.Fatalf("classifyOrphanPDF = %q，want 退化件", got)
		}
	})

	t.Run("缺失文件不 panic", func(t *testing.T) {
		if got := classifyOrphanPDF(filepath.Join(dir, "nope.pdf"), 0); got != "读取失败" {
			t.Fatalf("classifyOrphanPDF = %q，want 读取失败", got)
		}
	})
}

// buildStream 把文本压成 zlib 流，供上面造样本用。
func buildStream(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(text)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}
