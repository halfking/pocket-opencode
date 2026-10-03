package email

// invoice_zip.go — 电子发票 **ZIP 压缩包**（EUI/数电票标准下发形态）的识别与取件。
//
// ## 为什么需要它（2026-10-03 真实数据实测）
//
// 真实通行费电子发票邮件的附件是：
//
//	[0] 通行费电子发票.zip              136KB  ← 真正的发票在这里面
//	[1] 通行费电子票据汇总单(票据).pdf    45KB
//	[2] 通行费电子票据汇总单(行程).pdf    45KB
//
// zip 内是 EUI 标准三件套：`xml/*.xml`（发票数据）、`ofd/*.ofd`、
// `pdf/*.pdf`（票面 PDF，各约 105KB）。
//
// 采集器（invoice_harvest.go 的 harvestOne）此前**完全不认 .zip**，于是：
//
//  1. `HasInvoiceAttachment` 判不出 zip ⇒ 纯 zip 邮件连建档门槛都过不去；
//  2. 更糟的是步骤 1「PDF/图片附件」会先命中那两份**汇总单**，
//     把汇总单当发票存盘 —— 而汇总单不是发票凭证。
//
// 需求原文那条「XML 数据格式（可解析后重新渲染）」在这类邮件上**从未跑过**。
//
// ## 设计取舍
//
// 1. **zip 优先于同级 PDF**。宁可多解一层，也不能把汇总单当成票。
// 2. 只取 `pdf/` 与 `xml/` 下的条目，**忽略 ofd**——OFD 是版式文件，
//    现有渲染链不产 OFD，硬转反而造出打不开的文件。
// 3. **全部有上限**（条目数、单条目解压量、总解压量）。zip 是外部输入，
//    解压炸弹能让一轮采集把内存吃光——这与 `downloadPDF` 里那个
//    「静默截断成 20MB」是同一类事故，只是方向相反。
// 4. 目录条目（大小 0）跳过。

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"strings"
)

const (
	// maxZipEntries 限制单个压缩包最多处理多少条目。真实 EUI 包是 7 个
	// （3 目录 + 3 文件 + 可能再有），给到 64 已有 10 倍余量。
	maxZipEntries = 64
	// maxZipEntryBytes 限制单个条目解压后的字节数。票面 PDF 实测约 105KB，
	// XML 约 2.3KB；给到 20MB 与 MaxInvoicePDFBytes 同量级。
	maxZipEntryBytes = MaxInvoicePDFBytes
	// maxZipTotalBytes 限制单个压缩包所有条目的解压总量，防炸弹。
	maxZipTotalBytes = 4 * MaxInvoicePDFBytes
)

// zipInvoiceContents 是从一个发票 zip 里取出的可用内容。
type zipInvoiceContents struct {
	// PDFs 是票面 PDF（zip 内 pdf/ 目录优先，其次任何位置的 .pdf）。
	PDFs [][]byte
	// XMLs 是发票数据（zip 内 xml/ 目录优先，其次任何位置的 .xml）。
	XMLs [][]byte
}

// Empty 报告这个包是否什么都没取到。
func (z zipInvoiceContents) Empty() bool { return len(z.PDFs) == 0 && len(z.XMLs) == 0 }

// isZipBytes 用 **magic bytes** 判断，不看文件名——
// 真实数据里那封的 filename 是 `.zip` 且头是 `PK`，两者都成立；但
// 只信文件名的话，一个 Content-Type 写成 zip 实际是 PDF 的附件就会被误判。
// 反过来只看 magic 又会漏掉某些 Windows 压缩工具产出的变体，所以
// filename 只作**补充**，且必须同时具备 .zip 后缀。
func isZipBytes(b []byte, filename string) bool {
	if len(b) < 4 {
		return false
	}
	if b[0] != 'P' || b[1] != 'K' {
		return false
	}
	// local file header (0x04034b50) 或 empty archive (0x06054b50)
	if b[2] == 3 && b[3] == 4 {
		return true
	}
	if b[2] == 5 && b[3] == 6 {
		return true
	}
	return strings.HasSuffix(strings.ToLower(filename), ".zip")
}

// readZipInvoiceContents 打开一个发票 zip，取出其中的 PDF 与 XML。
//
// 非 zip / 损坏的 zip 一律返回零值而不报错：调用方会继续走原有的
// PDF/链接/XML 分支，不该因为「有个坏 zip」就中断整封邮件的处理。
func readZipInvoiceContents(b []byte) zipInvoiceContents {
	var out zipInvoiceContents
	if len(b) < 4 {
		return out
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return out
	}
	var total int64
	pdfFromPdfDir, pdfOther, xmlFromXmlDir, xmlOther := [][]byte{}, [][]byte{}, [][]byte{}, [][]byte{}

	for i, f := range zr.File {
		if i >= maxZipEntries {
			break
		}
		if f.FileInfo().IsDir() || len(f.Name) == 0 {
			continue
		}
		// 先看声明的解压量：超限就不必真去解压。
		if f.UncompressedSize64 > uint64(maxZipEntryBytes) ||
			total+int64(f.UncompressedSize64) > maxZipTotalBytes {
			continue
		}
		ext := strings.ToLower(path.Ext(f.Name))
		isPDF := ext == ".pdf"
		isXML := ext == ".xml"
		if !isPDF && !isXML {
			continue // ofd 及其它一律不取，见文件头「忽略 ofd」
		}
		rc, oerr := f.Open()
		if oerr != nil {
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
		rc.Close()
		if rerr != nil || len(data) == 0 || len(data) > maxZipEntryBytes {
			continue
		}
		total += int64(len(data))
		dir := strings.ToLower(path.Dir(f.Name))
		switch {
		case isPDF && dir == "pdf":
			pdfFromPdfDir = append(pdfFromPdfDir, data)
		case isPDF:
			pdfOther = append(pdfOther, data)
		case dir == "xml":
			xmlFromXmlDir = append(xmlFromXmlDir, data)
		default:
			xmlOther = append(xmlOther, data)
		}
	}
	out.PDFs = append(pdfFromPdfDir, pdfOther...)
	out.XMLs = append(xmlFromXmlDir, xmlOther...)
	return out
}

// zipAttachmentContents 遍历一封邮件的附件，返回第一个可用 zip 里的内容。
// 多个 zip 时合并（EUI 包通常一张票一个，但一次多开票会给出多个）。
func zipAttachmentContents(atts []ParsedAttachment) zipInvoiceContents {
	var merged zipInvoiceContents
	for _, a := range atts {
		if len(a.Data) == 0 || !isZipBytes(a.Data, a.Filename) {
			continue
		}
		c := readZipInvoiceContents(a.Data)
		merged.PDFs = append(merged.PDFs, c.PDFs...)
		merged.XMLs = append(merged.XMLs, c.XMLs...)
	}
	return merged
}
