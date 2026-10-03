package email

// diag_toll_a4_ledger_offline_test.go — 真实通行费发票的**下游两段**
// （A4 拼版导出 + 按币种汇总统计）端到端离线验收。
//
// 门控：POCKET_DIAG_TOLL_E2E=1 + POCKET_DIAG_QP_DATADIR
// 可选：POCKET_DIAG_EXPORT_OUT=<dir> 把产物留在那里（不设则用 t.TempDir()，
// 跑完即删，只用于断言）。
//
// ## 为什么需要这一个
//
// §7.7 之前，A4 拼版与汇总统计一直是「代码与端点本就齐全，但**从未在真实发票
// 集合上端到端跑过**」的状态。齐全不等于能用：拼版要吃 pdfcpu 的 NUp 语义
// （PageDim 是**单格**尺寸、Grid 才是格数，代码注释里专门警告过容易读反），
// 汇总要吃 currencyOrDefault / InvoiceCountsTowardTotal 这套判据。任一处
// 与真实数据对不上，页面上摆着的数字就是错的，而前面几轮的验收都停在
// 「解析 + 建档」为止，压根没走到这里。
//
// ## 这一个不证明什么
//
// - 不证明 08:00 定时路径会走到这两段（那要在生产上核对）。
// - 不证明飞书台账（凭据缺失，整条推送环节一次没跑过）。
// - 只覆盖 grid=2/3 的**单页**情形（2 张票在 2x2 里只占 2 格）。跨页填充
//   与 9 格满页由 export_pdf_test.go 的合成件覆盖。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// a4DimWithinPt 是 A4 竖版判定容差。pdfcpu 用 595.28x841.89 折算成 MediaBox
// 时会有亚 pt 误差，判 0.5pt 足够严（远小于任何真实排版误差）又不脆。
const a4DimWithinPt = 0.5

func assertA4Portrait(t *testing.T, path string) {
	t.Helper()
	dims, err := api.PageDimsFile(path)
	if err != nil {
		t.Fatalf("读 %s 的页尺寸失败：%v", filepath.Base(path), err)
	}
	if len(dims) != 1 {
		t.Fatalf("%s 有 %d 页，本用例只验单页拼版（2 张票在 2x2 里只占 1 页）；"+
			"页数不对说明分页语义变了：%v", filepath.Base(path), len(dims), dims)
	}
	got := dims[0]
	if diff := got.Width - a4WidthPt; diff > a4DimWithinPt || diff < -a4DimWithinPt {
		t.Errorf("%s 页宽 = %.2fpt，want %.2f±%.2f（不是 A4 竖版）",
			filepath.Base(path), got.Width, a4WidthPt, a4DimWithinPt)
	}
	if diff := got.Height - a4HeightPt; diff > a4DimWithinPt || diff < -a4DimWithinPt {
		t.Errorf("%s 页高 = %.2fpt，want %.2f±%.2f（不是 A4 竖版）",
			filepath.Base(path), got.Height, a4HeightPt, a4DimWithinPt)
	}
	t.Logf("%s 页面尺寸 = %.2f x %.2f pt（A4 竖版）", filepath.Base(path), got.Width, got.Height)
}

func TestDiagTollInvoiceA4AndLedgerOffline(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_E2E") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_E2E=1 (and POCKET_DIAG_QP_DATADIR) for the real-data A4/ledger offline e2e")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if dataDir == "" {
		t.Fatal("POCKET_DIAG_QP_DATADIR 未设置")
	}
	outDir := t.TempDir()
	if v := os.Getenv("POCKET_DIAG_EXPORT_OUT"); v != "" {
		if err := os.MkdirAll(v, 0o700); err != nil {
			t.Fatalf("建产物目录失败：%v", err)
		}
		outDir = v
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

	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	dataOut := t.TempDir()

	const acctID = "acct-toll-a4"
	const wsID = "ws_user-admin"
	const userID = "user-admin"
	if err := store.InsertAccount(ctx, &Account{
		ID: acctID, UserID: userID, WorkspaceID: wsID,
		EmailAddress: "acct@example.com",
		IMAPHost:    "", IMAPPort: 0, AuthType: "password",
		Enabled: true, CreatedAt: time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// ---- 段 0：真实原文 → 真实票面 PDF（复用 diag_toll_e2e 的同一段路径） ----
	var invoices []Invoice
	var files []string
	for i, c := range tollE2ECases {
		raw, gerr := cache.Get(c.id, c.uid)
		if gerr != nil || len(raw) == 0 {
			t.Fatalf("case %d 真实原文缓存未命中（uid=%d）：%v"+
				"\n没有真实数据，这个用例就退化成夹具测试，而夹具证明不了拼版对真实票面成立",
				i, c.uid, gerr)
		}
		e := Email{
			ID: c.id, AccountID: acctID, WorkspaceID: wsID,
			MessageID: fmt.Sprintf("<toll-a4-%d@vendor.example>", c.uid),
			UID:       c.uid, FromAddress: "noreply@toll.example",
			Subject: "通行费电子发票", Date: c.date,
		}
		if err := store.InsertEmail(ctx, e); err != nil {
			t.Fatalf("case %d insert email: %v", i, err)
		}
		gotRaw, src, rerr := resolveRawBody(ctx, nil, cache, &e, " a4")
		if rerr != nil {
			t.Fatalf("case %d resolveRawBody: %v", i, rerr)
		}
		if src != rawBodyCache {
			t.Errorf("case %d 原文来源 = %s，want %s", i, src, rawBodyCache)
		}
		parsed, perr := ParseMIMEMessage(gotRaw)
		if perr != nil {
			t.Fatalf("case %d ParseMIMEMessage: %v", i, perr)
		}
		inv, hit := ExtractInvoiceLoose(e, parsed.TextBody+"\n"+parsed.HTMLBody,
			HasInvoiceAttachment(parsed.Attachments))
		if !hit {
			t.Fatalf("case %d 建档判定 hit=false", i)
		}
		inv.ID = fmt.Sprintf("inv-a4-%d", i)
		inv.EmailID = e.ID
		inv.AccountID = acctID
		inv.WorkspaceID = wsID
		inv.Status = "pending"
		if _, uerr := store.UpsertInvoice(ctx, inv, userID, wsID); uerr != nil {
			t.Fatalf("case %d seed invoice: %v", i, uerr)
		}
		h := &InvoiceHarvester{
			Store: store, Fetcher: nil, DataDir: dataOut, BodyCache: cache,
		}
		if got := h.harvestOne(ctx, inv); got != "downloaded" {
			t.Fatalf("case %d harvestOne = %q（last_error=%q）", i, got, inv.LastError)
		}
		if inv.InvoiceNo != c.wantNo {
			t.Fatalf("case %d 发票号 = %q, want %q", i, inv.InvoiceNo, c.wantNo)
		}
		full := filepath.Join(dataOut, inv.FilePath)
		st, serr := os.Stat(full)
		if serr != nil {
			t.Fatalf("case %d 读不到落盘文件：%v", i, serr)
		}
		if st.Size() < 80*1024 {
			t.Errorf("case %d 落盘只有 %d 字节；真实票面约 105KB，45KB 的是汇总单", i, st.Size())
		}
		t.Logf("case %d 票面落盘 %d 字节 file=%s", i, st.Size(), inv.FileName)
		invoices = append(invoices, *inv)
		files = append(files, full)
	}

	// ---- 段 1：A4 拼版（2x2 与 3x3 各一份） ----
	for _, grid := range []int{2, 3} {
		res, err := ExportInvoiceGridDetailed(outDir, files, grid)
		if err != nil {
			t.Fatalf("grid=%d 导出失败：%v", grid, err)
		}
		if res.Count != len(files) {
			t.Errorf("grid=%d Count = %d, want %d —— 有真实票面被当成畸形件跳过了：%v",
				grid, res.Count, len(files), res.Skipped)
		}
		if len(res.Skipped) != 0 {
			t.Errorf("grid=%d 跳过 %d 个：%v", grid, len(res.Skipped), res.Skipped)
		}
		// 2 张票在任何 grid 下都只占 1 页。
		if n, perr := pdfPageCountSafe(res.Path); perr != nil {
			t.Fatalf("grid=%d 数页失败：%v", grid, perr)
		} else if n != 1 {
			t.Errorf("grid=%d 输出 %d 页，want 1（2 张票在 %dx%d 里只占 1 页）", grid, n, grid, grid)
		}
		assertA4Portrait(t, res.Path)
		st, _ := os.Stat(res.Path)
		t.Logf("grid=%d 产物 %s（%d 字节）", grid, filepath.Base(res.Path), st.Size())
	}

	// 负控·拼版：坏件必须被跳过而不是拖垮整批。**断言方向要反过来**——
	// 正向路径 Count==2 是"没出事"，只有坏件真的进了 Skipped 才证明
	// normalizeInvoiceFilesToPDF 的容错在真实调用方身上生效。
	badDir := t.TempDir()
	badPDF := filepath.Join(badDir, "malformed.pdf")
	if werr := os.WriteFile(badPDF, []byte("%PDF-1.4\ntrailer\n%%EOF\n"), 0o600); werr != nil {
		t.Fatalf("写坏件失败：%v", werr)
	}
	mixed, err := ExportInvoiceGridDetailed(outDir, append([]string{badPDF}, files...), 2)
	if err != nil {
		t.Fatalf("混入坏件后导出失败：%v", err)
	}
	if len(mixed.Skipped) != 1 || !strings.Contains(mixed.Skipped[0], "malformed.pdf") {
		t.Errorf("坏件没有被记进 Skipped：Skipped=%v —— 容错没接上，好件也会被拖死", mixed.Skipped)
	}
	if mixed.Count != len(files) {
		t.Errorf("混入坏件后 Count = %d, want %d —— 好件不该因为坏件被丢掉", mixed.Count, len(files))
	}
	t.Logf("负控：坏件被跳过，Skipped=%v，好件 %d 张照常入网格", mixed.Skipped, mixed.Count)

	// ---- 段 2：按币种汇总 ----
	// 期望值不是写死的常数：它是两封真实票的金额之和，且**先自证**——
	// 用例自己从 tollE2ECases 把期望算出来，避免"我以为它是 24.61"。
	var wantCents int64
	for _, c := range tollE2ECases {
		wantCents += int64(c.wantAmt*100 + 0.5)
	}
	wantTotal := float64(wantCents) / 100
	t.Logf("期望合计 = %.2f（= %d 分，由 %d 封真实发票金额推出）",
		wantTotal, wantCents, len(tollE2ECases))

	totals := SumByCurrency(invoices)
	if len(totals) != 1 {
		t.Fatalf("SumByCurrency 返回 %d 个币种组, want 1：%+v", len(totals), totals)
	}
	if totals[0].Currency != "CNY" {
		t.Errorf("币种 = %q, want CNY（真实通行费票是人民币；空币种应被 currencyOrDefault 兜成 CNY）",
			totals[0].Currency)
	}
	if totals[0].Count != len(invoices) {
		t.Errorf("张数 = %d, want %d", totals[0].Count, len(invoices))
	}
	if totals[0].Amount != wantTotal {
		t.Errorf("合计 = %.2f, want %.2f", totals[0].Amount, wantTotal)
	}
	if InvoiceVerifiedLabel(invoices[0]) != "已核验" {
		t.Errorf("已落盘的票核验列 = %q, want 已核验", InvoiceVerifiedLabel(invoices[0]))
	}

	rows, rowTotals := LedgerRows(invoices)
	// 表头 + 每票一行 + 每币种一行合计。
	if len(rows) != len(invoices)+2 {
		t.Errorf("LedgerRows 行数 = %d, want %d（表头 %d + 明细 %d + 合计 1）",
			len(rows), len(invoices)+2, 1, len(invoices))
	}
	if len(rowTotals) != 1 || rowTotals[0].Amount != wantTotal {
		t.Errorf("LedgerRows 的合计 = %+v, want 单组 %.2f", rowTotals, wantTotal)
	}
	t.Logf("台账 %d 行，最后一行：%v", len(rows), rows[len(rows)-1])

	// 负控·汇总：证明「合计真的随输入变」，而不是恰好等于 24.61。
	//  1) 加一张 ⇒ 合计跟着涨
	//  2) 混币种 ⇒ 必须拆成两组，而不是把 USD 和 CNY 加成一个数
	//
	// 【口径说明·第一版这里写成错的】第一版断言「SumByCurrency 会把无凭证的票
	// 剔掉」，实测转红。查证后是**判据形态不匹配**，不是产品缺陷：
	// SumByCurrency 的契约是「把给它的都加起来」，筛选是**调用方**的职责，
	// 生产链路 server_email_pipeline.go:622-627 正是先按
	// InvoiceCountsTowardTotal 等价条件预筛再传进来。改判据，不改产品。
	withExtra := append(append([]Invoice{}, invoices...),
		Invoice{ID: "inv-ctrl", Status: "downloaded", FilePath: "x.pdf", Currency: "CNY", Amount: 100})
	if got := SumByCurrency(withExtra); len(got) != 1 || got[0].Amount != wantTotal+100 {
		t.Errorf("加一张 100 元后合计 = %+v, want 单组 %.2f", got, wantTotal+100)
	}

	// 真正该守的不变量：**筛选口径只有一处**。LedgerRows 内部走
	// InvoiceCountsTowardTotal；调用方也必须走同一个函数。两边一旦漂移，
	// 同一批发票会在台账表里显示一个数、在汇总接口里显示另一个数——
	// 2026-10-02 真实库上就出过 3,500 vs 61,500（17.6 倍）。
	//
	// 负控方向：无凭证的那张票必须**同时**被两处排除。若某天有人把
	// InvoiceCountsTowardTotal 改严了而调用方没跟上，这里立刻转红。
	noFile := append([]Invoice{}, invoices...)
	noFile[0].FilePath = ""
	var canonical []Invoice
	for _, iv := range noFile {
		if InvoiceCountsTowardTotal(iv) {
			canonical = append(canonical, iv)
		}
	}
	gotNoFile := SumByCurrency(canonical)
	rowsNoFile, totalsNoFile := LedgerRows(noFile)
	if len(gotNoFile) != 1 || len(totalsNoFile) != 1 {
		t.Fatalf("无凭证场景下两处口径的组数不一致：调用方 %+v，LedgerRows %+v", gotNoFile, totalsNoFile)
	}
	if gotNoFile[0].Amount != wantTotal-tollE2ECases[0].wantAmt {
		t.Errorf("调用方口径合计 = %.2f, want %.2f（无凭证的票不该计入）",
			gotNoFile[0].Amount, wantTotal-tollE2ECases[0].wantAmt)
	}
	if totalsNoFile[0].Amount != gotNoFile[0].Amount || totalsNoFile[0].Count != gotNoFile[0].Count {
		t.Errorf("同一批发票两处合计不一致：调用方 %.2f/%d 张，LedgerRows %.2f/%d 张"+
			"\n这两条数会分别出现在汇总接口与台账表里，对不上就是错账",
			gotNoFile[0].Amount, gotNoFile[0].Count, totalsNoFile[0].Amount, totalsNoFile[0].Count)
	}
	t.Logf("口径一致：%.2f / %d 张（无凭证的票两处都排除）", totalsNoFile[0].Amount, totalsNoFile[0].Count)
	if InvoiceVerifiedLabel(noFile[0]) != "未核验" {
		t.Errorf("无凭证票的核验列 = %q, want 未核验（与合计口径必须一致）",
			InvoiceVerifiedLabel(noFile[0]))
	}
	if len(rowsNoFile) != len(noFile)+2 {
		t.Errorf("无凭证的票仍在明细里时行数 = %d, want %d（明细 %d + 表头 1 + 合计 1）",
			len(rowsNoFile), len(noFile)+2, len(noFile))
	}

	mixedCur := append(append([]Invoice{}, invoices...),
		Invoice{ID: "inv-usd", Status: "downloaded", FilePath: "y.pdf", Currency: "USD", Amount: 50})
	gotMixed := SumByCurrency(mixedCur)
	if len(gotMixed) != 2 {
		t.Fatalf("混入 USD 后币种组数 = %d, want 2：%+v（跨币种直接相加不是金额）", len(gotMixed), gotMixed)
	}
	seen := map[string]float64{}
	for _, g := range gotMixed {
		seen[g.Currency] = g.Amount
	}
	if seen["CNY"] != wantTotal || seen["USD"] != 50 {
		t.Errorf("混币种合计 = %+v，want CNY=%.2f USD=50", gotMixed, wantTotal)
	}
	t.Logf("负控：CNY=%.2f / USD=50 分组，未被加成一个数", seen["CNY"])
}
