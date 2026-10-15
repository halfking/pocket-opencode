package server

// integration_status_gateway_trim_test.go —— §690：诊断页说「网关已配置」，
// 而实际 state 里 key 是空的。
//
// ── 缺陷形态 ──────────────────────────────────────────────────────
//
// `handleIntegrationStatus` 里两处对「网关是否配置」用了**两套口径**：
//
//	gwSource      ← os.Getenv("POCKET_LLM_GATEWAY_API_KEY") != ""      （原写法，裸比较）
//	gwConfigured  ← gwSource != "" || effectiveGatewayState(…).APIKey != ""
//	                                └─ 走 defaultLLMGatewayState 的
//	                                   strings.TrimSpace(os.Getenv(...))
//
// ⇒ env 是**纯空白**（"  "）时：gwSource 命中（已配置），
// 而 effectiveGatewayState 给出的 APIKey 是 `""`。
// ⇒ 本页于是报 **Enabled/Configured = true、source: env**，
// 而真实状态是**没有 key**——**诊断页说谎**。
//
// 这正是本文件开头那段注释自己怕的事：
// 「网关的地址与 key，未配 env 的全新实例照样能对话……诊断页说谎比不报更糟」。
//
// 同族依据不是推测：`datadir_test.go` 的 `TestResolveDataDir_TrimsWhitespace`
// 早就把「env 很容易带上首首尾空格（env 文件、脚本拼接）」定为**真实现象**，
// 只是 dataDir 那侧跟上了、gateway 这侧没跟上。
//
// ── 为什么这条判据值钱 ────────────────────────────────────────────
//
// ⚠ **本文件第一版在这里写过一句错话，已更正**（留着当反面教材）：
// 它写的是「本函数此前**零测试覆盖**（全仓 grep handleIntegrationStatus
// 只命中 server.go 与本文件）」。
// **那是错的**——`integration_status_test.go` 里已有 8 条
// `TestIntegrationStatus_*`，它们走 `srv.Handler().ServeHTTP` 打**整条路由**，
// 所以 grep 函数名抓不到（grep 只找**直接引用**）。
// ⇒ 与本项目已记录的「量具问错了问题」同族：**我以为在报覆盖缺口，其实在报检索方式的缺口。**
//
// 缺陷没被抓住的**真实原因**是：
// 既有两条相关用例只喂 `""`（`integration_status_test.go:135-136`）
// 与一个真 URL（`:174`）——**没有任何一条喂纯空白**。
// ⇒ 所以本文件补的是那一格，不是「从零覆盖」。
//
// 另外也正因如此，本文件的判据必须**端到端打 handler**（而不是抄一遍源码里的表达式），
// 否则被测实现换了写法、判据照样绿（同 §685 的教训）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type gwProbeEntry struct {
	Enabled      bool   `json:"enabled"`
	Configured   bool   `json:"configured"`
	Capabilities string `json:"capabilities"`
}

func probeGatewayStatus(t *testing.T, url, key string) gwProbeEntry {
	t.Helper()
	t.Setenv("POCKET_LLM_GATEWAY_URL", url)
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", key)
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleIntegrationStatus(rec, httptest.NewRequest(http.MethodGet, "/api/integration/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Integrations map[string]gwProbeEntry `json:"integrations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解不出 integrations：%v body=%s", err, rec.Body.String())
	}
	e, ok := out.Integrations["llm_gateway"]
	if !ok {
		t.Fatalf("响应里没有 llm_gateway 条目：%s", rec.Body.String())
	}
	return e
}

// 核心格：key 是纯空白时**不得**报已配置。
//
// ⚠ 断言要落在**端到端响应**上，不是「把源码里的表达式抄一遍」——
// 那样即使被测函数换了实现判据也会照样绿（同 §685 的教训）。
func TestIntegrationStatus_WhitespaceKeyIsNotConfigured(t *testing.T) {
	e := probeGatewayStatus(t, "", "  ")
	if e.Configured || e.Enabled {
		t.Fatalf("key 是纯空白时本页报了已配置（Enabled=%v Configured=%v caps=%s）—— "+
			"而 defaultLLMGatewayState() 的 APIKey 是空 ⇒ **诊断页说谎**",
			e.Enabled, e.Configured, e.Capabilities)
	}
	if e.Capabilities == "" {
		t.Fatal("未配置分支必须带 capabilities 说明为什么 disabled")
	}
}

// URL 同样是纯空白时也不得算「显式配置」。
func TestIntegrationStatus_WhitespaceURLIsNotConfigured(t *testing.T) {
	e := probeGatewayStatus(t, "   ", "")
	if e.Configured || e.Enabled {
		t.Fatalf("URL 是纯空白时本页报了已配置（Enabled=%v Configured=%v caps=%s）",
			e.Enabled, e.Configured, e.Capabilities)
	}
}

// 负控：真 key 与「带首尾空格的真 key」都必须**仍然**报已配置。
// 少了这两条，上面两条可能靠「一律报 disabled」而全绿。
func TestIntegrationStatus_RealKeyStillConfigured(t *testing.T) {
	for _, c := range []struct{ name, url, key string }{
		{"真 key", "", "sk-real-key"},
		{"URL 与 key 都带首尾空格", " https://gw.example.com ", " sk-real-key "},
		{"只有 URL", "https://gw.example.com", ""},
	} {
		e := probeGatewayStatus(t, c.url, c.key)
		if !e.Configured || !e.Enabled {
			t.Fatalf("[%s] 必须报已配置，实际 Enabled=%v Configured=%v caps=%s",
				c.name, e.Enabled, e.Configured, e.Capabilities)
		}
		if e.Capabilities == "" {
			t.Fatalf("[%s] 已配置分支必须带 capabilities", c.name)
		}
	}
}
