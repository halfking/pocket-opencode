package email

// diag_toll_e2e_offline_test.go — 真实原文的**端到端离线验收**（不写生产库、不连 IMAP）。
//
// ## 为什么需要它
//
// §7.6 之后的修复（POP3 取原文、HTML 标签容差、EUI XML 三处、ZIP 采集）
// 都是分段验证的：**没有一次**把它们串起来跑过真实数据。
// 而 08:00 跑的是本轮改动之前的二进制，真正的端到端要等实例重启——
// 那是需要授权的动作。本文件先把证据补上，让重启后的验收有一个明确的对照。
//
// ## 三段分别对应流水线的哪一步
//
//	段 1  ExtractInvoice(e, "")                 pipeline.go 第 1 趟（只有 envelope）
//	段 2  resolveRawBody + ExtractInvoiceLoose   pipeline.go 第 2 趟（取原文 → 放宽建档）
//	段 3  harvestOne                            流水线第 4 步（落盘 + 补全字段）
//
// ## 只读保证
//
//   * 库：newWorkspaceTestStore 建**隔离 schema**（email_ws_test_*），不碰 opencode_pocket
//   * 原文：FileBodyCache 只调 Get（不调 Put），指向真实 data 目录
//   * 落盘：DataDir = t.TempDir()
//   * 网络：Fetcher 为 nil —— 任何一次 IMAP/POP3 外呼都会立刻失败并暴露出来
//
// 门控：POCKET_DIAG_TOLL_E2E=1 + POCKET_DIAG_QP_DATADIR

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tollE2ECases 是真实库里那两封（uid / date 取自 opencode_pocket.emails，
// 2026-10-03 06:40 只读核对过）。
var tollE2ECases = []struct {
	id       string
	uid      int64
	date     int64
	wantNo   string
	wantAmt  float64
	wantSell string
}{
	{
		id:       "em-pop3-acct-1790870162047413500-2-ZL0014_NzbN7QSM14kuaWoAEvTJP10",
		uid:      32,
		date:     1789364471,
		wantNo:   "26337904450900255091",
		wantAmt:  5.61,
		wantSell: "浙江沪杭甬高速公路股份有限公司",
	},
	{
		id:       "em-pop3-acct-1790870162047413500-2-ZL0014_FhfN7AWM14kuaWoAA7LLi10",
		uid:      33,
		date:     1789364696,
		wantNo:   "26337903130900517835",
		wantAmt:  19.00,
		wantSell: "浙江高速公路智能收费运营服务有限公司",
	},
}

func TestDiagTollEndToEndOffline(t *testing.T) {
	if os.Getenv("POCKET_DIAG_TOLL_E2E") != "1" {
		t.Skip("set POCKET_DIAG_TOLL_E2E=1 (and POCKET_DIAG_QP_DATADIR) for the real-data offline e2e")
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
	// 只读缓存：只 Get，不 Put。
	cache := NewFileBodyCache(dataDir, cr)
	if cache == nil {
		t.Fatal("FileBodyCache 为 nil（crypto 或 dataDir 缺失）")
	}

	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()
	dataOut := t.TempDir()

	const acctID = "acct-toll-e2e"
	const wsID = "ws_user-admin"
	const userID = "user-admin"
	// 账户建在子测试**外面**：两封邮件共用一个账户，在子测试里各建一次
	// 会撞 email_accounts 主键，第二封直接 fail —— 那是夹具缺陷不是被测行为。
	if err := store.InsertAccount(ctx, &Account{
		ID: acctID, UserID: userID, WorkspaceID: wsID,
		EmailAddress: "acct@example.com",
		// IMAPHost 留空：任何外呼都必然失败，保证没偷偷联网。
		IMAPHost: "", IMAPPort: 0, AuthType: "password",
		Enabled: true, CreatedAt: time.Now().Unix(),
	}, "enc-cred"); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	for _, c := range tollE2ECases {
		t.Run(c.id, func(t *testing.T) {
			// ---- 前置：真实原文缓存必须命中，否则下面全是空转 ----
			raw, gerr := cache.Get(c.id, c.uid)
			if gerr != nil || len(raw) == 0 {
				t.Fatalf("真实原文缓存未命中（uid=%d）：%v"+
					"\n这会让本文件静默变成夹具测试，而夹具不是本文件要验的东西", c.uid, gerr)
			}
			t.Logf("原文缓存命中：%d 字节", len(raw))

			e := Email{
				ID: c.id, AccountID: acctID, WorkspaceID: wsID,
				// MessageID 必须逐封唯一：emails 上有
				// (account_id, message_id) 唯一约束，两封共用一个会直接
				// 报 duplicate key —— 那是夹具缺陷，不是被测行为。
				MessageID:   "<toll-e2e-" + c.wantNo + "@vendor.example>",
				UID:         c.uid,
				FromAddress: "noreply@toll.example",
				Subject:   "通行费电子发票",
				Snippet:   "",
				Date:      c.date,
			}
			if err := store.InsertEmail(ctx, e); err != nil {
				t.Fatalf("insert email: %v", err)
			}

			// ---- 段 1：envelope 上的抽取。真实形态下**必然**抽不到金额 ----
			_, hit1 := ExtractInvoice(e, "")
			t.Logf("段1 ExtractInvoice(envelope) hit=%v（预期 false：这正是要走第 2 趟的原因）", hit1)

			// ---- 段 2：取原文（真实缓存）+ 放宽建档 ----
			gotRaw, src, rerr := resolveRawBody(ctx, nil, cache, &e, " e2e")
			if rerr != nil {
				t.Fatalf("段2 resolveRawBody 失败（via=%s）：%v", src, rerr)
			}
			if src != rawBodyCache {
				t.Errorf("段2 原文来源 = %s，want %s", src, rawBodyCache)
			}
			parsed, perr := ParseMIMEMessage(gotRaw)
			if perr != nil {
				t.Fatalf("段2 ParseMIMEMessage: %v", perr)
			}
			hasAtt := HasInvoiceAttachment(parsed.Attachments)
			inv, hit2 := ExtractInvoiceLoose(e, parsed.TextBody+"\n"+parsed.HTMLBody, hasAtt)
			if !hit2 {
				t.Fatalf("段2 建档判定 hit=false：放宽路径仍然没生效")
			}
			t.Logf("段2 hit=true amount=%v invoiceNo=%q seller=%q hasAtt=%v",
				inv.Amount, inv.InvoiceNo, inv.Seller, hasAtt)
			if inv.Amount != c.wantAmt {
				t.Errorf("段2 amount = %v, want %v", inv.Amount, c.wantAmt)
			}

			// ---- 段 3：采集落盘（Fetcher 留 nil，只能靠缓存） ----
			inv.ID = "inv-e2e-" + c.id[len(c.id)-6:]
			inv.EmailID = c.id
			inv.AccountID = acctID
			inv.WorkspaceID = wsID
			inv.Status = "pending"
			if _, uerr := store.UpsertInvoice(ctx, inv, userID, wsID); uerr != nil {
				t.Fatalf("段3 seed invoice: %v", uerr)
			}
			h := &InvoiceHarvester{
				Store:     store,
				Fetcher:   nil, // 任何外呼都会立刻失败——这是「没联网」的证据
				DataDir:   dataOut,
				BodyCache: cache,
			}
			got := h.harvestOne(ctx, inv)
			if got != "downloaded" {
				t.Fatalf("段3 harvestOne = %q, want \"downloaded\"（last_error=%q）", got, inv.LastError)
			}
			t.Logf("段3 status=%s source=%s file=%s", inv.Status, inv.FileSource, inv.FileName)

			if inv.InvoiceNo != c.wantNo {
				t.Errorf("段3 发票号 = %q, want %q", inv.InvoiceNo, c.wantNo)
			}
			if inv.Amount != c.wantAmt {
				t.Errorf("段3 金额 = %v, want %v", inv.Amount, c.wantAmt)
			}
			if inv.Seller != c.wantSell {
				t.Errorf("段3 销售方 = %q, want %q", inv.Seller, c.wantSell)
			}
			// 落盘的必须是 zip 里的**票面**（真实约 105KB），不是 45KB 的汇总单。
			full := filepath.Join(dataOut, inv.FilePath)
			data, rerr2 := os.ReadFile(full)
			if rerr2 != nil {
				t.Fatalf("段3 读不到落盘文件 %s：%v", full, rerr2)
			}
			if !strings.HasSuffix(full, ".pdf") {
				t.Errorf("段3 落盘不是 PDF：%s", full)
			}
			if len(data) < 80*1024 {
				t.Errorf("段3 落盘文件只有 %d 字节；真实票面 PDF 约 105KB，"+
					"45KB 的是**汇总单** —— 说明又存错了（%s）", len(data), inv.FileName)
			}
			t.Logf("段3 落盘 %d 字节（票面量级，非 45KB 汇总单）", len(data))
			// 规范文件名应含费用类型-单位-金额-日期-发票号
			t.Logf("段3 规范文件名 = %s", inv.FileName)
			if !strings.Contains(inv.FileName, c.wantNo) {
				t.Errorf("段3 文件名 %q 不含发票号 %q；缺了它，同单位同额同日的票会撞名",
					inv.FileName, c.wantNo)
			}
		})
	}
}
