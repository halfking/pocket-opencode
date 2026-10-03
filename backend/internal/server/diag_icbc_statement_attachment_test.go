package server

// diag_icbc_statement_attachment_test.go —— **只读**诊断：那封工行信用卡对账单
// 到底带没带「发票类附件」？
//
// ## 为什么必须先弄清这件事
//
// `admitDebtNotice(joined, hasInvoiceAttachment)` 的两个分支实测是
// **false / true**（见 diag_stale_debt_notice_row_test.go）：不带发票类附件就拒，
// 带上就放行。而流水线在 envelope 不命中时会走 `ExtractInvoiceLoose(…,
// HasInvoiceAttachment(attachments))` 那条腿，附件证据正是在那里生效。
//
// 于是有一个岔路口，而它决定「给那一行加终止重试」是不是在掩盖真问题：
//
//   - 带发票类附件 → 采集器当初**应该**用附件建档，却跑去抓了两个 HTML 落地页
//     （112KB / 523KB）。那是一个**真缺陷**，加终止重试等于把它盖住。
//   - 不带 → 它只是一次早于修复的误建档，终止重试就是止血，干净。
//
// ## 走的是哪条路
//
// 本诊断**不自己实现解密**：直接调生产读路径
// `(*Server).readCachedEmailBody`（它按 8 字节 UID + 1 字节格式版本读头、
// 校验 UID、拒绝 legacy 格式、交给 s.emailCrypto 解密），密钥走生产
// `email.EnsureMasterKey("", dataDir)` 的回退路径（data/email_master.key），
// 正文用生产 `email.ParseMIMEMessage` 解析，附件判定用生产
// `email.HasInvoiceAttachment`。
//
// 自己重写一遍解密就等于「测的是测试自己写的代码」——本仓多处踩过这个坑。
//
// ## 安全与只读
//
//   - **只读**：只 os.ReadFile，不写任何文件、不连数据库、不发网络请求。
//   - **不打印密钥，也不打印正文**；只打印附件的元数据（文件名 / 类型 / 字节数）
//     与若干统计量。
//
// 门禁 POCKET_DIAG_ICBC_ATTACH=1，且必须显式给出 dataDir（POCKET_DIAG_DATA_DIR），
// 避免它默认指向某个真实数据目录。

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// readCacheHeader 读缓存文件头里的 UID 与格式版本，不解密、不打印任何密文。
func readCacheHeader(t *testing.T, dataDir, emailID string) (int64, byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, bodyCacheDirName, emailID+".bin"))
	if err != nil {
		t.Fatalf("read cache header (%v) —— 缓存文件不存在，是四种空返回原因之一", err)
	}
	if len(data) < 9 {
		t.Fatalf("cache file is %d bytes, too short to hold a header", len(data))
	}
	return int64(binary.BigEndian.Uint64(data[:8])), data[8]
}

func TestDiagICBCStatementAttachment(t *testing.T) {
	if os.Getenv("POCKET_DIAG_ICBC_ATTACH") != "1" {
		t.Skip("set POCKET_DIAG_ICBC_ATTACH=1 to run this read-only diagnostic")
	}
	dataDir := os.Getenv("POCKET_DIAG_DATA_DIR")
	if dataDir == "" {
		t.Skip("POCKET_DIAG_DATA_DIR not set; refusing to guess a data directory")
	}

	// uid 必须与库里一致，否则 readCachedEmailBody 会按「旧缓存」返回未命中，
	// 而那会让我把「格式不认」误读成「没有附件」。
	const (
		emailID = "em-1298896144-acct-1790870162079171800-5"
		uid     = int64(1298896144)
	)

	key, err := email.EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey: %v", err)
	}
	t.Logf("master key 来源：EnsureMasterKey 的回退路径（data/email_master.key），长度 %d 字节（不打印内容）", len(key))
	cr, err := email.NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	// 只用到 readCachedEmailBody 触碰的那两个字段，其余保持零值。
	s := &Server{emailCrypto: cr, dataDir: dataDir}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 先量一次文件头里的 UID，并如实报告它与库里的 uid 是否一致。
	//
	// 这是本诊断**必须**先分清的一件事：readCachedEmailBody 返回空有四种原因
	//（不存在 / UID 不匹配 / legacy 格式 / 解密失败），它们在返回值上**长得一样**。
	// 不先量就下结论，就会把「UID 对不上」误当成「没有附件」。
	headerUID, formatByte := readCacheHeader(t, dataDir, emailID)
	t.Logf("缓存文件头：UID=%d format=0x%02X；库里 emails.uid=%d", headerUID, formatByte, uid)
	if headerUID != uid {
		t.Logf("⚠ 文件头 UID(%d) 与库里的 uid(%d) 不一致 —— 生产读路径会按「旧缓存」判未命中，"+
			"也就是说**这个缓存对生产详情页而言是死的**。下面传 expectedUID=0 绕过这道校验，"+
			"只为把附件信息读出来；这是诊断专用，不是建议改生产校验。", headerUID, uid)
	}

	raw, err := s.readCachedEmailBody(ctx, emailID, 0)
	if err != nil {
		t.Fatalf("readCachedEmailBody: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("readCachedEmailBody 返回空 —— 即使绕过 UID 校验仍为空，则成因只剩三种："+
			"缓存不存在 / legacy 格式 / 解密失败。文件头 format=0x%02X（0x01=MIME, 0x02=text, "+
			"0x00/其它=legacy 判未命中）。", formatByte)
	}
	t.Logf("解密成功：原文 %d 字节", len(raw))

	parsed, perr := email.ParseMIMEMessage(raw)
	if perr != nil {
		t.Fatalf("ParseMIMEMessage: %v", perr)
	}
	t.Logf("主题=%s 发件人=%s", parsed.Subject, parsed.From)
	t.Logf("正文：text=%d 字节 html=%d 字节", len(parsed.TextBody), len(parsed.HTMLBody))
	t.Logf("附件数=%d", len(parsed.Attachments))

	for i, a := range parsed.Attachments {
		t.Logf("  [%d] filename=%q content-type=%q bytes=%d",
			i, a.Filename, a.ContentType, len(a.Data))
	}

	has := email.HasInvoiceAttachment(parsed.Attachments)
	t.Logf("=== 关键判定：HasInvoiceAttachment = %v ===", has)
	if has {
		t.Logf("→ 这封信带着发票类附件。**终止重试会掩盖真缺陷**：采集器当初应当用附件建档，" +
			"却去抓了两个 HTML 落地页（112KB / 523KB）。该查的是采集器为什么没用附件。")
	} else {
		t.Logf("→ 这封信**不带**发票类附件。那么 admitDebtNotice(joined,true)=true 这个分支在" +
			"本封上不会开：它只是一次早于 2026-10-02 修复的误建档，「终止重试」是止血而非掩盖。")
	}
}
