package email

// invoice_pdf_cjk_output_test.go —— 验「产出的 PDF 里真的嵌了能画中文的字体」。
//
// ## 为什么已有的测试不够
//
// invoice_font_cjk_test.go 覆盖的是**选字体**那一层：候选表里挑中的文件
// 确实含中文字形（fontHasCJK 读源字体的 cmap）。但那只证明**输入**没问题，
// 不证明**输出**里有字形。
//
// 中间那一环正是本仓库已经栽过一次的：go-pdf/fpdf 的 AddUTF8Font 对
// 「字体里没有这个字形」**不报错**，照常输出一张合法 PDF，只是每个中文字符
// 都画成空白。实测 2026-10-02：arial.ttf 渲染出的 PDF 24,649 bytes，
// 是一张完全正常的文件——不打开看就发现不了。
//
// ## 为什么不能用「抽取 PDF 文本」当判据
//
// 实测：simhei.pdf 与 arial.pdf 用同样的工具抽出的文本**逐字相同**。
// fpdf 写的是内嵌子集字体 + 自定义 ToUnicode 映射，`.notdef` 的空白字符
// 照样能被映射回 Unicode。文本层存在**不等于**字形存在。
// （同一条坑此前在 `GetStringWidth` 上踩过：对 UTF8 字体恒返回 0，连 "AAAA" 都是 0。
//   宽度、文本抽取、文件大小三者都不能当判据。）
//
// ## 本文件的判据
//
// 直接把 PDF 里内嵌的字体程序（/FontFile2）解压出来，对**它**跑 fontHasCJK。
// 嵌入的是子集：真正画过的字形才会被写进去，所以「子集里有 CJK 字形」
// 等价于「这些中文字符有东西可画」。
//
// 正控用真实 CJK 字体，负控用只有拉丁字形的 arial.ttf。**两个必须给出不同的
// 答案**——如果它们一样，本文件就是一条恒真判据。

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// latinOnlyFont 找一个只有拉丁字形的系统字体，用作负控。
// latinOnlyFont 找一本**本机确实存在**且不含 CJK 字形的字体做负控。
//
// 2026-10-03 修：原实现只判 fontHasCJK(p)，而 fontHasCJK 对**不存在的文件**
// 同样返回 false，于是 `!fontHasCJK(p)` 在第一个候选（Windows 的 arial.ttf）
// 上就成立并被直接返回——在 Linux 上返回的是一个根本不存在的路径，
// 紧接着 renderProbePDF 加载失败 t.Fatalf。后果有两层：
//  1. 非 Windows 宿主上这条负控恒红，go test ./... 过不去；
//  2. 更糟的是那行 t.Skip("本机找不到可用的拉丁字体做负控") **永远不可达**——
//     「环境不支持」被伪装成了「负控失败」，正是负控最不该有的那种失效。
//
// 所以必须先 stat 再判字形，并补上 Linux 上真实存在的拉丁字体候选。
func latinOnlyFont(t *testing.T) string {
	t.Helper()
	win := os.Getenv("SystemRoot")
	if win == "" {
		win = `C:\Windows`
	}
	cands := []string{
		filepath.Join(win, "Fonts", "arial.ttf"),
		filepath.Join(win, "Fonts", "arialbd.ttf"),
		filepath.Join(win, "Fonts", "tahoma.ttf"),
		filepath.Join(win, "Fonts", "segoeui.ttf"),
		// Linux（Debian/Ubuntu 的 fonts-dejavu / fonts-liberation）
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
	}
	for _, p := range cands {
		if !fontFileExists(p) {
			continue
		}
		if !fontHasCJK(p) {
			return p
		}
	}
	t.Skip("本机找不到可用的拉丁字体做负控（本测试只验证判据的区分力，跳过不影响正控）")
	return ""
}

func fontFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

var fontFile2Re = regexp.MustCompile(`/FontFile2\s+(\d+)\s+0\s+R`)

// embeddedFontPrograms 从 PDF 字节里取出所有 /FontFile2 引用的字体程序。
//
// 只做最小解析：找引用号 → 定位该间接对象 → 取 stream 段 →
// FlateDecode 则解压。不追求通用 PDF 解析器，只需要能吃下 go-pdf/fpdf 的输出。
func embeddedFontPrograms(t *testing.T, pdf []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for _, m := range fontFile2Re.FindAllSubmatch(pdf, -1) {
		objNum, err := strconv.Atoi(string(m[1]))
		if err != nil {
			continue
		}
		objStart := bytes.Index(pdf, []byte(strconv.Itoa(objNum)+" 0 obj"))
		if objStart < 0 {
			continue
		}
		rest := pdf[objStart:]
		sIdx := bytes.Index(rest, []byte("stream"))
		if sIdx < 0 {
			continue
		}
		// stream 关键字后是 \r\n 或 \n
		body := rest[sIdx+len("stream"):]
		body = bytes.TrimPrefix(body, []byte("\r\n"))
		body = bytes.TrimPrefix(body, []byte("\n"))
		eIdx := bytes.Index(body, []byte("endstream"))
		if eIdx < 0 {
			continue
		}
		raw := bytes.TrimRight(body[:eIdx], "\r\n")
		// 是否压缩：字典在 stream 之前
		dict := rest[:sIdx]
		if bytes.Contains(dict, []byte("/FlateDecode")) {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Logf("FontFile2 %d: zlib 打开失败: %v", objNum, err)
				continue
			}
			dec, err := io.ReadAll(zr)
			_ = zr.Close()
			if err != nil {
				t.Logf("FontFile2 %d: 解压失败: %v", objNum, err)
				continue
			}
			out = append(out, dec)
			continue
		}
		out = append(out, raw)
	}
	return out
}

func renderProbePDF(t *testing.T, fontPath string) []byte {
	t.Helper()
	inv := &Invoice{
		InvoiceNo:   "26332000008261110741",
		Seller:      "杭州创客家投资管理有限公司",
		Amount:      3500,
		Currency:    "CNY",
		InvoiceDate: "2026-09-24",
	}
	raw, err := RenderInvoiceXMLPDF(fontPath, inv, []byte(`<Invoice/>`))
	if err != nil {
		t.Fatalf("RenderInvoiceXMLPDF(%s): %v", fontPath, err)
	}
	return raw
}

// 正控：用真有 CJK 字形的字体渲染，产物里内嵌的字体子集必须也有 CJK 字形。
func TestRenderInvoiceXMLPDF_OutputEmbedsCJGGlyphs(t *testing.T) {
	font := FindChineseFont("")
	if font == "" {
		t.Skip("本机没有可用的 CJK 字体，无法验正控")
	}
	t.Logf("使用字体 %s", font)

	pdf := renderProbePDF(t, font)
	progs := embeddedFontPrograms(t, pdf)
	if len(progs) == 0 {
		t.Fatalf("没从 %d 字节的 PDF 里解析出任何 /FontFile2 字体程序", len(pdf))
	}
	found := false
	for i, p := range progs {
		tmp := filepath.Join(t.TempDir(), "embedded.ttf")
		if err := os.WriteFile(tmp, p, 0o600); err != nil {
			t.Fatalf("写临时字体: %v", err)
		}
		has := fontHasCJK(tmp)
		t.Logf("内嵌字体程序 #%d：%d bytes，fontHasCJK=%v", i, len(p), has)
		if has {
			found = true
		}
	}
	if !found {
		t.Errorf("渲染用了含中文字形的字体（%s），但产出的 PDF 里没有任何一个内嵌字体子集含 CJK 字形 —— "+
			"中文仍会被画成空白", font)
	}
}

// 负控：只有拉丁字形的字体渲染出的产物，**不得**含 CJK 字形。
//
// 这条不是为了证明「arial 是坏的」，而是为了证明上一条不是恒真判据：
// 两者必须给出不同答案。若这里也 true，说明内嵌字体的提取或判据坏了。
func TestRenderInvoiceXMLPDF_LatinFontOutputHasNoCJGGlyphs(t *testing.T) {
	latin := latinOnlyFont(t)
	t.Logf("负控字体 %s", latin)

	pdf := renderProbePDF(t, latin)
	progs := embeddedFontPrograms(t, pdf)
	if len(progs) == 0 {
		t.Skipf("没解析出 /FontFile2（%d 字节），本条不构成有效负控", len(pdf))
	}
	for i, p := range progs {
		tmp := filepath.Join(t.TempDir(), "embedded.ttf")
		if err := os.WriteFile(tmp, p, 0o600); err != nil {
			t.Fatalf("写临时字体: %v", err)
		}
		if fontHasCJK(tmp) {
			t.Errorf("内嵌字体程序 #%d（%d bytes）被判定含 CJK 字形，但源字体 %s 没有中文——"+
				"判据或提取环节有 bug，本文件的两条用例会一起失去区分力", i, len(p), latin)
		}
	}
}
