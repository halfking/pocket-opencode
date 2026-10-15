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
// 仍是 90s，不会无界挂死）。
//
// **2026-10-07 人工拍板再次改为 85s**：上面那个推理只覆盖了
// `handleNoteSummarize` 那种「handler 预算 60s」的形状。**非流式**调用里
// ResponseHeaderTimeout 压掉的是每一个调用——响应头要等上游生成全部完成才到。
// 实测：32 分钟会议的精校回答裸 HTTP 直连 84.4s 能出结果，而产品路径
// 60.581s 就撞 `http2: timeout awaiting response headers` 失败
// ⇒ 配置写着 90s，实际可用预算只有 60s。
// 取 85s：90s 是整体上限，响应头拖到 90s 则 body 没时间读；85s 让 84.4s 那次
// 能过并留 5s 余量。代价同款：上游真挂死时最多等 85s。
//
// ⚠ 本测试锁的是**数字**，且额外锁住「ResponseHeaderTimeout 必须小于整体
// Timeout」这条不变量（85 < 90 成立）——若将来要收回，调实现的同时必须改这里。
func TestNewClient_TransportTimeouts(t *testing.T) {
	c := NewClient("https://example.com/v1", "sk-test")
	if c.Client.Timeout != 90*time.Second {
		t.Errorf("整体 Timeout = %v, want 90s", c.Client.Timeout)
	}
	tr, ok := c.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T, 期望 *http.Transport", c.Client.Transport)
	}
	if tr.ResponseHeaderTimeout != 85*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 85s（2026-10-07 人工拍板：非流式调用下 60s 让 60~85s 变成死预算，"+
			"实测长会精校 84.4s 能出结果却在 60.581s 被砍）", tr.ResponseHeaderTimeout)
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
