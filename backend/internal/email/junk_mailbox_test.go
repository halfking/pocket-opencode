package email

// junk.go 垃圾箱定位的纯函数测试（需求 2「将它们移到垃圾邮件箱」）。
//
// 背景：findJunkMailbox 靠两步定位垃圾箱——
//  1. RFC 6154 的 \Junk 特殊用途属性；
//  2. 常见命名匹配（junkMailboxNames），比对前先用 baseMailboxName 剥掉层级前缀。
// 这两步都抽成了纯函数，但此前**零测试**。而这一步一旦判错，
// MoveUIDsToJunk 会返回 ErrNoJunkMailbox，调用方只做本地标记，
// 真实邮件**不会被移进垃圾箱**——需求 2 静默降级，且不报错。
//
// 真实 IMAP 链路（Greenmail）验证需要 Docker，当前环境不可用，
// 且现有 fetcher_greenmail_test.go 也不覆盖 MOVE 路径，只覆盖同步。
// 因此这里先把可测的判定部分钉住。
//
// 负控：
//   - hasMailboxAttr 只查 attrs[0] -> TestHasMailboxAttr_... 转红
//   - baseMailboxName 去掉 '.' 分隔符 -> TestBaseMailboxName_... 转红

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestHasMailboxAttr_FindsJunk(t *testing.T) {
	attrs := []imap.MailboxAttr{imap.MailboxAttrJunk}
	if !hasMailboxAttr(attrs, imap.MailboxAttrJunk) {
		t.Fatal("含 \\Junk 属性却没认出来 —— 垃圾箱会退化成按名字猜")
	}
}

// 陷阱：属性是切片，真实 LIST 响应里一个信箱常带多个属性。
// 只查 attrs[0] 的实现会在这里漏判。
func TestHasMailboxAttr_ScansWholeSlice(t *testing.T) {
	attrs := []imap.MailboxAttr{imap.MailboxAttrNoInferiors, imap.MailboxAttrJunk}
	if !hasMailboxAttr(attrs, imap.MailboxAttrJunk) {
		t.Fatal("\\Junk 不在首位就没认出来 —— 说明只查了 attrs[0]")
	}
	attrs = []imap.MailboxAttr{imap.MailboxAttrJunk, imap.MailboxAttrNoSelect}
	if !hasMailboxAttr(attrs, imap.MailboxAttrJunk) {
		t.Fatal("\\Junk 在首位但后面还有属性时反而没认出来")
	}
}

func TestHasMailboxAttr_AbsentAndEmpty(t *testing.T) {
	if hasMailboxAttr([]imap.MailboxAttr{imap.MailboxAttrSent}, imap.MailboxAttrJunk) {
		t.Fatal("已发送箱被判成垃圾箱 —— 会把正常邮件移走")
	}
	if hasMailboxAttr(nil, imap.MailboxAttrJunk) {
		t.Fatal("nil 属性被当成含 \\Junk")
	}
	if hasMailboxAttr([]imap.MailboxAttr{}, imap.MailboxAttrJunk) {
		t.Fatal("空属性被当成含 \\Junk")
	}
}

func TestBaseMailboxName_StripsHierarchy(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"Junk", "Junk", "顶层信箱原样返回"},
		{"INBOX.Junk", "Junk", "IMAP 标准层级分隔符 '.'"},
		{"INBOX\\Junk", "Junk", "部分服务商用反斜杠"},
		{"其他文件夹/垃圾邮件", "垃圾邮件", "QQ 企业邮的前缀形态（junk.go:79 注释所述）"},
		{"", "", "空名不炸"},
	}
	for _, c := range cases {
		if got := baseMailboxName(c.in); got != c.want {
			t.Errorf("baseMailboxName(%q) = %q, want %q（%s）", c.in, got, c.want, c.why)
		}
	}
}

// baseMailboxName 的产物要能被 junkMailboxNames 匹配上（大小写不敏感），
// 否则 2 号定位路径整体失效。
func TestBaseMailboxName_FeedsJunkNameMatching(t *testing.T) {
	for _, full := range []string{"Junk", "INBOX.Junk", "INBOX\\Junk", "其他文件夹/垃圾邮件"} {
		base := baseMailboxName(full)
		matched := false
		for _, want := range junkMailboxNames {
			if equalFoldASCII(base, want) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("baseMailboxName(%q)=%q 未命中任何常见垃圾箱名", full, base)
		}
	}
}

// 非垃圾箱的层级名不得被误判成垃圾箱。
func TestBaseMailboxName_NoFalsePositiveOnInbox(t *testing.T) {
	for _, full := range []string{"INBOX", "INBOX.Sent", "其他文件夹/已发送", "INBOX.Archive"} {
		base := baseMailboxName(full)
		for _, want := range junkMailboxNames {
			if equalFoldASCII(base, want) {
				t.Errorf("baseMailboxName(%q)=%q 被误判为垃圾箱（命中 %q）", full, base, want)
			}
		}
	}
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
