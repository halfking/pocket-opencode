package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// startFakePOP3 璧蜂竴涓渶灏?RFC 1939 鏈嶅姟鍣細greeting + USER/PASS/STAT/UIDL/RETR銆?
// 鍥炲綊浠峰€硷細姝ゅ墠鐘舵€佽璇敤 textproto.ReadResponse(200)锛堝彧璁?HTTP 鏁板瓧 code锛夛紝
// 鐪熷疄 163 鏈嶅姟鍣ㄧ殑 "+OK Welcome..." greeting 鐩存帴鎶?invalid response code锛?
// 鍙︽湁姝ｆ枃 bufio 涓庣姸鎬?bufio 鍙屽眰缂撳啿浜掔浉鍚炴暟鎹殑闂銆備袱鑰呴兘闇€瑕佷竴涓?
// 浼氳瘽绾?fake server 鎵嶈兘鏆撮湶銆?
func startFakePOP3(t *testing.T, messages map[int]string) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn, messages)
		}
	}()
	return ln.Addr()
}

func handle(conn net.Conn, messages map[int]string) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	write := func(s string) { fmt.Fprint(conn, s) }
	readCmd := func() string {
		line, err := br.ReadString('\n')
		if err != nil {
			return ""
		}
		return strings.TrimSpace(line)
	}
	write("+OK Welcome to fake coremail Pop3 Server\r\n")
	for {
		switch cmd := readCmd(); {
		case cmd == "":
			return
		case strings.HasPrefix(cmd, "USER"), strings.HasPrefix(cmd, "PASS"):
			write("+OK\r\n")
		case cmd == "STAT":
			write(fmt.Sprintf("+OK %d 0\r\n", len(messages)))
		case cmd == "UIDL":
			write("+OK\r\n")
			for i := 1; i <= len(messages); i++ {
				write(fmt.Sprintf("%d uidl-%d\r\n", i, i))
			}
			write(".\r\n")
		case strings.HasPrefix(cmd, "RETR"):
			var idx int
			fmt.Sscanf(cmd, "RETR %d", &idx)
			body, ok := messages[idx]
			if !ok {
				write("-ERR no such message\r\n")
				continue
			}
			write("+OK\r\n")
			// 琛岄 . 杞箟鎸?RFC 1939 byte-stuffing 澶勭悊
			for _, line := range strings.Split(body, "\r\n") {
				if strings.HasPrefix(line, ".") {
					line = "." + line
				}
				write(line + "\r\n")
			}
			write(".\r\n")
		case cmd == "QUIT":
			write("+OK bye\r\n")
			return
		default:
			write("-ERR unknown\r\n")
		}
	}
}

func TestFetchPOP3MailboxFetchesNew(t *testing.T) {
	addr := startFakePOP3(t, map[int]string{
		1: "From: a@b.c\r\nSubject: invoice one\r\n\r\nbody one",
		2: "From: d@e.f\r\nSubject: dot escape\r\n\r\n.line starts with dot\r\n..two dots",
	})
	newUIDLs, payloads, err := FetchPOP3Mailbox(context.Background(), addr.String(), false, "u@163.com", "authcode", nil)
	if err != nil {
		t.Fatalf("FetchPOP3Mailbox: %v", err)
	}
	if len(newUIDLs) != 2 || len(payloads) != 2 {
		t.Fatalf("want 2 new messages, got uidls=%v payloads=%d", newUIDLs, len(payloads))
	}
	// dot-unstuffing锛氳棣?".." 杩樺師涓?"."锛圧FC 822 杞箟锛?
	if !strings.Contains(string(payloads[1]), "\r\n.line starts with dot\r\n") {
		t.Errorf("dot-escape line mismatch: %q", payloads[1])
	}

	// 澧為噺锛氬凡瑙佽繃鐨?UIDL 璺宠繃
	seen := map[string]struct{}{"uidl-1": {}, "uidl-2": {}}
	newUIDLs, payloads, err = FetchPOP3Mailbox(context.Background(), addr.String(), false, "u@163.com", "authcode", seen)
	if err != nil {
		t.Fatalf("FetchPOP3Mailbox(seen): %v", err)
	}
	if len(newUIDLs) != 0 || len(payloads) != 0 {
		t.Fatalf("want 0 new, got %v", newUIDLs)
	}
}

func TestFetchPOP3MailboxAuthRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// 涓?handle() 鍚屾牱鐨勩€屽厛璇诲悗鍐欍€嶈妭濂忥細涓€娆℃€ф妸涓夋潯鍝嶅簲绯婁笂鍘诲啀绔嬪埢
		// Close锛學indows 涓婁細鍥犳帴鏀剁紦鍐插尯閲岃繕鏈夋湭璇荤殑瀹㈡埛绔懡浠よ€屽彂 RST锛?
		// 瀹㈡埛绔笅涓€娆″啓鐩存帴鎷垮埌 wsasend WSAECONNABORTED锛屼簬鏄繖鏉＄敤渚嬫祴鍒扮殑
		// 鏄紶杈撳眰閿欒鑰屼笉鏄畠鎯虫柇瑷€鐨勩€?ERR 鐘舵€佽琚€忎紶銆嶃€?
		br := bufio.NewReader(conn)
		readCmd := func() string {
			line, err := br.ReadString('\n')
			if err != nil {
				return ""
			}
			return strings.TrimSpace(line)
		}
		fmt.Fprint(conn, "+OK ready\r\n")
		if readCmd() == "" {
			return
		}
		fmt.Fprint(conn, "+OK\r\n")
		if readCmd() == "" {
			return
		}
		fmt.Fprint(conn, "-ERR Unable to log on\r\n")
		// 璇诲共鍒?EOF 鍐嶅叧锛岄伩鍏嶅湪瀹㈡埛绔瀹屼箣鍓?RST銆?
		br.ReadString('\n')
	}()
	_, _, err = FetchPOP3Mailbox(context.Background(), ln.Addr().String(), false, "u", "bad", nil)
	if err == nil || !strings.Contains(err.Error(), "Unable to log on") {
		t.Fatalf("want server -ERR message surfaced, got %v", err)
	}
}

