// Package email — IMAP authentication helpers (password + XOAUTH2 / OAUTHBEARER).
//
// We implement two SASL mechanisms used by Gmail / Outlook / Fastmail:
//
//   - XOAUTH2 (RFC 4959): a single-shot IMAP command carrying the access
//     token; most modern IMAP servers still accept it as a fallback.
//   - OAUTHBEARER (RFC 7628): SASL-style token exchange. Preferred when the
//     server advertises it via CAPABILITY.
//
// Both wrap the same payload ("n,a=username,\x01auth=Bearer <token>\x01\x01")
// because XOAUTH2 is just an OAUTHBEARER initial client response with the SASL
// framing stripped. We expose both via the go-sasl Client interface so the
// existing imapclient.Authenticate path picks them up.
package email

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/emersion/go-sasl"
)

// XOAUTH2 is the literal IMAP SASL mechanism name (RFC 4959).
const XOAUTH2 = "XOAUTH2"

// buildOAuthBearerPayload 拼出 Gmail/Outlook 共享的 IMAP 凭证格式。
//
// Reference: https://developers.google.com/gmail/imap/xoauth2-protocol
//
//	"n,a=<email>\x01auth=Bearer <token>\x01\x01"
//
// username 可为空字符串（部分 IMAP server 接受），但 Gmail / Outlook 都要求
// 等于账户 email。
func buildOAuthBearerPayload(username, token string) string {
	var b strings.Builder
	b.WriteString("n,")
	if username != "" {
		b.WriteString("a=")
		b.WriteString(username)
	}
	b.WriteString(",\x01auth=Bearer ")
	b.WriteString(token)
	b.WriteString("\x01\x01")
	return b.String()
}

// OAuthBearerClient 是 go-sasl.Client 的轻量包装，把我们的 username+token
// 输入转换成 RFC 7628 兼容的 initial response。
//
// 与 go-sasl 内置 NewOAuthBearerClient 的差别：内置版本会把 host/port 写入
// payload，但 IMAP 路径里这些字段常被忽略；我们提供一个最小实现，便于在
// 单元测试里精确控制输出。
type OAuthBearerClient struct {
	Username string
	Token    string
	Host     string
	Port     int
}

// NewOAuthBearerClient 返回 go-sasl 兼容的 Client 实现。
func NewOAuthBearerClient(username, token, host string, port int) sasl.Client {
	return &OAuthBearerClient{Username: username, Token: token, Host: host, Port: port}
}

// Start 实现 sasl.Client，返回 OAUTHBEARER initial response。
func (c *OAuthBearerClient) Start() (mech string, ir []byte, err error) {
	if c.Token == "" {
		return "", nil, errors.New("email: oauth bearer token is empty")
	}
	// We follow the same format as go-sasl.OAuthBearerClient but without the
	// host/port padding; this is also the payload XOAUTH2 expects.
	mech = sasl.OAuthBearer
	var sb strings.Builder
	sb.WriteString("n,")
	if c.Username != "" {
		sb.WriteString("a=")
		sb.WriteString(c.Username)
	}
	sb.WriteString(",")
	if c.Host != "" {
		sb.WriteString("\x01host=")
		sb.WriteString(c.Host)
	}
	if c.Port != 0 {
		sb.WriteString("\x01port=")
		sb.WriteString(strconv.Itoa(c.Port))
	}
	sb.WriteString("\x01auth=Bearer ")
	sb.WriteString(c.Token)
	sb.WriteString("\x01\x01")
	return mech, []byte(sb.String()), nil
}

// Next 处理 server challenge。当前我们只面对一次性 success/failure，
// 如果 server 返回 JSON OAuthBearerError 直接转 error。
func (c *OAuthBearerClient) Next(challenge []byte) ([]byte, error) {
	if len(challenge) == 0 {
		return nil, nil
	}
	// 任何 challenge 都按错误处理（RFC 7628: 失败时 server 返回 JSON）。
	return nil, fmt.Errorf("xoauth2: unexpected server challenge: %s", string(challenge))
}

// XOAuth2Client 把 XOAUTH2 协议包成 sasl.Client，方便 imapclient.Authenticate
// 直接使用。imapclient 在调用 Start() 拿到 mech 名称时会优先匹配 server
// CAPABILITY，因此 XOAUTH2 与 OAUTHBEARER 走同一份 client 也能正常工作。
type XOAuth2Client struct {
	Username string
	Token    string
}

// NewXOAuth2Client 返回 XOAUTH2 SASL 客户端。
func NewXOAuth2Client(username, token string) *XOAuth2Client {
	return &XOAuth2Client{Username: username, Token: token}
}

// Start 返回 IMAP XOAUTH2 协议期望的 SASL 机制名 + 单包载荷。
func (c *XOAuth2Client) Start() (mech string, ir []byte, err error) {
	if c.Token == "" {
		return "", nil, errors.New("email: xoauth2 token is empty")
	}
	return XOAUTH2, []byte(buildOAuthBearerPayload(c.Username, c.Token)), nil
}

// Next 处理 server challenge（XOAUTH2 协议里 server 可能返回人类可读错误，
// 我们直接把 raw bytes 当 error message 上抛，便于日志/前端展示）。
func (c *XOAuth2Client) Next(challenge []byte) ([]byte, error) {
	if len(challenge) == 0 {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(string(challenge))
	if err != nil {
		return nil, fmt.Errorf("xoauth2: %s", string(challenge))
	}
	return nil, fmt.Errorf("xoauth2: %s", string(decoded))
}
