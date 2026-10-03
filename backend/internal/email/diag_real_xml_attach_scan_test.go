package email

// diag_real_xml_attach_scan_test.go — 真实邮箱里到底有没有**独立 XML 附件**
// （不是 zip 里的那种），需求「原邮件中有 XML 数据格式（可解析后重新渲染）」
// 这条腿在真实数据上跑过没有。
//
// 门控：POCKET_DIAG_XML_SCAN=1 + POCKET_DIAG_QP_DATADIR
//
// ## 为什么查这个
//
// 需求原文明确列了两条取票路径：「有 PDF 下载地址（可直接下载已有 PDF），
// 也有 XML 数据格式（可解析后重新渲染）」。第一条（PDF 直下 / PDF 附件 /
// ZIP 里的票面）本轮都在真实数据上验过了；第二条对应 harvestOne 的
// `xml-render` 分支。生产库里 7 张票的 file_source 分布是
// attachment×3 / zip-pdf×2 / pdf-url×1 / 空×1 —— **没有一张 xml-render**。
//
// 但「没有产出」有两种完全不同的原因，必须区分：
//   (a) 真实邮件里根本没有独立 XML 附件 ⇒ 这条腿**在当前数据上不可达**，
//       那么「已实现并测过」就只能算单测层面的说法；
//   (b) 有独立 XML 附件却没走到 ⇒ 那是真缺陷。
//
// 这个诊断只读：body cache 只 Get 不 Put，不联网、不写库。

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type xmlScanStat struct {
	files          int
	decryptErr     int
	parseErr       int
	withStandalone int
	withZip        int
	withPDF        int
	withImage      int
	xmlRenderable  int // 独立 XML 且 zip 里没有票面 PDF ⇒ 会走 xml-render
	samples        []string
}

func TestDiagRealXMLAttachmentScan(t *testing.T) {
	if os.Getenv("POCKET_DIAG_XML_SCAN") != "1" {
		t.Skip("set POCKET_DIAG_XML_SCAN=1 (and POCKET_DIAG_QP_DATADIR) to scan real cached bodies")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if dataDir == "" {
		t.Fatal("POCKET_DIAG_QP_DATADIR 未设置")
	}
	key, kerr := EnsureMasterKey("", dataDir)
	if kerr != nil {
		t.Fatalf("EnsureMasterKey: %v", kerr)
	}
	cr, cerr := NewCrypto(key)
	if cerr != nil {
		t.Fatalf("NewCrypto: %v", cerr)
	}
	cache := NewFileBodyCache(dataDir, cr)
	if cache == nil {
		t.Fatal("FileBodyCache 为 nil")
	}

	// 缓存文件名 = <emailID>.bin，文件头 8 字节大端 UID。
	dir := filepath.Join(dataDir, "email-bodies-raw")
	entries, derr := os.ReadDir(dir)
	if derr != nil {
		t.Fatalf("读缓存目录失败：%v", derr)
	}

	var st xmlScanStat
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		st.files++
		emailID := strings.TrimSuffix(e.Name(), ".bin")
		head, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil || len(head) < 8 {
			st.decryptErr++
			continue
		}
		uid := int64(binary.BigEndian.Uint64(head[:8]))
		raw, gerr := cache.Get(emailID, uid)
		if gerr != nil || len(raw) == 0 {
			st.decryptErr++
			continue
		}
		parsed, perr := ParseMIMEMessage(raw)
		if perr != nil {
			st.parseErr++
			continue
		}
		var standaloneXML, pdf, image bool
		for _, att := range parsed.Attachments {
			switch {
			case isXMLFile(att):
				standaloneXML = true
			case isPDFBytes(att.Data):
				pdf = true
			case isImageBytes(att.Data):
				image = true
			}
		}
		zip := zipAttachmentContents(parsed.Attachments)
		if standaloneXML {
			st.withStandalone++
			if len(st.samples) < 8 {
				st.samples = append(st.samples, emailID+" 主题="+parsed.Subject)
			}
		}
		if !zip.Empty() {
			st.withZip++
		}
		if pdf {
			st.withPDF++
		}
		if image {
			st.withImage++
		}
		// harvestOne 的取件顺序：zip 里有票面 PDF 就用 zip-pdf；否则若有独立
		// XML 附件且配置了 XMLRenderer，才走 xml-render。
		if standaloneXML && !hasZipInvoicePDF(zip) {
			st.xmlRenderable++
		}
	}

	t.Logf("扫描 %d 份真实原文缓存：解密失败 %d / 解析失败 %d", st.files, st.decryptErr, st.parseErr)
	t.Logf("  带独立 XML 附件：%d", st.withStandalone)
	t.Logf("  带 zip 发票包：%d", st.withZip)
	t.Logf("  带 PDF 附件：%d", st.withPDF)
	t.Logf("  带图片附件：%d", st.withImage)
	t.Logf("  ⇒ 可走 xml-render 的：%d", st.xmlRenderable)
	for _, s := range st.samples {
		t.Logf("  样本 %s", s)
	}

	if st.files == 0 {
		t.Fatal("没扫到任何缓存文件：本用例什么都没验，别把它当成结论")
	}
	// 这个诊断本身不判对错，只报事实；把结论写进日志供人读，
	// 因为「真实数据里有没有独立 XML」不是可以用断言钉死的性质。
	_ = context.Background()
}

func hasZipInvoicePDF(z zipInvoiceContents) bool {
	for _, p := range z.PDFs {
		if isPDFBytes(p) {
			return true
		}
	}
	return false
}
