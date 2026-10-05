package email

// diag_amount_provenance_test.go — **只读**诊断：台账里每一张已下载发票的
// **金额与票号**，是否真的能在它的来源里找到。
//
// ## 为什么需要它
//
// 第三十五节查出：58000 那一行的「金额」其实来自信用卡**信用额度**、
// 「日期」来自**到期还款日**——两个字段都不是发票事实。
// 那是个孤例还是一类问题？「汇总金额 4038.01 与台账一致」**证不了**这个：
// 一致性只保证三处口径相同，不保证**抽出来的数本身是对的**。
//
// 这是「自动解析各类发票」这条链上最后一条没验过的链接：
// 解析器抽的金额 == 票面真实金额。
//
// ## 判据：来源必须包含该金额与票号，且不得有「非发票金额」语义
//
// - 金额/票号要在**邮件正文或票面 PDF** 里找得到（两者都算，票面更权威）
// - 命中一个「看起来像金额的数字」**不够**——额度/余额/上限同样是数字。
//   所以额外扫一组**非发票金额语义词**（信用额度、应还款、余额…），
//   出现即报：说明这封邮件里的金额字段很可能取错了位置。
//
// 期望值是台账行自己的值，但**判据的成败不依赖它对不对**——
// 它只回答「这个数在来源里找不找得到」，找得到 ≠ 对，找不到 = 一定有问题。
//
// ## 只读
//
// 连接上 `SET default_transaction_read_only = on`；只读 dataDir 下的票面。
// 门控 POCKET_DIAG_AMOUNT_PROV=1 + 显式 DSN/SCHEMA/DATA_DIR。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// nonInvoiceAmountWords 是「这个数字不是发票金额」的语义信号。
// 命中即说明该邮件里存在额度/余额/还款一类数字，与发票金额无关。
var nonInvoiceAmountWords = []string{
	"信用额度", "授信额度", "额度", "限额", "上限",
	"应还款", "最低还款", "还款日", "已用额度", "可用额度",
}

var reAmountLiteral = regexp.MustCompile(`^\d+\.\d{2}$`)

// hasAmount 判断来源里是否出现该金额。
//
// 必须同时试**带千分位逗号**的形态：正文里是 `58,000.00`，而
// fmt.Sprintf("%.2f") 给的是 `58000.00`。只比后者会把「数字就在眼前」
// 报成「无来源」——本轮第一版就栽在这（58000 明明在正文里却报 no）。
func hasAmount(hay, amountLit string) bool {
	if strings.Contains(hay, amountLit) || strings.Contains(hay, trimZeros(amountLit)) {
		return true
	}
	// 58,000.00 ← 58000.00
	if i := strings.IndexByte(amountLit, '.'); i > 0 {
		whole := amountLit[:i]
		var b strings.Builder
		for j := 0; j < len(whole); j++ {
			if j > 0 && (len(whole)-j)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteByte(whole[j])
		}
		withComma := b.String() + amountLit[i:]
		if strings.Contains(hay, withComma) {
			return true
		}
	}
	return false
}

func TestDiagAmountProvenance(t *testing.T) {
	if os.Getenv("POCKET_DIAG_AMOUNT_PROV") != "1" {
		t.Skip("set POCKET_DIAG_AMOUNT_PROV=1 to run (read-only)")
	}
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dsn == "" || schema == "" || dataDir == "" {
		t.Fatal("POCKET_REAL_MAIL_DSN / SCHEMA / DATA_DIR 必须显式给全")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	safe := pgx.Identifier{schema}.Sanitize()
	cfg.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(c, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := conn.Exec(c, "SET search_path TO "+safe)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// **不按 status 过滤**：把 pending 行也带进来，否则「非发票金额语义词」
	// 这条检测永远不会被触发，看不出它到底有没有鉴别力。
	// 58000 那一行正是反例——它的 58,000.00 确实「在来源里」(naive 判据说
	// 已对上)，只有语义扫描才能揭穿它取的是信用卡额度。
	rows, err := pool.Query(ctx,
		`SELECT i.amount, i.invoice_no, i.file_path, e.snippet, e.subject, i.status
		   FROM email_invoices i JOIN emails e ON e.id = i.email_id
		  WHERE i.file_path IS NOT NULL OR i.status <> 'downloaded'
		  ORDER BY i.amount`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	n, provOK, risky := 0, 0, 0
	for rows.Next() {
		var amt float64
		var no, fp, snip, subj, status string
		if err := rows.Scan(&amt, &no, &fp, &snip, &subj, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		amountLit := fmt.Sprintf("%.2f", amt)
		if !reAmountLiteral.MatchString(amountLit) {
			t.Errorf("金额格式异常: %q", amountLit)
		}

		// 来源 1：邮件正文 + 主题
		mail := compact(snip + " " + subj)
		inMail := hasAmount(mail, amountLit)
		noInMail := no == "" || strings.Contains(mail, no)

		// 来源 2：票面 PDF（更权威）
		inPDF, noInPDF := false, false
		full := fp
		if !filepath.IsAbs(full) {
			full = filepath.Join(dataDir, fp)
		}
		if b, rerr := os.ReadFile(full); rerr == nil {
			pdf := compact(string(b) + "\n" + inflateAllStreams(b))
			inPDF = hasAmount(pdf, amountLit)
			noInPDF = no == "" || strings.Contains(pdf, no)
		}

		hit := "**无来源**"
		if inMail || inPDF {
			hit = "已对上"
			provOK++
		}
		// 非发票金额语义：只在**邮件文本**里扫。票面 PDF 里出现「余额」之类
		// 是正常的对账信息，不是「这个金额取错了」的证据。
		var found []string
		for _, w := range nonInvoiceAmountWords {
			if strings.Contains(mail, w) {
				found = append(found, w)
			}
		}
		flag := ""
		if len(found) > 0 {
			flag = "  ⚠ 邮件含非发票金额语义词: " + strings.Join(found, ",")
			risky++
		}
		t.Logf("[%s] %10s 票号=%-20s 邮件(金额/号)=%s/%s 票面(金额/号)=%s/%s  %s%s",
			status, amountLit, short(no), yn(inMail), yn(noInMail), yn(inPDF), yn(noInPDF),
			hit, flag)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if n == 0 {
		t.Fatal("没有已下载且有文件的发票行：先确认数据")
	}
	t.Logf("小计 %d 张，金额在来源中命中 %d 张，含非发票金额语义词 %d 张", n, provOK, risky)
}

func compact(s string) string {
	return strings.NewReplacer("\x00", "", " ", "", "\n", "", "\r", "", "\t", "").Replace(s)
}

func trimZeros(a string) string {
	return strings.TrimSuffix(a, ".00")
}

func yn(b bool) string {
	if b {
		return "YES"
	}
	return " no"
}

func short(s string) string {
	if len(s) > 20 {
		return s[:20]
	}
	return s
}
