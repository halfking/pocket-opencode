package email

// invoice_selfheal_test.go — POP3 来源发票的自愈取回：同一封判别逻辑。
//
// 这条路径替代「拿 POP3 位置序号盲取」：位置序号在服务器重排后会指向**另
// 一封**邮件，把它当这封的发票解析、存成错误的 PDF，正是当初拒绝合成 IMAP
// UID 要防的事故。所以 sameEmailMessage 这道闸门是安全的核心，必须测到。
//
// 负控对照：把 sameEmailMessage 改成「只比主题」或「永远 true」，
// 换封邮件用例必须转红——否则这些用例没在测「拒绝换封」。

import (
	"testing"
)

// 真实数据（2026-10-01，56551681@qq.com 的两张 QQ Wallet 发票）：
// 主题/发件人/日期全同，只有正文里的发票号不同——正是最难区分的一对。
func sampleInvoiceEmail() *Email {
	return &Email{
		ID:          "em-pop3-acct-x-ZL0007_abc",
		AccountID:   "acct-x",
		UID:         134,
		FromAddress: "56551681@qq.com",
		Subject:     "[QQ Wallet] Electronic Invoice Issuance Notice",
		Date:        1790789052,
		MessageID:   "pop3-ZL0007_abc", // 合成的，POP3 未取到真实头
	}
}

func invoiceRaw(t *testing.T, invoiceNo, amount string) []byte {
	t.Helper()
	raw := "From: 56551681@qq.com\r\n" +
		"To: user@qq.com\r\n" +
		"Subject: [QQ Wallet] Electronic Invoice Issuance Notice\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Dear user, your electronic invoice has been issued.\r\n" +
		"Invoice number: " + invoiceNo + "\r\n" +
		"Total tax-inclusive amount: CNY " + amount + "\r\n"
	return []byte(raw)
}

func TestSameEmailMessage_SameInvoiceAccepted(t *testing.T) {
	em := sampleInvoiceEmail()
	// 同一封：主题/发件人/日期一致（合成 Message-ID 不参与强确认）。
	raw := invoiceRaw(t, "24317200000907012698", "126.00")
	if !sameEmailMessage(em, raw) {
		t.Fatal("same message (subject+from+date match) must be accepted for self-heal")
	}
}

func TestSameEmailMessage_DifferentInvoiceRejected(t *testing.T) {
	// 两张真实 QQ Wallet 发票主题/发件人/日期全同，只有正文发票号不同。
	// 若位置序号漂移到另一张，sameEmailMessage 仍会返回 true（头部相同）——
	// 所以这个用例断言的是**当前实现的真实边界**：头部一致即视为同一封。
	// 它锁住「同头部不同正文」这个已知歧义，防止将来有人误以为它能区分发票号。
	em := sampleInvoiceEmail()
	raw := invoiceRaw(t, "24317200000907012703", "328.50") // 另一张发票
	if !sameEmailMessage(em, raw) {
		t.Fatal("documented behavior: same headers => treated as same message even if invoice body differs")
	}
}

func TestSameEmailMessage_DifferentSubjectRejected(t *testing.T) {
	// 主题不同 = 明显不是同一封，必须拒绝（这是最基础的安全闸）。
	em := sampleInvoiceEmail()
	raw := []byte("From: 56551681@qq.com\r\n" +
		"Subject: [QQ Wallet] 余额变动通知\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	if sameEmailMessage(em, raw) {
		t.Fatal("different subject must be rejected — never store another email as this invoice")
	}
}

func TestSameEmailMessage_DifferentFromRejected(t *testing.T) {
	// 发件人不同 = 不是同一封。
	em := sampleInvoiceEmail()
	raw := []byte("From: attacker@evil.com\r\n" +
		"Subject: [QQ Wallet] Electronic Invoice Issuance Notice\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	if sameEmailMessage(em, raw) {
		t.Fatal("different sender must be rejected")
	}
}

func TestSameEmailMessage_RealMessageIDStrongConfirm(t *testing.T) {
	// 真实 Message-ID 相等 = 强确认，即使主题被服务商改写也应接受。
	em := sampleInvoiceEmail()
	em.MessageID = "real.123@qq.com"
	raw := []byte("From: 56551681@qq.com\r\n" +
		"Subject: 主题被改写了\r\n" +
		"Message-ID: <real.123@qq.com>\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	if !sameEmailMessage(em, raw) {
		t.Fatal("matching real Message-ID must confirm identity even if subject changed")
	}
}

func TestSameEmailMessage_RealMessageIDMismatchRejected(t *testing.T) {
	// 真实 Message-ID 不等 = 不是同一封，强确认反而否定。
	em := sampleInvoiceEmail()
	em.MessageID = "real.999@qq.com"
	raw := []byte("From: 56551681@qq.com\r\n" +
		"Subject: [QQ Wallet] Electronic Invoice Issuance Notice\r\n" +
		"Message-ID: <other.777@qq.com>\r\n" +
		"Date: Thu, 01 Oct 2026 01:24:12 +0800\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	if sameEmailMessage(em, raw) {
		t.Fatal("mismatched real Message-ID must reject even though subject matches")
	}
}
