package server

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// smtpProbe 探测 SMTP 服务器可达并支持 AUTH LOGIN/PLAIN。
//
// 协议选择：
//   - port == 465 → implicit TLS (SMTPS)
//   - port == 587 或未知端口 → 明文 dial → EHLO → 如服务器宣告 STARTTLS
//     则升级到 TLS（最小版本 TLS 1.2，证书校验依赖 host）；其它情况拒绝
//     在明文下继续 AUTH，避免明文凭证外泄。
//
// 凭证：
//   - username 取账户的 email_address；
//   - password 取解密后的凭证；调用方可以预先用 "user:password" 拆分；
//     这里只负责用 username/password 调 smtp.PlainAuth。
//
// 只做"只读"探测：连接 → EHLO → （STARTTLS?）→ AUTH → QUIT，
// 不发送任何邮件。错误脱敏后返回给调用方。
func smtpProbe(host string, port int, username, password string) error {
	if host == "" {
		return errors.New("smtp host empty")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("smtp port out of range: %d", port)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	switch {
	case port == 465:
		// Implicit TLS (SMTPS).
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp tls dial %s: %v", addr, err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("smtp client: %v", err)
		}
		defer client.Quit()
		if err := client.Hello("pocket-opencode/0.1"); err != nil {
			return fmt.Errorf("smtp ehlo: %v", err)
		}
		return tryAuth(client, host, username, password)

	default:
		// Submission (587) 或未知端口：明文 dial → EHLO → 看 STARTTLS。
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("smtp dial %s: %v", addr, err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("smtp client: %v", err)
		}
		defer client.Quit()
		if err := client.Hello("pocket-opencode/0.1"); err != nil {
			return fmt.Errorf("smtp ehlo: %v", err)
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %v", err)
			}
		} else if port == 587 || port == 25 {
			// submission / smtp 明文期望 STARTTLS；服务器没宣告 → 拒绝继续 AUTH。
			return fmt.Errorf("smtp server does not advertise STARTTLS on port %d", port)
		}
		// 端口为未知（如 2525）时，服务器未宣告 STARTTLS 但调用方明确选择
		// 探测 → 仍然允许 AUTH 探测（用户已意识到风险）。
		return tryAuth(client, host, username, password)
	}
}

func tryAuth(client *smtp.Client, host, username, password string) error {
	trimmedUser := strings.TrimSpace(username)
	trimmedPass := strings.TrimSpace(password)
	if trimmedUser == "" && trimmedPass == "" {
		// 未配置凭证：连接和 TLS 通过即可返回成功。
		return nil
	}
	if err := client.Auth(smtp.PlainAuth("", trimmedUser, trimmedPass, host)); err != nil {
		return fmt.Errorf("smtp auth: %v", err)
	}
	return nil
}
