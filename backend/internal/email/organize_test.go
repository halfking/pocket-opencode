package email

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestNormalizeNotifySubject(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Re: 您的订单已发货", "您的订单已发货"},
		{"Fwd:  通知：系统维护", "通知 系统维护"},
		{"您的快递 LT123456789 已到驿站", "您的快递 lt 已到驿站"},
		{"【重要】10月账单提醒 (2026-10-01)", "重要 月账单提醒"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeNotifySubject(tc.in); got != tc.want {
			t.Errorf("normalizeNotifySubject(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// 同组标题（仅单号不同）应落进同一规整化结果。
	a := normalizeNotifySubject("您的快递 LT111111 已到驿站")
	b := normalizeNotifySubject("您的快递 LT999999 已到驿站")
	if a != b {
		t.Errorf("similar subjects diverged: %q vs %q", a, b)
	}
}

func TestMatchNotification(t *testing.T) {
	groups := map[string]struct{}{"您的快递 已到驿站": {}}
	cases := []struct {
		name        string
		from        string
		subject     string
		category    string
		wantMatched bool
		wantReason  string
	}{
		{"category直判", "someone@example.com", "随便聊聊", "notification", true, "category"},
		{"发件人noreply", "noreply@taobao.com", "随便聊聊", "", true, "sender:noreply"},
		{"标题验证码", "cs@bank.cn", "您的登录验证码是 882133", "", true, "subject:验证码"},
		{"重复标题组", "express@example.com", "您的快递 LT88213 已到驿站", "", true, "subject:快递"},
		{"普通邮件不命中", "friend@example.com", "周末一起吃饭吗", "", false, ""},
		{"空标题不按重复判", "express@example.com", "", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchNotification(tc.from, tc.subject, tc.category, groups)
			if got.Matched != tc.wantMatched {
				t.Fatalf("matched = %v (%s), want %v", got.Matched, got.Reason, tc.wantMatched)
			}
			if tc.wantReason != "" && got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

func TestSelectNotificationEmails(t *testing.T) {
	emails := []Email{
		{ID: "1", FromAddress: "noreply@x.com", Subject: "验证码 123"},
		{ID: "2", FromAddress: "buddy@example.com", Subject: "晚饭吃啥"},
		// 同主题三连发 → repeat 组。
		{ID: "3", FromAddress: "a@x.com", Subject: "系统通知：例行维护 10-01"},
		{ID: "4", FromAddress: "b@x.com", Subject: "系统通知：例行维护 10-02"},
		{ID: "5", FromAddress: "c@x.com", Subject: "系统通知：例行维护 10-03"},
		// 已在目录里的也照常参与识别（调用方决定范围）。
		{ID: "6", FromAddress: "d@x.com", Subject: "系统通知：例行维护 10-04", FolderName: "通知"},
	}
	picked, reasons := SelectNotificationEmails(emails)
	if len(picked) != 5 {
		t.Fatalf("picked %d emails (%v), want 5", len(picked), ids(picked))
	}
	if picked[0].ID != "1" {
		t.Errorf("first picked = %s, want 1", picked[0].ID)
	}
	if reasons[0] == "" {
		t.Error("reasons must be populated for picked emails")
	}
	// 「晚饭吃啥」绝不能被误收。
	for _, p := range picked {
		if p.ID == "2" {
			t.Error("personal email wrongly matched as notification")
		}
	}
}

func ids(emails []Email) []string {
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		out = append(out, e.ID)
	}
	return out
}

func TestClassifyMailbox(t *testing.T) {
	cases := []struct {
		name  string
		attrs []imap.MailboxAttr
		want  string
	}{
		{"INBOX", nil, "inbox"},
		{"属性Trash", []imap.MailboxAttr{imap.MailboxAttrTrash}, "trash"},
		{"属性Junk", []imap.MailboxAttr{imap.MailboxAttrJunk}, "junk"},
		{"中文已删除", nil, "trash"},
		{"QQ层级垃圾邮件", nil, "junk"},
		{"普通目录", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in string
			switch tc.name {
			case "INBOX":
				in = "INBOX"
			case "属性Trash":
				in = "Trash"
			case "属性Junk":
				in = "Junk"
			case "中文已删除":
				in = "已删除邮件"
			case "QQ层级垃圾邮件":
				in = "其他文件夹/垃圾邮件"
			case "普通目录":
				in = "账单"
			}
			if got := classifyMailbox(in, tc.attrs); got != tc.want {
				t.Errorf("classifyMailbox(%q) = %q, want %q", in, got, tc.want)
			}
		})
	}
}
