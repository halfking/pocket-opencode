package email

// merge_dupe_test.go — 合并判定的三道安全闸（纯函数，无 DB 无网络）。
//
// 这段判定决定「哪些邮件行会被打墓碑」。判错一次就是**真实数据丢失**，
// 所以每个用例都配负控对照：把对应闸门去掉，用例必须转红。
//
// §7w 已实证「同主题同时刻可能是两封不同邮件」，所以闸 2（真实 Message-ID
// 相等）是唯一可靠的判据——去掉它，这段代码就退化成按标题去重。

import (
	"errors"
	"strings"
	"testing"
)

func imapSide() MergeSide {
	return MergeSide{EmailID: "em-10432-acct-3", IsPOP3: false, UID: 10432,
		RealMessageID: "010001a0ceb7-4159@amazonaws.com"}
}

func pop3Side() MergeSide {
	return MergeSide{EmailID: "em-pop3-acct-3-ZC0023_7u_N", IsPOP3: true, UID: 260,
		RealMessageID: "010001a0ceb7-4159@amazonaws.com"}
}

func TestPlanMergeDupes_ConfirmedPairKeepsIMAPSide(t *testing.T) {
	plan, err := planMergeDupes(imapSide(), pop3Side(), "AWS 账户提醒")
	if err != nil {
		t.Fatalf("confirmed pair must plan a merge: %v", err)
	}
	if plan.KeepEmailID != "em-10432-acct-3" {
		t.Fatalf("KeepEmailID=%q, want the IMAP side", plan.KeepEmailID)
	}
	if plan.TombstoneID != "em-pop3-acct-3-ZC0023_7u_N" {
		t.Fatalf("TombstoneID=%q, want the POP3 side", plan.TombstoneID)
	}
}

func TestPlanMergeDupes_ArgumentOrderDoesNotMatter(t *testing.T) {
	// 调用方可能先拿到 POP3 侧；判定结果必须与参数顺序无关。
	p1, err1 := planMergeDupes(imapSide(), pop3Side(), "s")
	p2, err2 := planMergeDupes(pop3Side(), imapSide(), "s")
	if err1 != nil || err2 != nil {
		t.Fatalf("both orders must plan: %v / %v", err1, err2)
	}
	if p1 != p2 {
		t.Fatalf("plan depends on argument order:\n  %+v\n  %+v", p1, p2)
	}
}

func TestPlanMergeDupes_DifferentMessageIDsAreNotDuplicates(t *testing.T) {
	// §7w 实测的真实场景：network-switch 两封同主题同时刻但 message_id 不同。
	p3 := pop3Side()
	p3.RealMessageID = "mis_0055C0BF65985D1D0FD550CE@qq.com"
	_, err := planMergeDupes(imapSide(), p3, "【network-switch】邮件通道测试")
	if !errors.Is(err, ErrSkipMerge) {
		t.Fatalf("different message-ids must NOT merge, got err=%v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "two different emails") {
		t.Fatalf("error should explain it's two different emails, got %q", err.Error())
	}
}

func TestPlanMergeDupes_MissingMessageIDIsSkipped(t *testing.T) {
	// 任一侧取不到真实 Message-ID（邮件已删除等）-> 不动。
	// 绝不能「反正主题一样就合」。
	p3 := pop3Side()
	p3.RealMessageID = ""
	if _, err := planMergeDupes(imapSide(), p3, "s"); !errors.Is(err, ErrSkipMerge) {
		t.Fatalf("missing drop-side message-id must skip, got %v", err)
	}
	p1 := imapSide()
	p1.RealMessageID = ""
	if _, err := planMergeDupes(p1, pop3Side(), "s"); !errors.Is(err, ErrSkipMerge) {
		t.Fatalf("missing keep-side message-id must skip, got %v", err)
	}
}

func TestPlanMergeDupes_SameSourcePairIsSkipped(t *testing.T) {
	// 两侧都是 POP3（或都是 IMAP）-> 不是「IMAP+POP3 重复副本」，
	// 那是同协议内的多次真实投递（§7v 实测 90/90 订阅提醒），不能碰。
	other := pop3Side()
	other.EmailID = "em-pop3-acct-3-ZC0001_other"
	if _, err := planMergeDupes(pop3Side(), other, "您的订阅额度即将用尽"); !errors.Is(err, ErrSkipMerge) {
		t.Fatalf("same-source pair must skip, got %v", err)
	}
}

func TestPlanMergeDupes_KeepSideNeedsRealIMAPUID(t *testing.T) {
	// 保留侧必须持有真实 IMAP UID；uid<=0 说明它拿不到 IMAP 正文，
	// 打墓碑等于把唯一可回取的记录弄丢。
	bad := imapSide()
	bad.UID = 0
	if _, err := planMergeDupes(bad, pop3Side(), "s"); !errors.Is(err, ErrSkipMerge) {
		t.Fatalf("keep side without IMAP uid must skip, got %v", err)
	}
}
