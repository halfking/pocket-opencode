package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// smtpSender adapts the server SMTP implementation to the email scheduler.
type smtpSender struct{}

// NewSMTPVacationSender returns the scheduler-compatible SMTP sender.
func NewSMTPVacationSender() email.VacationSender { return smtpSender{} }

func (smtpSender) Send(ctx context.Context, message email.OutgoingMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return smtpSendMessage(message)
}

func smtpSendMessage(message email.OutgoingMessage) error {
	if message.Host == "" {
		return errors.New("smtp host empty")
	}
	if message.Port < 1 || message.Port > 65535 {
		return fmt.Errorf("smtp port out of range: %d", message.Port)
	}
	if message.From == "" {
		return errors.New("smtp sender empty")
	}
	if len(message.To) == 0 {
		return errors.New("smtp recipient empty")
	}

	addr := net.JoinHostPort(message.Host, strconv.Itoa(message.Port))
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	ips, err := resolveSMTPHost(message.Host)
	if err != nil {
		return fmt.Errorf("smtp resolve %s: %v", message.Host, err)
	}
	overallDeadline := time.Now().Add(smtpProbeTimeout)
	dialValidated := func() (net.Conn, error) {
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(message.Port)))
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

	tlsCfg := &tls.Config{ServerName: message.Host, MinVersion: tls.VersionTLS12}
	var client *smtp.Client
	var closeConn func() error
	switch {
	case message.Port == 465:
		conn, err := dialValidated()
		if err != nil {
			return fmt.Errorf("smtp tls dial %s: %v", addr, err)
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtp tls handshake %s: %v", addr, err)
		}
		client, err = smtp.NewClient(tlsConn, message.Host)
		if err != nil {
			_ = tlsConn.Close()
			return fmt.Errorf("smtp client: %v", err)
		}
		closeConn = tlsConn.Close
	default:
		conn, err := dialValidated()
		if err != nil {
			return fmt.Errorf("smtp dial %s: %v", addr, err)
		}
		client, err = smtp.NewClient(conn, message.Host)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtp client: %v", err)
		}
		closeConn = conn.Close
	}
	defer func() { _ = client.Quit(); _ = closeConn() }()

	if err := client.Hello("pocket-opencode/0.1"); err != nil {
		return fmt.Errorf("smtp ehlo: %v", err)
	}
	if message.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %v", err)
			}
		} else if message.Port == 587 || message.Port == 25 {
			return fmt.Errorf("smtp server does not advertise STARTTLS on port %d", message.Port)
		}
	}
	if username := strings.TrimSpace(message.Username); username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, strings.TrimSpace(message.Password), message.Host)); err != nil {
			return fmt.Errorf("smtp auth: %v", err)
		}
	}
	if err := client.Mail(message.From); err != nil {
		return fmt.Errorf("smtp mail from: %v", err)
	}
	for _, rcpt := range message.To {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %v", rcpt, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %v", err)
	}
	if _, err := wc.Write(buildMessageWithHeaders(message)); err != nil {
		return fmt.Errorf("smtp write message: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp send data: %v", err)
	}
	return nil
}

// smtpSend delivers an email over SMTP using the account's saved SMTP config.
func smtpSend(host string, port int, username, password string, from string, to []string, subject, body string) error {
	return smtpSendMessage(email.OutgoingMessage{
		Host: host, Port: port, Username: username, Password: password,
		From: from, To: to, Subject: subject, Body: body,
	})
}

// buildMessage renders a minimal RFC 5322 message. Headers are sanitized to
// avoid header-injection: CR/LF are stripped from subject.
func buildMessage(from string, to []string, subject, body string) []byte {
	return buildMessageWithHeaders(email.OutgoingMessage{
		From: from, To: to, Subject: subject, Body: body,
	})
}

func buildMessageWithHeaders(message email.OutgoingMessage) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\r\n", sanitizeHeaderValue(message.From))
	fmt.Fprintf(&sb, "To: %s\r\n", sanitizeHeaderValue(strings.Join(message.To, ", ")))
	if subject := sanitizeHeaderValue(message.Subject); subject != "" {
		fmt.Fprintf(&sb, "Subject: %s\r\n", subject)
	}
	keys := make([]string, 0, len(message.Headers))
	for key := range message.Headers {
		if validHeaderName(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&sb, "%s: %s\r\n", key, sanitizeHeaderValue(message.Headers[key]))
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(message.Body)
	if !strings.HasSuffix(message.Body, "\n") {
		sb.WriteString("\r\n")
	}
	return []byte(sb.String())
}

func sanitizeHeaderValue(value string) string {
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		return value[:index]
	}
	return value
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < 33 || r > 126 || r == ':' {
			return false
		}
	}
	return true
}
