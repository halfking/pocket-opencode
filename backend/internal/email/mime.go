package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
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
	"github.com/emersion/go-imap/v2/imapclient"
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
	Subject string
	From    string
	Date    time.Time
	// MessageID 是 RFC 5322 的 Message-ID 头（已去尖括号）。
	//
	// POP3 降级路径以前不用它，自己合成 "pop3-<uidl>" 当 message_id，于是同一
	// 封邮件走 IMAP 落一条、走 POP3 再落一条，两条 message_id 不同，
	// UNIQUE(account_id, message_id) 拦不住 —— 实测 QQ 信箱 444 封里
	// 284 封走 POP3 路径，其中 47 组是同一封的重复副本。
	MessageID   string
	TextBody    string // text/plain 聚合
	HTMLBody    string // text/html 聚合（发票链接多藏在 href 里）
	Attachments []ParsedAttachment
}

// selectInboxWithClientID 声明 RFC 2971 客户端标识后 SELECT INBOX。
//
// 顺序是硬约束：网易 Coremail 在 SELECT 之前没收到 ID 就回
// `NO SELECT Unsafe Login`。sendClientID 内部对失败只记日志不阻断
// （ID 是扩展命令，服务器 CAPABILITY 里没有时返回 BAD 属正常），
// 但 163 会因为「没发」而拒绝 SELECT，所以必须发。
func selectInboxWithClientID(client *imapclient.Client, emailAddress string) error {
	sendClientID(client, emailAddress)
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("select INBOX: %w", err)
	}
	return nil
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
	// SELECT 之前必须先发 RFC 2971 ID：网易 Coremail（163/126）在缺客户端标识时
	// 直接 `NO SELECT Unsafe Login. Please contact kefu@188.com`，而不是鉴权失败。
	// Sync 里一直有这一步（fetcher.go 的 sendClientID），但本函数原先漏了，
	// 于是同一个 163 账户「常规同步成功、拉原文必失败」——发票二次提取与
	// 发票采集（harvestOne 也走这里）在 163 邮箱上 100% 拿不到正文。
	// 真实日志：acct-...-5 uid=1298896126 select INBOX: imap: NO SELECT Unsafe Login。
	//
	// 抽成 selectInboxWithClientID 是为了让「ID 必须在 SELECT 之前」这条协议
	// 约束能被**真实调用**验证：早期版本的测试自己手搓 ID 命令，结果把
	// mime.go 里这行 sendClientID 删掉测试照样全绿——测的是测试自己写的命令，
	// 不是生产代码。负控：删掉本函数里的 sendClientID -> imap_clientid_test.go 转红。
	if err := selectInboxWithClientID(client, acc.EmailAddress); err != nil {
		return nil, err
	}

	// 单封原文上限。发票 PDF 实际 <200KB，XML 几 KB，8MB 对「带附件的普通
	// 邮件」仍然宽松。
	//
	// 为什么从 32MB 降下来：这是**唯一能给单次拉取耗时设上界的杠杆**——
	// go-imap 不响应 ctx 取消（imapclient.Options 没有 ReadTimeout），所以
	// 一封超大邮件的 BODY[] literal 读取可以一路拖到库内 5 分钟上限。
	// 实测一封 QQ 邮件让第 4 步单张卡了 13 分钟（uid=134，attempts 都没来得及
	// 递增），整轮采集因此无上界。宁可让超大邮件采集失败进 pending 等下一轮，
	// 也不能让一轮流水线没有上界。
	const maxMessageBytes = 8 << 20
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
	if fetchErr == nil {
		if len(messages) == 0 {
			// go-imap 没报错、却一条都没匹配到（uid 已被服务端 expunge、
			// 或 UIDVALIDITY 变了）。不显式记下来就会掉到降级路径，
			// 最终只报一句跟本问题无关的降级错误。
			fetchErr = fmt.Errorf("go-imap matched 0 messages for uid=%d", uid)
		} else if body, ferr := findBodySection(messages[0].BodySection); ferr == nil && len(body) > 0 {
			return body, nil
		} else {
			// 匹配到了但没给出正文：单独记下来。原实现直接掉进降级路径，
			// 最终只报降级的错（实测 `read greeting: EOF` / `<nil>`），主路径
			// 这个真正有用的线索被完全吞掉。
			fetchErr = fmt.Errorf("go-imap returned %d message(s) but no usable body section", len(messages))
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
//  1. 与 server 握手读 greeting；
//  2. LOGIN user pass；
//  3. UID FETCH <uid> BODY.PEEK[]<0.size>；
//  4. 解析 {size}\r\n 字面量取字节。
//
// 这是 Greenmail / 自建 quirk server 失败时的回退通道（绕过 go-imapwire
// 类型化解析器）。Go 的 net/textproto 让我们直接拼装命令，错误信息更直观。
func (f *Fetcher) fetchRawByTextproto(ctx context.Context, acc *Account, password string, uid, maxBytes int64) ([]byte, error) {
	dialer := net.Dialer{Timeout: 30 * time.Second}
	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// **建连之后一个 deadline 都没有**（这是本函数原来最大的问题）。
	// `net.Dialer.Timeout` 只管三次握手，之后 `br.ReadString('\n')` 能挂多久
	// 完全看服务器脸色，而且**不看 ctx** —— ctx 只喂给了 DialContext。
	// 于是「服务器 accept 了 TCP 但一句话不说」的组合会让单封邮件**永久**
	// 阻塞，整轮流水线失去上界，正是上方 maxMessageBytes 注释里明确要求的
	// 「也不能让一轮流水线没有上界」那条不变量。
	//
	// 复用 fetcher.go 的 deadlineConn（滚动空闲 60s + 绝对硬截止 45s）：
	// go-imap 主路径早就套了它，降级路径当时漏了。
	dc := &deadlineConn{
		Conn: conn,
		idle: imapIdleTimeout,
		hard: time.Now().Add(imapHardTimeout),
	}
	dc.start()
	conn = dc
	// ctx 取消也要能打断正在阻塞的读：把 deadline 钉到过去即可让 Read
	// 立刻返回；连接关闭后 deadlineConn 的看门狗自行退出。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = dc.SetDeadline(time.Now().Add(-time.Second))
		case <-done:
		}
	}()

	// 明文 / 隐式 TLS 的判定必须与 fetcher.dial 用同一条规则
	// （isPlainIMAPPort），降级通道才可能在主路径失败后真正救回场子。
	//
	// 原先是 `if acc.IMAPPort == 993` 这种写死判断：只认 993，143 与其它
	// 一切端口都当隐式 TLS。对生产账户（qq/163 都是 993）恰好正确，但
	// 对任何非 993 的**明文**端口就是必然的协议错配 —— 最典型的是 Greenmail
	// 的明文 3143：我们说 TLS、服务端等明文 greeting，双向互等，最后以
	// `read greeting: EOF` 收场。账户的 IMAP 端口是逐账户配置项，
	// 143/1143/993 之外的值（例如自建邮件网关的 1993）在旧写法下必然走错分支。
	//
	// 反过来把 993 也放掉是不能接受的：那是把加密通道悄悄降级成明文，
	// 163 上会变成明文外发 LOGIN 密码。所以这里取的是「与主路径一致」，
	// 而不是「尽量猜」。
	if !isPlainIMAPPort(addr) {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: acc.IMAPHost,
			// 与 imapDialWithTimeout 保持一致：仅自签测试服务器才跳过校验，
			// 生产走默认 CA 池验证，避免中间人攻击。
			InsecureSkipVerify: f.insecureSkipVerify, //nolint:gosec // 仅测试服务器
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		conn = tlsConn
	}

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

	// ID（RFC 2971）：与 go-imap 路径同样必须在 SELECT 之前发，否则网易 Coremail
	// 回 `NO SELECT Unsafe Login`。它不是 CAPABILITY 必备命令，服务器不支持时
	// 回 BAD/BADCHARSET 属正常，忽略继续即可（与 sendClientID 同语义）。
	if _, err := bw.WriteString("A0 ID (\"name\" \"pocketd\" \"version\" \"1.0.0\" \"vendor\" \"openpocket\")\r\n"); err != nil {
		return nil, fmt.Errorf("write id: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush id: %w", err)
	}
	if idLine, err := readLine(); err == nil {
		log.Printf("[email/fetcher] textproto ID %s -> %s", acc.EmailAddress, strings.TrimSpace(idLine))
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
			if !strings.Contains(line, " OK ") {
				return collected.Bytes(), fmt.Errorf("server rejected UID FETCH: %s", strings.TrimSpace(line))
			}
			break
		}
		if len(line) == 0 {
			// 跳过空行
			continue
		}
	}
	if collected.Len() == 0 {
		// 契约：要么给正文，要么给错误。原来这里返回 (空, nil)，调用方只看到
		// 「textproto fallback: <nil>」，把「一条 literal 都没解析到」这个
		// 真正的线索吞掉了（真实 163/QQ 账户上就是这么变成一句废话报错的）。
		return nil, fmt.Errorf("no BODY literal in UID FETCH response (uid=%d)", uid)
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

// normalizeMessageID 取出 Message-ID 头的裸值（去掉 < > 和空白）。
// 取不到时返回空串，由调用方决定回退策略。
func normalizeMessageID(h string) string {
	v := strings.TrimSpace(h)
	v = strings.TrimPrefix(v, "<")
	v = strings.TrimSuffix(v, ">")
	return strings.TrimSpace(v)
}

// ParseMIMEMessage 把整封原文拆成正文与附件。
func ParseMIMEMessage(raw []byte) (*ParsedMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse mail header: %w", err)
	}
	out := &ParsedMessage{Subject: decodeMIMEWord(msg.Header.Get("Subject"))}
	out.From = decodeMIMEWord(msg.Header.Get("From"))
	out.MessageID = normalizeMessageID(msg.Header.Get("Message-Id"))
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
		// Go 的 quotedprintable.Reader **不认 CRLF 软换行**。RFC 2045 规定
		// 邮件用 CRLF，于是真实邮件里的软换行是 "=\r\n"，喂给它会直接报
		//     quotedprintable: invalid bytes after =: "\r\r\n"
		// 整段正文被丢掉（decodePartBody 返回 err，调用方只看到空正文）。
		// 2026-10-02 实测：把同一段中文正文用 LF 排版能解出，用 CRLF 排版
		// 解出空串 —— 差别只有行尾。
		// QP 载荷本身是 7bit ASCII 安全的，把行尾统一成 LF 不会改变任何
		// 被编码的字节，所以这里先归一再解。
		raw, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte("\r\n")) {
			raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		}
		if bytes.Contains(raw, []byte("\r")) {
			raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
		}
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
	default:
		return io.ReadAll(r)
	}
}

// decodeMIMEWord 解 RFC 2047 编码头（=?utf-8?B?...?=）。
// decodeMIMEWord 解 RFC 2047 编码字（=?charset?B|Q?text?=）。
//
// 必须挂 CharsetReader：mime.WordDecoder 默认只认 UTF-8/ISO-8859-1，
// 遇到国内企业邮箱最常见的 `=?GBK?B?...?=` 会直接报错，于是**整个头字段原样返回**
// ——症状是列表里所有中文主题都显示成 `=?GBK?B?5Y2G5bCP?=`。实测企业微信邮箱
// 收信 5 封、5 封主题全是编码字原文。
func decodeMIMEWord(s string) string {
	if s == "" {
		return ""
	}
	dec := &mime.WordDecoder{
		CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
			raw, err := io.ReadAll(input)
			if err != nil {
				return nil, err
			}
			out, derr := decodeCharset(raw, strings.ToLower(strings.TrimSpace(label)))
			if derr != nil {
				return nil, derr
			}
			return bytes.NewReader(out), nil
		},
	}
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

// ExtractDisplayBody 从整封原文提取可直接展示的正文文本：
// 优先 text/plain 聚合，其次 text/html，解析失败退回原文本身。
// 超长截断到 256KB，与 /api/emails/{id}/body 的拉取上限一致。
//
// ## 「解析失败退回原文本身」这条兜底已被移除
//
// 原注释写着「解析失败退回原文本身」。实测这条兜底就是缺陷本身：
// 2026-10-03 真机（Redmi 2411DRN47C）上它把整段 MIME 源码原样交给
// LLM 摘要输入（server_email_summary.go），摘要质量随之塌掉；
// 同一条路径也把 `------=_Part_… Content-Type: …` 灌进 assistant 上下文。
//
// 拆开看它原本想覆盖的两种情形：
//
//	· 旧版本缓存下来的**拍平展示文本**（非 MIME）——不是 MIME 源码，
//	  containsMIMESource 放行，行为不变；
//	· 真正的 MIME 源码但解析失败——**正是要拦的那一类**。
//
// 所以判据不是「解析成功没有」，而是「这段文本本身是不是 MIME 源码」：
// 是就返回空串（宁可没有正文，也不要 MIME 转储），不是就照旧返回。
// 判据用的是 containsMIMESource 而不是 looksLikeMIMEStructure，原因见
// snippet.go 里那条注释——出口的字符串常已被压平成一行。
func ExtractDisplayBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if msg, err := ParseMIMEMessage(raw); err == nil {
		// 判据跑在未压平的原文上：TextBody 是所有 text/plain 部件的聚合，
		// 对 multipart/mixed 里嵌内层报文原文的形态会把 boundary 行和
		// Content-* 头一起聚合进来（真机 5/5 封全中）。
		if t := strings.TrimSpace(msg.TextBody); t != "" && !containsMIMESource(msg.TextBody) {
			return t
		}
		if h := strings.TrimSpace(msg.HTMLBody); h != "" && !containsMIMESource(msg.HTMLBody) {
			return h
		}
	}
	const maxDisplay = 256 * 1024
	if len(raw) > maxDisplay {
		raw = raw[:maxDisplay]
	}
	if containsMIMESource(string(raw)) {
		return ""
	}
	return string(raw)
}
