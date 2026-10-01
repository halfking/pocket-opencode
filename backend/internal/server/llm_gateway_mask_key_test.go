package server

import "testing"

// maskKey 需在「可识别」与「不泄漏」间平衡：末 6 位用于多 key 场景识别当前
// 绑定的 key；短 key 整体打码，避免前后缀重叠时把整把 key 暴露出来。
func TestMaskKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "******"},
		{"short", "******"},
		{"exactly12chr", "exa******y12chr"}, // len==12：前 3 + 末 6，中间仅遮 3 位仍可接受
		// 夹具定性（2026-10-01，consolidation §5.2）：原为一把 sk- 形态的
		// 49 位字面量。全历史核查（git log -S --all）它只在本测试的引入提交
		// 14e35a1 里出现过，从未在配置 / 证据 / 脚本中被使用，与租户网关
		// 密钥（sk-6tGL…K51YV）前后缀均不同——判定为合成夹具，非真实密钥。
		// 即便如此仍换成下方无歧义的合成串，让「这把 key 是不是真的」
		// 这个问题永久关闭；maskKey 是纯字符串操作，语义不受影响。
		{"sk-test-fixture-not-a-real-key-000111", "sk-******000111"}, // secret-scan-ok — 合成串，非真实凭据
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
