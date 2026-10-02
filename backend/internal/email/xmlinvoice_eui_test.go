package email

// xmlinvoice_eui_test.go — 护栏：EUI 标准电子发票（通行费场景）必须解析正确。
//
// ## 来源：真实数据，不是编的夹具
//
// 2026-10-03 从 data/email-bodies-raw/<id>.bin（**加密原文缓存**）里解出
// 通行费电子发票的 zip 附件，zip 内 xml/ 下的 EUI XML。
// 本文件的 fixture 是按那份 XML 的**真实元素结构**缩减而成的：
// 元素名、层级、连字符拼写全部照抄，只删掉了无关字段。
//
// 缩减是为了让用例可读，但**结构必须一字不差**——上一轮「发票金额共计19元」
// 那个夹具把 HTML 标签抹掉了，结论当场作废（handoff §7.4.4）。
//
// ## 它钉住的三件事（都是修复前实测为错的）
//
//  1. invoiceNo 必须是 TaxSupervisionInfo/InvoiceNumber（20 位），
//     **不是** PlateNumber（车牌号）。修复前两封不同的票都读出「浙AB59453」，
//     而 invoice_dedup 按发票号判重 ⇒ 两张真票会被当重复丢掉一张。
//  2. amount 必须是价税合计 TotalTax-includedAmount（5.61 / 19.00），
//     **不是**不含税金额 TotalAmWithoutTax（5.45），也不是项目单价 Amount（5.45）。
//  3. seller 必须是 SellerName，**不是** SellerIdNum / SellerAddr /
//     SellerTelNum / SellerBankName（这几个元素名都含 "seller"，修复前也会命中）。
//
// ## 负控
//
//  1) 删掉 labelMatch 里的 platenumber 排除 → ① 转红（读成车牌号）
//  2) 删掉 amount 词表里的 totaltax-includedamount → ② 转红（amount=0）
//  3) 删掉 platenumber 排除 → ③ 也可能变化

import "testing"

// euiTollXML 是照抄真实 EUI XML 结构的缩减版（金额/号码/日期已换成对照值）。
// 注意保留了两个易错点：
//   - `TotalTax-includedAmount` 里间的**连字符**
//   - `SpecificInformation/Toll/PlateNumber` 出现在 InvoiceNumber **之前**
const euiTollXML = `<?xml version="1.0" encoding="UTF-8"?>
<EInvoice>
  <Header>
    <EIid>26337904450900255091</EIid>
    <EInvoiceTag>SWEI3300</EInvoiceTag>
    <Version>0.32</Version>
  </Header>
  <EInvoiceData>
    <SellerInformation>
      <SellerIdNum>91330000142942095H</SellerIdNum>
      <SellerName>浙江沪杭甬高速公路股份有限公司</SellerName>
      <SellerAddr>杭州市五星路199号明珠国际商务中心</SellerAddr>
      <SellerTelNum>0571-87985588</SellerTelNum>
      <SellerBankName>工行武林支行</SellerBankName>
    </SellerInformation>
    <BuyerInformation>
      <BuyerIdNum>91330106MACKX4QG41</BuyerIdNum>
      <BuyerName>杭州开轩科技有限公司</BuyerName>
    </BuyerInformation>
    <BasicInformation>
      <TotalAmWithoutTax>5.45</TotalAmWithoutTax>
      <TotalTaxAm>0.16</TotalTaxAm>
      <TotalTax-includedAmount>5.61</TotalTax-includedAmount>
      <TotalTax-includedAmountInChinese>伍元陆角壹分</TotalTax-includedAmountInChinese>
      <Drawer>马斯妮</Drawer>
      <RequestTime>2026-09-14 12:51:03</RequestTime>
    </BasicInformation>
    <IssuItemInformation>
      <ItemName>*生产生活服务*通行费</ItemName>
      <UnPrice>5.45</UnPrice>
      <Amount>5.45</Amount>
      <TaxRate>0.03</TaxRate>
      <ComTaxAm>0.16</ComTaxAm>
      <TotaltaxIncludedAmount>5.61</TotaltaxIncludedAmount>
      <TaxClassificationCode>3040502020301000000</TaxClassificationCode>
    </IssuItemInformation>
    <SpecificInformation>
      <Toll>
        <PlateNumber>浙AB59453</PlateNumber>
        <VehicleType>客车</VehicleType>
        <StartDatesOfPassage>20260914094742000</StartDatesOfPassage>
        <EndDatesOfPassage>20260914094742000</EndDatesOfPassage>
      </Toll>
    </SpecificInformation>
  </EInvoiceData>
  <TaxSupervisionInfo>
    <InvoiceNumber>26337904450900255091</InvoiceNumber>
    <IssueTime>2026-09-14</IssueTime>
    <TaxBureauCode>13300000000</TaxBureauCode>
    <TaxBureauName>国家税务总局浙江省税务局</TaxBureauName>
  </TaxSupervisionInfo>
</EInvoice>`

func TestParseInvoiceXML_EUINoIsInvoiceNumberNotPlate(t *testing.T) {
	f := ParseInvoiceXML([]byte(euiTollXML))
	if f == nil {
		t.Fatal("真实的 EUI 电子发票 XML 没被识别为发票")
	}
	const wantNo = "26337904450900255091"
	if f.InvoiceNo != wantNo {
		t.Errorf("invoiceNo = %q, want %q", f.InvoiceNo, wantNo)
	}
	// 反向护栏：车牌号绝不能被当成发票号码。
	// 它出现得更早，正是修复前 first-wins 取错的那个字段。
	if f.InvoiceNo == "浙AB59453" {
		t.Error("invoiceNo 取到了车牌号（PlateNumber）——"+
			"同一辆车的两张票会读出同一个号码，去重时会被当成同一张票而丢一张")
	}
}

func TestParseInvoiceXML_EUIAmountIsTaxInclusive(t *testing.T) {
	f := ParseInvoiceXML([]byte(euiTollXML))
	if f == nil {
		t.Fatal("真实的 EUI 电子发票 XML 没被识别为发票")
	}
	if f.Amount != 5.61 {
		t.Errorf("amount = %v, want 5.61（价税合计 TotalTax-includedAmount）", f.Amount)
	}
	// 反向护栏：不含税金额与项目单价都不是发票金额。
	// 加它们进词表会让「单价 5.45」冒充「总额 5.61」，少算 0.16 的税额。
	for _, wrong := range []float64{5.45, 0.16} {
		if f.Amount == wrong {
			t.Errorf("amount = %v，取到了不含税金额或项目单价；发票金额应是价税合计 5.61", wrong)
		}
	}
}

func TestParseInvoiceXML_EUISellerIsSellerName(t *testing.T) {
	f := ParseInvoiceXML([]byte(euiTollXML))
	if f == nil {
		t.Fatal("真实的 EUI 电子发票 XML 没被识别为发票")
	}
	if f.Seller != "浙江沪杭甬高速公路股份有限公司" {
		t.Errorf("seller = %q, want 浙江沪杭甬高速公路股份有限公司"+
			"（SellerIdNum / SellerAddr / SellerTelNum / SellerBankName 的元素名"+
			"都含 seller，first-wins 可能取错）", f.Seller)
	}
}
