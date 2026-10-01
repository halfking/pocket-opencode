package email

import (
	"strings"
)

// organize.go — 智能识别「系统通知类」邮件的启发式。
//
// 需求：收件箱里大量类似标题的系统通知（验证码、物流、订阅、公告、日报……）
// 应一键整理进独立目录。分类器（classify）已经给一部分邮件打了 notification
// 标签，但依赖 LLM 且只覆盖已归类的行；这里用**零成本确定性规则**补齐剩余：
//
//   1. 发件人形态：noreply / no-reply / notify / alerts / newsletter /mailer-daemon
//      这类 local-part 是系统通知的最强信号；
//   2. 标题形态：验证码 / 通知 / 提醒 / 公告 / 日报…（中文）与 verify /
//      notification / newsletter / receipt / digest…（英文）；
//   3. 标题重复度：同一「规整化标题」出现 ≥3 次的整组视为系统群发
//      ——这是「大量类似标题」最直接的量化。
//
// 规则刻意保守偏误杀为零：个人邮件误进通知目录的代价比漏掉一封通知高。
// 规整化会剥掉 Re:/Fwd: 前缀、数字、日期与长随机串，让「您的快递 LT123456
// 已到驿站」和「您的快递 LT987654 已到驿站」落进同组。

// notifySenderParts 发件人 local-part 命中即认定（按 @ 前的整段或 `-`/`_`/`.` 分词匹配）。
var notifySenderParts = []string{
	"noreply", "no-reply", "no_reply", "donotreply", "do-not-reply",
	"notification", "notifications", "notify",
	"newsletter", "newsletters", "mailer", "mailer-daemon", "postmaster",
	"alert", "alerts", "notice", "announce", "announcement", "broadcast",
	"account-notifications", "service", "system", "messaging",
}

// notifySubjectKeywords 标题关键字（小写包含匹配，中英文都查）。
var notifySubjectKeywords = []string{
	// 中文
	"验证码", "校验码", "动态码", "系统通知", "系统消息", "系统提醒",
	"到账通知", "账单提醒", "物流跟踪", "快递", "取件码", "签收",
	"订阅", "退订", "公告", "通知", "提醒您", "日报", "周报", "月报",
	"尊敬的用户", "尊敬的客户", "请查收", "注册成功", "激活成功",
	// 英文（按词干收录，小写包含匹配）
	"verification code", "verify your", "confirm your", "password reset",
	"notification", "newsletter", "digest", "receipt", "invoice is ready",
	"order confirmed", "shipped", "tracking", "one-time code", "otp",
	"security alert", "login alert", "sign-in", "action required",
	"no-reply", "unsubscribe", "announcing", "your weekly", "your daily",
}

// normalizeNotifySubject 规整化标题用于重复分组：
// 剥 Re:/Fwd:/回复:/转发: 前缀 → 去数字/日期/长随机串 → 压空白 → 小写。
func normalizeNotifySubject(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, prefix := range []string{"re:", "fwd:", "fw:", "回复：", "回复:", "转发：", "转发:"} {
		lower = strings.TrimPrefix(lower, prefix)
	}
	var b strings.Builder
	for _, r := range lower {
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			b.WriteRune(' ')
		case r >= 'a' && r <= 'z' || r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
		default:
			// 数字、符号、日期、单号等一律折叠成分隔符。
			b.WriteRune(' ')
		}
	}
	// 压缩连续空格。
	fields := strings.Fields(b.String())
	return strings.Join(fields, " ")
}

// NotificationMatch 是单封邮件的识别结论。
type NotificationMatch struct {
	Matched bool
	Reason  string // sender | subject | repeat | category
}

// MatchNotification 对单封邮件跑启发式。groupSubjects 是调用方预先算好的
// 「出现 ≥minRepeat 次」的规整化标题集合（可空）。
func MatchNotification(fromAddress, subject, category string, groupSubjects map[string]struct{}) NotificationMatch {
	if strings.EqualFold(category, "notification") {
		return NotificationMatch{true, "category"}
	}
	local := strings.ToLower(fromAddress)
	if at := strings.IndexByte(local, '@'); at > 0 {
		local = local[:at]
	}
	for _, part := range notifySenderParts {
		if local == part || strings.Contains(local, part) {
			return NotificationMatch{true, "sender:" + part}
		}
	}
	subj := strings.ToLower(subject)
	for _, kw := range notifySubjectKeywords {
		if strings.Contains(subj, kw) {
			return NotificationMatch{true, "subject:" + kw}
		}
	}
	if groupSubjects != nil {
		if _, hit := groupSubjects[normalizeNotifySubject(subject)]; hit && subject != "" {
			return NotificationMatch{true, "repeat"}
		}
	}
	return NotificationMatch{false, ""}
}

// SelectNotificationEmails 从一批邮件里挑出系统通知类。
// 重复标题组的阈值 minRepeat 取 3：同类通知一周来三封以上就是「大量」。
func SelectNotificationEmails(emails []Email) ([]Email, []string) {
	if len(emails) == 0 {
		return nil, nil
	}
	const minRepeat = 3
	counts := make(map[string]int, len(emails))
	for _, e := range emails {
		if key := normalizeNotifySubject(e.Subject); key != "" {
			counts[key]++
		}
	}
	groups := make(map[string]struct{}, len(counts))
	for key, n := range counts {
		if n >= minRepeat {
			groups[key] = struct{}{}
		}
	}
	out := make([]Email, 0, len(emails)/2)
	reasons := make([]string, 0, len(emails)/2)
	for _, e := range emails {
		m := MatchNotification(e.FromAddress, e.Subject, e.Category, groups)
		if m.Matched {
			out = append(out, e)
			reasons = append(reasons, m.Reason)
		}
	}
	return out, reasons
}
