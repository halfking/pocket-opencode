package email

// invoice_banner_voucher_admission_test.go —— 采信侧的横幅闸门。
//
// ## 这条判据在钉什么
//
// `1da030ab` 已经把「内联图片不被当成下载候选」做掉了（`reHTMLSrcs`，
// `extractInvoiceURLs` 里的 inline 集合）。但那修的是**候选收集侧**，
// 采信侧一行没动，而采信判据至今仍是纯魔数：
//
//	invoice_harvest.go:370  附件：isPDFBytes(att.Data) || isImageBytes(att.Data)
//	invoice_harvest.go:385  链接：dlErr == nil && (isPDFBytes(data) || isImageBytes(data))
//	invoice_harvest.go:679  建档门槛 HasInvoiceAttachment 同款
//
// `isImageBytes` = 「magic 是 JPEG/PNG/WEBP」。于是横幅只要从**任何**不是
// `src=` 的路进来就照样被当成凭证：
//
//   - `<a href="https://cdn.x.com/banner.jpg">`（1da030ab 的三条反向保护
//     第 1 条恰恰断言这种链接**必须仍被收走**——收候选是对的，收下之后
//     采信侧没有任何东西能挡住它下载回来的横幅）
//   - 正文里的裸 URL
//   - 直接作为 MIME 附件带进来的内联图片（`cid:` 图也会被解成附件）
//
// ## 为什么这不是假想，是已经发生过的
//
// `data/email-invoices/ws_user-admin/` 里 2026-10-04 08:00 那轮真跑留下了：
//
//	其他-系统服务-6071.00-2026-09-15-26332000007943899111.jpg
//	其他-系统服务-283.20-2026-09-15-26112000003895678291.jpg
//
// 两个文件 SHA256 **完全相同**（9CED44F438A7998…），各 21429 字节。
// 打开第一个：一张百望平台宣传横幅，印着「用心服务 贴心用户」，
// 实测尺寸 **572 x 140**（长宽比 4.09）。
// 而台账里这两行的状态是 `downloaded` / `已核验`，
// 金额 6071.00 + 283.20 = 6354.20 CNY，占当轮 CNY 合计 10392.21 的 **61.1%**。
//
// 同一份字节不可能同时是两笔不同发票的凭证 —— 这一条不需要任何阈值。
// 本判据用**形状**把它拦在落盘之前：572x140 的横幅与 A4 票面
// （竖版 0.707 / 横版 1.414）差着一个数量级的长宽比。
//
// ## 阈值是怎么定的（不要只看数字）
//
// 观测值 4.09；A4 横版 1.414。取 2.5 落在两者之间，余量对称。
// 判别式是「这东西像不像一份**文档**」，不是「它是不是图」——
// 后者问一百遍也是 true。
//
// ## 刻意**没有**做的事（如实记）
//
// 1. **没有最小像素尺寸下限。** 现有护栏
//    `TestHarvestOne_ImageAttachmentKeepsImageExtension` 的夹具是一张
//    **1x1** 的最小 PNG。给图片发票加尺寸下限会直接把它打红，而那张 1x1
//    是夹具、不是真票。我手上没有任何一张真·拍照发票样本可以标定下限，
//    「没有证据就不改」。
// 2. **没有动 `isImageBytes` 本身。** 它还被 `ExtractInvoiceThumb` /
//    `DetectInvoiceMedia` 用着，缩略图场景下横幅**本来就该**能显示。
//    把闸门加在 isImageBytes 上会把列表页缩略图一起弄坏。
// 3. **解码不出尺寸时放行（fail-open）。** webp 没有 stdlib 解码器
//    （`image.DecodeConfig` 会报 unsupported format），真出现 webp 发票时
//    会走放行分支。这是有意的：宁可放过一张 webp 横幅，也不要因为
//    「测不出尺寸」把真票判死。代价是 webp 这条路上仍有洞，见 handoff 遗留风险。
import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannerJPEG 造一张指定尺寸的 JPEG（真实编码，不是拼魔数），
// 这样 image.DecodeConfig 能读出真实宽高。
func bannerJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: uint8((x + y) % 239), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode jpeg %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

// 缺陷复现：真实观测到的横幅尺寸必须被拒。
func TestImagePlausibleAsVoucher_RejectsObservedBannerGeometry(t *testing.T) {
	// 572x140 = data/email-invoices 里那张「用心服务 贴心用户」横幅的实测尺寸。
	// 用**独立字面量**而不是从被测函数推导期望值。
	for _, dim := range []struct{ w, h int }{
		{572, 140}, // 真实观测值，长宽比 4.09
		{750, 200}, // 1da030ab 注释里那个 …/banner?w=750&h=200 的真实比例
		{600, 100},
		{120, 572}, // 竖版窄条（页脚分隔图），对称地也该拒
	} {
		data := bannerJPEG(t, dim.w, dim.h)
		if imagePlausibleAsVoucher(data) {
			t.Errorf("%dx%d（长宽比 %.2f）被当成发票凭证了 —— 这正是那张横幅的形状。\n"+
				"  落盘后果：台账多一行 downloaded/已核验，金额凭空进合计。",
				dim.w, dim.h, float64(dim.w)/float64(dim.h))
		}
		if !isImageBytes(data) {
			t.Fatalf("%dx%d 竟不是图片，夹具本身坏了", dim.w, dim.h)
		}
	}
}

// 反向保护（防修过头）：文档形状必须**仍被**采信。
//
// 这一条和上一条同量级重要。只写「横幅被拒」的判据，一个把所有图片
// 一律拒掉的实现照样全绿。
func TestImagePlausibleAsVoucher_AcceptsDocumentGeometry(t *testing.T) {
	for _, dim := range []struct {
		name string
		w, h int
	}{
		{"A4 竖版 150dpi", 1240, 1754},
		{"A4 横版", 1754, 1240},
		{"手机拍照长边竖版", 1080, 1920},
		{"手机拍照横置", 1920, 1080},
		{"近方形扫描件", 1000, 1000},
		{"1x1 最小 PNG 夹具（既有护栏依赖它）", 1, 1},
	} {
		var data []byte
		if dim.w == 1 && dim.h == 1 {
			// 复用既有护栏 invoice_attachment_harvest_wiring_test.go 里那份
			// 最小合法 PNG 的字节形状，避免又造一份「看起来一样」的东西。
			data = minimalPNG1x1()
		} else {
			data = bannerJPEG(t, dim.w, dim.h)
		}
		if !imagePlausibleAsVoucher(data) {
			t.Errorf("%s（%dx%d）被误拒了 —— 拍照发票/扫描件是合法凭证。\n"+
				"  这条转红说明闸门收得太紧，真票会被判成 pending。", dim.name, dim.w, dim.h)
		}
	}
}

// minimalPNG1x1 造一张 1x1 的合法 PNG。
func minimalPNG1x1() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// 端到端（行为，不是源码）：横幅作为**直接附件**进来时不得落盘为凭证。
func TestHarvestOne_BannerAttachmentIsNotArchivedAsVoucher(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	banner := bannerJPEG(t, 572, 140)
	raw := buildE2EMIME(t, "电子发票下载", "发票见附件。",
		[]e2eAttachment{{name: "banner.jpg", contentType: "image/jpeg", data: banner}})

	dir := t.TempDir()
	seedAccount(t, store, "acct-bnr", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-bnr-acct-bnr", AccountID: "acct-bnr", WorkspaceID: "ws-1",
		FromAddress: "service@baiwang.com", Subject: "电子发票下载",
		Snippet: "发票见附件", Date: 1750000000, UID: 13,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-bnr-acct-bnr")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	inv := &Invoice{
		ID: "inv-bnr", EmailID: em.ID, AccountID: "acct-bnr",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new",
		Category: "其他", Seller: "系统服务", Amount: 6071, InvoiceDate: "2026-09-15",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir,
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
	}
	got := h.harvestOne(ctx, inv)

	if got == "downloaded" {
		t.Errorf("harvestOne = \"downloaded\"，横幅被当成了发票凭证。\n"+
			"  这正是 2026-10-04 08:00 那轮 6071.00/283.20 两行走的路：\n"+
			"  status=downloaded、汇总单标「已核验」、金额进合计。\n"+
			"  last_error=%q", inv.LastError)
	}
	if inv.FilePath != "" {
		t.Errorf("横幅被落盘了：FilePath=%q FileName=%q", inv.FilePath, inv.FileName)
	}
	if inv.LastError == "" {
		t.Error("被拒却没有留下任何 last_error —— 下一次排查无从判断该不该重试")
	} else {
		t.Logf("横幅被拒并留痕：status=%q last_error=%q", got, inv.LastError)
	}
	assertNoInvoiceFiles(t, dir)
}

// 端到端：横幅藏在 `<a href>` 后面（**不是** src=，1da030ab 管不到这条路）时
// 同样不得落盘。这一条是 1da030ab 留下的缺口，判据要单独钉住。
func TestHarvestOne_BannerBehindHrefIsNotArchivedAsVoucher(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	banner := bannerJPEG(t, 572, 140)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(banner)
	}))
	defer srv.Close()

	body := fmt.Sprintf(`<html><body>
<p>浙江智谱新篇科技有限公司为您开具了电子发票</p>
<a href="%s/portal/invoice/preview?id=88&amp;tk=9">点击查看发票</a>
</body></html>`, srv.URL)

	// **非空洞前提**：这条链接必须真的进了候选集合，否则下面 harvestOne
	// 返回非 downloaded 只是因为「压根没试过」，判据就白写了。
	// 一个「一律返回 nil」的采集器在这条上同样会绿。
	wantURL := srv.URL + "/portal/invoice/preview?id=88&tk=9"
	collected := false
	for _, u := range extractInvoiceURLs(body) {
		if u == wantURL {
			collected = true
		}
	}
	if !collected {
		t.Fatalf("前提不成立：这个 href 没进候选集合（urls=%v，want=%q）。\n"+
			"  判据必须建立在「它确实会被尝试下载」之上，否则拒绝理由和修复都对不上。",
			extractInvoiceURLs(body), wantURL)
	}

	raw := buildE2EMIME(t, "电子发票下载", body, nil)

	dir := t.TempDir()
	seedAccount(t, store, "acct-hrf", "user-1", "ws-1")
	if err := store.InsertEmail(ctx, Email{
		ID: "em-pop3-hrf-acct-hrf", AccountID: "acct-hrf", WorkspaceID: "ws-1",
		FromAddress: "service@baiwang.com", Subject: "电子发票下载",
		Snippet: "发票见正文链接", Date: 1750000000, UID: 14,
	}); err != nil {
		t.Fatalf("insert email: %v", err)
	}
	em, err := store.GetEmailByID(ctx, "em-pop3-hrf-acct-hrf")
	if err != nil || em == nil {
		t.Fatalf("get email: %v", err)
	}
	inv := &Invoice{
		ID: "inv-hrf", EmailID: em.ID, AccountID: "acct-hrf",
		UserID: "user-1", WorkspaceID: "ws-1", Status: "new",
		Category: "其他", Seller: "系统服务", Amount: 283.2, InvoiceDate: "2026-09-15",
	}
	if _, err := store.UpsertInvoice(ctx, inv, "user-1", "ws-1"); err != nil {
		t.Fatalf("upsert invoice: %v", err)
	}

	h := &InvoiceHarvester{
		Store: store, Fetcher: &Fetcher{}, DataDir: dir, HTTPClient: srv.Client(),
		BodyCache: &memBodyCache{data: map[string][]byte{em.ID: raw}},
	}
	got := h.harvestOne(ctx, inv)

	if got == "downloaded" {
		t.Errorf("harvestOne = \"downloaded\"，href 背后的横幅被当成了发票凭证。\n"+
			"  `1da030ab` 只挡了 `src=`；`href=` / 裸 URL / 附件这三条路当时未设防。\n"+
			"  last_error=%q", inv.LastError)
	}
	if inv.FilePath != "" {
		t.Errorf("横幅被落盘了：FilePath=%q", inv.FilePath)
	}
	assertNoInvoiceFiles(t, dir)
}

// assertNoInvoiceFiles 断言采集目录里没有落下任何 .jpg/.png/.pdf。
func assertNoInvoiceFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".pdf" || ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			t.Errorf("发票目录里多出凭证文件：%s", e.Name())
		}
	}
}
