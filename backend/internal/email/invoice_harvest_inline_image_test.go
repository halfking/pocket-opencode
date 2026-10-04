package email

// invoice_harvest_inline_image_test.go — 正文里的**内联图片**被当成发票文件下载了。
//
// ## 缺陷（2026-10-04 08:00 定时流水线真实产出，交付物里肉眼可见）
//
// 台账里两张「票」的文件其实**不是发票**：
//
//	inv_1791072004989035300_2  amount=6071.00  status=downloaded
//	    其他-系统服务-6071.00-2026-09-15-26332000007943899111.jpg
//	inv_1791072004990035300_3  amount=283.20   status=downloaded
//	    其他-系统服务-283.20-2026-09-15-26112000003895678291.jpg
//
// 把这两个 .jpg **打开看**（不是只看字节）：内容是一张百望平台的**营销横幅**，
// 上面印着「用心服务 贴心用户」。两个文件 **SHA256 完全相同**
// （`9CED44F438A7998…`，均 21429 字节）——同一张标语被存成了两张发票凭证。
//
// 当天的日志显示两个真正的发票链接都失败了：
//
//	link download failed … pis.baiwang.com/…previewInvoiceAllEle  → text/html 1812 字节
//	link download failed … ad.efapiao.com/…maillink?taxid=…       → text/html 312849 字节
//	saved 其他-系统服务-6071.00-….jpg (source=pdf-url)
//
// ⇒ 说明**第三个 URL** 下载成功且被判成了「发票文件」。而判据是
// `if dlErr == nil && (isPDFBytes(data) || isImageBytes(data))`
// （invoice_harvest.go:373）——**任意 JPEG 都算通过**，横幅当然算。
//
// ## 根因：跳过内联图片靠的是「扩展名」，不是「它来自 src=」
//
// `reSkippable`（invoice_harvest.go:82）里含 `\.png|\.jpg|\.jpeg|\.gif`，
// 所以 `<img src="https://cdn.cn/pic.png"/>` 会被跳过——
// **这个意图已经被现有判据钉住**（invoice_harvest_test.go 的
// `TestHarvest…` 用例断言 urls 长度为 2 而那个 `<img src=…png>` 不在其中）。
//
// 但跳过条件是**扩展名子串**，于是 `<img src="https://…/banner?w=750&h=200">`
// 这类**无扩展名**的动态图片 URL 漏过去：`reBareURLs` 匹配正文里任意
// `https?://`，`<img src=` 的地址同样在匹配范围内（`reHTMLHrefs` 只管
// `href=`，管不到 `src=`）。
//
// ⇒ 修法与既有意图一致：**凡是从 `src="…"` 来的 URL 一律不作为下载候选**，
// 不再依赖「URL 里有没有图片扩展名」。
//
// ## 未验证的部分（如实记）
//
// 那两个 `.jpg` 到底是从哪个 URL 下载的，**没能确认**：正文缓存为空
// （`body_path` 空、`attachments` 空），当天的日志已被轮转掉。
// 上面描述的是**与全部已知事实一致的机制**，不是对那次事件的直接观测。
// 本判据针对的是**代码层面的漏洞**（无扩展名的内联图片会被采信），
// 那一条是可直接验证的。
import (
	"strings"
	"testing"
)

// TestExtractInvoiceURLs_SkipsExtensionlessInlineImage 缺陷复现：
// 无扩展名的 `<img src>` 横幅不该进入下载候选。
func TestExtractInvoiceURLs_SkipsExtensionlessInlineImage(t *testing.T) {
	// 形如百望「电子发票下载」邮件的 HTML：页头一张横幅（动态 URL，无扩展名），
	// 正文里一个真正的发票查看链接。
	body := `<html><body>
<img src="https://cdn.baiwang.com/mail/banner?w=750&amp;h=200&amp;ticket=abc123" />
<p>浙江智谱新篇科技有限公司为您开具了电子发票</p>
<a href="https://pis.baiwang.com/smkp-vue/previewInvoiceAllEle?param=5EA9BC23">点击链接查看</a>
</body></html>`

	urls := extractInvoiceURLs(body)
	for _, u := range urls {
		if strings.Contains(u, "cdn.baiwang.com") {
			t.Fatalf("内联横幅图片被当成发票下载候选：%q\n"+
				"  它下载回来是 JPEG，`isImageBytes` 放行，于是营销横幅被存成了发票文件。\n"+
				"  urls=%v", u, urls)
		}
	}
	if len(urls) != 1 || !strings.Contains(urls[0], "pis.baiwang.com") {
		t.Fatalf("urls=%v，want 恰好 1 个（真正的发票链接）", urls)
	}
}

// TestExtractInvoiceURLs_StillCollectsRealInvoiceLinks 反向保护：
// 收窄不能把真正的下载链接挡掉——三条来源都要覆盖。
func TestExtractInvoiceURLs_StillCollectsRealInvoiceLinks(t *testing.T) {
	cases := []struct {
		name, body, wantSub string
	}{
		{
			name:    "a 标签 href",
			body:    `<a href="https://fp.example.com/invoice/abc123.pdf">下载发票</a>`,
			wantSub: "fp.example.com",
		},
		{
			name:    "纯文本裸 URL",
			body:    `发票已开具：https://oss.example-invoice.com/bill/2026/9/inv_3500.pdf`,
			wantSub: "oss.example-invoice.com",
		},
		{
			name:    "无扩展名但带发票特征的查询串链接",
			body:    `<a href="https://fp.example.com/download?file=invoice&id=88">查看发票</a>`,
			wantSub: "fp.example.com",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			urls := extractInvoiceURLs(c.body)
			found := false
			for _, u := range urls {
				if strings.Contains(u, c.wantSub) {
					found = true
				}
			}
			if !found {
				t.Errorf("真正的发票链接被漏掉了，urls=%v（body=%s）", urls, c.body)
			}
		})
	}
}

// TestExtractInvoiceURLs_ExtensionSkippingStillWorks 钉住**原有**的扩展名跳过
// 没被这次改动破坏（它是既有判据的一部分）。
func TestExtractInvoiceURLs_ExtensionSkippingStillWorks(t *testing.T) {
	body := `<a href="https://t.cn/unsubscribe?u=1">退订</a>
<img src="https://cdn.cn/pic.png"/>
<a href="https://fp.example.com/invoice/abc.pdf">发票</a>
https://plain.example.com/file.pdf`
	urls := extractInvoiceURLs(body)
	for _, u := range urls {
		if strings.Contains(u, "unsubscribe") {
			t.Errorf("退订链接被采信：%v", urls)
		}
		if strings.Contains(u, "cdn.cn") {
			t.Errorf("图片链接被采信：%v", urls)
		}
	}
	if len(urls) != 2 {
		t.Errorf("urls=%v，want 2 个", urls)
	}
}
