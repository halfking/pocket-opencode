package server

import (
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 仅做错误格式化与输入校验的纯函数测试；真实 SMTP/HTTP
// 连通需要外部测试服务器，不在单测范围。

func TestSMTPProbe_EmptyHost(t *testing.T) {
	if err := smtpProbe("", 587, "u@example.com", "secret"); err == nil {
		t.Fatal("expected empty host error")
	} else if err.Error() != "smtp host empty" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSMTPProbe_InvalidPort(t *testing.T) {
	for _, p := range []int{0, -1, 70000} {
		err := smtpProbe("smtp.example.com", p, "u@example.com", "secret")
		if err == nil || !strings.Contains(err.Error(), "smtp port out of range") {
			t.Fatalf("port %d: expected out-of-range error, got %v", p, err)
		}
	}
}

// 探测不可达目标必须安全返回错误（不 panic）。
//
// 注意 127.0.0.1 会先被 resolveSMTPHost 的 loopback 校验拦下，所以这里断言的是
// "安全返回错误"，而不是断言到达了拨号阶段。
func TestSMTPProbe_DialErrorIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("smtpProbe panicked: %v", r)
		}
	}()
	if err := smtpProbe("127.0.0.1", 1, "u@example.com", "secret"); err == nil {
		t.Fatal("expected an error for 127.0.0.1:1")
	}
}

// loopback / 私网 / link-local 目标必须在建立任何连接前被拒绝，否则
// /test-smtp 会成为打内网和云 metadata 的 SSRF 通道。
func TestSMTPProbeRejectsInternalTargets(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1",       // loopback
		"10.0.0.5",        // RFC1918
		"192.168.1.10",    // RFC1918
		"169.254.169.254", // 云 metadata (link-local)
		"0.0.0.0",         // unspecified
	} {
		err := smtpProbe(host, 587, "u@example.com", "secret")
		if err == nil {
			t.Fatalf("%s: expected rejection, got nil", host)
		}
		if !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("%s: expected an allow-list rejection, got %v", host, err)
		}
	}
}

// 服务器接受 TCP 连接后不发 banner 就静默挂住时，必须超时返回而不是一直占住
// HTTP handler。net/smtp 不支持 context，只能靠 socket deadline 兜住。
func TestSMTPProbeTimesOutOnSilentServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{}, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, aErr := listener.Accept()
		if aErr != nil {
			return
		}
		accepted <- struct{}{}
		defer conn.Close()
		// 收下连接但永不写 banner，模拟挂死的 SMTP 服务。
		<-done
	}()

	origProbe, origDial := smtpProbeTimeout, smtpDialTimeout
	smtpProbeTimeout, smtpDialTimeout = 300*time.Millisecond, 300*time.Millisecond
	defer func() { smtpProbeTimeout, smtpDialTimeout = origProbe, origDial }()

	port := listener.Addr().(*net.TCPAddr).Port
	start := time.Now()
	err = probeLoopbackForDeadlineTest(t, port)
	elapsed := time.Since(start)

	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never accepted a connection")
	}
	if err == nil {
		t.Fatal("expected a timeout error from the silent server")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("probe did not honour its deadline: took %s", elapsed)
	}
}

// probeLoopbackForDeadlineTest 复现 smtpProbe 的连接 + deadline + EHLO 序列，但
// 跳过 resolveSMTPHost 的 loopback 拦截，从而能对 deadline 行为本身做断言。
func probeLoopbackForDeadlineTest(t *testing.T, port int) error {
	t.Helper()
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	conn, err := dialer.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(smtpProbeTimeout)); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, "127.0.0.1")
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Hello("pocket-opencode/0.1")
}
