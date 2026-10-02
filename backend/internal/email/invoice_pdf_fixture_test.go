package email

import (
	"bytes"
	"fmt"
)

// minimalInvoicePDF 造一份**结构完整**的单页 PDF，供落盘/采集相关用例当夹具。
//
// 为什么需要它（合并修订）：本文件原先的夹具是 `[]byte("%PDF-1.7 body")` ——
// 只有魔数、没有交叉引用表、没有页对象。而 main 侧（243cda44）给采集器加了一道
// `pdfHasPages` 校验（pdfcpu 的 PageCount >= 1），理由是：拿回来的东西根本不是
// PDF 时，落盘会让台账多出一张 0 元「发票」，导出接口还会被 pdfcpu 的页树
// panic 打成 500。
//
// 于是夹具比生产**干净**：生产里永远是真 PDF，测试里却是个假 PDF。这道校验
// 对本用例完全不可见——它测的是「命名与落盘」，不是「PDF 是否可用」。
// 换成结构完整的最小 PDF 之后，savePDF 走的是与生产同一条判定路径。
//
// 这里手写字节而不是调 pdfcpu 的写接口：写接口要在测试里构造 model.XRefTable
// 与 Configuration，比这份 700 字节的固定内容重得多，而夹具需要的恰恰是
// 「最小且稳定」——不随 pdfcpu 版本变化。
func minimalInvoicePDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << >> >>",
		"", // 内容流，下面按长度拼
	}

	stream := "BT /F1 12 Tf 72 770 Td (invoice fixture) Tj ET"
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	// 注释一行含非 ASCII，标明这是测试夹具、不是真实票据。
	buf.WriteString("% minimal invoice test fixture\n")

	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref)
	return buf.Bytes()
}
