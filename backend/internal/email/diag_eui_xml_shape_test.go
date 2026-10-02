package email

// diag_eui_xml_shape_test.go — 只读诊断：把真实电子发票 XML 的元素名/值全量摊开，
// 定位 ParseInvoiceXML 为什么把**两封不同的票**读出**同一个 invoiceNo**，
// 以及为什么 amount=0。
//
// ## 现象（2026-10-03 06:16 实测，data/email-bodies-raw 里两封通行费邮件的 zip 内 XML）
//
//	邮件 A: EIid=26337904450900255091  seller=浙江沪杭甬高速公路股份有限公司
//	邮件 B: EIid=26337903130900517835  seller=浙江高速公路智能收费运营服务有限公司
//	两封解析出的 invoiceNo **都是** 「浙AB59453」，amount 都是 0
//
// 两封不同的票读出同一个号码，只有两种可能：
//  1) 号码取自一个两封共有的字段（比如发票代码/平台号），不是这张票的号码；
//  2) 号码与金额在 XML 里的元素名没被 labelMatch 认出来，取到了别处。
//
// 无论哪种，对「自动解析各类发票」都是硬伤：台账里会出现**号码相同**的两行，
// 而去重逻辑（invoice_dedup_test.go）按发票号判定同一张票——同号的两张真票
// 会被当成重复而丢掉一张，金额随之少算。
//
// ## 方法
//
// 只用 encoding/xml 把叶子元素的「路径=值」全列出来，与 labelMatch 的词表
// 对照。不自己猜格式——那正是上一轮「加个『共计』就好」翻车的原因。
//
// 门控：POCKET_DIAG_TOLL_ATT=1 + POCKET_DIAG_QP_DATADIR

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDiagEUIXMLShape(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_ATT") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_ATT=1 (and POCKET_DIAG_QP_DATADIR)")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey: %v", err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	// 每个「邮件 + zip 内 xml」组合记一份摊开的清单
	seen := 0
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
		pm, perr := ParseMIMEMessage([]byte(dec))
		if perr != nil {
			continue
		}
		for _, a := range pm.Attachments {
			if !isZipFile(a) {
				continue
			}
			zr, zerr := zip.NewReader(bytes.NewReader(a.Data), int64(len(a.Data)))
			if zerr != nil {
				continue
			}
			for _, f := range zr.File {
				if !strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
					continue
				}
				rc, oerr := f.Open()
				if oerr != nil {
					continue
				}
				inner, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
				rc.Close()
				seen++
				t.Logf("========== %s :: %s (%d 字节) ==========", id, f.Name, len(inner))
				dumpXMLLeaves(t, string(inner))
				if fields := ParseInvoiceXML(inner); fields != nil {
					t.Logf("--- ParseInvoiceXML => invoiceNo=%q amount=%v date=%q seller=%q",
						fields.InvoiceNo, fields.Amount, fields.InvoiceDate, fields.Seller)
				} else {
					t.Logf("--- ParseInvoiceXML => nil")
				}
			}
		}
	}
	if seen == 0 {
		t.Fatalf("没有摊到任何 XML —— 语料读法变了，本文件的结论全部作废")
	}
}

// dumpXMLLeaves 把所有叶子元素按出现顺序打成 `路径=值`。
func dumpXMLLeaves(t *testing.T, doc string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var path []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Logf("  [XML 解析中断] %v", err)
			return
		}
		switch e := tok.(type) {
		case xml.StartElement:
			path = append(path, e.Name.Local)
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			v := strings.TrimSpace(string(e))
			if v == "" {
				continue
			}
			joined := strings.Join(path, "/")
			// 与生产 labelMatch 同源的对照：看它会把这个元素名判成哪个字段
			t.Logf("  %-72s = %-40s   labelMatch=>%q", joined, euiTrunc(v, 40), labelMatch(path[len(path)-1]))
		}
	}
}

func euiTrunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
