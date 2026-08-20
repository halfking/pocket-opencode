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
// Both are vars rather than consts so tests can shorten them; production code
// never reassigns them.
var (
	// smtpDialTimeout bounds TCP connection setup for a single candidate IP.
	smtpDialTimeout = 10 * time.Second
	// smtpProbeTimeout bounds the whole probe (banner, EHLO, STARTTLS, AUTH,
	// QUIT). net/smtp has no context support, so this is enforced as a socket
	// deadline rather than a context cancellation.
	smtpProbeTimeout = 20 * time.Second
)

func smtpProbe(host string, port int, username, password string) error {
	if host == "" {
		return errors.New("smtp host empty")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("smtp port out of range: %d", port)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	ips, err := resolveSMTPHost(host)
	if err != nil {
		return fmt.Errorf("smtp resolve %s: %v", host, err)
	}
	// net/smtp drives Hello/StartTLS/Auth/Quit through a textproto.Conn and takes
	// no context, so net.Dialer.Timeout only bounds connection setup. Without a
	// deadline on the socket, a server that accepts the TCP connection and then
	// stalls on the banner (or any later command) would pin this HTTP handler
	// indefinitely. One absolute deadline covers every phase, Quit included.
	overallDeadline := time.Now().Add(smtpProbeTimeout)
	dialValidated := func() (net.Conn, error) {
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
			if dialErr == nil {
				if dErr := conn.SetDeadline(overallDeadline); dErr != nil {
					_ = conn.Close()
					return nil, fmt.Errorf("smtp set deadline: %w", dErr)
				}
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("smtp dial %s: %w", addr, lastErr)
	}

	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	switch {
	case port == 465:
		// Implicit TLS (SMTPS).
		conn, err := dialValidated()
		if err != nil {
			return fmt.Errorf("smtp tls dial %s: %v", addr, err)
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return fmt.Errorf("smtp tls handshake %s: %v", addr, err)
		}
		defer tlsConn.Close()
		client, err := smtp.NewClient(tlsConn, host)
		if err != nil {
			return fmt.Errorf("smtp client: %v", err)
		}
		defer client.Quit()
		if err := client.Hello("pocket-opencode/0.1"); err != nil {
			return fmt.Errorf("smtp ehlo: %v", err)
		}
		return tryAuth(client, host, username, password)

	default:
		// Submission (587) or unknown port: connect, EHLO, then STARTTLS.
		conn, err := dialValidated()
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
			return fmt.Errorf("smtp server does not advertise STARTTLS on port %d", port)
		}
		return tryAuth(client, host, username, password)
	}
}

func resolveSMTPHost(host string) ([]net.IP, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("host has no addresses")
	}
	for _, ip := range ips {
		if isBlockedOutboundIP(ip) {
			return nil, fmt.Errorf("resolved address %s is not allowed", ip)
		}
	}
	return ips, nil
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
