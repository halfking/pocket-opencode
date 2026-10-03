package llmgateway

import (
	"net/http"
	"testing"
	"time"
)

// TestNewClient_TransportTimeouts 锁定 NewClient 的超时策略，避免未来重构
// 把 Transport.ResponseHeaderTimeout 误删或改回 30s。
//
// 30s 那一版的由来：llm.kxpms.cn 的 preferred 模型里有两个 model 对
// /v1/chat/completions 既不返结果也不返错误（连接挂死），此前无该上限时前端要
// 等满 60s；加 30s 后能 30s 内即触发 client 错误（参见 2026-08-31 的
// minimax-m3/kimi-k3 挂死诊断）。
//
// **2026-10-02 人工拍板改为 60s**：ResponseHeaderTimeout 约束的是「响应头何时
// 到达」，而推理模型（网关自动路由到 glm-5.2）先把 token 花在
// reasoning_content 上才吐正文——实测一句 63 字的总结用掉 985 个 reasoning
// token。于是 30s 会把 handleNoteSummarize 的 60s 预算压成「前 30s 可用、
// 后 30s 是死预算」。
//
// 代价是**明确接受的**：上游真挂死时用户从等 30s 变成最多等 60s（整体 Timeout
// 仍是 90s，不会无界挂死）。本测试锁的就是这个拍板结果与「必须小于整体
// Timeout」这条不变量（60 < 90 成立）——若将来要收回，调实现的同时必须改这里。
func TestNewClient_TransportTimeouts(t *testing.T) {
	c := NewClient("https://example.com/v1", "sk-test")
	if c.Client.Timeout != 90*time.Second {
		t.Errorf("整体 Timeout = %v, want 90s", c.Client.Timeout)
	}
	tr, ok := c.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T, 期望 *http.Transport", c.Client.Transport)
	}
	if tr.ResponseHeaderTimeout != 60*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 60s（2026-10-02 人工拍板：30s 会把推理模型那 30~60s 变成死预算）", tr.ResponseHeaderTimeout)
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
