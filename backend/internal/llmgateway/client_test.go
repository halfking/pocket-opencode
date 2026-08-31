package llmgateway

import (
	"net/http"
	"testing"
	"time"
)

// TestNewClient_TransportTimeouts 锁定 NewClient 的超时策略，避免未来重构
// 把 Transport.ResponseHeaderTimeout 误删后再次出现「上游 model 挂死时前端
// 60s 一直转圈」的情况（参见 2026-08-31 llm.kxpms.cn 上 minimax-m3/kimi-k3
// 挂死诊断）。
func TestNewClient_TransportTimeouts(t *testing.T) {
	c := NewClient("https://example.com/v1", "sk-test")
	if c.Client.Timeout != 90*time.Second {
		t.Errorf("整体 Timeout = %v, want 90s", c.Client.Timeout)
	}
	tr, ok := c.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T, 期望 *http.Transport", c.Client.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s（防止上游 model 挂死）", tr.ResponseHeaderTimeout)
	}
	if tr.ResponseHeaderTimeout >= c.Client.Timeout {
		t.Errorf("ResponseHeaderTimeout(%v) 必须小于整体 Timeout(%v)，否则握手阶段永远不会先触发",
			tr.ResponseHeaderTimeout, c.Client.Timeout)
	}
}

// TestNormalizeBaseURL 锁定 baseURL 归一化（剥结尾 /v1 与斜杠），避免
// 历史拼出 /v1/v1/chat/completions 的双 /v1 错误路径再次出现。
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://llm.kxpms.cn/v1", "https://llm.kxpms.cn"},
		{"https://llm.kxpms.cn/v1/", "https://llm.kxpms.cn"},
		{"https://llm.kxpms.cn", "https://llm.kxpms.cn"},
		{"https://llm.kxpms.cn/", "https://llm.kxpms.cn"},
		{"  https://llm.kxpms.cn/v1  ", "https://llm.kxpms.cn"},
	}
	for _, tc := range cases {
		if got := normalizeBaseURL(tc.in); got != tc.want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
