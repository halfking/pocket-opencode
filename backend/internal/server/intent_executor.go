package server

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// intentExecutor 消费 email_action_intents 里 route-folder / trigger-autoreply 意图。
//
// 设计取舍：
//   - route-folder 本期只标记 applied（不做真实 IMAP MOVE）。MOVE 需要 fetcher 暴露
//     UID MOVE 能力 + UIDVALIDITY 处理，超出本次切片；返回 ErrSkipIntent 让调度器
//     标 skipped（终态），意图仍保留行可观测，后续接 IMAP MOVE 时改 Execute 即可。
//   - trigger-autoreply 复用 vacation 配置（若账户有 enabled 且在时间窗内的 vacation，
//     用其 subject/body）作为自动回复正文；无 vacation 配置时用默认模板。SMTP 发送
//     复用 smtpSendMessage（与 vacation/handleEmailSend 同一安全姿态）。
type intentExecutor struct {
	store  *email.Store
	crypto *email.Crypto
}

// NewIntentExecutor 返回调度器可注入的 IntentExecutor。store/crypto 缺一不可。
func NewIntentExecutor(store *email.Store, crypto *email.Crypto) email.IntentExecutor {
	return &intentExecutor{store: store, crypto: crypto}
}

func (e *intentExecutor) Execute(ctx context.Context, intent email.ActionIntent) error {
	if e.store == nil || e.crypto == nil {
		return errors.New("intent executor: store/crypto not configured")
	}
	switch intent.Action {
	case "route-folder":
		// 本期不真实 IMAP MOVE；标 skipped 留行可观测。后续接 MOVE 时改这里。
		return email.ErrSkipIntent
	case "trigger-autoreply":
		return e.executeAutoReply(ctx, intent)
	default:
		// archive 不会进 intent（fetcher 落库即归档）；未知 action 标 failed 而非静默。
		return fmt.Errorf("intent executor: unsupported action %q", intent.Action)
	}
}

// executeAutoReply 发送一封自动回复给原邮件发件人。
func (e *intentExecutor) executeAutoReply(ctx context.Context, intent email.ActionIntent) error {
	if intent.EmailID == "" || intent.AccountID == "" || intent.WorkspaceID == "" || intent.UserID == "" {
		return fmt.Errorf("auto-reply: intent missing identity fields")
	}
	em, err := e.store.GetEmailByIDScoped(ctx, intent.EmailID, intent.UserID, intent.WorkspaceID)
	if err != nil {
		return fmt.Errorf("auto-reply: load email: %w", err)
	}
	if em == nil {
		// 原邮件已被删除：无法回复，标终态跳过而非反复重试失败。
		return email.ErrSkipIntent
	}
	recipient := strings.TrimSpace(em.FromAddress)
	if recipient == "" || isAutoResponderAddress(recipient) {
		// 空 envelope / no-reply / mailer-daemon 类地址不自动回复（RFC 3834），
		// 与 vacation claim 的过滤一致；标终态跳过。
		return email.ErrSkipIntent
	}

	host, sender, port, encryptedCred, err := e.store.GetSMTPCredentialScoped(ctx, intent.AccountID, intent.UserID, intent.WorkspaceID)
	if err != nil {
		return fmt.Errorf("auto-reply: load smtp config: %w", err)
	}
	if host == "" || port == 0 || encryptedCred == "" {
		// 账户未配置 SMTP：无法发送。标 failed（退避后重试）—— 用户补 SMTP 配置后
		// 下一次 claim 周期会重新拿到并成功。
		return fmt.Errorf("auto-reply: account %s has no SMTP config", intent.AccountID)
	}
	cred, err := e.crypto.DecryptString(encryptedCred)
	if err != nil {
		return fmt.Errorf("auto-reply: decrypt smtp credential: %w", err)
	}
	username, password := sender, cred
	if u, p, ok := strings.Cut(cred, ":"); ok {
		username, password = u, p
	}

	subject, body := e.autoReplyContent(ctx, intent, em.Subject)
	headers := map[string]string{
		"Auto-Submitted":           "auto-replied",
		"Precedence":               "bulk",
		"X-Auto-Response-Suppress": "All",
		"Date":                     time.Now().Format(time.RFC1123Z),
	}
	if em.MessageID != "" {
		msgID := "<" + strings.Trim(em.MessageID, "<>") + ">"
		headers["In-Reply-To"] = msgID
		headers["References"] = msgID
	}
	return smtpSendMessage(email.OutgoingMessage{
		Host: host, Port: port, Username: username, Password: password,
		From: sender, To: []string{recipient},
		Subject: subject, Body: body, Headers: headers,
	})
}

// autoReplyContent 优先用账户在时间窗内的 enabled vacation 配置（subject/body），
// 让 trigger-autoreply 与 vacation 共用同一份回复文案；无 vacation 配置时回落到默认模板。
func (e *intentExecutor) autoReplyContent(ctx context.Context, intent email.ActionIntent, originalSubject string) (subject, body string) {
	vacations, err := e.store.ListVacationsScoped(ctx, intent.AccountID, intent.UserID, intent.WorkspaceID)
	if err == nil {
		now := time.Now().Unix()
		for _, v := range vacations {
			if v.Enabled && v.StartAt <= now && v.EndAt >= now {
				s := strings.TrimSpace(v.Subject)
				if s == "" {
					s = "Re: " + strings.TrimSpace(originalSubject)
				}
				return s, v.BodyText
			}
		}
	}
	// 默认模板：明确告知对方这是自动回复，避免被当作真人响应。
	return "Re: " + strings.TrimSpace(originalSubject),
		"Hi,\n\nThanks for your message. This is an automated reply confirming receipt.\n" +
			"I'll get back to you as soon as I can.\n"
}

// isAutoResponderAddress 判定不应触发自动回复的发件人地址（RFC 3834 / 休假响应约定）。
// 与 store.ClaimNextVacationDelivery 的 SQL 过滤保持同一组规则。
func isAutoResponderAddress(addr string) bool {
	a := strings.ToLower(strings.TrimSpace(addr))
	if a == "" {
		return true
	}
	// 只取 @ 后的 local-part 判断，避免带名字的 "Foo <noreply@x>" 漏判。
	if parsed, err := mail.ParseAddress(addr); err == nil && parsed.Address != "" {
		a = strings.ToLower(parsed.Address)
	}
	local := a
	if at := strings.IndexByte(a, '@'); at > 0 {
		local = a[:at]
	}
	switch local {
	case "no-reply", "noreply", "do-not-reply", "donotreply",
		"mailer-daemon", "postmaster", "mail-daemon":
		return true
	}
	return false
}
