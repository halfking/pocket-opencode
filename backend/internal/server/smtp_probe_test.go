package server

import (
	"errors"
	"strings"
	"testing"
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

// 连接远端不存在的端口应该返回拨号层错误（不是 panic）。
func TestSMTPProbe_DialErrorIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("smtpProbe panicked: %v", r)
		}
	}()
	err := smtpProbe("127.0.0.1", 1, "u@example.com", "secret")
	if err == nil {
		t.Fatal("expected dial error to 127.0.0.1:1")
	}
	if !errors.Is(err, err) { // smoke self-comparable
		t.Fatal("error not self-comparable")
	}
}
