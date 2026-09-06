package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

// mime.go — 发票采集流水线的全文抓取与 MIME 解析。
//
// Fetcher.FetchMessageRaw 在「Sync 只拉 envelope + UID」的前提下按需单封拉
// 整封原文。优先走 go-imap v2 client.Fetch(BODY.PEEK[]<0..max>)；当 server
// 在 BODY[]<n> 响应里缺空格分隔符（Greenmail 等 quirk server）导致
// imapwire 解析失败时，降级为 textproto 级别的 RAW FETCH —— 自己拼装
// `UID FETCH BODY.PEEK[]<0.${size}>`，自己读 literal NSTRING(size) 后做 payload
// 字节拼接。两条路径都返回 RFC 5322 字节流，由 ParseMIMEMessage 解析。
//
// 附件 + XML 解析是后续步骤，在 invoice_harvest.go 调。

// ParsedAttachment 是解析出的一份附件。
type ParsedAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ParsedMessage 是一封邮件的结构化拆解结果。
type ParsedMessage struct {
	Subject     string
	From        string
	Date        time.Time
	TextBody    string // text/plain 聚合
	HTMLBody    string // text/html 聚合（发票链接多藏在 href 里）
	Attachments []ParsedAttachment
}

// FetchMessageRaw 按 UID 单封拉整封原文。先用 go-imap 的 client.Fetch，
// 失败/异常时降级为 net/textproto 级别的 RAW FETCH 抓取。
func (f *Fetcher) FetchMessageRaw(ctx context.Context, accountID string, uid int64) ([]byte, error) {
	if f == nil || f.store == nil || f.crypto == nil {
		return nil, fmt.Errorf("email: fetcher not configured")
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return nil, fmt.Errorf("account disabled")
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		return nil, fmt.Errorf("account has no usable credential")
	}
	if uid <= 0 {
		return nil, fmt.Errorf("invalid uid")
	}

	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()
	if err := f.login(client, *acc, cred); err != nil {
		return nil, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return nil, fmt.Errorf("select INBOX: %w", err)
	}

	const maxMessageBytes = 32 << 20
	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(uid))
	fetchOpts := &imap.FetchOptions{
		UID: true,
		BodySection: []*imap.FetchItemBodySection{{
			Peek:    true,
			Partial: &imap.SectionPartial{Offset: 0, Size: maxMessageBytes},
		}},
	}
	messages, fetchErr := client.Fetch(uidSet, fetchOpts).Collect()
	if fetchErr == nil && len(messages) > 0 {
		if body, ferr := findBodySection(messages[0].BodySection); ferr == nil && len(body) > 0 {
			return body, nil
		}
	}

	// 降级路径：raw textproto FETCH。
	body, rawErr := f.fetchRawByTextproto(ctx, acc, cred, uid, maxMessageBytes)
	if rawErr == nil && len(body) > 0 {
		log.Printf("[email/fetcher] raw textproto fallback ok uid=%d bytes=%d", uid, len(body))
		return body, nil
	}
	if fetchErr != nil {
		return nil, fmt.Errorf("fetch raw uid=%d (go-imap): %v; textproto fallback: %v", uid, fetchErr, rawErr)
	}
	return nil, fmt.Errorf("fetch raw uid=%d (textproto): %v", uid, rawErr)
}

// fetchRawByTextproto 走原始 IMAP 文本协议直接拉取 BODY[]：
//   1. 与 server 握手读 greeting；
//   2. LOGIN user pass；
//   3. UID FETCH <uid> BODY.PEEK[]<0.size>；
//   4. 解析 {size}\r\n 字面量取字节。
//
// 这是 Greenmail / 自建 quirk server 失败时的回退通道（绕过 go-imapwire
// 类型化解析器）。Go 的 net/textproto 让我们直接拼装命令，错误信息更直观。
func (f *Fetcher) fetchRawByTextproto(ctx context.Context, acc *Account, password string, uid, maxBytes int64) ([]byte, error) {
	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)
	readLine := func() (string, error) {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		return line, nil
	}

	// greeting
	if _, err := readLine(); err != nil {
		return nil, fmt.Errorf("read greeting: %w", err)
	}

	// LOGIN
	if _, err := bw.WriteString("A1 LOGIN " + quoteIMAPString(acc.EmailAddress) + " " + quoteIMAPString(password) + "\r\n"); err != nil {
		return nil, fmt.Errorf("write login: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush login: %w", err)
	}
	loginLine, err := readLine()
	if err != nil {
		return nil, fmt.Errorf("read login resp: %w", err)
	}
	if !strings.Contains(loginLine, " OK ") {
		return nil, fmt.Errorf("login rejected: %s", loginLine)
	}

	// SELECT INBOX — UID 命令需要先 SELECT 才能用
	if _, err := bw.WriteString("A2 SELECT INBOX\r\n"); err != nil {
		return nil, fmt.Errorf("write select: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush select: %w", err)
	}
	for {
		line, err := readLine()
		if err != nil {
			return nil, fmt.Errorf("read select: %w", err)
		}
		if strings.HasPrefix(line, "A2 ") {
			break
		}
		// * 行（FLAGS/EXISTS 等）继续读
	}

	// UID FETCH
	tag := "A2"
	cmd := tag + " UID FETCH " + strconv.FormatInt(uid, 10) + " BODY.PEEK[]<0." + strconv.FormatInt(maxBytes, 10) + ">\r\n"
	if _, err := bw.WriteString(cmd); err != nil {
		return nil, fmt.Errorf("write fetch: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush fetch: %w", err)
	}

	// 读响应：untagged * 行（首部含 BODY[...] {size}）+ size 字节 literal + \r\n + 后续可能更多 untagged + 最后 tagged "A2 OK"
	var collected bytes.Buffer
	linesScanned := 0
	for {
		line, err := readLine()
		if err != nil {
			return nil, fmt.Errorf("read fetch line: %w", err)
		}
		linesScanned++
		if linesScanned <= 3 {
			log.Printf("[email/fetcher/raw] line#%d: %q", linesScanned, line)
		}
		if strings.HasPrefix(line, "* ") {
			// 解析 BODY 字段后的 {size}
			size := parseBodyLiteralSize(line)
			if size > 0 {
				payload := make([]byte, size)
				if _, err := io.ReadFull(br, payload); err != nil {
					return nil, fmt.Errorf("read literal %d: %w", size, err)
				}
				collected.Write(payload)
				// literal 末尾的 \r\n
				br.ReadByte() // \r
				br.ReadByte() // \n
			}
			continue
		}
		if strings.HasPrefix(line, tag+" ") {
			// tagged response，A2 OK / NO / BAD —— 结束
			break
		}
		if len(line) == 0 {
			// 跳过空行
			continue
		}
	}
	return collected.Bytes(), nil
}

// parseBodyLiteralSize 从 untagged FETCH 响应行找 BODY[...]<...> 后的 {size}。
// 返回 0 表示这一行不携带 literal。
//   - line 形如 "* 1 FETCH (UID 1 BODY[] {2075}\\r\\n"（trim 后\\r\\n 没了）。
//   - LastIndex("{") 找 literal 起始位置 idx（已排除正文里其它 { 字符）。
//   - IndexByte(line[idx:], \'}\') 找 \'}\' 的相对偏移 rel。
//   - size 字节切片 = line[idx+1 : idx+rel]（rel 处是 \'}\' 本身，不含）。
func parseBodyLiteralSize(line string) int {
	idx := strings.LastIndex(line, "{")
	if idx < 0 {
		return 0
	}
	// \'} 在 line[idx:] 里的相对偏移 end
	rel := strings.IndexByte(line[idx:], '}')
	if rel < 0 {
		return 0
	}
	// size 字符串 = line[idx+1 : idx+rel]（不含 \'}，因为 \'}\' 在 idx+rel）
	sizeStr := line[idx+1 : idx+rel]
	size, err := strconv.Atoi(sizeStr)
	if err != nil {
		return 0
	}
	if !strings.Contains(strings.ToUpper(line), "BODY") {
		return 0
	}
	return size
}

// readLineCRLF 读一行（到 \r\n 之前）。
func readLineCRLF(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
}

// quoteIMAPString 把字符串中可能干扰 IMAP 协议的字符转义为 quoted 形式。
// 简化实现：含双引号或反斜杠或空格就用 quoted literal；否则原样。
func quoteIMAPString(s string) string {
	if !strings.ContainsAny(s, "\"\\ ") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
// ParseMIMEMessage 把整封原文拆成正文与附件。
func ParseMIMEMessage(raw []byte) (*ParsedMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse mail header: %w", err)
	}
	out := &ParsedMessage{Subject: decodeMIMEWord(msg.Header.Get("Subject"))}
	out.From = decodeMIMEWord(msg.Header.Get("From"))
	if d, err := mail.ParseDate(msg.Header.Get("Date")); err == nil {
		out.Date = d
	}
	mediaType, params, merr := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if merr != nil || !strings.HasPrefix(mediaType, "multipart/") {
		// 单 part：整封就是正文
		body, derr := decodePartBody(msg.Body, msg.Header.Get("Content-Type"),
			msg.Header.Get("Content-Transfer-Encoding"))
		if derr == nil {
			if strings.Contains(mediaType, "html") {
				out.HTMLBody = body
			} else {
				out.TextBody = body
			}
		}
		return out, nil
	}
	walkMultipart(msg.Body, mediaType, params["boundary"], out, 0)
	return out, nil
}

// maxWalkDepth 防 multipart 深层嵌套（恶意构造）打爆递归。
const maxWalkDepth = 8

func walkMultipart(r io.Reader, mediaType, boundary string, out *ParsedMessage, depth int) {
	if depth > maxWalkDepth || boundary == "" {
		return
	}
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		partType, params, perr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if perr != nil {
			partType = "application/octet-stream"
		}
		disposition := part.Header.Get("Content-Disposition")
		filename := part.FileName()
		switch {
		case strings.HasPrefix(partType, "multipart/"):
			walkMultipart(part, partType, params["boundary"], out, depth+1)
		case isAttachmentPart(disposition, partType, filename):
			data, derr := decodePartBytes(part, part.Header.Get("Content-Transfer-Encoding"))
			if derr == nil && len(data) > 0 {
				out.Attachments = append(out.Attachments, ParsedAttachment{
					Filename:    decodeMIMEWord(filename),
					ContentType: partType,
					Data:        data,
				})
			}
		case strings.EqualFold(partType, "text/plain"):
			body, derr := decodePartBody(part, part.Header.Get("Content-Type"),
				part.Header.Get("Content-Transfer-Encoding"))
			if derr == nil {
				out.TextBody += "\n" + body
			}
		case strings.EqualFold(partType, "text/html"):
			body, derr := decodePartBody(part, part.Header.Get("Content-Type"),
				part.Header.Get("Content-Transfer-Encoding"))
			if derr == nil {
				out.HTMLBody += "\n" + body
			}
		}
	}
}

func isAttachmentPart(disposition, contentType, filename string) bool {
	if strings.HasPrefix(strings.ToLower(disposition), "attachment") {
		return true
	}
	if filename != "" {
		return true
	}
	// PDF 常以内联（inline）携带，仍按附件对待。
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "pdf") || strings.Contains(ct, "octet-stream")
}

func decodePartBody(r io.Reader, contentType, transferEncoding string) (string, error) {
	data, err := decodePartBytes(r, transferEncoding)
	if err != nil {
		return "", err
	}
	// charset 解码失败时按原样返回（UTF-8 环境）
	if mediaType, params, err := mime.ParseMediaType(contentType); err == nil {
		if cs := strings.ToLower(params["charset"]); cs != "" && cs != "utf-8" && cs != "us-ascii" {
			if decoded, derr := decodeCharset(data, cs); derr == nil {
				data = decoded
			}
		}
		_ = mediaType
	}
	return string(data), nil
}

func decodePartBytes(r io.Reader, transferEncoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		raw, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
		if err != nil {
			if len(raw) == 0 {
				return nil, err
			}
			return raw, nil
		}
		return raw, nil
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(r))
	default:
		return io.ReadAll(r)
	}
}

// decodeMIMEWord 解 RFC 2047 编码头（=?utf-8?B?...?=）。
func decodeMIMEWord(s string) string {
	if s == "" {
		return ""
	}
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// decodeCharset 用 golang.org/x/text 做 GBK/GB18030 等常见中文编码兜底。
// x/text 已在依赖树里（间接依赖），显式引入按需。
func decodeCharset(data []byte, charset string) ([]byte, error) {
	switch charset {
	case "gbk", "gb2312", "gb18030", "gb_2312":
		return gbkToUTF8(data)
	default:
		return data, nil
	}
}