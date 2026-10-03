package email

// diag_toll_attachment_test.go — 只读诊断：真实通行费邮件的附件里到底有什么，
// 发票号与金额能否从那里补出来。
//
// ## 为什么查这个
//
// §7.6 修完之后，重放真实原文得到的建档结果是：
//
//	amount=19 / 5.61  ✔（正文里已能抽到）
//	invoiceNo=""      ✘（发票号在附件里，正文没有）
//
// 发票号是「一张票」的唯一标识，缺了它，规范文件名会退化成
// `其他-<单位>-19.00-<日期>.pdf`（invoice_file.go 补了 `-{发票号}` 段就是
// 为了唯一标识一张票），同一单位同一天多张票会撞名——已有
// invoice_filename_collision_test.go 钉这个。
//
// 而金额设计上要留给采集器从附件补（invoice.go:609-620）。所以问题变成：
// **附件里到底有没有发票号与金额？**
//
// - 有      → 采集器能补齐，24.61 能真正进台账合计
// - 没有    → 采集器也补不出来，需要别的数据源（平台 API / 手工）
//
// 这个问题此前**从未在真实数据上回答过**（附件解析链路一次都没跑过）。
//
// ## 只读
//
// 只解密磁盘上的原文缓存并调解析器，**不连 PG、不连 IMAP、不写任何数据、
// 不落任何文件**。门控：POCKET_DIAG_TOLL_ATT=1 + POCKET_DIAG_QP_DATADIR。

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDiagTollAttachments(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_ATT") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_ATT=1 (and POCKET_DIAG_QP_DATADIR) to inspect the real toll attachments")
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

	for _, id := range tollTargets {
		blob, rerr := os.ReadFile(dataDir + string(os.PathSeparator) + bodyCacheRawDirName +
			string(os.PathSeparator) + id + ".bin")
		if rerr != nil {
			t.Errorf("%s: 读缓存 %v", id, rerr)
			continue
		}
		_, payload, lerr := locateCiphertext(blob)
		if lerr != nil {
			t.Errorf("%s: locateCiphertext %v", id, lerr)
			continue
		}
		dec, derr := cr.DecryptString(payload)
		if derr != nil {
			t.Errorf("%s: 解密 %v", id, derr)
			continue
		}
		parsed, perr := ParseMIMEMessage([]byte(dec))
		if perr != nil {
			t.Errorf("%s: ParseMIMEMessage %v", id, perr)
			continue
		}

		t.Logf("=== %s ===", id)
		t.Logf("附件 %d 个；HasInvoiceAttachment=%v", len(parsed.Attachments), HasInvoiceAttachment(parsed.Attachments))
		anyXML, anyPDF := false, false
		for i, a := range parsed.Attachments {
			head := string(a.Data)
			if len(head) > 40 {
				head = head[:40]
			}
			head = strings.Map(func(r rune) rune {
				if r < 32 && r != '\t' && r != '\n' && r != '\r' {
					return '.'
				}
				return r
			}, head)
			kind := "?"
			switch {
			case isPDFBytes(a.Data):
				kind = "PDF"
				anyPDF = true
			case isXMLFile(a):
				kind = "XML"
				anyXML = true
			case isImageBytes(a.Data):
				kind = "IMAGE"
			}
			t.Logf("  [%d] name=%q contentType=%q bytes=%d kind=%s", i, a.Filename, a.ContentType, len(a.Data), kind)
			t.Logf("      head=%q", head)
		}
		t.Logf("  有 XML 发票数据: %v   有 PDF: %v", anyXML, anyPDF)
		if !anyXML && !anyPDF {
			t.Logf("  ⇒ 没有任何可归档的票据附件。采集器无从补出发票号，")
			t.Logf("    invoiceNo 会一直是空，规范文件名会退化并可能撞名。")
		}
		t.Logf("")
	}
}

// TestDiagTollAttachmentNoInvoiceNoFromXML 回答「XML 里到底有没有发票号」：
// 附件若是电子发票 XML，用生产解析器（ParseInvoiceXML 之类）真跑一遍。
//
// 先确认生产侧有没有这个函数——用反射式的引用会抄错，所以这里只调真实存在的
// 入口，函数名不对就编译报错（那也是有用的信号）。
func TestDiagTollAttachmentXMLHasInvoiceNo(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_ATT") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_ATT=1")
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

	foundXML := 0
	parsedNo := 0
	foundZip := 0
	zipEntries := 0
	for _, id := range tollTargets {
		blob, rerr := os.ReadFile(dataDir + string(os.PathSeparator) + bodyCacheRawDirName +
			string(os.PathSeparator) + id + ".bin")
		if rerr != nil {
			continue
		}
		_, payload, lerr := locateCiphertext(blob)
		if lerr != nil {
			continue
		}
		dec, derr := cr.DecryptString(payload)
		if derr != nil {
			continue
		}
		parsed, perr := ParseMIMEMessage([]byte(dec))
		if perr != nil {
			continue
		}

		// ZIP 里的电子发票数据：国内电子发票平台的标准下发形态
		// （EUI 压缩包内含 invoice.xml / ofd）。hasInvoiceAttachment 认不出
		// .zip，所以这里必须单独看，否则会以为「没有发票数据」。
		for _, a := range parsed.Attachments {
			if !isZipFile(a) {
				continue
			}
			foundZip++
			t.Logf("%s  ZIP 附件 name=%q bytes=%d", id, a.Filename, len(a.Data))
			zr, zerr := zip.NewReader(bytes.NewReader(a.Data), int64(len(a.Data)))
			if zerr != nil {
				t.Logf("  ⇒ 打开失败: %v", zerr)
				continue
			}
			for _, f := range zr.File {
				zipEntries++
				t.Logf("  entry: %-44s bytes=%d", f.Name, f.UncompressedSize64)
			}
			// 逐个把 zip 里的 XML 喂给**生产**解析器。
			for _, f := range zr.File {
				if !strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
					continue
				}
				rc, oerr := f.Open()
				if oerr != nil {
					t.Logf("  entry %s 打开失败: %v", f.Name, oerr)
					continue
				}
				inner, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
				rc.Close()
				foundXML++
				t.Logf("  zip 内 XML %s (%d 字节) 前 400: %s", f.Name, len(inner), previewXML(string(inner), 400))
				if fields := ParseInvoiceXML(inner); fields != nil {
					parsedNo++
					t.Logf("    ⇒ ParseInvoiceXML 成功：invoiceNo=%q amount=%v date=%q seller=%q currency=%q",
						fields.InvoiceNo, fields.Amount, fields.InvoiceDate, fields.Seller, fields.Currency)
				} else {
					t.Logf("    ⇒ ParseInvoiceXML 返回 nil（未识别为发票）")
				}
			}
		}

		for _, a := range parsed.Attachments {
			if !isXMLFile(a) {
				continue
			}
			foundXML++
			t.Logf("%s  顶层 XML 附件 name=%q bytes=%d", id, a.Filename, len(a.Data))
			t.Logf("  前 600 字节: %s", previewXML(string(a.Data), 600))
			// 生产解析器：xmlinvoice.go 的 ParseInvoiceXML，返回 nil 表示
			// 认不出。用它而不是自己 grep 元素名，否则答的是「我以为的格式」。
			if f := ParseInvoiceXML(a.Data); f != nil {
				parsedNo++
				t.Logf("  ⇒ ParseInvoiceXML 成功：invoiceNo=%q amount=%v date=%q seller=%q currency=%q",
					f.InvoiceNo, f.Amount, f.InvoiceDate, f.Seller, f.Currency)
			} else {
				t.Logf("  ⇒ ParseInvoiceXML **未**识别为发票（返回 nil）")
			}
		}
	}
	t.Logf("")
	t.Logf("ZIP 附件 %d 个，内含条目 %d 个；XML（含 zip 内）%d 个，其中被 ParseInvoiceXML 识别出发票号的 %d 个",
		foundZip, zipEntries, foundXML, parsedNo)
	if foundXML == 0 {
		t.Logf("真实数据里找不到任何 XML 发票数据 —— 「从 XML 解析后重新渲染」这条需求路径")
		t.Logf("在这两封邮件上不成立，不能拿它当已有能力的证据。")
	}
}

// isZipFile 判一个附件是不是 zip。生产代码目前**没有**这个判据
// （hasInvoiceAttachment 只认 PDF/图片/XML），这里是诊断侧的独立判断，
// 故意不复用生产代码，免得「生产认不出」与「它其实是不是 zip」混为一谈。
func isZipFile(a ParsedAttachment) bool {
	if len(a.Data) < 4 {
		return false
	}
	if a.Data[0] != 'P' || a.Data[1] != 'K' {
		return false
	}
	return strings.HasSuffix(strings.ToLower(a.Filename), ".zip") ||
		(a.Data[2] == 3 && a.Data[3] == 4) // local file header
}

func previewXML(s string, n int) string {
	r := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")
	s = r.Replace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
