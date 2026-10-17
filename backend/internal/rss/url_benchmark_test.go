package rss

// 注意：以下两个用例切换包级全局 SetAllowBenchmarkIPs，勿加 t.Parallel()；
// 每个用例都自行显式设置初态，-shuffle 下也能自愈。

import (
	"net"
	"testing"
)

// TestBenchmarkIPGuardDefaultBlocks 基准段（RFC2544 v4 / RFC5180 v6）默认
// 必须被拦——fail-closed 是生产行为。
func TestBenchmarkIPGuardDefaultBlocks(t *testing.T) {
	SetAllowBenchmarkIPs(false)
	for _, raw := range []string{"198.18.0.5", "198.19.255.1", "2001:2::ca"} {
		if !IsBlockedIP(net.ParseIP(raw)) {
			t.Errorf("benchmark ip %s should be blocked by default", raw)
		}
	}
}

// TestBenchmarkIPGuardEscapeHatch 打开开关后只放开两个基准段；
// loopback/内网/CGNAT/文档段必须照旧被拦。
func TestBenchmarkIPGuardEscapeHatch(t *testing.T) {
	SetAllowBenchmarkIPs(true)
	defer SetAllowBenchmarkIPs(false)
	for _, raw := range []string{"198.18.1.243", "198.19.0.9", "2001:2::dead"} {
		if IsBlockedIP(net.ParseIP(raw)) {
			t.Errorf("benchmark ip %s should be allowed when escape hatch on", raw)
		}
	}
	for _, raw := range []string{
		"127.0.0.1", "::1", "10.1.2.3", "192.168.1.2", "172.16.0.9",
		"100.64.0.1", // CGNAT
		"169.254.169.254",
		"192.0.2.1", // 文档段
	} {
		if !IsBlockedIP(net.ParseIP(raw)) {
			t.Errorf("ip %s must stay blocked even with escape hatch on", raw)
		}
	}
}
